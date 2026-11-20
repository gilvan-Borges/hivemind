// Package tools exposes workspace, shell, git, GitHub and Jira tools to ADK agents.
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

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/mcpx"
	"github.com/gilvan-Borges/HiveMind/internal/shellx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// Deps are the clients a tool set closes over. Each developer gets its own
// Root so parallel agents cannot write into each other's worktrees.
type Deps struct {
	Root                   string
	Role                   string
	Settings               team.Settings
	DisableGitWrite        bool
	DisableMerge           bool // Hard-gate: omite github_merge_pr do catálogo (usado no modo auto-review)
	ReviewCommentOnly      bool // Quando true, github_review_pr aceita apenas evento COMMENT
	AllowReadOnlyTestShell bool // Libera run_shell somente-leitura/testes (bloqueando commit/push/merge/PR) no Chefe em auto-review
	ReviewRequestedInObj   bool
	// DocsOnlyWrite libera APENAS o write_doc (gravação restrita a documentação)
	// para fases SDD, onde código permanece bloqueado até a aprovação dos docs.
	DocsOnlyWrite bool
	Shell         shellx.Runner
	Allowlist     []string
	Confirm       shellx.Confirmer
	Approve       func(prompt string) bool
	Git           gitx.Git
	GitHub        githubx.Client
	Jira          jirax.Client
	JiraKey       string
	JiraProject   string
	BaseBranch    string
	// MCP, quando preenchido, expõe só os servidores ligados e as duas
	// ferramentas leves de lista e de ligar sob aprovação.
	MCP *mcpx.Host
}

