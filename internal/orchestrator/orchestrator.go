// Package orchestrator runs a HiveMind team with ADK sequential, parallel and loop agents.
package orchestrator

import (
	"context"
	"fmt"
	"io"
	"iter"
	"os"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/agent/workflowagents/loopagent"
	"google.golang.org/adk/v2/agent/workflowagents/parallelagent"
	"google.golang.org/adk/v2/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/names"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/shellx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/tools"
	"github.com/gilvan-Borges/HiveMind/internal/ui"
)

// Event is a structured live update emitted by Runner when OnEvent is set.
type Event struct {
	Kind     string `json:"kind"` // "stage", "handoff", "text", "tool_call", "tool_result", "finish", "error"
	Stage    string `json:"stage,omitempty"`
	Author   string `json:"author,omitempty"`
	Label    string `json:"label,omitempty"`
	Text     string `json:"text,omitempty"`
	ToolName string `json:"toolName,omitempty"`
	Partial  bool   `json:"partial,omitempty"`
	Thought  bool   `json:"thought,omitempty"`
	Tokens   int32  `json:"tokens,omitempty"`
}

// Attachment represents an image or file attached by the user in chat.
type Attachment struct {
	Name     string `json:"name"`
	MIMEType string `json:"mimeType"`
	Data     []byte `json:"data"`
}

// Runner executes one team against a workspace.
type Runner struct {
	Workspace          string
	Project            team.File
	Team               team.Team
	Git                gitx.Git
	GitHub             githubx.Client
	Jira               jirax.Client
	Sessions           *sessionx.Store
	Models             map[string]model.LLM
	NewModel           func(ctx context.Context, a team.Agent) (model.LLM, error)
	Confirm            shellx.Confirmer
	Out                io.Writer
	Err                io.Writer
	Streaming          agent.StreamingMode
	OnEvent            func(Event)
	EnableTriage       bool
	EnableClosingChief bool
	integration        string
	issueKey           string
	objectiveText      string
	devSlices          []string
	chiefPlan          string
}

// Request is one user turn.
type Request struct {
	Objective   string
	JiraKey     string
	SessionID   string
	Attachments []Attachment
}

// Result is what the CLI prints after the team finishes.
type Result struct {
	SessionID    string
	Summary      string
	Branches     []string
	PullRequests []githubx.PullRequest
}

type devEnv struct {
	Agent  team.Agent
	ADK    string
	Root   string
	Branch string
}

