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
	"github.com/gilvan-Borges/HiveMind/internal/shellx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// Deps are the clients a tool set closes over. Each developer gets its own
// Root so parallel agents cannot write into each other's worktrees.
type Deps struct {
	Root       string
	Shell      shellx.Runner
	Allowlist  []string
	Confirm    shellx.Confirmer
	Git        gitx.Git
	GitHub     githubx.Client
	Jira       jirax.Client
	JiraKey    string
	BaseBranch string
}

// ForRole returns the tools a role is allowed to call.
func ForRole(role string, deps Deps) ([]tool.Tool, error) {
	if deps.Shell == nil {
		deps.Shell = shellx.Local{}
	}
	if deps.Git == nil {
		deps.Git = gitx.Local{}
	}
	if deps.Allowlist == nil {
		deps.Allowlist = team.DefaultAllowlist
	}
	var out []tool.Tool
	add := func(t tool.Tool, err error) error {
		if err != nil {
			return err
		}
		out = append(out, t)
		return nil
	}
	switch team.NormalizeRole(role) {
	case team.RoleProjectManager:
		if err := add(deps.canRead()); err != nil {
			return nil, err
		}
		if err := add(deps.canList()); err != nil {
			return nil, err
		}
		if err := add(deps.gitStatus()); err != nil {
			return nil, err
		}
		if err := deps.addJira(&out); err != nil {
			return nil, err
		}
		if err := deps.addGitHubRead(&out); err != nil {
			return nil, err
		}
	case team.RoleTechLead:
		if err := add(deps.canRead()); err != nil {
			return nil, err
		}
		if err := add(deps.canList()); err != nil {
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
	case team.RoleArchitect:
		if err := add(deps.canRead()); err != nil {
			return nil, err
		}
		if err := add(deps.canList()); err != nil {
			return nil, err
		}
		if err := add(deps.canWrite()); err != nil {
			return nil, err
		}
		if err := add(deps.gitStatus()); err != nil {
			return nil, err
		}
	case team.RoleDeveloper:
		if err := add(deps.canRead()); err != nil {
			return nil, err
		}
		if err := add(deps.canList()); err != nil {
			return nil, err
		}
		if err := add(deps.canWrite()); err != nil {
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
		if err := add(deps.gitCommit()); err != nil {
			return nil, err
		}
		if err := add(deps.gitPush()); err != nil {
			return nil, err
		}
		if err := add(deps.gitPull()); err != nil {
			return nil, err
		}
		if err := deps.addGitHubWrite(&out); err != nil {
			return nil, err
		}
	case team.RoleQA:
		if err := add(deps.canRead()); err != nil {
			return nil, err
		}
		if err := add(deps.canList()); err != nil {
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
	return out, nil
}

type textResult struct {
	Text string `json:"text"`
}

type pathArgs struct {
	Path string `json:"path" jsonschema:"Caminho relativo ao diretório de trabalho do agente."`
}

func (d Deps) canRead() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "read_file",
		Description: "Lê um arquivo de texto dentro do workspace do agente.",
	}, func(_ agent.Context, args pathArgs) (textResult, error) {
		path, err := workspace.ResolveWithOptions(d.Root, args.Path, workspace.Options{AllowMemoryRead: true})
		if err != nil {
			return textResult{}, err
		}
		f, err := os.Open(path)
		if err != nil {
			return textResult{}, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return textResult{}, err
		}
		if info.IsDir() {
			return textResult{}, fmt.Errorf("%s é um diretório", args.Path)
		}
		const limit = 64 * 1024
		data, err := io.ReadAll(io.LimitReader(f, limit+8))
		if err != nil {
			return textResult{}, err
		}
		text := strx.TruncateBytes(string(data), limit, "\n…(arquivo truncado)")
		return textResult{Text: text}, nil
	})
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
			if strings.EqualFold(name, ".git") || strings.EqualFold(name, ".hivemind") {
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
		Description: "Grava um arquivo de texto dentro do workspace do agente, criando pastas se preciso.",
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

type shellArgs struct {
	Command string `json:"command" jsonschema:"Comando em formato argv, por exemplo: go test ./..."`
	Dir     string `json:"dir,omitempty" jsonschema:"Diretório relativo ao workspace. Vazio usa a raiz do agente."`
}

func (d Deps) canShell() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "run_shell",
		Description: "Executa um comando permitido dentro do workspace. Comandos fora da lista são recusados. Comandos destrutivos pedem confirmação.",
	}, func(ctx agent.Context, args shellArgs) (textResult, error) {
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
		Description: "Mostra o resumo do diff em relação ao HEAD.",
	}, func(ctx agent.Context, _ struct {
		Unused string `json:"unused,omitempty" jsonschema:"Campo ignorado."`
	}) (textResult, error) {
		out, err := d.Git.Diff(ctx, d.Root)
		out = strx.TruncateBytes(out, 16*1024, "\n…(diff truncado)")
		return textResult{Text: out}, err
	})
}

