package tools

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/gilvan-Borges/HiveMind/internal/atomicx"
	"github.com/gilvan-Borges/HiveMind/internal/pathsec"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

type pathArgs struct {
	Path string `json:"path" jsonschema:"Caminho relativo ao diretório de trabalho do agente."`
}

type readFileArgs struct {
	Path      string `json:"path" jsonschema:"Caminho relativo ao diretório de trabalho do agente."`
	StartLine int    `json:"start_line,omitempty" jsonschema:"Linha inicial (1-based) opcional para ler apenas um trecho do arquivo."`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"Linha final inclusiva (1-based) opcional (máx. 400 linhas por chamada quando especificado)."`
}

func (d Deps) readFile(args readFileArgs) (string, error) {
	out, err := readFileRange(d.Root, args)
	if err != nil {
		return "", err
	}
	if d.tracker != nil {
		if resolved, rErr := workspace.ResolveWithOptions(d.Root, args.Path, workspace.Options{AllowMemoryRead: true}); rErr == nil {
			d.tracker.refresh(resolved)
		}
	}
	return out, nil
}

func (d Deps) canRead() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "read_file",
		Description: "Lê um arquivo de texto dentro do workspace do agente. Aceita start_line e end_line opcionais (1-based, até 400 linhas) para ler apenas o trecho relevante de arquivos grandes.",
	}, func(_ agent.Context, args readFileArgs) (textResult, error) {
		out, err := d.readFile(args)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: out}, nil
	})
}

func readFileRange(root string, args readFileArgs) (string, error) {
	// Spillover archives and the per-worktree vault are the scoped .hivemind
	// read carve-outs: they hold tool payloads and repo-map/obsidian notes.
	// Everything else under .hivemind stays sensitive.
	spill := pathsec.IsSpilloverPath(args.Path)
	vault := pathsec.IsVaultPath(args.Path)
	if !spill && !vault && pathsec.IsSensitivePath(args.Path) {
		return "", fmt.Errorf("leitura recusada: %s é um arquivo sensível (credencial, chave ou .env)", args.Path)
	}
	path, err := workspace.ResolveWithOptions(root, args.Path, workspace.Options{AllowMemoryRead: true, AllowSpilloverRead: spill, AllowVaultRead: vault})
	if err != nil {
		return "", err
	}
	if !spill && !vault && pathsec.IsSensitivePath(path) {
		return "", fmt.Errorf("leitura recusada: %s é um arquivo sensível (credencial, chave ou .env)", args.Path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s é um diretório", args.Path)
	}

	const limit = 64 * 1024
	if args.StartLine <= 0 && args.EndLine <= 0 {
		data, err := io.ReadAll(io.LimitReader(f, limit+8))
		if err != nil {
			return "", err
		}
		text := strx.TruncateBytes(string(data), limit, "\n…(arquivo truncado; use start_line e end_line para ler trechos específicos)")
		return text, nil
	}

	const maxRangeFileBytes = 2 * 1024 * 1024
	const maxWindowLines = 400
	data, err := io.ReadAll(io.LimitReader(f, maxRangeFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxRangeFileBytes {
		return "", fmt.Errorf("%s excede o limite de 2 MiB para leitura por faixa de linhas", args.Path)
	}

	rawLines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	// Drop a single trailing empty element produced by a final newline.
	if len(rawLines) > 0 && rawLines[len(rawLines)-1] == "" {
		rawLines = rawLines[:len(rawLines)-1]
	}
	totalLines := len(rawLines)
	if totalLines == 0 {
		return fmt.Sprintf("# %s (0 linhas — arquivo vazio)", args.Path), nil
	}

	start := args.StartLine
	if start <= 0 {
		start = 1
	}
	if start > totalLines {
		return "", fmt.Errorf("start_line=%d excede o total de linhas de %s (%d linhas)", start, args.Path, totalLines)
	}
	end := args.EndLine
	if end <= 0 {
		end = start + 199
	}
	if end < start {
		return "", fmt.Errorf("end_line=%d é menor que start_line=%d", end, start)
	}
	if end-start+1 > maxWindowLines {
		end = start + maxWindowLines - 1
	}
	if end > totalLines {
		end = totalLines
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s (linhas %d-%d de %d)\n", args.Path, start, end, totalLines)
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "L%d: %s\n", i, rawLines[i-1])
	}
	return strx.TruncateBytes(strings.TrimRight(b.String(), "\n"), limit, "\n…(trecho truncado; reduza a faixa start_line..end_line)"), nil
}

func (d Deps) canList() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "list_dir",
		Description: "Lista arquivos e pastas de um diretório dentro do workspace.",
	}, func(_ agent.Context, args pathArgs) (struct {
		Entries []string `json:"entries"`
	}, error) {
		if pathsec.IsSensitivePath(args.Path) {
			return struct {
				Entries []string `json:"entries"`
			}{}, fmt.Errorf("listagem recusada: %s é um caminho sensível", args.Path)
		}
		path, err := workspace.Resolve(d.Root, args.Path)
		if err != nil {
			return struct {
				Entries []string `json:"entries"`
			}{}, err
		}
		if pathsec.IsSensitivePath(path) {
			return struct {
				Entries []string `json:"entries"`
			}{}, fmt.Errorf("listagem recusada: %s é um caminho sensível", args.Path)
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return struct {
				Entries []string `json:"entries"`
			}{}, err
		}
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			name := e.Name()
			if strings.EqualFold(name, ".git") || strings.EqualFold(name, ".hivemind") || strings.EqualFold(name, ".worktrees") {
				continue
			}
			if pathsec.IsSensitivePath(name) || pathsec.IsSensitivePath(filepath.Join(args.Path, name)) {
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
		return struct {
			Entries []string `json:"entries"`
		}{Entries: out}, nil
	})
}

type writeArgs struct {
	Path    string `json:"path" jsonschema:"Caminho relativo ao diretório de trabalho do agente."`
	Content string `json:"content" jsonschema:"Conteúdo completo que substitui o arquivo."`
}

func (d Deps) writeFile(args writeArgs) (string, error) {
	if len(args.Content) > 1<<20 {
		return "", fmt.Errorf("conteúdo acima de 1 MiB")
	}
	if pathsec.IsSensitivePath(args.Path) {
		return "", fmt.Errorf("escrita recusada: %s é um arquivo sensível", args.Path)
	}
	path, err := workspace.Resolve(d.Root, args.Path)
	if err != nil {
		return "", err
	}
	if pathsec.IsSensitivePath(path) {
		return "", fmt.Errorf("escrita recusada: %s é um arquivo sensível", args.Path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := atomicx.WriteFile(path, []byte(args.Content), 0o644); err != nil {
		return "", err
	}
	d.tracker.refresh(path)
	return "arquivo gravado: " + args.Path, nil
}

func (d Deps) canWrite() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "write_file",
		Description: "Cria um arquivo novo ou substitui todo o conteúdo de um arquivo no workspace. Para alterar arquivos existentes, prefira edit_file (preserva encoding, quebras de linha e economiza tokens).",
	}, func(_ agent.Context, args writeArgs) (textResult, error) {
		out, err := d.writeFile(args)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: out}, nil
	})
}

type editArgs struct {
	Path       string `json:"path" jsonschema:"Caminho relativo ao diretório de trabalho do agente."`
	OldText    string `json:"old_text" jsonschema:"Trecho exato e contíguo a ser substituído no arquivo existente."`
	NewText    string `json:"new_text" jsonschema:"Novo texto que substituirá old_text (vazio remove o trecho)."`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"Quando true, substitui todas as ocorrências de old_text; por padrão exige correspondência única."`
}