// Run builds the ADK pipeline and streams events until it finishes.
func (r *Runner) Run(ctx context.Context, req Request) (Result, error) {
	var result Result
	if err := r.Team.ValidateForRun(); err != nil {
		return result, err
	}
	if r.Git == nil {
		r.Git = gitx.Local{}
	}
	if r.Out == nil {
		r.Out = os.Stdout
	}
	if r.Err == nil {
		r.Err = os.Stderr
	}
	if r.Jira == nil {
		if r.Project.Settings.JiraEnabled {
			r.Jira = jirax.FromEnv()
		} else {
			r.Jira = jirax.Disabled{Reason: "Jira está desativado na configuração do time"}
		}
	}
	if r.GitHub == nil {
		r.GitHub = r.detectGitHub(ctx)
	}
	if r.Sessions == nil {
		store, err := sessionx.Open(r.Workspace)
		if err != nil {
			return result, err
		}
		r.Sessions = store
	}

	objective := strings.TrimSpace(req.Objective)
	key := names.IssueKey(req.JiraKey)
	if req.JiraKey != "" && key == "" {
		return result, fmt.Errorf("chave Jira inválida: %s", req.JiraKey)
	}
	if key != "" {
		if !r.Jira.Enabled() {
			return result, fmt.Errorf("Jira é necessário para %s e está desativado ou sem credenciais", key)
		}
		issue, err := r.Jira.GetIssue(ctx, key)
		if err != nil {
			return result, err
		}
		block := issue.Key + ": " + issue.Summary
		if issue.Description != "" {
			block += "\n" + issue.Description
		}
		if objective == "" {
			objective = block
		} else {
			objective += "\n\nIssue " + block
		}
	}
	if objective == "" {
		return result, fmt.Errorf("informe um objetivo ou uma chave Jira")
	}

	r.issueKey = key
	r.objectiveText = objective
	r.devSlices = nil
	r.chiefPlan = ""

	sess, err := r.openSession(ctx, req.SessionID, objective, key)
	if err != nil {
		return result, err
	}
	result.SessionID = sess.ID()

	mode := r.Streaming
	if mode == "" {
		mode = agent.StreamingModeSSE
	}
	msg := buildUserContent(objective, req.Attachments)

	if r.EnableTriage {
		delegated, summary, err := r.runTriageChief(ctx, sess, msg, key, mode)
		if err != nil {
			if r.OnEvent != nil {
				r.OnEvent(Event{Kind: "error", Text: err.Error()})
			}
			return result, err
		}
		if !delegated {
			result.Summary = summary
			fmt.Fprintf(r.Out, "\nSessão %s concluída (resposta direta do Chefe).\n", result.SessionID)
			if r.OnEvent != nil {
				r.OnEvent(Event{Kind: "finish", Stage: "chief", Text: fmt.Sprintf("Sessão %s concluída.", result.SessionID)})
			}
			return result, nil
		}
		// Squad implementation continues with a follow-up kickoff prompt so the user prompt + images remain in session history.
		msg = genai.NewContentFromText("Prossigam com a implementação e validação conforme o plano e divisão de tarefas do Chefe.", genai.RoleUser)
	}

	devs, err := r.prepareDevs(ctx, key, objective)
	if err != nil {
		return result, err
	}
	root, labels, err := r.build(ctx, devs, key, objective)
	if err != nil {
		return result, err
	}

	adkRunner, err := runner.New(runner.Config{
		AppName:        sessionx.AppName,
		Agent:          root,
		SessionService: r.Sessions,
	})
	if err != nil {
		return result, err
	}
	printer := &ui.Printer{Out: r.Out, Labels: labels}
	for ev, err := range adkRunner.Run(ctx, sessionx.UserID, sess.ID(), msg, agent.RunConfig{
		StreamingMode: mode,
		MaxLLMCalls:   r.Project.Settings.MaxLLMCalls,
	}) {
		if err != nil {
			if r.OnEvent != nil {
				r.OnEvent(Event{Kind: "error", Text: err.Error()})
			}
			return result, err
		}
		printer.Event(ev)
		if r.OnEvent != nil && ev != nil {
			r.emitEvent(ev, labels)
		}
		if ev == nil || ev.Partial || ev.LLMResponse.Content == nil {
			continue
		}
		for _, part := range ev.LLMResponse.Content.Parts {
			if part != nil && part.Text != "" && !part.Thought {
				result.Summary = strings.TrimSpace(part.Text)
			}
		}
	}

	if err := r.finish(ctx, &result, devs, key, objective); err != nil {
		if r.OnEvent != nil {
			r.OnEvent(Event{Kind: "error", Text: err.Error()})
		}
		return result, err
	}
	fmt.Fprintf(r.Out, "\nSessão %s concluída.\n", result.SessionID)
	if r.OnEvent != nil {
		r.OnEvent(Event{Kind: "finish", Text: fmt.Sprintf("Sessão %s concluída.", result.SessionID)})
	}
	return result, nil
}

func (r *Runner) detectGitHub(ctx context.Context) githubx.Client {
	url, err := r.Git.RemoteURL(ctx, r.Workspace)
	if err != nil {
		return githubx.Disabled{Reason: "repositório sem remote origin"}
	}
	owner, repo, ok := githubx.ParseRemote(url)
	if !ok {
		return githubx.Disabled{Reason: "remote origin não é um repositório GitHub"}
	}
	return githubx.Detect(owner, repo)
}

func (r *Runner) prepareDevs(ctx context.Context, key, objective string) ([]devEnv, error) {
	agents := r.Team.ByRole(team.RoleDeveloper)
	envs := make([]devEnv, 0, len(agents))
	repo := r.Git.IsRepo(ctx, r.Workspace) && r.Git.HasCommit(ctx, r.Workspace)
	if !repo {
		fmt.Fprintln(r.Err, "aviso: o workspace não tem um commit Git; os desenvolvedores compartilham a pasta e não usam worktrees")
	}
	for _, a := range agents {
		adkName := names.ADK(a.Name)
		branch := names.Branch(key, a.Name, objective)
		root := r.Workspace
		if repo {
			wt, err := r.Git.EnsureWorktree(ctx, r.Workspace, adkName, branch)
			if err != nil {
				return nil, fmt.Errorf("worktree de %s: %w", a.Name, err)
			}
			root = wt
		}
		envs = append(envs, devEnv{Agent: a, ADK: adkName, Root: root, Branch: branch})
	}
	_ = gitx.EnsureHiveIgnore(r.Workspace)
	if repo {
		_, wt, err := r.Git.Integrate(ctx, r.Workspace, nil)
		if err == nil {
			r.integration = wt
		}
	}
	return envs, nil
}

