package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// ResidentBroker is the single policy-enforcing gateway for resident agent tool
// calls (see docs/SPEC_HIVE_AGENT.md §4). ADK function tools registered on the
// resident never touch the filesystem or OS directly; they only construct a
// structured call and return the broker's fenced result.
type ResidentBroker interface {
	Execute(ctx context.Context, name string, args map[string]any) (string, error)
}

type residentListProjectsArgs struct{}

type residentMemorySearchArgs struct {
	Query      string `json:"query" jsonschema:"Termos ou assunto para consultar na memória pessoal local desta conta."`
	Workstream string `json:"workstream,omitempty" jsonschema:"Workstream opcional para filtrar fatos específicos."`
}

// ForResidentReadOnly returns the Phase 1 read-only toolset for the Hive
// resident agent. Every tool is a thin wrapper over broker.Execute — no raw
// write, exec, network, credentials, or destructive tool is registered.
func ForResidentReadOnly(broker ResidentBroker) ([]tool.Tool, error) {
	if broker == nil {
		return nil, fmt.Errorf("broker do agente residente é obrigatório")
	}

	var out []tool.Tool
	add := func(t tool.Tool, err error) error {
		if err != nil {
			return err
		}
		out = append(out, t)
		return nil
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "read_file",
		Description: "Lê um arquivo de texto dentro do workspace ativo (somente leitura). Aceita start_line e end_line opcionais (1-based, até 400 linhas).",
	}, func(ctx agent.Context, args readFileArgs) (textResult, error) {
		m := map[string]any{"path": args.Path}
		if args.StartLine > 0 {
			m["start_line"] = args.StartLine
		}
		if args.EndLine > 0 {
			m["end_line"] = args.EndLine
		}
		res, err := broker.Execute(ctx, "read_file", m)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "list_dir",
		Description: "Lista arquivos e subdiretórios dentro do workspace ativo (somente leitura).",
	}, func(ctx agent.Context, args pathArgs) (textResult, error) {
		res, err := broker.Execute(ctx, "list_dir", map[string]any{"path": args.Path})
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "search_text",
		Description: "Busca ocorrências de texto ou regex (Go RE2) nos arquivos do workspace ativo (somente leitura).",
	}, func(ctx agent.Context, args searchTextArgs) (textResult, error) {
		m := map[string]any{
			"query":          args.Query,
			"path":           args.Path,
			"glob":           args.Glob,
			"regex":          args.Regex,
			"case_sensitive": args.CaseSensitive,
		}
		if args.MaxResults > 0 {
			m["max_results"] = args.MaxResults
		}
		res, err := broker.Execute(ctx, "search_text", m)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "find_files",
		Description: "Encontra arquivos no workspace ativo por nome ou padrão glob (somente leitura).",
	}, func(ctx agent.Context, args findFilesArgs) (textResult, error) {
		m := map[string]any{
			"pattern": args.Pattern,
			"path":    args.Path,
		}
		if args.MaxResults > 0 {
			m["max_results"] = args.MaxResults
		}
		res, err := broker.Execute(ctx, "find_files", m)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "list_projects",
		Description: "Lista os projetos/workspaces conhecidos e abertos na IDE neste computador (somente leitura).",
	}, func(ctx agent.Context, _ residentListProjectsArgs) (textResult, error) {
		res, err := broker.Execute(ctx, "list_projects", nil)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "memory_search",
		Description: "Consulta fatos salvos na memória pessoal local desta conta (somente leitura).",
	}, func(ctx agent.Context, args residentMemorySearchArgs) (textResult, error) {
		m := map[string]any{
			"query":      args.Query,
			"workstream": args.Workstream,
		}
		res, err := broker.Execute(ctx, "memory_search", m)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	return out, nil
}

// ReadFileReadOnly reads a jailed workspace file (reusing the same range and
// truncation guards as the squad's read_file tool).
func ReadFileReadOnly(root, rel string, startLine, endLine int) (string, error) {
	return readFileRange(root, readFileArgs{
		Path:      rel,
		StartLine: startLine,
		EndLine:   endLine,
	})
}

// ListDirReadOnly lists directory entries inside root, omitting .git, .hivemind,
// and .worktrees.
func ListDirReadOnly(root, rel string) ([]string, error) {
	path, err := workspace.Resolve(root, rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.EqualFold(name, ".git") || strings.EqualFold(name, ".hivemind") || strings.EqualFold(name, ".worktrees") {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		out = append(out, name)
		if len(out) >= 400 {
			out = append(out, "…")
			break
		}
	}
	return out, nil
}

// SearchTextReadOnly searches text or regex matches inside root.
func SearchTextReadOnly(ctx context.Context, root, query, subPath, glob string, regex, caseSensitive bool, maxResults int) (string, error) {
	return runSearchText(ctx, root, searchTextArgs{
		Query:         query,
		Path:          subPath,
		Glob:          glob,
		Regex:         regex,
		CaseSensitive: caseSensitive,
		MaxResults:    maxResults,
	})
}

// FindFilesReadOnly finds files matching pattern inside root.
func FindFilesReadOnly(ctx context.Context, root, pattern, subPath string, maxResults int) (string, error) {
	return runFindFiles(ctx, root, findFilesArgs{
		Pattern:    pattern,
		Path:       subPath,
		MaxResults: maxResults,
	})
}
