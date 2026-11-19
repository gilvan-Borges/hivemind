package tools

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

const (
	searchMaxFileBytes    = 512 * 1024
	searchMaxFilesScanned = 20000
	searchMaxLineBytes    = 200
	searchMaxOutputBytes  = 24 * 1024
	searchDefaultResults  = 50
	searchHardMaxResults  = 120

	findDefaultResults = 60
	findHardMaxResults = 200
)

var searchIgnoredDirs = map[string]bool{
	".git":         true,
	".hivemind":    true,
	".worktrees":   true,
	"node_modules": true,
	"dist":         true,
	"build":        true,
	"target":       true,
	".gradle":      true,
	".next":        true,
	"vendor":       true,
	"__pycache__":  true,
	".venv":        true,
	"bin":          true,
	"obj":          true,
}

type searchTextArgs struct {
	Query         string `json:"query" jsonschema:"Texto ou expressão regular a buscar no código."`
	Path          string `json:"path,omitempty" jsonschema:"Subdiretório relativo opcional para restringir a busca (padrão: raiz do workspace)."`
	Glob          string `json:"glob,omitempty" jsonschema:"Filtro de nome de arquivo opcional (ex.: *.ts, *.java, *.go, *Controller*)."`
	Regex         bool   `json:"regex,omitempty" jsonschema:"Quando true, interpreta query como expressão regular (Go RE2)."`
	CaseSensitive bool   `json:"case_sensitive,omitempty" jsonschema:"Quando true, diferencia maiúsculas e minúsculas (padrão: false)."`
	MaxResults    int    `json:"max_results,omitempty" jsonschema:"Limite de ocorrências retornadas (padrão: 50, máximo: 120)."`
}