func (r *Runner) build(ctx context.Context, devs []devEnv, key, objective string) (agent.Agent, map[string]string, error) {
	labels := map[string]string{}
	used := map[string]int{
		"pipeline_root": 1, "parallel_developers": 1, "loop_qa": 1, "sequential_qa": 1,
	}
	unique := func(name string) string {
		base := names.ADK(name)
		n := used[base]
		used[base]++
		if n == 0 {
			return base
		}
		return fmt.Sprintf("%s_%d", base, n+1)
	}
	stateKeys := []string{"objective", "jira_key", "chief_plan", "pm_plan", "tech_lead_plan", "architect_plan", "qa_feedback", "qa_report", "qa_approval_note"}

	var steps []agent.Agent
	addGroup := func(output string, agents []team.Agent, root, extra string) error {
		for i, a := range agents {
			name := unique(a.Name)
			keyName := output
			if output == "" {
				keyName = name + "_report"
			} else if i > 0 {
				keyName = fmt.Sprintf("%s_%d", output, i+1)
			}
			stateKeys = append(stateKeys, keyName)
			ag, err := r.llm(ctx, a, name, keyName, root, extra, key, stateKeys, false)
			if err != nil {
				return err
			}
			labels[name] = roleLabel(a.Role) + " · " + a.Name
			steps = append(steps, ag)
		}
		return nil
	}
	if !r.EnableTriage {
		if err := addGroup("pm_plan", r.Team.ByRole(team.RoleProjectManager), r.Workspace, ""); err != nil {
			return nil, nil, err
		}
		if err := addGroup("tech_lead_plan", r.Team.ByRole(team.RoleTechLead), r.Workspace, ""); err != nil {
			return nil, nil, err
		}
		if err := addGroup("architect_plan", r.Team.ByRole(team.RoleArchitect), r.Workspace, ""); err != nil {
			return nil, nil, err
		}
	} else {
		// In triage mode, the Chief already planned and sliced tasks; Tech Lead refines technical guardrails if needed.
		if err := addGroup("tech_lead_plan", r.Team.ByRole(team.RoleTechLead), r.Workspace, "Refine brevemente os pontos críticos de implementação sem repetir o plano do Chefe."); err != nil {
			return nil, nil, err
		}
	}

	var parallelSubs []agent.Agent
	var wtLines []string
	for idx, dev := range devs {
		name := unique(dev.Agent.Name)
		report := "dev_" + name + "_report"
		stateKeys = append(stateKeys, report)
		extra := fmt.Sprintf("Seu diretório de trabalho é %s.\nSua branch é %s.\nNão altere arquivos fora desse diretório.\nSe houver [qa_feedback] ou [qa_report], corrija prioritariamente todos os pontos apontados pelo QA.", dev.Root, dev.Branch)
		if idx < len(r.devSlices) && strings.TrimSpace(r.devSlices[idx]) != "" {
			extra += fmt.Sprintf("\n\n[Sua Subtarefa Exclusiva atribuída pelo Chefe]\n%s\nRestrinja suas edições aos arquivos desta subtarefa para evitar conflitos com outros desenvolvedores.", strings.TrimSpace(r.devSlices[idx]))
		} else if len(devs) > 1 {
			extra += fmt.Sprintf("\n\nVocê é o desenvolvedor %d de %d (%s). Respeite a divisão de arquivos do plano para não colidir com os demais desenvolvedores.", idx+1, len(devs), dev.Agent.Name)
		}
		ag, err := r.llm(ctx, dev.Agent, name, report, dev.Root, extra, key, stateKeys, false)
		if err != nil {
			return nil, nil, err
		}
		labels[name] = roleLabel(dev.Agent.Role) + " · " + dev.Agent.Name
		parallelSubs = append(parallelSubs, ag)
		wtLines = append(wtLines, dev.ADK+": "+dev.Root+" ("+dev.Branch+")")
	}
	parallel, err := parallelagent.New(parallelagent.Config{AgentConfig: agent.Config{
		Name:        "parallel_developers",
		Description: "Desenvolvedores trabalhando em paralelo, cada um na sua worktree.",
		SubAgents:   parallelSubs,
	}})
	if err != nil {
		return nil, nil, err
	}

	qaRoot := r.Workspace
	if r.integration != "" {
		qaRoot = r.integration
	}
	qaAgents := r.Team.ByRole(team.RoleQA)
	qaExtra := "Worktrees para revisar:\n" + strings.Join(wtLines, "\n") + "\nA revisão roda na worktree de integração: " + qaRoot
	var qaSubs []agent.Agent
	for i, a := range qaAgents {
		name := unique(a.Name)
		report := "qa_report"
		if i > 0 {
			report = fmt.Sprintf("qa_report_%d", i+1)
		}
		stateKeys = append(stateKeys, report)
		ag, err := r.llm(ctx, a, name, report, qaRoot, qaExtra, key, stateKeys, true)
		if err != nil {
			return nil, nil, err
		}
		labels[name] = roleLabel(a.Role) + " · " + a.Name
		qaSubs = append(qaSubs, ag)
	}
	var qa agent.Agent
	if len(qaSubs) == 1 {
		qa = qaSubs[0]
	} else {
		qa, err = sequentialagent.New(sequentialagent.Config{AgentConfig: agent.Config{
			Name:        "sequential_qa",
			Description: "Revisores de QA em sequência.",
			SubAgents:   qaSubs,
		}})
		if err != nil {
			return nil, nil, err
		}
	}
	loops := r.Project.Settings.MaxQALoops
	if loops <= 0 {
		loops = 3
	}
	integrate, err := r.newIntegrateAgent(devs)
	if err != nil {
		return nil, nil, err
	}
	loop, err := loopagent.New(loopagent.Config{
		MaxIterations: uint(loops),
		AgentConfig: agent.Config{
			Name:        "loop_qa",
			Description: "Desenvolvedores e QA se alternam até os testes passarem.",
			SubAgents:   []agent.Agent{parallel, integrate, qa},
		},
	})
	if err != nil {
		return nil, nil, err
	}
	steps = append(steps, loop)

	if r.EnableClosingChief {
		chief := r.primaryChief()
		closingName := unique(chief.Name + "_fecho")
		closingAgent := chief
		// Use read/diff/comment toolset on qaRoot so Closing Chief inspects the integrated diff without editing code.
		closingAgent.Role = team.RoleTechLead
		closingExtra := "Você é o Chefe (Fecho) encerrando a entrega após o ciclo Dev → QA.\nRevise [qa_report], [qa_approval_note], [qa_feedback] e use git_diff se necessário.\nEntregue ao usuário um resumo executivo claro em Markdown contendo:\n1. O que foi implementado/alterado por cada Dev;\n2. Resultado da revisão/testes do QA;\n3. Lista dos arquivos impactados e próximos passos."
		ag, err := r.llm(ctx, closingAgent, closingName, "closing_summary", qaRoot, closingExtra, key, stateKeys, false)
		if err != nil {
			return nil, nil, err
		}
		labels[closingName] = "Chefe (Fecho) · " + chief.Name
		steps = append(steps, ag)
	}

	root, err := sequentialagent.New(sequentialagent.Config{AgentConfig: agent.Config{
		Name:        "pipeline_root",
		Description: "Pipeline do time HiveMind: planejamento, implementação paralela e QA.",
		SubAgents:   steps,
	}})
	if err != nil {
		return nil, nil, err
	}
	return root, labels, nil
}