// ForRole returns the tools a role is allowed to call.
// Governance rule: ONLY the Chief (RoleProjectManager) or Architect (RoleArchitect)
// may commit, push, create PRs, reply/request-review on PRs, or merge PRs.
func ForRole(role string, deps Deps) ([]tool.Tool, error) {
	normRole := team.NormalizeRole(role)
	deps.Role = normRole
	if deps.Shell == nil {
		deps.Shell = shellx.Local{}
	}
	if deps.Git == nil {
		deps.Git = gitx.Local{}
	}
	if deps.Settings.IsFullAccess() {
		deps.Allowlist = []string{"*"}
		deps.Confirm = func(string) bool { return true }
	} else {
		if len(deps.Allowlist) == 0 {
			deps.Allowlist = deps.Settings.EffectiveAllowlist()
		}
		if !deps.Settings.NeedsShellConfirm() {
			deps.Confirm = func(string) bool { return true }
		}
	}

	var out []tool.Tool
	add := func(t tool.Tool, err error) error {
		if err != nil {
			return err
		}
		out = append(out, t)
		return nil
	}

	switch normRole {
	case team.RoleProjectManager:
		if err := add(deps.canRead()); err != nil {
			return nil, err
		}
		if err := add(deps.canList()); err != nil {
			return nil, err
		}
		if err := add(deps.canSearchText()); err != nil {
			return nil, err
		}
		if err := add(deps.canFindFiles()); err != nil {
			return nil, err
		}
		if err := add(deps.gitStatus()); err != nil {
			return nil, err
		}
		if err := add(deps.gitDiff()); err != nil {
			return nil, err
		}
		if !deps.DisableGitWrite {
			if err := add(deps.gitCommit()); err != nil {
				return nil, err
			}
			if err := add(deps.gitPush()); err != nil {
				return nil, err
			}
			if err := add(deps.gitPull()); err != nil {
				return nil, err
			}
		}
		if deps.AllowReadOnlyTestShell {
			if err := add(deps.canShell()); err != nil {
				return nil, err
			}
		}
		if err := deps.addJira(&out); err != nil {
			return nil, err
		}
		if deps.DocsOnlyWrite {
			if err := add(deps.writeDoc()); err != nil {
				return nil, err
			}
		}
		if err := deps.addGitHubGovernance(&out, !deps.DisableGitWrite); err != nil {
			return nil, err
		}

	case team.RoleTechLead:
		if err := add(deps.canRead()); err != nil {
			return nil, err
		}
		if err := add(deps.canList()); err != nil {
			return nil, err
		}
		if err := add(deps.canSearchText()); err != nil {
			return nil, err
		}
		if err := add(deps.canFindFiles()); err != nil {
			return nil, err
		}
		if err := add(deps.gitStatus()); err != nil {
			return nil, err
		}
		if err := add(deps.gitDiff()); err != nil {
			return nil, err
		}
		if err := deps.addJiraComment(&out); err != nil {
			return nil, err
		}
		if err := deps.addGitHubRead(&out); err != nil {
			return nil, err
		}

	case team.RoleArchitect:
		if err := add(deps.canRead()); err != nil {
			return nil, err
		}
		if err := add(deps.canList()); err != nil {
			return nil, err
		}
		if err := add(deps.canSearchText()); err != nil {
			return nil, err
		}
		if err := add(deps.canFindFiles()); err != nil {
			return nil, err
		}
		if err := add(deps.canWrite()); err != nil {
			return nil, err
		}
		if err := add(deps.canEdit()); err != nil {
			return nil, err
		}
		if err := add(deps.gitStatus()); err != nil {
			return nil, err
		}
		if err := add(deps.gitDiff()); err != nil {
			return nil, err
		}
		if !deps.DisableGitWrite {
			if err := add(deps.gitCommit()); err != nil {
				return nil, err
			}
			if err := add(deps.gitPush()); err != nil {
				return nil, err
			}
			if err := add(deps.gitPull()); err != nil {
				return nil, err
			}
		}
		if deps.AllowReadOnlyTestShell {
			if err := add(deps.canShell()); err != nil {
				return nil, err
			}
		}
		if err := deps.addGitHubGovernance(&out, !deps.DisableGitWrite); err != nil {
			return nil, err
		}

	case team.RoleDeveloper:
		// Developers implement and test inside their isolated worktree; they NEVER commit, push, or manage PRs.
		if err := add(deps.canRead()); err != nil {
			return nil, err
		}
		if err := add(deps.canList()); err != nil {
			return nil, err
		}
		if err := add(deps.canSearchText()); err != nil {
			return nil, err
		}
		if err := add(deps.canFindFiles()); err != nil {
			return nil, err
		}
		if err := add(deps.canWrite()); err != nil {
			return nil, err
		}
		if err := add(deps.canEdit()); err != nil {
			return nil, err
		}
		if err := add(deps.canShell()); err != nil {
			return nil, err
		}
		if err := add(deps.gitStatus()); err != nil {
			return nil, err
		}
		if err := add(deps.gitDiff()); err != nil {
			return nil, err
		}

	case team.RoleQA:
		if err := add(deps.canRead()); err != nil {
			return nil, err
		}
		if err := add(deps.canList()); err != nil {
			return nil, err
		}
		if err := add(deps.canSearchText()); err != nil {
			return nil, err
		}
		if err := add(deps.canFindFiles()); err != nil {
			return nil, err
		}
		if err := add(deps.canShell()); err != nil {
			return nil, err
		}
		if err := add(deps.gitDiff()); err != nil {
			return nil, err
		}
		if err := add(deps.qaApprove()); err != nil {
			return nil, err
		}
		if err := add(deps.qaChanges()); err != nil {
			return nil, err
		}
		if err := deps.addJiraComment(&out); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("papel sem ferramentas: %s", role)
	}
	if err := deps.addMCP(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (d Deps) canCommitAndManagePR() bool {
	return d.Role == team.RoleProjectManager || d.Role == team.RoleArchitect
}

type textResult struct {
	Text string `json:"text"`
}

type pathArgs struct {
	Path string `json:"path" jsonschema:"Caminho relativo ao diretório de trabalho do agente."`
}

type readFileArgs struct {
	Path      string `json:"path" jsonschema:"Caminho relativo ao diretório de trabalho do agente."`
	StartLine int    `json:"start_line,omitempty" jsonschema:"Linha inicial (1-based) opcional para ler apenas um trecho do arquivo."`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"Linha final inclusiva (1-based) opcional (máx. 400 linhas por chamada quando especificado)."`
}

func (d Deps) canRead() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "read_file",
		Description: "Lê um arquivo de texto dentro do workspace do agente. Aceita start_line e end_line opcionais (1-based, até 400 linhas) para ler apenas o trecho relevante de arquivos grandes.",
	}, func(_ agent.Context, args readFileArgs) (textResult, error) {
		out, err := readFileRange(d.Root, args)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: out}, nil
	})
}

func readFileRange(root string, args readFileArgs) (string, error) {
	path, err := workspace.ResolveWithOptions(root, args.Path, workspace.Options{AllowMemoryRead: true})
	if err != nil {
		return "", err
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
		path, err := workspace.Resolve(d.Root, args.Path)
		if err != nil {
			return struct {
				Entries []string `json:"entries"`
			}{}, err
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

func (d Deps) canWrite() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "write_file",
		Description: "Cria um arquivo novo ou substitui todo o conteúdo de um arquivo no workspace. Para alterar arquivos existentes, prefira edit_file (preserva encoding, quebras de linha e economiza tokens).",
	}, func(_ agent.Context, args writeArgs) (textResult, error) {
		if len(args.Content) > 1<<20 {
			return textResult{}, fmt.Errorf("conteúdo acima de 1 MiB")
		}
		path, err := workspace.Resolve(d.Root, args.Path)
		if err != nil {
			return textResult{}, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return textResult{}, err
		}
		if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
			return textResult{}, err
		}
		return textResult{Text: "arquivo gravado: " + args.Path}, nil
	})
}

type editArgs struct {
	Path       string `json:"path" jsonschema:"Caminho relativo ao diretório de trabalho do agente."`
	OldText    string `json:"old_text" jsonschema:"Trecho exato e contíguo a ser substituído no arquivo existente."`
	NewText    string `json:"new_text" jsonschema:"Novo texto que substituirá old_text (vazio remove o trecho)."`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"Quando true, substitui todas as ocorrências de old_text; por padrão exige correspondência única."`
}

func (d Deps) canEdit() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "edit_file",
		Description: "Edita cirurgicamente um arquivo existente substituindo old_text por new_text, preservando exatamente bytes não tocados, encoding original e quebras de linha (CRLF/LF). Sempre prefira edit_file a write_file ou scripts shell para modificar arquivos existentes.",
	}, func(_ agent.Context, args editArgs) (textResult, error) {
		out, err := editFileSurgical(d.Root, args)
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
	path, err := workspace.Resolve(root, args.Path)
	if err != nil {
		return "", err
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
	if err := os.WriteFile(path, []byte(updated), perm); err != nil {
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
		path, err := workspace.Resolve(d.Root, args.Path)
		if err != nil {
			return textResult{}, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return textResult{}, err
		}
		if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
			return textResult{}, err
		}
		return textResult{Text: "documento gravado: " + args.Path}, nil
	})
}

type shellArgs struct {
	Command string `json:"command" jsonschema:"Comando em formato argv, por exemplo: go test ./..."`
	Dir     string `json:"dir,omitempty" jsonschema:"Diretório relativo ao workspace. Vazio usa a raiz do agente."`
}

func (d Deps) canShell() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "run_shell",
		Description: "Executa um comando permitido dentro do workspace. Operações de git commit/push/merge e PR são bloqueadas neste modo.",
	}, func(ctx agent.Context, args shellArgs) (textResult, error) {
		if !d.canCommitAndManagePR() {
			if blocked, what := shellx.IsCommitOrPRCommand(args.Command); blocked {
				return textResult{}, fmt.Errorf("governança HiveMind: apenas o Chefe ou o Arquiteto podem executar %s (desenvolvedores e QA não comitam nem operam PR)", what)
			}
		} else if d.AllowReadOnlyTestShell || d.DisableGitWrite || d.DisableMerge {
			if blocked, what := shellx.IsCommitOrPRCommand(args.Command); blocked {
				return textResult{}, fmt.Errorf("governança auto-review: operação %s bloqueada neste modo (apenas leitura/testes permitidos; sem commit, push ou merge)", what)
			}
		}
		dir := d.Root
		if strings.TrimSpace(args.Dir) != "" {
			resolved, err := workspace.Resolve(d.Root, args.Dir)
			if err != nil {
				return textResult{}, err
			}
			dir = resolved
		}
		out, err := d.Shell.Run(ctx, dir, args.Command, d.Allowlist, d.Confirm)
		if err != nil {
			return textResult{Text: out}, err
		}
		return textResult{Text: out}, nil
	})
}

