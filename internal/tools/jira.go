package tools

import (
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/gilvan-Borges/HiveMind/internal/jirax"
)

func (d Deps) addJira(out *[]tool.Tool) error {
	if d.Jira == nil || !d.Jira.Enabled() {
		return nil
	}
	get, err := functiontool.New(functiontool.Config{
		Name:        "jira_get",
		Description: "Lê uma issue do Jira Cloud (incluindo tipo, status, issue pai e lista de subtasks/tarefas filhas com seus status).",
	}, func(ctx agent.Context, args struct {
		Key string `json:"key" jsonschema:"Chave da issue, por exemplo PROJ-123."`
	}) (textResult, error) {
		issue, err := d.Jira.GetIssue(ctx, args.Key)
		if err != nil {
			return textResult{}, err
		}
		var b strings.Builder
		typeTag := ""
		if issue.IssueType != "" {
			typeTag = " (" + issue.IssueType + ")"
		}
		fmt.Fprintf(&b, "%s%s [%s] %s\n", issue.Key, typeTag, issue.Status, issue.Summary)
		if issue.ParentKey != "" {
			fmt.Fprintf(&b, "Pai: %s — %s\n", issue.ParentKey, issue.ParentSummary)
		}
		if issue.Description != "" {
			fmt.Fprintf(&b, "\n[Descrição]\n%s\n", issue.Description)
		}
		// Merge inline subtasks + JQL child issues (for Epics/Stories whose children are Tasks).
		seenSub := map[string]bool{}
		var children []jirax.SubtaskItem
		for _, st := range issue.Subtasks {
			seenSub[strings.ToUpper(st.Key)] = true
			children = append(children, st)
		}
		if jqlChildren, sErr := d.Jira.Search(ctx, fmt.Sprintf(`parent = "%s" ORDER BY key ASC`, issue.Key), 40); sErr == nil {
			for _, ch := range jqlChildren {
				k := strings.ToUpper(ch.Key)
				if !seenSub[k] {
					seenSub[k] = true
					children = append(children, jirax.SubtaskItem{Key: ch.Key, Summary: ch.Summary, Status: ch.Status})
				}
			}
		}
		if len(children) > 0 {
			fmt.Fprintf(&b, "\n[Subtasks / Tasks Filhas (%d)]\n", len(children))
			for _, st := range children {
				fmt.Fprintf(&b, "- %s [%s] %s\n", st.Key, st.Status, st.Summary)
			}
		}
		return textResult{Text: strings.TrimSpace(b.String())}, nil
	})
	if err != nil {
		return err
	}
	search, err := functiontool.New(functiontool.Config{
		Name:        "jira_search",
		Description: "Pesquisa issues e subtasks no Jira com JQL (retorna chave, status, tipo, pai e título).",
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
			status := issue.Status
			if status == "" {
				status = "Sem status"
			}
			parent := ""
			if issue.ParentKey != "" {
				parent = fmt.Sprintf(" (pai: %s)", issue.ParentKey)
			}
			subCount := ""
			if len(issue.Subtasks) > 0 {
				subCount = fmt.Sprintf(" [%d subtask(s)]", len(issue.Subtasks))
			}
			fmt.Fprintf(&b, "%s [%s]%s%s — %s\n", issue.Key, status, parent, subCount, issue.Summary)
		}
		if b.Len() == 0 {
			return textResult{Text: "Nenhuma issue encontrada para o JQL informado."}, nil
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