func (r *Runner) llm(ctx context.Context, a team.Agent, adkName, outputKey, root, extra, jiraKey string, stateKeys []string, qa bool) (agent.Agent, error) {
	llm, err := r.modelFor(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("modelo de %s: %w", a.Name, err)
	}
	roleTools, err := tools.ForRole(a.Role, tools.Deps{
		Root:       root,
		Allowlist:  r.Project.Settings.ShellAllowlist,
		Confirm:    r.Confirm,
		Git:        r.Git,
		GitHub:     r.GitHub,
		Jira:       r.Jira,
		JiraKey:    jiraKey,
		BaseBranch: r.Project.Settings.BaseBranch,
	})
	if err != nil {
		return nil, err
	}
	instruction := a.Instructions
	if extra != "" {
		instruction += "\n\n" + extra
	}
	keys := append([]string(nil), stateKeys...)
	cfg := llmagent.Config{
		Name:                     adkName,
		Description:              roleLabel(a.Role) + " do time HiveMind.",
		Model:                    llm,
		Mode:                     llmagent.ModeChat,
		IncludeContents:          llmagent.IncludeContentsDefault,
		DisallowTransferToParent: true,
		DisallowTransferToPeers:  true,
		Tools:                    roleTools,
		OutputKey:                outputKey,
		InstructionProvider: func(ctx agent.ReadonlyContext) (string, error) {
			var b strings.Builder
			b.WriteString(instruction)
			for _, k := range keys {
				v, err := ctx.ReadonlyState().Get(k)
				if err != nil || v == nil {
					continue
				}
				text := strings.TrimSpace(fmt.Sprint(v))
				if text == "" {
					continue
				}
				fmt.Fprintf(&b, "\n\n[%s]\n%s", k, text)
			}
			return b.String(), nil
		},
	}
	if qa {
		cfg.AfterToolCallbacks = []llmagent.AfterToolCallback{
			func(ctx agent.Context, tl tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
				if err == nil && tl != nil && tl.Name() == "qa_approve" && ctx.Actions() != nil {
					ctx.Actions().Escalate = true
				}
				return nil, nil
			},
		}
	}
	return llmagent.New(cfg)
}