func (d Deps) editFile(args editArgs) (string, error) {
	if err := d.tracker.checkNotStale(d.Root, args.Path); err != nil {
		return "", err
	}
	out, err := editFileSurgical(d.Root, args)
	if err != nil {
		return "", err
	}
	if d.tracker != nil {
		if resolved, rErr := workspace.Resolve(d.Root, args.Path); rErr == nil {
			d.tracker.refresh(resolved)
		}
	}
	return out, nil
}

func (d Deps) canEdit() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "edit_file",
		Description: "Edita cirurgicamente um arquivo existente substituindo old_text por new_text, preservando exatamente bytes não tocados, encoding original e quebras de linha (CRLF/LF). Sempre prefira edit_file a write_file ou scripts shell para modificar arquivos existentes.",
	}, func(_ agent.Context, args editArgs) (textResult, error) {
		out, err := d.editFile(args)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: out}, nil
	})
}

func editFileSurgical(root string, args editArgs) (string, error) {
	if args.OldText == "" {
		return "", fmt.Errorf("old_text não pode ser vazio; informe o trecho exato a substituir ou use write_file para criar arquivo novo")
	}
	if pathsec.IsSensitivePath(args.Path) {
		return "", fmt.Errorf("edição recusada: %s é um arquivo sensível", args.Path)
	}
	path, err := workspace.Resolve(root, args.Path)
	if err != nil {
		return "", err
	}
	if pathsec.IsSensitivePath(path) {
		return "", fmt.Errorf("edição recusada: %s é um arquivo sensível", args.Path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s é um diretório", args.Path)
	}
	const maxEditBytes = 2 << 20
	if info.Size() > maxEditBytes {
		return "", fmt.Errorf("%s excede o limite de 2 MiB para edit_file", args.Path)
	}
	rawBytes, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	raw := string(rawBytes)

	oldText := args.OldText
	newText := args.NewText
	count := strings.Count(raw, oldText)

	// Adapta quebras de linha automaticamente quando o arquivo usa CRLF (Windows)
	// e o modelo enviou LF, preservando o estilo original sem tocar o restante dos bytes.
	if count == 0 && strings.Contains(raw, "\r\n") && !strings.Contains(oldText, "\r\n") {
		oldCRLF := strings.ReplaceAll(oldText, "\n", "\r\n")
		if c := strings.Count(raw, oldCRLF); c > 0 {
			oldText = oldCRLF
			newText = strings.ReplaceAll(strings.ReplaceAll(newText, "\r\n", "\n"), "\n", "\r\n")
			count = c
		}
	} else if count == 0 && !strings.Contains(raw, "\r\n") && strings.Contains(oldText, "\r\n") {
		oldLF := strings.ReplaceAll(oldText, "\r\n", "\n")
		if c := strings.Count(raw, oldLF); c > 0 {
			oldText = oldLF
			newText = strings.ReplaceAll(newText, "\r\n", "\n")
			count = c
		}
	}

	if count == 0 {
		return "", fmt.Errorf("trecho old_text não encontrado em %s; use read_file(start_line, end_line) para conferir o texto exato", args.Path)
	}
	if count > 1 && !args.ReplaceAll {
		var lines []string
		offset := 0
		for len(lines) < 6 {
			idx := strings.Index(raw[offset:], oldText)
			if idx < 0 {
				break
			}
			pos := offset + idx
			lineNo := strings.Count(raw[:pos], "\n") + 1
			lines = append(lines, fmt.Sprintf("L%d", lineNo))
			offset = pos + len(oldText)
		}
		return "", fmt.Errorf("old_text aparece %d vezes em %s (%s); inclua 1-2 linhas de contexto ao redor para tornar único ou use replace_all=true", count, args.Path, strings.Join(lines, ", "))
	}

	firstPos := strings.Index(raw, oldText)
	firstLine := strings.Count(raw[:firstPos], "\n") + 1

	n := 1
	if args.ReplaceAll {
		n = -1
	}
	updated := strings.Replace(raw, oldText, newText, n)
	if len(updated) > maxEditBytes {
		return "", fmt.Errorf("conteúdo resultante acima de 2 MiB")
	}

	perm := info.Mode().Perm()
	if perm == 0 {
		perm = 0o644
	}
	if err := atomicx.WriteFile(path, []byte(updated), perm); err != nil {
		return "", err
	}

	oldLines := strings.Count(oldText, "\n") + 1
	newLines := strings.Count(newText, "\n") + 1
	if newText == "" {
		newLines = 0
	}
	replaced := 1
	if args.ReplaceAll {
		replaced = count
	}
	delta := (newLines - oldLines) * replaced
	deltaLabel := fmt.Sprintf("%+d", delta)
	if delta == 0 {
		deltaLabel = "0"
	}
	return fmt.Sprintf("arquivo editado cirurgicamente: %s (%d substituição(ões) a partir da linha L%d, delta %s linhas)", args.Path, replaced, firstLine, deltaLabel), nil
}

// docsPathAllowed valida caminhos de documentação permitidos na fase SDD
// (antes da aprovação dos docs, nenhum código pode ser criado ou editado).
func docsPathAllowed(rel string) bool {
	p := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/")))
	p = strings.TrimPrefix(p, "./")
	if p == "" || strings.HasPrefix(p, "../") || strings.Contains(p, "/../") {
		return false
	}
	for _, prefix := range []string{"docs/", "specs/", ".specify/", "spec/"} {
		if strings.HasPrefix(p, prefix) {
			return strings.HasSuffix(p, ".md")
		}
	}
	if p == "readme.md" {
		return true
	}
	return false
}

func (d Deps) writeDoc() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "write_doc",
		Description: "Fase SDD: grava APENAS documentação (docs/, specs/, .specify/, README.md — sempre .md). Código-fonte permanece bloqueado até a aprovação dos docs.",
	}, func(_ agent.Context, args writeArgs) (textResult, error) {
		if !docsPathAllowed(args.Path) {
			return textResult{}, fmt.Errorf("governança SDD: nesta fase só é permitido gravar documentação (.md em docs/, specs/, .specify/ ou README.md); código será criado apenas na fase de desenvolvimento após as aprovações — caminho recusado: %s", args.Path)
		}
		if len(args.Content) > 1<<20 {
			return textResult{}, fmt.Errorf("conteúdo acima de 1 MiB")
		}
		if pathsec.IsSensitivePath(args.Path) {
			return textResult{}, fmt.Errorf("escrita recusada: %s é um arquivo sensível", args.Path)
		}
		path, err := workspace.Resolve(d.Root, args.Path)
		if err != nil {
			return textResult{}, err
		}
		if pathsec.IsSensitivePath(path) {
			return textResult{}, fmt.Errorf("escrita recusada: %s é um arquivo sensível", args.Path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return textResult{}, err
		}
		if err := atomicx.WriteFile(path, []byte(args.Content), 0o644); err != nil {
			return textResult{}, err
		}
		return textResult{Text: "documento gravado: " + args.Path}, nil
	})
}