func (d Deps) gitStatus() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "git_status",
		Description: "Mostra o status curto do git no diretório de trabalho.",
	}, func(ctx agent.Context, _ struct {
		Unused string `json:"unused,omitempty" jsonschema:"Campo ignorado."`
	}) (textResult, error) {
		out, err := d.Git.Status(ctx, d.Root)
		return textResult{Text: out}, err
	})
}

func (d Deps) gitDiff() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "git_diff",
		Description: "Mostra o resumo do diff em relação ao HEAD (incluindo arquivos novos não rastreados).",
	}, func(ctx agent.Context, _ struct {
		Unused string `json:"unused,omitempty" jsonschema:"Campo ignorado."`
	}) (textResult, error) {
		out, err := d.Git.Diff(ctx, d.Root)
		if untracked, uErr := d.Git.Run(ctx, d.Root, "ls-files", "--others", "--exclude-standard"); uErr == nil && strings.TrimSpace(untracked) != "" {
			if strings.TrimSpace(out) != "" {
				out += "\n"
			}
			out += "[arquivos novos na worktree]\n" + strings.TrimSpace(untracked)
		}
		out = strx.TruncateBytes(out, 16*1024, "\n…(diff truncado)")
		return textResult{Text: out}, err
	})
}

func (d Deps) gitCommit() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "git_commit",
		Description: "Exclusivo do Chefe/Arquiteto: faz stage de todas as mudanças da worktree e cria um commit.",
	}, func(ctx agent.Context, args struct {
		Message string `json:"message" jsonschema:"Mensagem do commit. Inclua a chave Jira quando houver."`
	}) (textResult, error) {
		ok, err := d.Git.CommitAll(ctx, d.Root, args.Message)
		if err != nil {
			return textResult{}, err
		}
		if !ok {
			return textResult{Text: "nada para commitar"}, nil
		}
		return textResult{Text: "commit criado"}, nil
	})
}

func (d Deps) gitPush() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "git_push",
		Description: "Exclusivo do Chefe/Arquiteto: envia a branch atual da worktree para origin (sem --force).",
	}, func(ctx agent.Context, _ struct {
		Unused string `json:"unused,omitempty" jsonschema:"Campo ignorado."`
	}) (textResult, error) {
		branch, err := d.Git.Run(ctx, d.Root, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return textResult{}, err
		}
		branch = strings.TrimSpace(branch)
		if err := d.Git.Push(ctx, d.Root, "origin", branch); err != nil {
			return textResult{}, err
		}
		return textResult{Text: "push de " + branch}, nil
	})
}

func (d Deps) gitPull() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "git_pull",
		Description: "Exclusivo do Chefe/Arquiteto: atualiza a branch atual com git pull --ff-only.",
	}, func(ctx agent.Context, _ struct {
		Unused string `json:"unused,omitempty" jsonschema:"Campo ignorado."`
	}) (textResult, error) {
		out, err := d.Git.Run(ctx, d.Root, "pull", "--ff-only")
		return textResult{Text: out}, err
	})
}