func (r *Runner) newIntegrateAgent(devs []devEnv) (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        "integrate_branches",
		Description: "Integra as branches dos desenvolvedores sem trocar o checkout do usuário.",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				var branches []string
				for _, dev := range devs {
					if dev.Root == "" || dev.Branch == "" {
						continue
					}
					_, _ = r.Git.CommitAll(ctx, dev.Root, names.CommitMessage(r.issueKey, r.objectiveText))
					branches = append(branches, dev.Branch)
				}
				merged, _, err := r.Git.Integrate(ctx, r.Workspace, branches)
				text := "Nada novo para integrar."
				if err != nil {
					text = "Falha ao integrar: " + err.Error()
				} else if len(merged) > 0 {
					text = "Integrado: " + strings.Join(merged, ", ")
				}
				yield(&session.Event{
					Author: "integrate_branches",
					LLMResponse: model.LLMResponse{Content: &genai.Content{
						Role:  "model",
						Parts: []*genai.Part{{Text: text}},
					}},
				}, nil)
			}
		},
	})
}

func (r *Runner) modelFor(ctx context.Context, a team.Agent) (model.LLM, error) {
	if r.Models != nil {
		if m, ok := r.Models[a.Name]; ok && m != nil {
			return m, nil
		}
	}
	if r.NewModel != nil {
		return r.NewModel(ctx, a)
	}
	return modelx.New(ctx, a.Provider, a.Model)
}

func (r *Runner) openSession(ctx context.Context, id, objective, key string) (session.Session, error) {
	if id != "" {
		got, err := r.Sessions.Get(ctx, &session.GetRequest{AppName: sessionx.AppName, UserID: sessionx.UserID, SessionID: id})
		if err != nil {
			return nil, fmt.Errorf("retomar sessão %s: %w", id, err)
		}
		if err := sessionx.SetObjective(got.Session, objective, key); err != nil {
			return nil, err
		}
		_ = got.Session.State().Set("qa_feedback", "")
		_ = got.Session.State().Set("qa_report", "")
		_ = got.Session.State().Set("qa_approval_note", "")
		_ = got.Session.State().Set("chief_plan", "")
		return got.Session, nil
	}
	created, err := r.Sessions.Create(ctx, &session.CreateRequest{
		AppName: sessionx.AppName,
		UserID:  sessionx.UserID,
		State: map[string]any{
			"objective":        objective,
			"jira_key":         key,
			"qa_feedback":      "",
			"qa_report":        "",
			"qa_approval_note": "",
			"chief_plan":       "",
		},
	})
	if err != nil {
		return nil, err
	}
	return created.Session, nil
}