type findFilesArgs struct {
	Pattern    string `json:"pattern" jsonschema:"Trecho do caminho/nome ou glob (ex.: *.spec.ts, *Route*, a11y-web.txt)."`
	Path       string `json:"path,omitempty" jsonschema:"Subdiretório relativo opcional (padrão: raiz)."`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"Limite de caminhos retornados (padrão: 60, máximo: 200)."`
}

func (d Deps) canSearchText() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "search_text",
		Description: "Busca ocorrências de texto ou regex nos arquivos do workspace, retornando caminho:linha: trecho. Use antes de read_file para localizar símbolos, rotas, DTOs, testes e referências no projeto inteiro em 1 chamada.",
	}, func(ctx agent.Context, args searchTextArgs) (textResult, error) {
		out, err := runSearchText(ctx, d.Root, args)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: out}, nil
	})
}

func (d Deps) canFindFiles() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "find_files",
		Description: "Encontra arquivos no workspace pelo nome ou padrão glob (ex.: *Cliente*.java, *.spec.ts, a11y*), evitando múltiplas chamadas de list_dir.",
	}, func(ctx agent.Context, args findFilesArgs) (textResult, error) {
		out, err := runFindFiles(ctx, d.Root, args)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: out}, nil
	})
}

func runSearchText(ctx interface{ Done() <-chan struct{} }, root string, args searchTextArgs) (string, error) {
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return "", fmt.Errorf("informe o parâmetro query para search_text")
	}
	searchDir, absRoot, err := resolveSearchBase(root, args.Path)
	if err != nil {
		return "", err
	}

	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = searchDefaultResults
	}
	if maxResults > searchHardMaxResults {
		maxResults = searchHardMaxResults
	}

	var re *regexp.Regexp
	needle := query
	if args.Regex {
		expr := query
		if !args.CaseSensitive {
			expr = "(?i)" + expr
		}
		re, err = regexp.Compile(expr)
		if err != nil {
			return "", fmt.Errorf("regex inválida %q: %w", query, err)
		}
	} else if !args.CaseSensitive {
		needle = strings.ToLower(query)
	}

	glob := strings.TrimSpace(args.Glob)
	var b strings.Builder
	totalMatches := 0
	filesScanned := 0
	truncated := false
	stopWalk := false

	err = filepath.WalkDir(searchDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if ctx != nil {
			select {
			case <-ctx.Done():
				return fs.SkipAll
			default:
			}
		}
		if stopWalk {
			return fs.SkipAll
		}

		name := entry.Name()
		if entry.IsDir() {
			if path != searchDir && searchIgnoredDirs[strings.ToLower(name)] {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			relCheck, relErr := filepath.Rel(absRoot, path)
			if relErr != nil {
				return nil
			}
			if _, resErr := workspace.Resolve(root, relCheck); resErr != nil {
				return nil
			}
		}

		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if glob != "" && !matchGlob(glob, name, relSlash) {
			return nil
		}

		filesScanned++
		if filesScanned > searchMaxFilesScanned {
			truncated = true
			stopWalk = true
			return fs.SkipAll
		}

		info, err := entry.Info()
		if err != nil || info.IsDir() || info.Size() == 0 || info.Size() > searchMaxFileBytes {
			return nil
		}

		matches, fileErr := scanFileForQuery(path, relSlash, re, needle, args.CaseSensitive, maxResults-totalMatches)
		if fileErr != nil || len(matches) == 0 {
			return nil
		}
		for _, line := range matches {
			if b.Len()+len(line)+1 > searchMaxOutputBytes {
				truncated = true
				stopWalk = true
				return fs.SkipAll
			}
			b.WriteString(line)
			b.WriteByte('\n')
			totalMatches++
			if totalMatches >= maxResults {
				truncated = true
				stopWalk = true
				return fs.SkipAll
			}
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return "", err
	}

	if totalMatches == 0 {
		return fmt.Sprintf("Nenhuma ocorrência encontrada para %q (%d arquivos verificados).", query, filesScanned), nil
	}
	var hdr strings.Builder
	fmt.Fprintf(&hdr, "# search_text %q — %d ocorrência(s) (%d arquivos verificados", query, totalMatches, filesScanned)
	if truncated {
		hdr.WriteString("; truncado — refine com path ou glob se precisar de mais")
	}
	hdr.WriteString(")\n")
	hdr.WriteString(b.String())
	return strings.TrimRight(hdr.String(), "\n"), nil
}

func scanFileForQuery(fullPath, relSlash string, re *regexp.Regexp, needle string, caseSensitive bool, remaining int) ([]string, error) {
	if remaining <= 0 {
		return nil, nil
	}
	f, err := os.Open(fullPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var probe [512]byte
	n, err := f.Read(probe[:])
	if err != nil && err != io.EOF {
		return nil, err
	}
	if bytes.IndexByte(probe[:n], 0) >= 0 {
		return nil, nil // binary file
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	var out []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), searchMaxFileBytes+1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		matched := false
		if re != nil {
			matched = re.MatchString(line)
		} else if caseSensitive {
			matched = strings.Contains(line, needle)
		} else {
			matched = strings.Contains(strings.ToLower(line), needle)
		}
		if !matched {
			continue
		}
		trimmed := strings.TrimSpace(line)
		trimmed = strx.TruncateBytes(trimmed, searchMaxLineBytes, "…")
		out = append(out, fmt.Sprintf("%s:%d: %s", relSlash, lineNo, trimmed))
		if len(out) >= remaining {
			break
		}
	}
	return out, nil
}

func runFindFiles(ctx interface{ Done() <-chan struct{} }, root string, args findFilesArgs) (string, error) {
	pattern := strings.TrimSpace(args.Pattern)
	if pattern == "" {
		return "", fmt.Errorf("informe o parâmetro pattern para find_files")
	}
	searchDir, absRoot, err := resolveSearchBase(root, args.Path)
	if err != nil {
		return "", err
	}

	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = findDefaultResults
	}
	if maxResults > findHardMaxResults {
		maxResults = findHardMaxResults
	}

	hasGlobMeta := strings.ContainsAny(pattern, "*?[")
	lowPattern := strings.ToLower(strings.ReplaceAll(pattern, "\\", "/"))

	var matches []string
	filesScanned := 0
	truncated := false

	err = filepath.WalkDir(searchDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if ctx != nil {
			select {
			case <-ctx.Done():
				return fs.SkipAll
			default:
			}
		}
		name := entry.Name()
		if entry.IsDir() {
			if path != searchDir && searchIgnoredDirs[strings.ToLower(name)] {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			relCheck, relErr := filepath.Rel(absRoot, path)
			if relErr != nil {
				return nil
			}
			if _, resErr := workspace.Resolve(root, relCheck); resErr != nil {
				return nil
			}
		}

		filesScanned++
		if filesScanned > searchMaxFilesScanned {
			truncated = true
			return fs.SkipAll
		}

		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)

		ok := false
		if hasGlobMeta {
			ok = matchGlob(pattern, name, relSlash)
		} else {
			ok = strings.Contains(strings.ToLower(relSlash), lowPattern)
		}
		if !ok {
			return nil
		}
		matches = append(matches, relSlash)
		if len(matches) >= maxResults {
			truncated = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return "", err
	}

	if len(matches) == 0 {
		return fmt.Sprintf("Nenhum arquivo encontrado para %q (%d arquivos verificados).", pattern, filesScanned), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# find_files %q — %d arquivo(s)", pattern, len(matches))
	if truncated {
		b.WriteString(" (truncado — refine pattern ou path)")
	}
	b.WriteByte('\n')
	for _, m := range matches {
		b.WriteString(m)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func resolveSearchBase(root, relPath string) (searchDir, absRoot string, err error) {
	absRoot, err = filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	if eval, evalErr := filepath.EvalSymlinks(absRoot); evalErr == nil {
		absRoot = eval
	}
	if strings.TrimSpace(relPath) == "" || strings.TrimSpace(relPath) == "." {
		return absRoot, absRoot, nil
	}
	resolved, err := workspace.Resolve(root, relPath)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", err
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("%s não é um diretório", relPath)
	}
	return resolved, absRoot, nil
}

func matchGlob(glob, baseName, relSlash string) bool {
	g := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(glob, "\\", "/")))
	if g == "" {
		return true
	}
	if ok, _ := filepath.Match(g, strings.ToLower(baseName)); ok {
		return true
	}
	if ok, _ := filepath.Match(g, strings.ToLower(relSlash)); ok {
		return true
	}
	// Support simple "**/suffix" patterns such as "**/*.ts"
	if strings.HasPrefix(g, "**/") {
		sub := strings.TrimPrefix(g, "**/")
		if ok, _ := filepath.Match(sub, strings.ToLower(baseName)); ok {
			return true
		}
		if ok, _ := filepath.Match(sub, strings.ToLower(relSlash)); ok {
			return true
		}
	}
	return false
}
