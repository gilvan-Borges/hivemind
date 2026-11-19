// Package orchestrator runs a HiveMind team with ADK sequential, parallel and loop agents.
package orchestrator

import (
	"context"
	"fmt"
	"io"
	"iter"
	"os"
	"strconv"
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
	"github.com/gilvan-Borges/HiveMind/internal/skillx"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/shellx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/tools"
	"github.com/gilvan-Borges/HiveMind/internal/ui"
)

// Event is a structured live update emitted by Runner when OnEvent is set.
type Event struct {
	Kind         string   `json:"kind"` // "stage", "handoff", "devs_scaled", "usage", "text", "tool_call", "tool_result", "finish", "error", "plan_ready", "approval_required"
	Stage        string   `json:"stage,omitempty"`
	Author       string   `json:"author,omitempty"`
	Label        string   `json:"label,omitempty"`
	Text         string   `json:"text,omitempty"`
	ToolName     string   `json:"toolName,omitempty"`
	Detail       string   `json:"detail,omitempty"`
	Devs         []string `json:"devs,omitempty"`
	Partial      bool     `json:"partial,omitempty"`
	Thought      bool     `json:"thought,omitempty"`
	InputTokens  int32    `json:"inputTokens,omitempty"`
	OutputTokens int32    `json:"outputTokens,omitempty"`
	Tokens       int32    `json:"tokens,omitempty"`
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
	Approve            func(prompt string) bool
	Out                io.Writer
	Err                io.Writer
	Streaming          agent.StreamingMode
	OnEvent            func(Event)
	EnableTriage       bool
	EnableClosingChief bool
	integration        string
	deliveryRoot       string
	deliveryBranch     string
	issueKey           string
	objectiveText      string
	reviewRequested    bool
	prNumber           int
	prBranch           string
	prReviewers        []string
	devSlices          []string
	chiefPlan          string
	skills             []skillx.Skill
	selectedSkills     []skillx.Skill
	// NativeSkills é o catálogo nativo da IDE, preenchido pelo runner da IDE (CLI deixa vazio).
	NativeSkills       []skillx.Skill
	NativeSkillsRoot   string
	selectedNative     []skillx.Skill
	turnInputTokens    int64
	turnOutputTokens   int64
	turnTotalTokens    int64
}

// Request is one user turn.
type Request struct {
	Objective   string
	JiraKey     string
	SessionID   string
	RunMode     string // "", "auto", "direct", "plan", "squad"
	Attachments []Attachment
}

// Result is what the CLI prints after the team finishes.
type Result struct {
	SessionID         string
	Summary           string
	Branches          []string
	IntegrationBranch string `json:"integrationBranch,omitempty"`
	PullRequests      []githubx.PullRequest
	InputTokens       int64 `json:"inputTokens"`
	OutputTokens      int64 `json:"outputTokens"`
	TotalTokens       int64 `json:"totalTokens"`
}

type devEnv struct {
	Agent  team.Agent
	ADK    string
	Root   string
	Branch string
}