func (r *Runner) finish(ctx context.Context, result *Result, devs []devEnv, key, objective string) error {
	base := r.Project.Settings.BaseBranch
	if base == "" {
		base = "main"
	}
	var notes []string
	for _, dev := range devs {
		result.Branches = append(result.Branches, dev.Branch)
		if !r.Project.Settings.AutoCommit && !r.Project.Settings.AutoPR {
			continue
		}
		if r.Project.Settings.AutoCommit || r.Project.Settings.AutoPR {
			if _, err := r.Git.CommitAll(ctx, dev.Root, names.CommitMessage(key, objective)); err != nil {
				return fmt.Errorf("commit em %s: %w", dev.Branch, err)
			}
		}
		if !r.Project.Settings.AutoPR {
			continue
		}
		if !r.GitHub.Enabled() {
			return fmt.Errorf("auto_pr está ligado, mas o GitHub não está configurado")
		}
		if err := r.Git.Push(ctx, dev.Root, "origin", dev.Branch); err != nil {
			return fmt.Errorf("push de %s: %w", dev.Branch, err)
		}
		body := fmt.Sprintf("Aberta pelo HiveMind.\n\nSessão: %s\n", result.SessionID)
		if key != "" {
			body += "Issue: " + key + "\n"
		}
		pr, err := r.GitHub.CreatePullRequest(ctx, githubx.CreatePR{
			Title: names.PRTitle(key, objective),
			Head:  dev.Branch,
			Base:  base,
			Body:  body,
		})
		if err != nil {
			return fmt.Errorf("pull request de %s: %w", dev.Branch, err)
		}
		result.PullRequests = append(result.PullRequests, pr)
		notes = append(notes, pr.URL)
		fmt.Fprintf(r.Out, "Pull request: %s\n", pr.URL)
	}
	if key != "" && r.Jira != nil && r.Jira.Enabled() {
		body := fmt.Sprintf("HiveMind concluiu a sessão %s.", result.SessionID)
		if len(result.Branches) > 0 {
			body += "\nBranches: " + strings.Join(result.Branches, ", ")
		}
		if len(notes) > 0 {
			body += "\nPull requests: " + strings.Join(notes, ", ")
		}
		if err := r.Jira.Comment(ctx, key, body); err != nil {
			return fmt.Errorf("comentário no Jira: %w", err)
		}
	}
	return nil
}

func roleLabel(role string) string {
	switch team.NormalizeRole(role) {
	case team.RoleProjectManager:
		return "Gerente de projeto"
	case team.RoleTechLead:
		return "Tech lead"
	case team.RoleArchitect:
		return "Arquiteto"
	case team.RoleDeveloper:
		return "Desenvolvedor"
	case team.RoleQA:
		return "QA"
	default:
		return role
	}
}

func (r *Runner) emitEvent(ev *session.Event, labels map[string]string) {
	label := labels[ev.Author]
	if label == "" {
		label = ev.Author
	}
	stage := inferStage(ev.Author, label)
	r.OnEvent(Event{Kind: "stage", Stage: stage, Author: ev.Author, Label: label})

	var tokens int32
	if ev.LLMResponse.UsageMetadata != nil {
		tokens = ev.LLMResponse.UsageMetadata.TotalTokenCount
	}
	if ev.LLMResponse.Content == nil {
		return
	}
	for _, part := range ev.LLMResponse.Content.Parts {
		if part == nil {
			continue
		}
		if part.FunctionCall != nil {
			r.OnEvent(Event{
				Kind:     "tool_call",
				Stage:    stage,
				Author:   ev.Author,
				Label:    label,
				ToolName: part.FunctionCall.Name,
				Tokens:   tokens,
			})
			if part.FunctionCall.Name == "qa_approve" {
				note := fmt.Sprint(part.FunctionCall.Args["note"])
				r.OnEvent(Event{
					Kind:   "handoff",
					Stage:  "qa",
					Author: ev.Author,
					Label:  "QA aprovou → Chefe (Fecho)",
					Text:   strings.TrimSpace(note),
				})
			} else if part.FunctionCall.Name == "qa_request_changes" {
				notes := fmt.Sprint(part.FunctionCall.Args["notes"])
				r.OnEvent(Event{
					Kind:   "handoff",
					Stage:  "dev",
					Author: ev.Author,
					Label:  "QA pediu ajustes → Devs",
					Text:   strings.TrimSpace(notes),
				})
			}
		} else if part.FunctionResponse != nil {
			r.OnEvent(Event{
				Kind:     "tool_result",
				Stage:    stage,
				Author:   ev.Author,
				Label:    label,
				ToolName: part.FunctionResponse.Name,
				Tokens:   tokens,
			})
		} else if part.Text != "" {
			r.OnEvent(Event{
				Kind:    "text",
				Stage:   stage,
				Author:  ev.Author,
				Label:   label,
				Text:    part.Text,
				Partial: ev.Partial,
				Thought: part.Thought,
				Tokens:  tokens,
			})
		}
	}
}