func (d Deps) addGitHubRead(out *[]tool.Tool) error {
	if d.GitHub == nil || !d.GitHub.Enabled() {
		return nil
	}
	getPR, err := functiontool.New(functiontool.Config{
		Name:        "github_get_pr",
		Description: "Lê detalhes completos de um Pull Request via conector nativo GitHub: autoria (indica se o PR é NOSSO ou de terceiro), branches (head -> base), revisores pendentes, revisões formais (APPROVED / CHANGES_REQUESTED), comentários inline no código (com arquivo, linha e comment_id) e comentários gerais na conversa.",
	}, func(ctx agent.Context, args struct {
		Number int `json:"number" jsonschema:"Número do pull request."`
	}) (textResult, error) {
		pr, err := d.GitHub.GetPullRequest(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		if len(pr.IssueComments) == 0 && !hasFetchError(pr.CommentFetchErrors, "comentários gerais") {
			if extra, cErr := d.GitHub.ListIssueComments(ctx, args.Number); cErr == nil {
				pr.IssueComments = extra
			} else {
				pr.CommentFetchErrors = append(pr.CommentFetchErrors, fmt.Sprintf("comentários gerais: %v", cErr))
			}
		}
		me, _ := d.GitHub.CurrentUser(ctx)
		return textResult{Text: formatPRDetail(pr, pr.IssueComments, me)}, nil
	})
	if err != nil {
		return err
	}
	comments, err := functiontool.New(functiontool.Config{
		Name:        "github_issue_comments",
		Description: "Lê todos os comentários de uma issue ou pull request no GitHub (comentários gerais na conversa + comentários inline de revisão no código quando for PR).",
	}, func(ctx agent.Context, args struct {
		Number int `json:"number" jsonschema:"Número da issue ou do pull request."`
	}) (textResult, error) {
		general, err := d.GitHub.ListIssueComments(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		inline, _ := d.GitHub.ListPullRequestComments(ctx, args.Number)
		var b strings.Builder
		if len(inline) > 0 {
			b.WriteString("[Comentários Inline no Código]\n")
			for _, c := range inline {
				loc := c.Path
				if c.Line > 0 {
					loc = fmt.Sprintf("%s:%d", c.Path, c.Line)
				}
				ts := ""
				if strings.TrimSpace(c.CreatedAt) != "" {
					ts = " (" + c.CreatedAt + ")"
				}
				reply := ""
				if c.InReplyToID > 0 {
					reply = fmt.Sprintf(" [em resposta a #%d]", c.InReplyToID)
				}
				fmt.Fprintf(&b, "- [comment_id=%d] @%s em %s%s%s: %s\n", c.ID, c.Author, loc, ts, reply, strings.TrimSpace(c.Body))
			}
		}
		if len(general) > 0 {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString("[Comentários Gerais]\n")
			for _, c := range general {
				ts := ""
				if strings.TrimSpace(c.CreatedAt) != "" {
					ts = " (" + c.CreatedAt + ")"
				}
				fmt.Fprintf(&b, "- @%s%s: %s\n", c.Author, ts, strings.TrimSpace(c.Body))
			}
		}
		if b.Len() == 0 {
			return textResult{Text: fmt.Sprintf("Nenhum comentário encontrado em #%d.", args.Number)}, nil
		}
		return textResult{Text: b.String()}, nil
	})
	if err != nil {
		return err
	}
	prFiles, err := functiontool.New(functiontool.Config{
		Name:        "github_pr_files",
		Description: "Lista apenas os caminhos e contagens (+add -del) dos arquivos alterados em um Pull Request sem trazer o patch completo. Ideal para mapear PRs grandes antes de filtrar github_pr_diff por arquivo.",
	}, func(ctx agent.Context, args struct {
		Number int `json:"number" jsonschema:"Número do pull request."`
	}) (textResult, error) {
		files, err := d.GitHub.ListPullRequestFiles(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		if len(files) == 0 {
			return textResult{Text: fmt.Sprintf("Nenhum arquivo alterado encontrado no PR #%d.", args.Number)}, nil
		}
		var b strings.Builder
		totalAdd, totalDel := 0, 0
		for _, f := range files {
			totalAdd += f.Additions
			totalDel += f.Deletions
		}
		fmt.Fprintf(&b, "PR #%d — %d arquivo(s) alterado(s) (+%d -%d):\n", args.Number, len(files), totalAdd, totalDel)
		for _, f := range files {
			fmt.Fprintf(&b, "- %s [%s] (+%d -%d)\n", f.Filename, f.Status, f.Additions, f.Deletions)
		}
		return textResult{Text: strings.TrimRight(b.String(), "\n")}, nil
	})
	if err != nil {
		return err
	}
	prDiff, err := functiontool.New(functiontool.Config{
		Name:        "github_pr_diff",
		Description: "Lista os arquivos modificados e os trechos de diff (patch) de um Pull Request no GitHub. Aceita filtro opcional por caminho de arquivo.",
	}, func(ctx agent.Context, args struct {
		Number int    `json:"number" jsonschema:"Número do pull request."`
		File   string `json:"file,omitempty" jsonschema:"Filtro opcional por nome/trecho do arquivo para ver o diff completo daquele arquivo."`
	}) (textResult, error) {
		files, err := d.GitHub.ListPullRequestFiles(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		filter := strings.ToLower(strings.TrimSpace(args.File))
		var b strings.Builder
		fmt.Fprintf(&b, "Arquivos alterados no PR #%d (%d arquivo(s)):\n", args.Number, len(files))
		const maxTotalChars = 24000
		for _, f := range files {
			if filter != "" && !strings.Contains(strings.ToLower(f.Filename), filter) {
				continue
			}
			fmt.Fprintf(&b, "\n=== %s [%s] (+%d -%d) ===\n", f.Filename, f.Status, f.Additions, f.Deletions)
			if strings.TrimSpace(f.Patch) != "" {
				remaining := maxTotalChars - b.Len()
				if remaining <= 200 {
					b.WriteString("... (diff truncado; informe o parâmetro file para ver arquivos específicos)\n")
					break
				}
				patch := f.Patch
				if filter == "" && len(patch) > 4000 {
					patch = strx.TruncateBytes(patch, 4000, "\n... (patch truncado; passe file=\""+f.Filename+"\" para ver completo)")
				} else if len(patch) > remaining {
					patch = strx.TruncateBytes(patch, remaining, "\n... (truncado)")
				}
				b.WriteString(patch + "\n")
			}
		}
		return textResult{Text: b.String()}, nil
	})
	if err != nil {
		return err
	}
	listPRs, err := functiontool.New(functiontool.Config{
		Name:        "github_list_prs",
		Description: "Lista Pull Requests do repositório no GitHub com autor, branches, revisores pedidos e marcação se o PR é NOSSO ou aguarda nossa revisão.",
	}, func(ctx agent.Context, args struct {
		State string `json:"state,omitempty" jsonschema:"Estado: open (padrão), closed ou all."`
		Limit int    `json:"limit,omitempty" jsonschema:"Quantidade máxima (padrão 20, máx 100)."`
	}) (textResult, error) {
		if args.Limit <= 0 {
			args.Limit = 20
		}
		prs, err := d.GitHub.ListPullRequests(ctx, args.State, args.Limit)
		if err != nil {
			return textResult{}, err
		}
		if len(prs) == 0 {
			return textResult{Text: "Nenhum Pull Request encontrado."}, nil
		}
		me, _ := d.GitHub.CurrentUser(ctx)
		var b strings.Builder
		for _, pr := range prs {
			tag := ""
			if me != "" && strings.EqualFold(pr.Author, me) {
				tag = " [NOSSO]"
			} else if me != "" {
				for _, rr := range pr.RequestedReviewers {
					if strings.EqualFold(rr, me) {
						tag = " [AGUARDA SUA REVISÃO]"
						break
					}
				}
			}
			draft := ""
			if pr.Draft {
				draft = " (DRAFT)"
			}
			fmt.Fprintf(&b, "#%d%s%s — %s | autor=@%s | %s -> %s", pr.Number, tag, draft, pr.Title, pr.Author, pr.HeadRef, pr.BaseRef)
			if len(pr.RequestedReviewers) > 0 {
				fmt.Fprintf(&b, " | revisores=[%s]", strings.Join(pr.RequestedReviewers, ", "))
			}
			b.WriteString("\n")
		}
		return textResult{Text: b.String()}, nil
	})
	if err != nil {
		return err
	}
	getIssue, err := functiontool.New(functiontool.Config{
		Name:        "github_get_issue",
		Description: "Lê detalhes completos e comentários de uma Issue do GitHub.",
	}, func(ctx agent.Context, args struct {
		Number int `json:"number" jsonschema:"Número da issue no GitHub."`
	}) (textResult, error) {
		iss, err := d.GitHub.GetIssue(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Issue #%d: %s\nAutor: @%s | Estado: %s\nURL: %s\n", iss.Number, iss.Title, iss.Author, iss.State, iss.URL)
		if len(iss.Labels) > 0 {
			fmt.Fprintf(&b, "Labels: %s\n", strings.Join(iss.Labels, ", "))
		}
		if strings.TrimSpace(iss.Body) != "" {
			fmt.Fprintf(&b, "\n[Descrição]\n%s\n", iss.Body)
		}
		if len(iss.Comments) > 0 {
			b.WriteString("\n[Comentários]\n")
			for _, c := range iss.Comments {
				fmt.Fprintf(&b, "- @%s: %s\n", c.Author, strings.TrimSpace(c.Body))
			}
		}
		return textResult{Text: b.String()}, nil
	})
	if err != nil {
		return err
	}
	ci, err := functiontool.New(functiontool.Config{
		Name:        "github_ci_status",
		Description: "Lê o status combinado de CI de um commit ou ref.",
	}, func(ctx agent.Context, args struct {
		Ref string `json:"ref" jsonschema:"SHA, branch ou tag."`
	}) (textResult, error) {
		state, err := d.GitHub.CIStatus(ctx, args.Ref)
		return textResult{Text: state}, err
	})
	if err != nil {
		return err
	}
	*out = append(*out, getPR, comments, prFiles, prDiff, listPRs, getIssue, ci)
	return nil
}

func hasFetchError(errs []string, prefix string) bool {
	for _, e := range errs {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

func (d Deps) addGitHubGovernance(out *[]tool.Tool, includeCreateFromWorktree bool) error {
	if err := d.addGitHubRead(out); err != nil {
		return err
	}
	if d.GitHub == nil || !d.GitHub.Enabled() {
		return nil
	}

	if includeCreateFromWorktree {
		create, err := functiontool.New(functiontool.Config{
			Name:        "github_create_pr",
			Description: "Exclusivo do Chefe/Arquiteto: abre um Pull Request da branch da worktree atual para a branch base (e opcionalmente solicita revisores).",
		}, func(ctx agent.Context, args struct {
			Title     string   `json:"title" jsonschema:"Título do pull request. Inclua a chave Jira quando houver."`
			Body      string   `json:"body" jsonschema:"Descrição do pull request."`
			Base      string   `json:"base,omitempty" jsonschema:"Branch de destino (ex: homolog ou main). Vazio usa a branch base do projeto."`
			Reviewers []string `json:"reviewers,omitempty" jsonschema:"Logins dos revisores no GitHub (opcional)."`
		}) (textResult, error) {
			head, err := d.Git.Run(ctx, d.Root, "rev-parse", "--abbrev-ref", "HEAD")
			if err != nil {
				return textResult{}, err
			}
			head = strings.TrimSpace(head)
			base := strings.TrimSpace(args.Base)
			if base == "" {
				base = d.BaseBranch
			}
			if base == "" {
				base = "main"
			}
			var pr githubx.PullRequest
			if existing, _ := d.GitHub.FindOpenPullRequestByHead(ctx, head); existing != nil {
				pr = *existing
			} else {
				pr, err = d.GitHub.CreatePullRequest(ctx, githubx.CreatePR{
					Title: args.Title,
					Body:  args.Body,
					Head:  head,
					Base:  base,
				})
				if err != nil {
					return textResult{}, err
				}
			}
			if len(args.Reviewers) > 0 && pr.Number > 0 {
				_ = d.GitHub.RequestReviewers(ctx, pr.Number, args.Reviewers)
			}
			return textResult{Text: pr.URL}, nil
		})
		if err != nil {
			return err
		}
		*out = append(*out, create)
	}

	commentPR, err := functiontool.New(functiontool.Config{
		Name:        "github_comment_pr",
		Description: "Comenta na conversa geral de um Pull Request. Permitido SOMENTE quando o PR é de sua própria autoria (ex: respondendo mudanças feitas após review) ou quando há pedido de revisão.",
	}, func(ctx agent.Context, args struct {
		Number int    `json:"number" jsonschema:"Número do pull request."`
		Body   string `json:"body" jsonschema:"Resumo das alterações realizadas ou resposta à revisão."`
	}) (textResult, error) {
		pr, err := d.GitHub.GetPullRequest(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		me, _ := d.GitHub.CurrentUser(ctx)
		if err := githubx.CheckPRCommentAllowed(pr, me, d.ReviewRequestedInObj); err != nil {
			return textResult{}, err
		}
		if err := d.GitHub.CommentOnIssue(ctx, args.Number, args.Body); err != nil {
			return textResult{}, err
		}
		return textResult{Text: fmt.Sprintf("comentário registrado no PR #%d", args.Number)}, nil
	})
	if err != nil {
		return err
	}

	replyComment, err := functiontool.New(functiontool.Config{
		Name:        "github_reply_review_comment",
		Description: "Responde diretamente dentro de uma thread de comentário inline de revisão no código (usando o comment_id retornado por github_get_pr ou github_issue_comments).",
	}, func(ctx agent.Context, args struct {
		Number    int    `json:"number" jsonschema:"Número do pull request."`
		CommentID int64  `json:"comment_id" jsonschema:"ID numérico do comentário inline ao qual responder."`
		Body      string `json:"body" jsonschema:"Resposta técnica para a thread inline."`
	}) (textResult, error) {
		pr, err := d.GitHub.GetPullRequest(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		me, _ := d.GitHub.CurrentUser(ctx)
		if err := githubx.CheckPRCommentAllowed(pr, me, d.ReviewRequestedInObj); err != nil {
			return textResult{}, err
		}
		if err := d.GitHub.ReplyToReviewComment(ctx, args.Number, args.CommentID, args.Body); err != nil {
			return textResult{}, err
		}
		return textResult{Text: fmt.Sprintf("resposta registrada no comentário inline #%d do PR #%d", args.CommentID, args.Number)}, nil
	})
	if err != nil {
		return err
	}

	reqReview, err := functiontool.New(functiontool.Config{
		Name:        "github_request_review",
		Description: "Solicita ou re-solicita revisão de um ou mais revisores em um PR de própria autoria (ex: após corrigir pontos pedidos pelo revisor).",
	}, func(ctx agent.Context, args struct {
		Number    int      `json:"number" jsonschema:"Número do pull request."`
		Reviewers []string `json:"reviewers" jsonschema:"Logins dos revisores a notificar para nova revisão."`
	}) (textResult, error) {
		pr, err := d.GitHub.GetPullRequest(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		me, _ := d.GitHub.CurrentUser(ctx)
		if me != "" && !strings.EqualFold(pr.Author, me) && !d.Settings.IsFullAccess() {
			return textResult{}, fmt.Errorf("governança PR: só é permitido solicitar revisores em PR de própria autoria (%s); PR #%d pertence a %q", me, pr.Number, pr.Author)
		}
		if err := d.GitHub.RequestReviewers(ctx, args.Number, args.Reviewers); err != nil {
			return textResult{}, err
		}
		return textResult{Text: fmt.Sprintf("revisão solicitada para %s no PR #%d", strings.Join(args.Reviewers, ", "), args.Number)}, nil
	})
	if err != nil {
		return err
	}

	reviewDesc := "Envia revisão oficial (APPROVE, REQUEST_CHANGES ou COMMENT) em um PR DE TERCEIRO. NUNCA use em PR de própria autoria; permitido SOMENTE quando há pedido de revisão."
	if d.ReviewCommentOnly {
		reviewDesc = "Envia comentário de revisão (APENAS event=COMMENT) em um PR DE TERCEIRO. Modo automático configurado como somente-comentário: APPROVE e REQUEST_CHANGES são bloqueados."
	}
	reviewPR, err := functiontool.New(functiontool.Config{
		Name:        "github_review_pr",
		Description: reviewDesc,
	}, func(ctx agent.Context, args struct {
		Number int    `json:"number" jsonschema:"Número do pull request."`
		Event  string `json:"event" jsonschema:"Decisão: APPROVE, REQUEST_CHANGES ou COMMENT."`
		Body   string `json:"body" jsonschema:"Parecer técnico da revisão."`
	}) (textResult, error) {
		if d.ReviewCommentOnly && !strings.EqualFold(strings.TrimSpace(args.Event), "COMMENT") {
			return textResult{}, fmt.Errorf("governança auto-review: modo configurado para somente comentar (comment_only); use event=\"COMMENT\" em vez de %q", args.Event)
		}
		pr, err := d.GitHub.GetPullRequest(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		me, _ := d.GitHub.CurrentUser(ctx)
		if err := githubx.CheckPRReviewAllowed(pr, me, args.Event, d.ReviewRequestedInObj); err != nil {
			return textResult{}, err
		}
		if err := d.GitHub.SubmitReview(ctx, args.Number, args.Event, args.Body); err != nil {
			return textResult{}, err
		}
		return textResult{Text: fmt.Sprintf("revisão %s enviada no PR #%d", strings.ToUpper(args.Event), args.Number)}, nil
	})
	if err != nil {
		return err
	}

	*out = append(*out, commentPR, replyComment, reqReview, reviewPR)
	if !d.DisableMerge {
		mergePR, err := functiontool.New(functiontool.Config{
			Name:        "github_merge_pr",
			Description: "Exclusivo do Chefe/Arquiteto: mescla um PR APÓS aprovação do revisor. Automático quando a branch alvo é homolog; quando a branch alvo é main, solicita permissão explícita ao usuário (salvo em modo full_access).",
		}, func(ctx agent.Context, args struct {
			Number      int    `json:"number" jsonschema:"Número do pull request."`
			Method      string `json:"method,omitempty" jsonschema:"Método: merge (padrão), squash ou rebase."`
			CommitTitle string `json:"commit_title,omitempty" jsonschema:"Título opcional do commit de merge."`
		}) (textResult, error) {
			pr, err := d.GitHub.GetPullRequest(ctx, args.Number)
			if err != nil {
				return textResult{}, err
			}
			if err := githubx.CheckPRMergeAllowed(pr); err != nil {
				return textResult{}, err
			}
			if d.Settings.NeedsMergePermission(pr.BaseRef) {
				approveFn := d.Approve
				if approveFn == nil && d.Confirm != nil {
					approveFn = func(prompt string) bool { return d.Confirm(prompt) }
				}
				prompt := fmt.Sprintf("O PR #%d (%s -> %s) já foi aprovado pelo revisor. Autoriza o Chefe a mesclar na branch protegida %q?", pr.Number, pr.HeadRef, pr.BaseRef, pr.BaseRef)
				if approveFn == nil || !approveFn(prompt) {
					return textResult{}, fmt.Errorf("governança PR: o PR #%d aponta para %q e exige permissão explícita do usuário antes de mesclar (revisor já aprovou, aguardando sua autorização)", pr.Number, pr.BaseRef)
				}
			}
			if err := d.GitHub.MergePullRequest(ctx, args.Number, args.Method, args.CommitTitle); err != nil {
				return textResult{}, err
			}
			return textResult{Text: fmt.Sprintf("PR #%d mesclado com sucesso em %s", pr.Number, pr.BaseRef)}, nil
		})
		if err != nil {
			return err
		}
		*out = append(*out, mergePR)
	}
	return nil
}

func formatPRDetail(pr githubx.PullRequestDetail, issueComments []githubx.Comment, currentUser string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PR #%d: %s\nAutor: @%s | Estado: %s | Mesclado: %v\nBranches: %s -> %s\nURL: %s\n",
		pr.Number, pr.Title, pr.Author, pr.State, pr.Merged, pr.HeadRef, pr.BaseRef, pr.URL)
	if pr.ChangedFiles > 0 || pr.Additions > 0 || pr.Deletions > 0 {
		fmt.Fprintf(&b, "Alterações: %d arquivo(s) (+%d -%d)\n", pr.ChangedFiles, pr.Additions, pr.Deletions)
	}
	me := strings.TrimSpace(currentUser)
	if me != "" {
		if strings.EqualFold(strings.TrimSpace(pr.Author), me) {
			fmt.Fprintf(&b, "Papel neste PR: PR NOSSO (autor = você, @%s). VOCÊ NÃO É O REVISOR — leia abaixo os feedbacks dos revisores externos (revisões, comentários inline e comentários gerais) e atenda/corrija o que foi solicitado.\n", me)
		} else {
			fmt.Fprintf(&b, "Papel neste PR: PR DE TERCEIRO (autor: @%s | usuário autenticado: @%s).\n", pr.Author, me)
		}
	}
	if len(pr.RequestedReviewers) > 0 {
		fmt.Fprintf(&b, "Revisores aguardando resposta: %s\n", strings.Join(pr.RequestedReviewers, ", "))
	}
	approved, reason := githubx.HasReviewerApproval(pr)
	if approved {
		b.WriteString("Status de aprovação: APROVADO por revisor\n")
	} else {
		fmt.Fprintf(&b, "Status de aprovação: PENDENTE (%s)\n", reason)
	}
	if len(pr.CommentFetchErrors) > 0 {
		fmt.Fprintf(&b, "AVISO ao ler feedbacks do GitHub: %s\n", strings.Join(pr.CommentFetchErrors, " | "))
	}
	if strings.TrimSpace(pr.Body) != "" {
		fmt.Fprintf(&b, "\n[Descrição]\n%s\n", pr.Body)
	}
	if len(pr.Reviews) > 0 {
		b.WriteString("\n[Revisões Formais]\n")
		for _, rv := range pr.Reviews {
			ts := ""
			if strings.TrimSpace(rv.SubmittedAt) != "" {
				ts = " (" + rv.SubmittedAt + ")"
			}
			roleTag := ""
			if me != "" && strings.EqualFold(rv.Author, pr.Author) {
				roleTag = " [autor]"
			}
			fmt.Fprintf(&b, "- @%s%s [%s]%s: %s\n", rv.Author, roleTag, rv.State, ts, strings.TrimSpace(rv.Body))
		}
	}
	if len(pr.ReviewComments) > 0 {
		b.WriteString("\n[Comentários Inline no Código]\n")
		for _, c := range pr.ReviewComments {
			loc := c.Path
			if c.Line > 0 {
				loc = fmt.Sprintf("%s:%d", c.Path, c.Line)
			}
			ts := ""
			if strings.TrimSpace(c.CreatedAt) != "" {
				ts = " (" + c.CreatedAt + ")"
			}
			reply := ""
			if c.InReplyToID > 0 {
				reply = fmt.Sprintf(" [resposta a #%d]", c.InReplyToID)
			}
			fmt.Fprintf(&b, "- [comment_id=%d] @%s em %s%s%s: %s\n", c.ID, c.Author, loc, ts, reply, strings.TrimSpace(c.Body))
			if strings.TrimSpace(c.DiffHunk) != "" {
				hunk := strx.TruncateBytes(strings.TrimSpace(c.DiffHunk), 400, "...")
				fmt.Fprintf(&b, "  trecho: %s\n", strings.ReplaceAll(hunk, "\n", " | "))
			}
		}
	}
	if len(issueComments) > 0 {
		b.WriteString("\n[Comentários Gerais na Conversa]\n")
		for _, c := range issueComments {
			ts := ""
			if strings.TrimSpace(c.CreatedAt) != "" {
				ts = " (" + c.CreatedAt + ")"
			}
			roleTag := ""
			if strings.EqualFold(c.Author, pr.Author) {
				roleTag = " [autor]"
			}
			fmt.Fprintf(&b, "- @%s%s%s: %s\n", c.Author, roleTag, ts, strings.TrimSpace(c.Body))
		}
	}
	if len(pr.Reviews) == 0 && len(pr.ReviewComments) == 0 && len(issueComments) == 0 && len(pr.CommentFetchErrors) == 0 {
		b.WriteString("\n[Feedbacks]\nNenhuma revisão formal, comentário inline ou comentário geral registrado neste PR até o momento.\n")
	}
	return b.String()
}

func (d Deps) addJira(out *[]tool.Tool) error {
	if d.Jira == nil || !d.Jira.Enabled() {
		return nil
	}
	get, err := functiontool.New(functiontool.Config{
		Name:        "jira_get",
		Description: "Lê uma issue do Jira Cloud.",
	}, func(ctx agent.Context, args struct {
		Key string `json:"key" jsonschema:"Chave da issue, por exemplo PROJ-123."`
	}) (textResult, error) {
		issue, err := d.Jira.GetIssue(ctx, args.Key)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: issue.Key + " [" + issue.Status + "] " + issue.Summary + "\n" + issue.Description}, nil
	})
	if err != nil {
		return err
	}
	search, err := functiontool.New(functiontool.Config{
		Name:        "jira_search",
		Description: "Pesquisa issues com JQL.",
	}, func(ctx agent.Context, args struct {
		JQL   string `json:"jql" jsonschema:"Consulta JQL."`
		Limit int    `json:"limit,omitempty" jsonschema:"Máximo de issues. Vazio usa 20."`
	}) (textResult, error) {
		issues, err := d.Jira.Search(ctx, args.JQL, args.Limit)
		if err != nil {
			return textResult{}, err
		}
		var b strings.Builder
		for _, issue := range issues {
			fmt.Fprintf(&b, "%s %s\n", issue.Key, issue.Summary)
		}
		return textResult{Text: b.String()}, nil
	})
	if err != nil {
		return err
	}
	sub, err := functiontool.New(functiontool.Config{
		Name:        "jira_create_subtask",
		Description: "Cria uma subtarefa na issue pai. Sem parent, usa a issue desta execução.",
	}, func(ctx agent.Context, args struct {
		Summary     string `json:"summary" jsonschema:"Resumo da subtarefa."`
		Description string `json:"description" jsonschema:"Descrição da subtarefa."`
		Parent      string `json:"parent,omitempty" jsonschema:"Chave pai. Vazio usa a issue da execução."`
	}) (textResult, error) {
		parent := args.Parent
		if parent == "" {
			parent = d.JiraKey
		}
		if parent == "" {
			return textResult{}, fmt.Errorf("nenhuma issue pai; passe parent ou rode com --jira")
		}
		issue, err := d.Jira.CreateSubtask(ctx, parent, args.Summary, args.Description)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: "subtarefa " + issue.Key}, nil
	})
	if err != nil {
		return err
	}
	createIssue, err := functiontool.New(functiontool.Config{
		Name:        "jira_create_issue",
		Description: "Cria uma issue (Epic, Story, Task ou outro tipo) no projeto Jira. Sem project, usa o projeto padrão do time/conector.",
	}, func(ctx agent.Context, args struct {
		Project     string `json:"project,omitempty" jsonschema:"Chave do projeto Jira (ex: PROJ). Vazio usa o projeto padrão configurado."`
		Type        string `json:"type" jsonschema:"Tipo da issue: Epic, Story, Task (padrão Task)."`
		Summary     string `json:"summary" jsonschema:"Título da issue."`
		Description string `json:"description" jsonschema:"Descrição com critérios de aceite e referência à spec."`
		Parent      string `json:"parent,omitempty" jsonschema:"Chave pai (para Tasks/Stories sob um Epic)."`
	}) (textResult, error) {
		project := strings.ToUpper(strings.TrimSpace(args.Project))
		if project == "" {
			project = strings.ToUpper(strings.TrimSpace(d.JiraProject))
		}
		if project == "" {
			if _, _, _, def, _ := jirax.LoadCredentialsForWorkspace(d.Root); def != "" {
				project = def
			}
		}
		if project == "" && d.JiraKey != "" {
			if i := strings.IndexByte(d.JiraKey, '-'); i > 0 {
				project = d.JiraKey[:i]
			}
		}
		issue, err := d.Jira.CreateIssue(ctx, project, args.Type, args.Summary, args.Description, args.Parent)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: fmt.Sprintf("issue %s [%s] criada em %s", issue.Key, strings.TrimSpace(args.Type), project)}, nil
	})
	if err != nil {
		return err
	}
	tr, err := functiontool.New(functiontool.Config{
		Name:        "jira_transition",
		Description: "Move uma issue para outra coluna pelo nome ou id da transição.",
	}, func(ctx agent.Context, args struct {
		Key        string `json:"key,omitempty" jsonschema:"Chave da issue. Vazio usa a issue da execução."`
		Transition string `json:"transition" jsonschema:"Nome ou id da transição, por exemplo In Progress."`
	}) (textResult, error) {
		key := args.Key
		if key == "" {
			key = d.JiraKey
		}
		if err := d.Jira.Transition(ctx, key, args.Transition); err != nil {
			return textResult{}, err
		}
		return textResult{Text: "transição aplicada em " + key}, nil
	})
	if err != nil {
		return err
	}
	*out = append(*out, get, search, sub, createIssue, tr)
	if err := d.addJiraComment(out); err != nil {
		return err
	}
	return nil
}

func (d Deps) addJiraComment(out *[]tool.Tool) error {
	if d.Jira == nil || !d.Jira.Enabled() {
		return nil
	}
	comment, err := functiontool.New(functiontool.Config{
		Name:        "jira_comment",
		Description: "Comenta uma issue do Jira. Sem key, usa a issue da execução.",
	}, func(ctx agent.Context, args struct {
		Key  string `json:"key,omitempty" jsonschema:"Chave da issue. Vazio usa a issue da execução."`
		Body string `json:"body" jsonschema:"Texto do comentário."`
	}) (textResult, error) {
		key := args.Key
		if key == "" {
			key = d.JiraKey
		}
		if key == "" {
			return textResult{}, fmt.Errorf("nenhuma issue para comentar")
		}
		if err := d.Jira.Comment(ctx, key, args.Body); err != nil {
			return textResult{}, err
		}
		return textResult{Text: "comentário publicado em " + key}, nil
	})
	if err != nil {
		return err
	}
	*out = append(*out, comment)
	return nil
}

func (d Deps) qaApprove() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "qa_approve",
		Description: "Encerra o ciclo de QA quando os testes passam. Não chame se ainda houver falhas.",
	}, func(ctx agent.Context, args struct {
		Note string `json:"note" jsonschema:"Resumo do que foi verificado."`
	}) (textResult, error) {
		_ = ctx.State().Set("qa_approval_note", args.Note)
		_ = ctx.State().Set("qa_feedback", "")
		_ = ctx.State().Set("qa_approved", "1")
		if actions := ctx.Actions(); actions != nil {
			if actions.StateDelta == nil {
				actions.StateDelta = map[string]any{}
			}
			actions.StateDelta["qa_approval_note"] = args.Note
			actions.StateDelta["qa_feedback"] = ""
			actions.StateDelta["qa_approved"] = "1"
			actions.Escalate = true
		}
		return textResult{Text: "aprovado: " + args.Note}, nil
	})
}

func (d Deps) qaChanges() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "qa_request_changes",
		Description: "Devolve o trabalho aos desenvolvedores com o que precisa ser corrigido.",
	}, func(ctx agent.Context, args struct {
		Notes string `json:"notes" jsonschema:"O que falhou e como corrigir."`
	}) (textResult, error) {
		if err := ctx.State().Set("qa_feedback", args.Notes); err != nil {
			return textResult{}, err
		}
		_ = ctx.State().Set("qa_approved", "")
		if actions := ctx.Actions(); actions != nil {
			if actions.StateDelta == nil {
				actions.StateDelta = map[string]any{}
			}
			actions.StateDelta["qa_feedback"] = args.Notes
			actions.StateDelta["qa_approved"] = ""
		}
		return textResult{Text: "ajustes pedidos: " + args.Notes}, nil
	})
}