// Run builds the ADK pipeline and streams events until it finishes.
func (r *Runner) Run(parentCtx context.Context, req Request) (Result, error) {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

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

	r.Project.Normalize()

	r.integration = ""
	r.deliveryRoot = ""
	r.deliveryBranch = ""
	r.issueKey = key
	r.objectiveText = objective
	r.reviewRequested = githubx.IsExplicitReviewObjective(objective)
	r.prNumber = 0
	r.prBranch = ""
	r.prReviewers = nil
	r.devSlices = nil
	r.chiefPlan = ""
	r.skills = skillx.Load(r.Workspace)
	r.selectedSkills = nil
	r.selectedNative = nil
	r.turnInputTokens = 0
	r.turnOutputTokens = 0
	r.turnTotalTokens = 0

	runMode := strings.ToLower(strings.TrimSpace(req.RunMode))
	sess, err := r.openSession(ctx, req.SessionID, objective, key, runMode == "squad")
	if err != nil {
		return result, err
	}
	result.SessionID = sess.ID()
	ctx = modelx.WithSessionID(ctx, sess.ID())

	mode := r.Streaming
	if mode == "" {
		mode = agent.StreamingModeSSE
	}
	msg := buildUserContent(objective, req.Attachments)

	useTriage := (runMode == "" && r.EnableTriage) ||
		runMode == "auto" ||
		runMode == "direct" ||
		runMode == "plan" ||
		(runMode == "squad" && r.EnableTriage && r.chiefPlan == "")
	if useTriage {
		delegated, summary, err := r.runTriageChief(ctx, sess, msg, key, mode, runMode)
		result.InputTokens = r.turnInputTokens
		result.OutputTokens = r.turnOutputTokens
		result.TotalTokens = r.turnTotalTokens
		if err != nil {
			if r.OnEvent != nil {
				r.OnEvent(Event{Kind: "error", Text: err.Error()})
			}
			return result, err
		}
		if runMode == "plan" {
			result.Summary = summary
			planBody := strings.TrimSpace(r.chiefPlan)
			if planBody == "" {
				planBody = summary
			}
			_ = sessionx.UpdateState(sess, map[string]any{
				"chief_plan":            planBody,
				"dev_slices_joined":     strings.Join(r.devSlices, "\n|||\n"),
				"pr_number":             strconv.Itoa(r.prNumber),
				"pr_branch":             r.prBranch,
				"pr_reviewers":          strings.Join(r.prReviewers, ","),
				"plan_pending_approval": "1",
			})
			if r.OnEvent != nil {
				r.OnEvent(Event{
					Kind:         "finish",
					Stage:        "chief",
					Text:         fmt.Sprintf("Plano pronto na sessão %s — revise e aprove para iniciar o Squad.", result.SessionID),
					InputTokens:  int32(r.turnInputTokens),
					OutputTokens: int32(r.turnOutputTokens),
					Tokens:       int32(r.turnTotalTokens),
				})
			}
			return result, nil
		}
		if (!delegated && runMode != "squad") || runMode == "direct" {
			result.Summary = summary
			fmt.Fprintf(r.Out, "\nSessão %s concluída (resposta direta do Chefe).\n", result.SessionID)
			if r.OnEvent != nil {
				r.OnEvent(Event{
					Kind:         "finish",
					Stage:        "chief",
					Text:         fmt.Sprintf("Sessão %s concluída.", result.SessionID),
					InputTokens:  int32(r.turnInputTokens),
					OutputTokens: int32(r.turnOutputTokens),
					Tokens:       int32(r.turnTotalTokens),
				})
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
			result.InputTokens = r.turnInputTokens
			result.OutputTokens = r.turnOutputTokens
			result.TotalTokens = r.turnTotalTokens
			if r.OnEvent != nil {
				r.OnEvent(Event{Kind: "error", Text: err.Error()})
			}
			return result, err
		}
		printer.Event(ev)
		if ev != nil {
			r.recordUsage(ev, labels)
			if r.OnEvent != nil {
				r.emitEvent(ev, labels)
			}
			if r.Project.Settings.MaxTokens > 0 && r.turnTotalTokens > r.Project.Settings.MaxTokens {
				budgetErr := fmt.Errorf("limite de tokens por execução atingido (%d > %d tokens)", r.turnTotalTokens, r.Project.Settings.MaxTokens)
				result.InputTokens = r.turnInputTokens
				result.OutputTokens = r.turnOutputTokens
				result.TotalTokens = r.turnTotalTokens
				if r.OnEvent != nil {
					r.OnEvent(Event{Kind: "error", Text: budgetErr.Error()})
				}
				return result, budgetErr
			}
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

	result.InputTokens = r.turnInputTokens
	result.OutputTokens = r.turnOutputTokens
	result.TotalTokens = r.turnTotalTokens

	if err := r.finish(ctx, &result, devs, key, objective); err != nil {
		if r.OnEvent != nil {
			r.OnEvent(Event{Kind: "error", Text: err.Error()})
		}
		return result, err
	}
	fmt.Fprintf(r.Out, "\nSessão %s concluída.\n", result.SessionID)
	if r.OnEvent != nil {
		r.OnEvent(Event{
			Kind:         "finish",
			Text:         fmt.Sprintf("Sessão %s concluída.", result.SessionID),
			InputTokens:  int32(r.turnInputTokens),
			OutputTokens: int32(r.turnOutputTokens),
			Tokens:       int32(r.turnTotalTokens),
		})
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

func (r *Runner) maxAllowedDevs() int {
	baseCount := len(r.Team.ByRole(team.RoleDeveloper))
	maxDevs := r.Project.Settings.EffectiveMaxDynamicDevs()
	if baseCount > maxDevs {
		return baseCount
	}
	return maxDevs
}

// resolveDynamicDevs picks the exact set of Developer agents for this execution.
// By default the team defines 1 base Dev template; if the Chief/Architect splits a large
// task into 2..MaxDynamicDevs non-overlapping slices in dev_tasks, extra Dev workers
// (dev_1..dev_N) are dynamically cloned from the base Dev template (model/instructions).
func (r *Runner) resolveDynamicDevs() []team.Agent {
	baseDevs := r.Team.ByRole(team.RoleDeveloper)
	if len(baseDevs) == 0 {
		return nil
	}
	desired := len(r.devSlices)
	if desired == 0 {
		return baseDevs
	}
	maxDevs := r.maxAllowedDevs()
	if desired > maxDevs {
		desired = maxDevs
	}
	if desired == 1 {
		return []team.Agent{baseDevs[0]}
	}

	out := make([]team.Agent, 0, desired)
	used := make(map[string]bool, desired)
	for i := 0; i < desired; i++ {
		var a team.Agent
		if i < len(baseDevs) {
			a = baseDevs[i]
			if len(baseDevs) == 1 && strings.EqualFold(a.Name, "dev") {
				a.Name = "dev_1"
			}
		} else {
			a = baseDevs[i%len(baseDevs)]
			a.Name = fmt.Sprintf("dev_%d", i+1)
		}
		if used[strings.ToLower(a.Name)] {
			for k := i + 1; k <= desired+10; k++ {
				cand := fmt.Sprintf("dev_%d", k)
				if !used[strings.ToLower(cand)] {
					a.Name = cand
					break
				}
			}
		}
		used[strings.ToLower(a.Name)] = true
		out = append(out, a)
	}
	return out
}

func (r *Runner) prepareDevs(ctx context.Context, key, objective string) ([]devEnv, error) {
	agents := r.resolveDynamicDevs()
	envs := make([]devEnv, 0, len(agents))
	allowWorktrees := r.Project.Settings.Worktrees == nil || *r.Project.Settings.Worktrees
	hasGitRepo := r.Git.IsRepo(ctx, r.Workspace) && r.Git.HasCommit(ctx, r.Workspace)
	repo := allowWorktrees && hasGitRepo
	if !hasGitRepo {
		fmt.Fprintln(r.Err, "aviso: o workspace não tem um commit Git; os desenvolvedores compartilham a pasta e não usam worktrees")
	} else if !allowWorktrees {
		fmt.Fprintln(r.Err, "aviso: worktrees desativadas em settings.worktrees; os desenvolvedores compartilham a pasta do workspace")
	}

	_ = gitx.EnsureHiveIgnore(r.Workspace)

	if repo {
		targetBranch := strings.TrimSpace(r.prBranch)
		preserveExisting := targetBranch != ""
		if targetBranch == "" {
			targetBranch = names.DeliveryBranch(key, objective)
		}
		delivWT, effBranch, err := gitx.PrepareDeliveryWorktree(ctx, r.Git, r.Workspace, targetBranch, preserveExisting)
		if err != nil {
			return nil, fmt.Errorf("worktree de entrega (%s): %w", targetBranch, err)
		}
		r.integration = delivWT
		r.deliveryRoot = delivWT
		r.deliveryBranch = effBranch
	} else {
		r.deliveryRoot = r.Workspace
	}

	devNames := make([]string, 0, len(agents))
	for _, a := range agents {
		adkName := names.ADK(a.Name)
		branch := names.Branch(key, a.Name, objective)
		root := r.Workspace
		if repo {
			wt, err := gitx.AlignDeveloperWorktree(ctx, r.Git, r.Workspace, adkName, branch, r.deliveryBranch)
			if err != nil {
				return nil, fmt.Errorf("worktree de %s: %w", a.Name, err)
			}
			root = wt
		}
		envs = append(envs, devEnv{Agent: a, ADK: adkName, Root: root, Branch: branch})
		devNames = append(devNames, a.Name)
	}
	if r.OnEvent != nil && len(devNames) > 0 {
		r.OnEvent(Event{
			Kind:  "devs_scaled",
			Stage: "dev",
			Devs:  devNames,
		})
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
	stateKeys := []string{"objective", "jira_key", "chief_plan", "pm_plan", "tech_lead_plan", "architect_plan", "integrate_status", "qa_feedback", "qa_report", "qa_approval_note"}

	qaRoot := r.Workspace
	if r.deliveryRoot != "" {
		qaRoot = r.deliveryRoot
	} else if r.integration != "" {
		qaRoot = r.integration
	}

	var steps []agent.Agent
	addGroup := func(output string, agents []team.Agent, root, extra string, disableGitWrite bool) error {
		for i, a := range agents {
			name := unique(a.Name)
			keyName := output
			if output == "" {
				keyName = name + "_report"
			} else if i > 0 {
				keyName = fmt.Sprintf("%s_%d", output, i+1)
			}
			stateKeys = append(stateKeys, keyName)
			ag, err := r.llm(ctx, a, name, keyName, root, extra, key, stateKeys, false, disableGitWrite)
			if err != nil {
				return err
			}
			labels[name] = roleLabel(a.Role) + " · " + a.Name
			steps = append(steps, ag)
		}
		return nil
	}
	chiefSkillGuide := r.guidanceForRole(team.SkillTargetChief, false)
	joinExtra := func(base string) string {
		if chiefSkillGuide == "" {
			return base
		}
		if base == "" {
			return chiefSkillGuide
		}
		return base + chiefSkillGuide
	}
	if r.chiefPlan != "" {
		// Chief already planned and sliced tasks (via auto triage or plan checkpoint); Tech Lead refines technical guardrails if needed.
		if err := addGroup("tech_lead_plan", r.Team.ByRole(team.RoleTechLead), qaRoot, joinExtra("Refine brevemente os pontos críticos de implementação sem repetir o plano do Chefe."), true); err != nil {
			return nil, nil, err
		}
	} else {
		if err := addGroup("pm_plan", r.Team.ByRole(team.RoleProjectManager), qaRoot, joinExtra(""), true); err != nil {
			return nil, nil, err
		}
		if err := addGroup("tech_lead_plan", r.Team.ByRole(team.RoleTechLead), qaRoot, joinExtra(""), true); err != nil {
			return nil, nil, err
		}
		if err := addGroup("architect_plan", r.Team.ByRole(team.RoleArchitect), qaRoot, joinExtra(""), true); err != nil {
			return nil, nil, err
		}
	}

	var parallelSubs []agent.Agent
	var wtLines []string
	devSkillGuide := r.guidanceForRole(team.SkillTargetDev, true)
	for idx, dev := range devs {
		name := unique(dev.Agent.Name)
		report := "dev_" + name + "_report"
		stateKeys = append(stateKeys, report)
		extra := fmt.Sprintf("Seu diretório de trabalho isolado é %s.\nSua branch de rascunho é %s.\nNÃO execute git commit, git push nem abra PR (apenas o Chefe ou o Arquiteto comitam na worktree de entrega após aprovação do QA).\nNão altere arquivos fora desse diretório.\nSe houver [qa_feedback] ou [qa_report], corrija prioritariamente todos os pontos apontados pelo QA.", dev.Root, dev.Branch)
		if idx < len(r.devSlices) && strings.TrimSpace(r.devSlices[idx]) != "" {
			extra += fmt.Sprintf("\n\n[Sua Subtarefa Exclusiva atribuída pelo Chefe]\n%s\nRestrinja suas edições aos arquivos desta subtarefa para evitar conflitos com outros desenvolvedores.", strings.TrimSpace(r.devSlices[idx]))
		} else if len(devs) > 1 {
			extra += fmt.Sprintf("\n\nVocê é o desenvolvedor %d de %d (%s). Respeite a divisão de arquivos do plano para não colidir com os demais desenvolvedores.", idx+1, len(devs), dev.Agent.Name)
		}
		if devSkillGuide != "" {
			extra += devSkillGuide
		}
		ag, err := r.llm(ctx, dev.Agent, name, report, dev.Root, extra, key, stateKeys, false, true)
		if err != nil {
			return nil, nil, err
		}
		labels[name] = roleLabel(dev.Agent.Role) + " · " + dev.Agent.Name
		parallelSubs = append(parallelSubs, ag)
		wtLines = append(wtLines, dev.ADK+": "+dev.Root+" ("+dev.Branch+")")
	}
	parallel, err := parallelagent.New(parallelagent.Config{AgentConfig: agent.Config{
		Name:        "parallel_developers",
		Description: "Desenvolvedores trabalhando em paralelo, cada um na sua worktree isolada.",
		SubAgents:   parallelSubs,
	}})
	if err != nil {
		return nil, nil, err
	}

	qaAgents := r.Team.ByRole(team.RoleQA)
	qaExtra := "Worktrees dos devs:\n" + strings.Join(wtLines, "\n") + "\nAs alterações foram consolidadas na worktree separada de entrega: " + qaRoot
	if qaSkillGuide := r.guidanceForRole(team.SkillTargetQA, true); qaSkillGuide != "" {
		qaExtra += qaSkillGuide
	}
	var qaSubs []agent.Agent
	for i, a := range qaAgents {
		name := unique(a.Name)
		report := "qa_report"
		if i > 0 {
			report = fmt.Sprintf("qa_report_%d", i+1)
		}
		stateKeys = append(stateKeys, report)
		ag, err := r.llm(ctx, a, name, report, qaRoot, qaExtra, key, stateKeys, true, true)
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
		// Closing Chief operates with full Chief governance (RoleProjectManager: commit/push/PR/review/merge, without write_file).
		closingAgent.Role = team.RoleProjectManager
		closingExtra := "Você é o Chefe (Fecho) encerrando a entrega na worktree separada (" + qaRoot + ", branch " + r.deliveryBranch + ") após aprovação do QA.\n" +
			"GOVERNANÇA GIT & PR:\n" +
			"- Apenas você (Chefe) ou o Arquiteto realizam commit, push e operações de Pull Request.\n"
		if r.prNumber > 0 {
			closingExtra += fmt.Sprintf("- Esta tarefa atende revisão do PR #%d (revisores: %s). Se houver alterações prontas, faça git_commit, git_push, responda ponto a ponto o que foi ajustado com github_comment_pr(number=%d) (e github_reply_review_comment para comentários inline) e peça nova revisão com github_request_review.\n",
				r.prNumber, strings.Join(r.prReviewers, ", "), r.prNumber)
		}
		closingExtra += "Revise [qa_report], [qa_approval_note], [qa_feedback] e use git_diff se necessário.\nEntregue ao usuário um resumo executivo claro em Markdown contendo:\n1. O que foi implementado/alterado;\n2. Resultado da revisão/testes do QA;\n3. Branch/PR e próximos passos."
		if chiefSkillGuide != "" {
			closingExtra += chiefSkillGuide
		}
		ag, err := r.llm(ctx, closingAgent, closingName, "closing_summary", qaRoot, closingExtra, key, stateKeys, false, false)
		if err != nil {
			return nil, nil, err
		}
		labels[closingName] = "Chefe (Fecho) · " + chief.Name
		steps = append(steps, ag)
	}

	root, err := sequentialagent.New(sequentialagent.Config{AgentConfig: agent.Config{
		Name:        "pipeline_root",
		Description: "Pipeline do time HiveMind: planejamento, implementação paralela em worktrees e QA.",
		SubAgents:   steps,
	}})
	if err != nil {
		return nil, nil, err
	}
	return root, labels, nil
}

func (r *Runner) llm(ctx context.Context, a team.Agent, adkName, outputKey, root, extra, jiraKey string, stateKeys []string, qa bool, disableGitWrite bool) (agent.Agent, error) {
	llm, err := r.modelFor(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("modelo de %s: %w", a.Name, err)
	}
	roleTools, err := tools.ForRole(a.Role, tools.Deps{
		Root:                 root,
		Settings:             r.Project.Settings,
		DisableGitWrite:      disableGitWrite,
		ReviewRequestedInObj: r.reviewRequested,
		Allowlist:            r.Project.Settings.ShellAllowlist,
		Confirm:              r.Confirm,
		Approve:              r.Approve,
		Git:                  r.Git,
		GitHub:               r.GitHub,
		Jira:                 r.Jira,
		JiraKey:              jiraKey,
		BaseBranch:           r.Project.Settings.BaseBranch,
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
		Description: "Consolida os arquivos das worktrees dos desenvolvedores na worktree de entrega sem criar commits de dev.",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				var devRoots []string
				for _, dev := range devs {
					if dev.Root != "" && dev.Root != r.deliveryRoot {
						devRoots = append(devRoots, dev.Root)
					}
				}
				synced, err := gitx.SyncUncommittedToDelivery(ctx, r.Git, devRoots, r.deliveryRoot)
				text := "Nada novo para consolidar."
				delta := map[string]any{"integrate_status": ""}
				if err != nil {
					text = "Falha ao consolidar worktrees: " + err.Error()
					delta["integrate_status"] = text
				} else if len(synced) > 0 {
					text = "Consolidado na worktree de entrega: " + strings.Join(synced, ", ")
					delta["integrate_status"] = text
				}
				yield(&session.Event{
					Author: "integrate_branches",
					LLMResponse: model.LLMResponse{Content: &genai.Content{
						Role:  "model",
						Parts: []*genai.Part{{Text: text}},
					}},
					Actions: session.EventActions{StateDelta: delta},
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
		if a.Role == team.RoleDeveloper {
			if m, ok := r.Models["dev"]; ok && m != nil {
				return m, nil
			}
			if m, ok := r.Models["dev_1"]; ok && m != nil {
				return m, nil
			}
		}
	}
	if r.NewModel != nil {
		return r.NewModel(ctx, a)
	}
	return modelx.New(ctx, a.Provider, a.Model)
}

func (r *Runner) openSession(ctx context.Context, id, objective, key string, consumePendingPlan bool) (session.Session, error) {
	if id != "" {
		got, err := r.Sessions.Get(ctx, &session.GetRequest{AppName: sessionx.AppName, UserID: sessionx.UserID, SessionID: id})
		if err != nil {
			return nil, fmt.Errorf("retomar sessão %s: %w", id, err)
		}
		pendingFlag, _ := got.Session.State().Get("plan_pending_approval")
		isPendingPlan := strings.TrimSpace(fmt.Sprint(pendingFlag)) == "1"
		if consumePendingPlan && isPendingPlan {
			if prevPlan, err := got.Session.State().Get("chief_plan"); err == nil && prevPlan != nil {
				r.chiefPlan = strings.TrimSpace(fmt.Sprint(prevPlan))
			}
			if prevSlices, err := got.Session.State().Get("dev_slices_joined"); err == nil && prevSlices != nil {
				if raw := strings.TrimSpace(fmt.Sprint(prevSlices)); raw != "" {
					r.devSlices = strings.Split(raw, "\n|||\n")
				}
			}
			if prevPR, err := got.Session.State().Get("pr_number"); err == nil && prevPR != nil {
				r.prNumber, _ = strconv.Atoi(strings.TrimSpace(fmt.Sprint(prevPR)))
			}
			if prevBranch, err := got.Session.State().Get("pr_branch"); err == nil && prevBranch != nil {
				r.prBranch = strings.TrimSpace(fmt.Sprint(prevBranch))
			}
			if prevRevs, err := got.Session.State().Get("pr_reviewers"); err == nil && prevRevs != nil {
				if raw := strings.TrimSpace(fmt.Sprint(prevRevs)); raw != "" {
					r.prReviewers = strings.Split(raw, ",")
				}
			}
			if prevSkills, err := got.Session.State().Get("selected_skills"); err == nil && prevSkills != nil {
				if raw := strings.TrimSpace(fmt.Sprint(prevSkills)); raw != "" {
					r.selectedSkills = skillx.Match(r.skills, strings.Split(raw, "\n"))
				}
			}
			if prevNative, err := got.Session.State().Get("selected_native_skills"); err == nil && prevNative != nil {
				if raw := strings.TrimSpace(fmt.Sprint(prevNative)); raw != "" {
					r.selectedNative = skillx.Match(r.NativeSkills, strings.Split(raw, "\n"))
				}
			}
		}
		if err := sessionx.SetObjective(got.Session, objective, key); err != nil {
			return nil, err
		}
		resetMap := map[string]any{
			"qa_feedback":           "",
			"qa_report":             "",
			"qa_approval_note":      "",
			"integrate_status":      "",
			"plan_pending_approval": "",
			"dev_slices_joined":     "",
			"pr_number":             "",
			"pr_branch":             "",
			"pr_reviewers":          "",
		}
		if !(consumePendingPlan && isPendingPlan && r.chiefPlan != "") {
			resetMap["chief_plan"] = ""
			resetMap["selected_skills"] = ""
			resetMap["selected_native_skills"] = ""
		}
		_ = sessionx.UpdateState(got.Session, resetMap)
		_ = sessionx.CompactOldToolOutputs(got.Session, 10)
		return got.Session, nil
	}
	created, err := r.Sessions.Create(ctx, &session.CreateRequest{
		AppName: sessionx.AppName,
		UserID:  sessionx.UserID,
		State: map[string]any{
			"objective":             objective,
			"jira_key":              key,
			"qa_feedback":           "",
			"qa_report":             "",
			"qa_approval_note":      "",
			"integrate_status":      "",
			"chief_plan":            "",
			"dev_slices_joined":     "",
			"pr_number":             "",
			"pr_branch":             "",
			"pr_reviewers":          "",
			"plan_pending_approval": "",
		},
	})
	if err != nil {
		return nil, err
	}
	return created.Session, nil
}

func (r *Runner) finish(ctx context.Context, result *Result, devs []devEnv, key, objective string) error {
	if r.integration != "" {
		result.IntegrationBranch = gitx.IntegrationBranch
	}
	base := strings.TrimSpace(r.Project.Settings.BaseBranch)
	if base == "" {
		base = "main"
	}

	commitRoot := r.deliveryRoot
	if commitRoot == "" {
		commitRoot = r.Workspace
	}
	commitBranch := strings.TrimSpace(r.deliveryBranch)
	if commitBranch == "" && r.Git.IsRepo(ctx, commitRoot) {
		commitBranch, _ = r.Git.CurrentBranch(ctx, commitRoot)
	}
	if commitBranch != "" {
		result.Branches = append(result.Branches, commitBranch)
	} else {
		for _, dev := range devs {
			if dev.Branch != "" {
				result.Branches = append(result.Branches, dev.Branch)
			}
		}
	}

	var notes []string
	if (r.Project.Settings.AutoCommit || r.Project.Settings.AutoPR || r.prNumber > 0) && r.Git.IsRepo(ctx, commitRoot) {
		if _, err := r.Git.CommitAll(ctx, commitRoot, names.CommitMessage(key, objective)); err != nil {
			return fmt.Errorf("commit do Chefe/Arquiteto em %s: %w", commitBranch, err)
		}
		gitx.UpdateIntegrationRef(ctx, r.Git, r.Workspace, commitRoot)
	}

	hasOrigin := false
	if _, remErr := r.Git.RemoteURL(ctx, r.Workspace); remErr == nil {
		hasOrigin = true
	}

	if r.prNumber > 0 && r.GitHub != nil && r.GitHub.Enabled() {
		if hasOrigin && commitBranch != "" {
			if err := r.Git.Push(ctx, commitRoot, "origin", commitBranch); err != nil {
				return fmt.Errorf("push de revisão %s: %w", commitBranch, err)
			}
		}
		if len(r.prReviewers) > 0 {
			_ = r.GitHub.RequestReviewers(ctx, r.prNumber, r.prReviewers)
		}
	} else if r.Project.Settings.AutoPR && commitBranch != "" {
		if !hasOrigin || r.GitHub == nil || !r.GitHub.Enabled() {
			fmt.Fprintf(r.Err, "aviso: auto_pr está ativo, mas remote origin / GitHub não está configurado; alterações commitadas na worktree isolada (%s)\n", commitBranch)
		} else {
			if err := r.Git.Push(ctx, commitRoot, "origin", commitBranch); err != nil {
				return fmt.Errorf("push de %s: %w", commitBranch, err)
			}
			var pr githubx.PullRequest
			if existing, _ := r.GitHub.FindOpenPullRequestByHead(ctx, commitBranch); existing != nil {
				pr = *existing
			} else {
				body := fmt.Sprintf("Aberta pelo Chefe/Arquiteto HiveMind a partir de worktree isolada.\n\nSessão: %s\n", result.SessionID)
				if key != "" {
					body += "Issue: " + key + "\n"
				}
				createdPR, err := r.GitHub.CreatePullRequest(ctx, githubx.CreatePR{
					Title: names.PRTitle(key, objective),
					Head:  commitBranch,
					Base:  base,
					Body:  body,
				})
				if err != nil {
					return fmt.Errorf("pull request de %s: %w", commitBranch, err)
				}
				pr = createdPR
			}
			result.PullRequests = append(result.PullRequests, pr)
			notes = append(notes, pr.URL)
			fmt.Fprintf(r.Out, "Pull request: %s\n", pr.URL)
		}
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

func filterOutSkill(list []skillx.Skill, name string) []skillx.Skill {
	out := make([]skillx.Skill, 0, len(list))
	for _, s := range list {
		if s.Name != name {
			out = append(out, s)
		}
	}
	return out
}

func skillSelectionLabel(project, native []string) string {
	var parts []string
	if len(project) > 0 {
		parts = append(parts, strings.Join(project, ", "))
	}
	if len(native) > 0 {
		parts = append(parts, "nativas da IDE: "+strings.Join(native, ", "))
	}
	return strings.Join(parts, " · ")
}

func (r *Runner) recordUsage(ev *session.Event, labels map[string]string) {
	if ev == nil || ev.Partial || ev.LLMResponse.UsageMetadata == nil {
		return
	}
	u := ev.LLMResponse.UsageMetadata
	inTok := u.PromptTokenCount
	outTok := u.CandidatesTokenCount
	totTok := u.TotalTokenCount
	if totTok == 0 && (inTok > 0 || outTok > 0) {
		totTok = inTok + outTok
	}
	if inTok == 0 && outTok == 0 && totTok == 0 {
		return
	}
	r.turnInputTokens += int64(inTok)
	r.turnOutputTokens += int64(outTok)
	r.turnTotalTokens += int64(totTok)

	if r.OnEvent != nil {
		label := labels[ev.Author]
		if label == "" {
			label = ev.Author
		}
		r.OnEvent(Event{
			Kind:         "usage",
			Stage:        inferStage(ev.Author, label),
			Author:       ev.Author,
			Label:        label,
			InputTokens:  inTok,
			OutputTokens: outTok,
			Tokens:       totTok,
		})
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
				Detail:   summarizeToolArgs(part.FunctionCall.Name, part.FunctionCall.Args),
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
				Detail:   summarizeToolResult(part.FunctionResponse.Response),
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

func summarizeToolArgs(name string, args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	if strings.HasPrefix(name, "github_") {
		if num, ok := args["number"]; ok && num != nil {
			n := strings.TrimSpace(fmt.Sprint(num))
			if n != "" {
				if f, ok := args["file"]; ok && f != nil && strings.TrimSpace(fmt.Sprint(f)) != "" {
					return strx.TruncateRunes(fmt.Sprintf("#%s · %s", n, strings.TrimSpace(fmt.Sprint(f))), 120, "…")
				}
				return "#" + n
			}
		}
	}
	for _, key := range []string{"path", "command", "message", "title", "number", "key", "jql", "ref", "note", "notes"} {
		if v, ok := args[key]; ok && v != nil {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" {
				return strx.TruncateRunes(strings.ReplaceAll(s, "\n", " "), 120, "…")
			}
		}
	}
	return ""
}

func summarizeToolResult(res map[string]any) string {
	if len(res) == 0 {
		return ""
	}
	for _, key := range []string{"text", "status", "error", "entries"} {
		if v, ok := res[key]; ok && v != nil {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" {
				return strx.TruncateRunes(s, 360, "\n…")
			}
		}
	}
	return ""
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

func (r *Runner) resolveSkillCatalogs(target string, includeDynamic bool) (project []skillx.Skill, native []skillx.Skill) {
	seenProject := map[string]bool{}
	seenNative := map[string]bool{}
	addProject := func(s skillx.Skill) {
		if !seenProject[s.Name] {
			seenProject[s.Name] = true
			project = append(project, s)
		}
	}
	addNative := func(s skillx.Skill) {
		if !seenNative[s.Name] {
			seenNative[s.Name] = true
			native = append(native, s)
		}
	}
	for _, a := range r.Project.Settings.SkillsForTarget(target) {
		switch strings.ToLower(strings.TrimSpace(a.Source)) {
		case "native":
			if hit := skillx.Find(r.NativeSkills, a.Name); hit != nil {
				addNative(*hit)
			}
		case "project":
			if hit := skillx.Find(r.skills, a.Name); hit != nil {
				addProject(*hit)
			}
		default:
			if hit := skillx.Find(r.skills, a.Name); hit != nil {
				addProject(*hit)
			} else if hit := skillx.Find(r.NativeSkills, a.Name); hit != nil {
				addNative(*hit)
			}
		}
	}
	if includeDynamic {
		for _, s := range r.selectedSkills {
			addProject(s)
		}
		for _, s := range r.selectedNative {
			addNative(s)
		}
	}
	return project, native
}

func (r *Runner) guidanceForRole(target string, includeDynamic bool) string {
	project, native := r.resolveSkillCatalogs(target, includeDynamic)
	return skillx.GuidanceBlock(r.Workspace, r.NativeSkillsRoot, project, native, 0, 0)
}

func (r *Runner) pinnedSkillsNote() string {
	if len(r.Project.Settings.SkillAssignments) == 0 {
		return ""
	}
	var lines []string
	for _, a := range r.Project.Settings.SkillAssignments {
		if len(a.Roles) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("- `%s`: %s", a.Name, strings.Join(a.Roles, ", ")))
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n\nSKILLS FIXADAS POR PAPEL NA CONFIGURAÇÃO DO TIME (já injetadas automaticamente nos respectivos agentes):\n" + strings.Join(lines, "\n")
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
	Plan      string   `json:"plan" jsonschema:"Plano técnico detalhado para a equipe executar."`
	DevTasks  []string `json:"dev_tasks" jsonschema:"Lista dinâmica de 1 até max_dynamic_devs subtarefas SEM sobreposição de arquivos. Cada item dispara 1 desenvolvedor paralelo em worktree isolada (use 1 item para tarefas pequenas/focadas, ou 2 a 5 itens para entregas grandes e paralelizáveis)."`
	PRNumber  int      `json:"pr_number,omitempty" jsonschema:"Número do PR existente quando a tarefa é corrigir mudanças solicitadas em revisão."`
	PRBranch  string   `json:"pr_branch,omitempty" jsonschema:"Branch headRef do PR existente, quando houver."`
	Reviewers []string `json:"reviewers,omitempty" jsonschema:"Logins dos revisores para pedir nova revisão após corrigir (ex: quem pediu mudança)."`
	Skills    []string `json:"skills,omitempty" jsonschema:"Opcional: nomes exatos das skills do catálogo .agents/skills do projeto que a equipe deve seguir (apenas as relevantes para a tarefa; vazio significa nenhuma)."`
	NativeSkills []string `json:"native_skills,omitempty" jsonschema:"Opcional: nomes das skills NATIVAS da IDE que a equipe deve seguir. Use quando a nativa parecer mais atual ou melhor que a versão do projeto; se houver conflito de nome, o usuário será consultado para autorizar."`
}

type delegateResult struct {
	Status string `json:"status"`
}

func (r *Runner) runTriageChief(ctx context.Context, sess session.Session, msg *genai.Content, jiraKey string, mode agent.StreamingMode, runMode string) (bool, string, error) {
	chief := r.primaryChief()
	llm, err := r.modelFor(ctx, chief)
	if err != nil {
		return false, "", fmt.Errorf("modelo de %s: %w", chief.Name, err)
	}
	// Triage Chief uses RoleProjectManager with DisableGitWrite=true so it never edits or commits on main workspace,
	// while retaining full PR inspection, comment, review, request-review, and merge governance tools.
	baseTools, err := tools.ForRole(team.RoleProjectManager, tools.Deps{
		Root:                 r.Workspace,
		Settings:             r.Project.Settings,
		DisableGitWrite:      true,
		ReviewRequestedInObj: r.reviewRequested,
		Allowlist:            r.Project.Settings.ShellAllowlist,
		Confirm:              r.Confirm,
		Approve:              r.Approve,
		Git:                  r.Git,
		GitHub:               r.GitHub,
		Jira:                 r.Jira,
		JiraKey:              jiraKey,
		BaseBranch:           r.Project.Settings.BaseBranch,
	})
	if err != nil {
		return false, "", err
	}

	baseDevs := r.Team.ByRole(team.RoleDeveloper)
	maxDevs := r.maxAllowedDevs()

	var delegated bool
	var planCardEmitted bool
	allTools := baseTools

	if runMode != "direct" {
		delegateTool, err := functiontool.New(functiontool.Config{
			Name:        "delegate_to_squad",
			Description: fmt.Sprintf("Registra o plano técnico e dispara dinamicamente de 1 até %d desenvolvedores paralelos em worktrees separadas (Dev(s) → QA → Chefe).", maxDevs),
		}, func(tctx agent.Context, args delegateArgs) (delegateResult, error) {
			delegated = true
			r.chiefPlan = strings.TrimSpace(args.Plan)

			cleaned := make([]string, 0, len(args.DevTasks))
			for _, s := range args.DevTasks {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					cleaned = append(cleaned, trimmed)
				}
			}
			if len(cleaned) > maxDevs && maxDevs > 0 {
				tail := strings.Join(cleaned[maxDevs-1:], "\n")
				cleaned = append(cleaned[:maxDevs-1], tail)
			}
			r.devSlices = cleaned
			r.prNumber = args.PRNumber
			r.prBranch = strings.TrimSpace(args.PRBranch)
			r.prReviewers = append([]string(nil), args.Reviewers...)
			r.selectedSkills = skillx.Match(r.skills, args.Skills)
			r.selectedNative = skillx.Match(r.NativeSkills, args.NativeSkills)
			// Fallback: nome pedido em "skills" que só existe como nativa da IDE (e vice-versa).
			for _, raw := range args.Skills {
				if skillx.Find(r.selectedSkills, raw) != nil {
					continue
				}
				if hit := skillx.Find(r.NativeSkills, raw); hit != nil && skillx.Find(r.selectedNative, raw) == nil {
					r.selectedNative = append(r.selectedNative, *hit)
					fmt.Fprintf(r.Err, "aviso: skill %q não existe no projeto; usando a nativa da IDE\n", raw)
				}
			}
			for _, raw := range args.NativeSkills {
				if skillx.Find(r.selectedNative, raw) != nil {
					continue
				}
				if hit := skillx.Find(r.skills, raw); hit != nil {
					r.selectedSkills = append(r.selectedSkills, *hit)
					fmt.Fprintf(r.Err, "aviso: skill nativa %q não existe na IDE; usando a versão do projeto\n", raw)
				}
			}
			// Conflito: o Chefe escolheu a nativa, mas o projeto tem versão própria — o usuário autoriza a troca.
			if len(r.selectedNative) > 0 {
				keptNative := make([]skillx.Skill, 0, len(r.selectedNative))
				for _, ns := range r.selectedNative {
					if skillx.Find(r.skills, ns.Name) == nil {
						keptNative = append(keptNative, ns)
						continue
					}
					prompt := fmt.Sprintf("O Chefe considera a skill nativa da IDE `%s` mais atual que a versão do projeto. Autorizar o uso da versão nativa da IDE? (Recuse para manter a versão do projeto.)", ns.Name)
					if r.Approve != nil && !r.Approve(prompt) {
						if skillx.Find(r.selectedSkills, ns.Name) == nil {
							if ps := skillx.Find(r.skills, ns.Name); ps != nil {
								r.selectedSkills = append(r.selectedSkills, *ps)
							}
						}
						continue
					}
					keptNative = append(keptNative, ns)
					r.selectedSkills = filterOutSkill(r.selectedSkills, ns.Name)
				}
				r.selectedNative = keptNative
			}
			if len(args.Skills) > 0 && len(r.selectedSkills) == 0 && len(r.selectedNative) == 0 {
				fmt.Fprintf(r.Err, "aviso: skills pedidas pelo Chefe não existem nos catálogos (%s e nativas da IDE): %s\n", skillx.SkillsDir, strings.Join(args.Skills, ", "))
			}
			skillNames := skillx.Names(r.selectedSkills)
			nativeNames := skillx.Names(r.selectedNative)
			joinedSlices := strings.Join(r.devSlices, "\n|||\n")
			_ = tctx.State().Set("chief_plan", r.chiefPlan)
			_ = tctx.State().Set("dev_slices_joined", joinedSlices)
			_ = tctx.State().Set("selected_skills", strings.Join(skillNames, "\n"))
			_ = tctx.State().Set("selected_native_skills", strings.Join(nativeNames, "\n"))
			if actions := tctx.Actions(); actions != nil {
				if actions.StateDelta == nil {
					actions.StateDelta = map[string]any{}
				}
				actions.StateDelta["chief_plan"] = r.chiefPlan
				actions.StateDelta["dev_slices_joined"] = joinedSlices
				actions.StateDelta["selected_skills"] = strings.Join(skillNames, "\n")
				actions.StateDelta["selected_native_skills"] = strings.Join(nativeNames, "\n")
			}

			resolved := r.resolveDynamicDevs()
			if r.OnEvent != nil {
				var hb strings.Builder
				if r.chiefPlan != "" {
					hb.WriteString(r.chiefPlan)
				}
				if r.prNumber > 0 {
					fmt.Fprintf(&hb, "\n- **PR Alvo**: #%d (%s)", r.prNumber, r.prBranch)
				}
				if skillLabel := skillSelectionLabel(skillNames, nativeNames); skillLabel != "" {
					fmt.Fprintf(&hb, "\n- **Skills selecionadas**: %s", skillLabel)
				}
				for i, slice := range r.devSlices {
					dn := fmt.Sprintf("dev_%d", i+1)
					if i < len(resolved) {
						dn = resolved[i].Name
					} else if len(r.devSlices) == 1 && len(baseDevs) > 0 {
						dn = baseDevs[0].Name
					}
					if strings.TrimSpace(slice) != "" {
						fmt.Fprintf(&hb, "\n- **%s**: %s", dn, strings.TrimSpace(slice))
					}
				}
				devCountLabel := fmt.Sprintf("%d Desenvolvedor", len(resolved))
				if len(resolved) > 1 {
					devCountLabel = fmt.Sprintf("%d Desenvolvedores em paralelo", len(resolved))
				}
				if runMode == "plan" {
					planCardEmitted = true
					r.OnEvent(Event{
						Kind:   "plan_ready",
						Stage:  "chief",
						Author: chief.Name,
						Label:  fmt.Sprintf("Plano pronto para aprovação (%s)", devCountLabel),
						Text:   strings.TrimSpace(hb.String()),
					})
				} else {
					r.OnEvent(Event{
						Kind:   "handoff",
						Stage:  "dev",
						Author: chief.Name,
						Label:  fmt.Sprintf("Chefe delegou → %s", devCountLabel),
						Text:   strings.TrimSpace(hb.String()),
					})
				}
			}
			if runMode == "plan" {
				return delegateResult{Status: fmt.Sprintf("plano registrado (%d dev(s) previstos); aguardando aprovação do usuário", len(resolved))}, nil
			}
			return delegateResult{Status: fmt.Sprintf("delegado para %d desenvolvedor(es) e QA em worktree separada", len(resolved))}, nil
		})
		if err != nil {
			return false, "", err
		}
		allTools = append(baseTools, delegateTool)
	}

	scaleGuidance := fmt.Sprintf(`ESCALA DINÂMICA DE DESENVOLVEDORES (1 até %d devs):
- O time base tem 3 agentes (Chefe/Arquiteto, Dev e QA), sem quantidade fixa de devs.
- Você decide quantos desenvolvedores disparar passando de 1 até %d itens em "dev_tasks":
  • Tarefa pequena/média ou concentrada nos mesmos arquivos: passe apenas 1 item em "dev_tasks" (dispara 1 dev, mais rápido e econômico).
  • Tarefa grande com módulos/arquivos independentes: divida em 2, 3, 4 ou até %d itens em "dev_tasks" (cada item dispara um dev paralelo em sua própria worktree isolada, SEM sobreposição de arquivos).`, maxDevs, maxDevs, maxDevs)

	govRules := `GOVERNANÇA GIT & PULL REQUESTS:
- Toda alteração de código roda em worktree separada via delegate_to_squad; apenas Chefe ou Arquiteto comitam e abrem PR.
- ANTES de opinar, comentar ou revisar qualquer PR, chame github_get_pr (o conector nativo GitHub retorna autoria, revisões formais, comentários inline no código e comentários gerais) e confira o campo "Papel neste PR":
  • Se o PR for NOSSO (autor = usuário autenticado): NUNCA atue como revisor nem emita parecer revisando o próprio PR. Leia todos os comentários gerais e inline deixados pelos revisores externos, faça a triagem técnica e despache para a equipe com delegate_to_squad informando pr_number, pr_branch e reviewers; ao terminar, o Chefe comita, responde ponto a ponto (github_comment_pr / github_reply_review_comment) e pede nova revisão (github_request_review).
  • Se o PR for DE TERCEIRO: só aprove/revise/comente quando houver pedido de revisão; use github_pr_diff para inspecionar o diff completo e github_review_pr para enviar o parecer.
- Só mescle (github_merge_pr) APÓS aprovação do revisor: em homolog pode mesclar direto; em main a ferramenta pede permissão ao usuário.`

	var triageInstr string
	switch runMode {
	case "direct":
		triageInstr = fmt.Sprintf(`%s

Você é o Chefe da equipe (%s) operando em MODO DIRETO (1 agente, sem delegar ao squad).
Inspecione arquivos/git/PRs se necessário e responda diretamente ao usuário com clareza, profundidade técnica e objetividade.
%s`,
			chief.Instructions, chief.Name, govRules)
	case "plan":
		triageInstr = fmt.Sprintf(`%s

Você é o Chefe da equipe (%s) operando em MODO PLANEJAMENTO (Human-in-the-Loop).
1. Inspecione os arquivos/PRs relevantes do projeto com read_file/list_dir/git_status/github_get_pr.
2. Elabore um plano arquitetural claro e chame delegate_to_squad preenchendo "plan" e "dev_tasks" (escolhendo dinamicamente de 1 até %d desenvolvedores conforme o tamanho da demanda).
3. Apresente um resumo objetivo do plano ao usuário informando quantos devs serão disparados e que a execução iniciará em worktree separada assim que ele aprovar.
%s
%s`,
			chief.Instructions, chief.Name, maxDevs, scaleGuidance, govRules)
	case "squad":
		triageInstr = fmt.Sprintf(`%s

Você é o Chefe da equipe (%s) operando em MODO SQUAD.
1. Inspecione os arquivos/PRs relevantes do projeto rapidamente.
2. Monte um plano objetivo, decida quantos desenvolvedores são necessários (1 até %d) e chame OBRIGATORIAMENTE delegate_to_squad passando "plan" e "dev_tasks" (e "pr_number", "pr_branch", "reviewers" se for revisão de PR existente).
%s
%s`,
			chief.Instructions, chief.Name, maxDevs, scaleGuidance, govRules)
	default:
		triageInstr = fmt.Sprintf(`%s

Você é o Chefe da equipe (%s).
REGRA DE TRIAGEM:
1. Se o usuário fez uma pergunta, pediu análise/diagnóstico, revisão de PR de terceiro (github_review_pr) ou merge de PR já aprovado pelo revisor (github_merge_pr) SEM precisar alterar código agora: execute as ferramentas de leitura/PR cabíveis, responda de forma clara e NÃO chame delegate_to_squad.
2. Se o usuário pediu para criar, editar, refatorar código OU atender mudanças pedidas em revisão de um PR nosso: monte um plano objetivo e chame delegate_to_squad passando "plan", "dev_tasks" (1 item para demandas pequenas/focadas, ou 2 a %d itens sem conflito de arquivos para demandas grandes) e, se for revisão de PR existente, "pr_number", "pr_branch" e "reviewers".
%s
%s`,
			chief.Instructions, chief.Name, maxDevs, scaleGuidance, govRules)
	}

	if catalogBlock := skillx.CatalogBlock(r.skills, r.NativeSkills); catalogBlock != "" {
		triageInstr += "\n\n" + catalogBlock +
			"SELEÇÃO DE SKILLS:\n" +
			"- Ao delegar com delegate_to_squad, avalie quais skills acima cercam ou aceleram a tarefa e passe os nomes exatos em \"skills\" (apenas as relevantes; vazio = nenhuma).\n" +
			"- Se a versão NATIVA da IDE parecer mais atual ou melhor que a versão do projeto, passe o nome em \"native_skills\"; se o projeto tiver uma skill de mesmo nome, o usuário será consultado para autorizar a substituição.\n" +
			"- Em modo direto/consulta você mesmo pode seguir uma skill: leia o arquivo SKILL.md indicado com read_file."
	}
	if note := r.pinnedSkillsNote(); note != "" {
		triageInstr += note
	}
	if chiefGuide := r.guidanceForRole(team.SkillTargetChief, false); chiefGuide != "" {
		triageInstr += chiefGuide
	}

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
		if ev != nil {
			r.recordUsage(ev, labels)
			if r.OnEvent != nil {
				r.emitEvent(ev, labels)
			}
			if r.Project.Settings.MaxTokens > 0 && r.turnTotalTokens > r.Project.Settings.MaxTokens {
				return false, "", fmt.Errorf("limite de tokens por execução atingido (%d > %d tokens)", r.turnTotalTokens, r.Project.Settings.MaxTokens)
			}
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
	if runMode == "plan" && !planCardEmitted && strings.TrimSpace(summary) != "" && r.OnEvent != nil {
		r.OnEvent(Event{
			Kind:   "plan_ready",
			Stage:  "chief",
			Author: chief.Name,
			Label:  "Plano pronto para aprovação",
			Text:   strings.TrimSpace(summary),
		})
	}
	return delegated, summary, nil
}