func inferStage(author, label string) string {
	l := strings.ToLower(author + " " + label)
	switch {
	case strings.Contains(l, "fecho"):
		return "closing"
	case strings.Contains(l, "qa"):
		return "qa"
	case strings.Contains(l, "desenvolvedor") || strings.Contains(l, "dev") || strings.Contains(l, "integrate"):
		return "dev"
	default:
		return "chief"
	}
}

func buildUserContent(objective string, attachments []Attachment) *genai.Content {
	parts := make([]*genai.Part, 0, 1+len(attachments))
	parts = append(parts, &genai.Part{Text: objective})
	for _, att := range attachments {
		if len(att.Data) == 0 {
			continue
		}
		mime := strings.TrimSpace(att.MIMEType)
		if mime == "" {
			mime = "image/png"
		}
		parts = append(parts, &genai.Part{
			InlineData: &genai.Blob{
				MIMEType: mime,
				Data:     att.Data,
			},
		})
	}
	return &genai.Content{
		Role:  genai.RoleUser,
		Parts: parts,
	}
}

func (r *Runner) primaryChief() team.Agent {
	if pms := r.Team.ByRole(team.RoleProjectManager); len(pms) > 0 {
		for _, a := range pms {
			if strings.EqualFold(a.Name, "chefe") {
				return a
			}
		}
	}
	if archs := r.Team.ByRole(team.RoleArchitect); len(archs) > 0 {
		for _, a := range archs {
			if strings.EqualFold(a.Name, "chefe") {
				return a
			}
		}
	}
	if pms := r.Team.ByRole(team.RoleProjectManager); len(pms) > 0 {
		return pms[0]
	}
	if archs := r.Team.ByRole(team.RoleArchitect); len(archs) > 0 {
		return archs[0]
	}
	return r.Team.Agents[0]
}

type delegateArgs struct {
	Plan     string   `json:"plan" jsonschema:"Plano técnico detalhado para a equipe executar."`
	DevTasks []string `json:"dev_tasks" jsonschema:"Lista de subtarefas sem sobreposição de arquivos, uma por desenvolvedor do time na ordem dos devs."`
}

type delegateResult struct {
	Status string `json:"status"`
}