func (d Deps) gitCommit() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "git_commit",
		Description: "Faz stage de todas as mudanças do worktree e cria um commit.",
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
		Description: "Envia a branch atual para origin. Não usa --force.",
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
		Description: "Atualiza a branch atual com git pull --ff-only.",
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
	comments, err := functiontool.New(functiontool.Config{
		Name:        "github_issue_comments",
		Description: "Lê comentários de uma issue ou pull request do GitHub.",
	}, func(ctx agent.Context, args struct {
		Number int `json:"number" jsonschema:"Número da issue ou do pull request."`
	}) (textResult, error) {
		list, err := d.GitHub.ListIssueComments(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		var b strings.Builder
		for _, c := range list {
			fmt.Fprintf(&b, "%s: %s\n", c.Author, c.Body)
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
	*out = append(*out, comments, ci)
	return nil
}

func (d Deps) addGitHubWrite(out *[]tool.Tool) error {
	if err := d.addGitHubRead(out); err != nil {
		return err
	}
	if d.GitHub == nil || !d.GitHub.Enabled() {
		return nil
	}
	create, err := functiontool.New(functiontool.Config{
		Name:        "github_create_pr",
		Description: "Abre um pull request da branch atual para a branch base.",
	}, func(ctx agent.Context, args struct {
		Title string `json:"title" jsonschema:"Título do pull request. Inclua a chave Jira quando houver."`
		Body  string `json:"body" jsonschema:"Descrição do pull request."`
		Base  string `json:"base,omitempty" jsonschema:"Branch de destino. Vazio usa a branch base do projeto."`
	}) (textResult, error) {
		head, err := d.Git.Run(ctx, d.Root, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return textResult{}, err
		}
		base := args.Base
		if base == "" {
			base = d.BaseBranch
		}
		if base == "" {
			base = "main"
		}
		pr, err := d.GitHub.CreatePullRequest(ctx, githubx.CreatePR{
			Title: args.Title,
			Body:  args.Body,
			Head:  strings.TrimSpace(head),
			Base:  base,
		})
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: pr.URL}, nil
	})
	if err != nil {
		return err
	}
	*out = append(*out, create)
	return nil
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
	*out = append(*out, get, search, sub, tr)
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
		if actions := ctx.Actions(); actions != nil {
			if actions.StateDelta == nil {
				actions.StateDelta = map[string]any{}
			}
			actions.StateDelta["qa_approval_note"] = args.Note
			actions.StateDelta["qa_feedback"] = ""
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
		if actions := ctx.Actions(); actions != nil {
			if actions.StateDelta == nil {
				actions.StateDelta = map[string]any{}
			}
			actions.StateDelta["qa_feedback"] = args.Notes
		}
		return textResult{Text: "ajustes pedidos: " + args.Notes}, nil
	})
}