func (r *Runner) runTriageChief(ctx context.Context, sess session.Session, msg *genai.Content, jiraKey string, mode agent.StreamingMode) (bool, string, error) {
	chief := r.primaryChief()
	llm, err := r.modelFor(ctx, chief)
	if err != nil {
		return false, "", fmt.Errorf("modelo de %s: %w", chief.Name, err)
	}
	// Read-only inspection tools + Jira/GitHub read so Triage Chief never edits files directly.
	readChiefRole := team.RoleTechLead
	if team.NormalizeRole(chief.Role) == team.RoleProjectManager {
		readChiefRole = team.RoleProjectManager
	}
	baseTools, err := tools.ForRole(readChiefRole, tools.Deps{
		Root:       r.Workspace,
		Allowlist:  r.Project.Settings.ShellAllowlist,
		Confirm:    r.Confirm,
		Git:        r.Git,
		GitHub:     r.GitHub,
		Jira:       r.Jira,
		JiraKey:    jiraKey,
		BaseBranch: r.Project.Settings.BaseBranch,
	})
	if err != nil {
		return false, "", err
	}

	devs := r.Team.ByRole(team.RoleDeveloper)
	var devNames []string
	for _, d := range devs {
		devNames = append(devNames, d.Name)
	}

	var delegated bool
	delegateTool, err := functiontool.New(functiontool.Config{
		Name:        "delegate_to_squad",
		Description: "Aciona o pipeline Dev → QA → Chefe para implementar mudanças de código. Chame SOMENTE quando o pedido exigir criar/alterar arquivos ou corrigir código.",
	}, func(tctx agent.Context, args delegateArgs) (delegateResult, error) {
		delegated = true
		r.chiefPlan = strings.TrimSpace(args.Plan)
		r.devSlices = append([]string(nil), args.DevTasks...)
		_ = tctx.State().Set("chief_plan", r.chiefPlan)
		if actions := tctx.Actions(); actions != nil {
			if actions.StateDelta == nil {
				actions.StateDelta = map[string]any{}
			}
			actions.StateDelta["chief_plan"] = r.chiefPlan
		}
		if r.OnEvent != nil {
			var hb strings.Builder
			if r.chiefPlan != "" {
				hb.WriteString(r.chiefPlan)
			}
			for i, slice := range r.devSlices {
				dn := fmt.Sprintf("dev_%d", i+1)
				if i < len(devNames) {
					dn = devNames[i]
				}
				if strings.TrimSpace(slice) != "" {
					fmt.Fprintf(&hb, "\n- **%s**: %s", dn, strings.TrimSpace(slice))
				}
			}
			r.OnEvent(Event{
				Kind:   "handoff",
				Stage:  "dev",
				Author: chief.Name,
				Label:  "Chefe delegou → Desenvolvedores",
				Text:   strings.TrimSpace(hb.String()),
			})
		}
		return delegateResult{Status: "delegado ao time de desenvolvimento e QA"}, nil
	})
	if err != nil {
		return false, "", err
	}
	allTools := append(baseTools, delegateTool)

	triageInstr := fmt.Sprintf(`%s

Você é o Chefe da equipe (%s). Desenvolvedores disponíveis (%d): %s.
REGRA DE TRIAGEM:
1. Se o usuário fez uma pergunta, pediu análise/diagnóstico (incluindo análise de prints/imagens anexadas), revisão ou sugestão SEM pedir para implementar/alterar arquivos agora: inspecione o que precisar com read_file/list_dir/git_status, responda diretamente de forma clara e completa e NÃO chame delegate_to_squad.
2. Se o usuário pediu para criar, editar, refatorar ou corrigir código/arquivos: monte um plano objetivo e chame delegate_to_squad passando "plan" e "dev_tasks" (uma subtarefa por desenvolvedor em [%s], dividindo arquivos distintos para não haver conflito de merge), e avise brevemente o usuário que acionou a equipe.`,
		chief.Instructions, chief.Name, len(devs), strings.Join(devNames, ", "), strings.Join(devNames, ", "))

	adkName := names.ADK(chief.Name + "_triage")
	triageAgent, err := llmagent.New(llmagent.Config{
		Name:                     adkName,
		Description:              "Triagem e coordenação do Chefe HiveMind.",
		Model:                    llm,
		Mode:                     llmagent.ModeChat,
		IncludeContents:          llmagent.IncludeContentsDefault,
		DisallowTransferToParent: true,
		DisallowTransferToPeers:  true,
		Tools:                    allTools,
		Instruction:              triageInstr,
	})
	if err != nil {
		return false, "", err
	}

	triageRunner, err := runner.New(runner.Config{
		AppName:        sessionx.AppName,
		Agent:          triageAgent,
		SessionService: r.Sessions,
	})
	if err != nil {
		return false, "", err
	}

	labels := map[string]string{
		adkName: roleLabel(chief.Role) + " (Chefe) · " + chief.Name,
	}
	if r.OnEvent != nil {
		r.OnEvent(Event{Kind: "stage", Stage: "chief", Author: adkName, Label: labels[adkName]})
	}
	printer := &ui.Printer{Out: r.Out, Labels: labels}
	var summary string
	for ev, err := range triageRunner.Run(ctx, sessionx.UserID, sess.ID(), msg, agent.RunConfig{
		StreamingMode: mode,
		MaxLLMCalls:   r.Project.Settings.MaxLLMCalls,
	}) {
		if err != nil {
			return false, "", err
		}
		printer.Event(ev)
		if r.OnEvent != nil && ev != nil {
			r.emitEvent(ev, labels)
		}
		if ev != nil && !ev.Partial && ev.LLMResponse.Content != nil {
			for _, part := range ev.LLMResponse.Content.Parts {
				if part != nil && part.Text != "" && !part.Thought {
					summary = strings.TrimSpace(part.Text)
				}
			}
		}
	}
	if delegated && r.chiefPlan != "" {
		_ = sess.State().Set("chief_plan", r.chiefPlan)
	}
	return delegated, summary, nil
}
