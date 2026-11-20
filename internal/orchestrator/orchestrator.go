// Package orchestrator runs a HiveMind team with ADK sequential, parallel and loop agents.
package orchestrator

import (
	"context"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/mcpx"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/names"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/shellx"
	"github.com/gilvan-Borges/HiveMind/internal/skillx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/tools"
	"github.com/gilvan-Borges/HiveMind/internal/ui"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// Event is a structured live update emitted by Runner when OnEvent is set.
type Event struct {
	Kind              string   `json:"kind"` // "stage", "handoff", "devs_scaled", "usage", "text", "tool_call", "tool_result", "finish", "error", "plan_ready", "approval_required", "sdd_gate", "qa_pending", "model_route"
	Stage             string   `json:"stage,omitempty"`
	Author            string   `json:"author,omitempty"`
	Label             string   `json:"label,omitempty"`
	Text              string   `json:"text,omitempty"`
	ToolName          string   `json:"toolName,omitempty"`
	Detail            string   `json:"detail,omitempty"`
	Devs              []string `json:"devs,omitempty"`
	Gate              string   `json:"gate,omitempty"`       // "docs" | "jira" | "budget"
	ApprovalID        string   `json:"approvalId,omitempty"` // identificador único da aprovação pendente (permite aprovações concorrentes na mesma lane)
	Partial           bool     `json:"partial,omitempty"`
	Thought           bool     `json:"thought,omitempty"`
	InputTokens       int32    `json:"inputTokens,omitempty"`
	OutputTokens      int32    `json:"outputTokens,omitempty"`
	Tokens            int32    `json:"tokens,omitempty"`
	Provider          string   `json:"provider,omitempty"`
	Model             string   `json:"model,omitempty"`
	Tier              string   `json:"tier,omitempty"`
	Reasons           []string `json:"reasons,omitempty"`
	EstimatedMicroUSD int64    `json:"estimatedMicroUsd,omitempty"`
	DecisionMicros    int64    `json:"decisionMicros,omitempty"`
}

// Attachment represents an image or file attached by the user in chat.
type Attachment struct {
	Name     string `json:"name"`
	MIMEType string `json:"mimeType"`
	Data     []byte `json:"data"`
}

// Runner executes one team against a workspace.
type Runner struct {
	Workspace string
	Project   team.File
	Team      team.Team
	Git       gitx.Git
	GitHub    githubx.Client
	Jira      jirax.Client
	Sessions  *sessionx.Store
	Models    map[string]model.LLM
	NewModel  func(ctx context.Context, a team.Agent) (model.LLM, error)
	Confirm   shellx.Confirmer
	Approve   func(prompt string) bool
	// MCP é o cliente da execução. Nil nos testes: nenhum servidor é iniciado.
	MCP                *mcpx.Host
	Out                io.Writer
	Err                io.Writer
	Streaming          agent.StreamingMode
	OnEvent            func(Event)
	EnableTriage       bool
	EnableClosingChief bool
	// ReviewRoot optionally isolates read/test tools during auto-review onto an ephemeral PR worktree.
	ReviewRoot        string
	autoReviewMode    bool
	integration       string
	deliveryRoot      string
	deliveryBranch    string
	issueKey          string
	objectiveText     string
	reviewRequested   bool
	prNumber          int
	prBranch          string
	prReviewers       []string
	devSlices         []string
	chiefPlan         string
	chiefContextBrief string
	pauseForApproval  bool
	skills            []skillx.Skill
	selectedSkills    []skillx.Skill
	// NativeSkills é o catálogo nativo da IDE, preenchido pelo runner da IDE (CLI deixa vazio).
	NativeSkills     []skillx.Skill
	NativeSkillsRoot string
	selectedNative   []skillx.Skill
	turnInputTokens  int64
	turnOutputTokens int64
	turnTotalTokens  int64
	qaLastStatus     string
	qaStallCount     int
	hasImages        bool
	jevRepo          string
	devBudgets       map[string]*devTurnBudget
}

// devTurnBudget tracks, per developer agent and per Dev↔QA round, how many LLM
// calls were made, how many were pure exploration (reads/searches), how many
// files were edited, and how many input tokens were consumed. It feeds the
// performance guard that aborts token-burning exploration spirals.
type devTurnBudget struct {
	calls       int
	explore     int
	writes      int
	inputTokens int64
}

var devExploreTools = map[string]bool{
	"read_file": true, "search_text": true, "find_files": true, "list_dir": true,
	"git_status": true, "git_diff": true, "git_log": true,
}

var devWriteTools = map[string]bool{
	"edit_file": true, "write_file": true, "write_doc": true,
}

func (r *Runner) resetDevBudgets() {
	r.devBudgets = map[string]*devTurnBudget{}
}

// checkDevGuard enforces per-dev performance budgets on the event stream.
// A dev that reads/searches too much without editing, exceeds its call cap or
// its input-token budget aborts the run with an actionable error instead of
// letting the model burn millions of tokens. Budgets reset when QA takes over.
func (r *Runner) checkDevGuard(ev *session.Event, labels map[string]string) error {
	if ev == nil || ev.Partial || ev.Author == "" {
		return nil
	}
	label := labels[ev.Author]
	stage := inferStage(ev.Author, label)
	if stage == "qa" {
		// A QA pass means the next dev invocation is a fresh round.
		r.resetDevBudgets()
		return nil
	}
	if stage != "dev" {
		return nil
	}
	if r.devBudgets == nil {
		r.devBudgets = map[string]*devTurnBudget{}
	}
	author := strings.ToLower(strings.TrimSpace(ev.Author))
	b := r.devBudgets[author]
	if b == nil {
		b = &devTurnBudget{}
		r.devBudgets[author] = b
	}

	if u := ev.LLMResponse.UsageMetadata; u != nil && u.PromptTokenCount > 0 {
		b.inputTokens += int64(u.PromptTokenCount)
	}
	if ev.LLMResponse.Content != nil {
		for _, part := range ev.LLMResponse.Content.Parts {
			if part == nil || part.FunctionCall == nil {
				continue
			}
			name := strings.ToLower(strings.TrimSpace(part.FunctionCall.Name))
			b.calls++
			if devExploreTools[name] {
				b.explore++
			}
			if devWriteTools[name] {
				b.writes++
				b.explore = 0
			}
		}
	}

	if maxCalls := r.Project.Settings.EffectiveMaxDevCalls(); b.calls > maxCalls {
		return fmt.Errorf("desempenho do dev %q: %d chamadas de LLM nesta rodada (limite %d) sem concluir a entrega — execução interrompida para economizar tokens. Divida a tarefa em fatias menores, detalhe os arquivos exatos no plano do Chefe ou use um modelo mais rápido para os devs.", ev.Author, b.calls, maxCalls)
	}
	if maxExplore := r.Project.Settings.EffectiveMaxDevExploreCalls(); b.explore >= maxExplore && b.writes == 0 {
		return fmt.Errorf("desempenho do dev %q: %d buscas/leituras seguidas sem editar NENHUM arquivo — padrão de loop exploratório detectado; execução interrompida antes de queimar mais tokens. A fatia provavelmente está grande ou ambígua demais: reduza o escopo no dev_tasks ou especifique os arquivos/trechos exatos no plano do Chefe.", ev.Author, b.explore)
	}
	if maxInput := r.Project.Settings.EffectiveMaxDevInputTokens(); b.inputTokens > maxInput {
		return fmt.Errorf("orçamento do dev %q: %d tokens de entrada nesta rodada (limite %d) sem concluir a entrega — execução interrompida. Reduza o escopo da fatia ou troque o modelo do dev por um mais barato/rápido.", ev.Author, b.inputTokens, maxInput)
	}
	return nil
}

// Request is one user turn.
type Request struct {
	Objective   string
	JiraKey     string
	SessionID   string
	RunMode     string // "", "auto", "direct", "plan", "plan:sdd", "plan:sdd-jira", "squad"
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
	// PendingQA reports that the run finished without an explicit QA approval, so
	// no commit/push/PR was made on the delivery worktree (see finish).
	PendingQA bool `json:"pendingQa,omitempty"`
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
	ctx = modelx.WithWorkspace(ctx, r.Workspace)

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
			r.Jira = jirax.FromWorkspace(r.Workspace)
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
	r.chiefContextBrief = ""
	r.skills = skillx.Load(r.Workspace)
	r.selectedSkills = nil
	r.selectedNative = nil
	r.turnInputTokens = 0
	r.turnOutputTokens = 0
	r.turnTotalTokens = 0
	r.qaLastStatus = ""
	r.qaStallCount = 0
	r.hasImages = hasImageAttachments(req.Attachments)
	r.jevRepo = r.resolveRepoKey(ctx)
	r.resetDevBudgets()

	runMode := strings.ToLower(strings.TrimSpace(req.RunMode))
	if runMode == "plan:feature" {
		runMode = "plan"
	}
	r.autoReviewMode = runMode == "auto-review"
	if r.autoReviewMode {
		r.reviewRequested = true
	}
	sess, err := r.openSession(ctx, req.SessionID, objective, key, runMode)
	if err != nil {
		return result, err
	}
	result.SessionID = sess.ID()
	ctx = modelx.WithSessionID(ctx, sess.ID())

	mode := r.Streaming
	if mode == "" {
		mode = agent.StreamingModeSSE
	}
	msg := buildUserContent(objective+r.teamMemorySnippet(), req.Attachments)

	useTriage := (runMode == "" && r.EnableTriage) ||
		runMode == "auto" ||
		runMode == "direct" ||
		runMode == "auto-review" ||
		runMode == "plan" ||
		runMode == "plan:sdd" ||
		runMode == "plan:sdd-jira" ||
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
		if runMode == "plan" || (r.pauseForApproval && (runMode == "" || runMode == "auto") && delegated) {
			result.Summary = summary
			planBody := strings.TrimSpace(r.chiefPlan)
			if planBody == "" {
				planBody = summary
			}
			_ = sessionx.UpdateState(sess, map[string]any{
				"chief_plan":            planBody,
				"chief_context_brief":   strings.TrimSpace(r.chiefContextBrief),
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
		if runMode == "plan:sdd" || runMode == "plan:sdd-jira" {
			result.Summary = summary
			if runMode == "plan:sdd-jira" && delegated && r.chiefPlan != "" {
				_ = sessionx.UpdateState(sess, map[string]any{
					"chief_plan":            r.chiefPlan,
					"chief_context_brief":   strings.TrimSpace(r.chiefContextBrief),
					"dev_slices_joined":     strings.Join(r.devSlices, "\n|||\n"),
					"plan_pending_approval": "1",
				})
			}
			if r.OnEvent != nil {
				r.OnEvent(Event{
					Kind:         "finish",
					Stage:        "chief",
					Text:         fmt.Sprintf("Fase SDD concluída na sessão %s — aguardando aprovação do gate para avançar.", result.SessionID),
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

	if mem := r.teamMemorySnippet(); mem != "" {
		_ = sess.State().Set("team_memory", mem)
	} else {
		_ = sess.State().Set("team_memory", "")
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
			r.printUsageSummary(result, r.Err, "uso até o erro: ")
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
			if guardErr := r.checkDevGuard(ev, labels); guardErr != nil {
				result.InputTokens = r.turnInputTokens
				result.OutputTokens = r.turnOutputTokens
				result.TotalTokens = r.turnTotalTokens
				r.printUsageSummary(result, r.Err, "uso até a interrupção: ")
				if r.OnEvent != nil {
					r.OnEvent(Event{Kind: "error", Text: guardErr.Error()})
				}
				return result, guardErr
			}
			if r.Project.Settings.MaxTokens > 0 && r.turnTotalTokens > r.Project.Settings.MaxTokens {
				budgetErr := fmt.Errorf("limite de tokens por execução atingido (%d > %d tokens)", r.turnTotalTokens, r.Project.Settings.MaxTokens)
				result.InputTokens = r.turnInputTokens
				result.OutputTokens = r.turnOutputTokens
				result.TotalTokens = r.turnTotalTokens
				r.printUsageSummary(result, r.Err, "uso até parar: ")
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

	if err := r.finish(ctx, sess, &result, devs, key, objective); err != nil {
		if r.OnEvent != nil {
			r.OnEvent(Event{Kind: "error", Text: err.Error()})
		}
		return result, err
	}
	// Trim bulky write_file arguments and old tool output before the next turn
	// reuses this session. The current run already finished, so no prompt is
	// affected.
	_ = sessionx.CompactOldToolOutputs(sess, 12)
	finishText := fmt.Sprintf("Sessão %s concluída.", result.SessionID)
	if result.PendingQA {
		finishText = fmt.Sprintf("Sessão %s concluída, mas a entrega ficou PENDENTE de aprovação do QA: nenhum commit, push ou PR foi feito.", result.SessionID)
	}
	fmt.Fprintf(r.Out, "\n%s\n", finishText)
	r.printUsageSummary(result, r.Out, "")
	if r.OnEvent != nil {
		r.OnEvent(Event{
			Kind:         "finish",
			Text:         finishText,
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
	stateKeys := []string{"objective", "jira_key", "team_memory", "prior_session_context", "chief_plan", "chief_context_brief", "pm_plan", "tech_lead_plan", "architect_plan", "integrate_status", "qa_feedback", "qa_report", "qa_approval_note"}

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
		extra := fmt.Sprintf("Seu diretório de trabalho isolado é %s.\nSua branch de rascunho é %s.\nO Chefe JÁ leu a spec/plano e registrou os contratos essenciais em [chief_plan] e [chief_context_brief]. NÃO releia spec.md/plan.md/tasks.md inteiros em loop; leia apenas os arquivos de código que precisa editar/chamar (máx. 3-4 leituras por turno) e comece a implementar logo.\nORÇAMENTO RÍGIDO: você tem até %d chamadas de LLM nesta rodada e no máximo %d leituras/buscas antes da primeira edição — se estourar, a execução inteira é interrompida. Localize os trechos com 1-2 buscas, edite e siga em frente.\nPara modificar arquivos existentes, use SEMPRE edit_file (preserva bytes/encoding/CRLF e economiza tokens); reserve write_file para arquivos novos e nunca use scripts python/node via run_shell para editar texto.\nNÃO execute git commit, git push nem abra PR (apenas o Chefe ou o Arquiteto comitam na worktree de entrega após aprovação do QA).\nNão altere arquivos fora desse diretório.\nSe houver [qa_feedback] ou [qa_report], corrija prioritariamente todos os pontos apontados pelo QA.", dev.Root, dev.Branch, r.Project.Settings.EffectiveMaxDevCalls(), r.Project.Settings.EffectiveMaxDevExploreCalls())
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
	ctx = modelx.WithAgentDir(ctx, root)
	label := roleLabel(a.Role) + " · " + a.Name
	inTok := int(r.turnInputTokens)
	if inTok <= 0 {
		inTok = jev.EstimateFirstStepInputTokens(len(r.objectiveText)+len(extra)+len(r.chiefPlan), len(r.teamMemorySnippet()), true)
	}
	runMode := ""
	if r.autoReviewMode {
		runMode = "auto-review"
	}
	ctx = withJevRouteHint(ctx, jevRouteHint{
		RunMode:      runMode,
		Step:         outputKey,
		ObjectiveLen: len(r.objectiveText),
		InputTokens:  inTok,
		HasImages:    r.hasImages,
		NeedsTools:   true,
		QAStall:      r.qaStallCount,
		Workspace:    r.Workspace,
		RepoKey:      r.jevRepo,
		Author:       adkName,
		Label:        label,
		Stage:        inferStage(adkName, label),
		Approve:      r.Approve,
		OnEvent:      r.OnEvent,
	})
	llm, err := r.modelFor(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("modelo de %s (provedor %s): %w", a.Name, a.Provider, err)
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
		MCP:                  r.MCP,
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
		Name:        adkName,
		Description: roleLabel(a.Role) + " do time HiveMind.",
		Model:       llm,
		Mode:        llmagent.ModeChat,
		// Pipeline agents receive the plan/reports through the explicit state
		// handoff below. Keeping the full prior conversation as well duplicates
		// that context in every prompt (one of the main drivers of the ~1.2M
		// tokens/run measured in docs/DOGSFOODING.md).
		IncludeContents:          llmagent.IncludeContentsNone,
		DisallowTransferToParent: true,
		DisallowTransferToPeers:  true,
		Tools:                    roleTools,
		OutputKey:                outputKey,
		BeforeModelCallbacks: []llmagent.BeforeModelCallback{
			newInTurnCompactionCallback(r.Project.Settings.EffectiveInTurnKeepToolResults()),
		},
		BeforeToolCallbacks: []llmagent.BeforeToolCallback{
			newRepeatReadGuardCallback(),
		},
		InstructionProvider: func(ctx agent.ReadonlyContext) (string, error) {
			var b strings.Builder
			b.WriteString(instruction)
			if outputKey == "closing_summary" && !qaApprovedReadonly(ctx.ReadonlyState()) {
				b.WriteString("\n\n[GATE DE QA — BLOQUEIO ATIVO]\nO QA NÃO aprovou esta entrega (`qa_approve` não foi chamado). Ferramentas de commit, push e PR estão bloqueadas. NÃO tente commitar, dar push ou abrir/atualizar PR; apenas apresente um resumo claro do que foi tentado, do feedback do QA e do que ficou pendente na worktree.")
			}
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
	if outputKey == "closing_summary" {
		cfg.BeforeToolCallbacks = append(cfg.BeforeToolCallbacks, func(ctx agent.Context, tl tool.Tool, _ map[string]any) (map[string]any, error) {
			if tl == nil || !isClosingPublishTool(tl.Name()) {
				return nil, nil
			}
			if qaApprovedReadonly(ctx.ReadonlyState()) {
				return nil, nil
			}
			return map[string]any{
				"blocked": true,
				"error":   "entrega NÃO aprovada pelo QA (qa_approve não foi chamado): operações de commit, push ou PR estão bloqueadas",
			}, nil
		})
	}
	if qa {
		cfg.AfterToolCallbacks = []llmagent.AfterToolCallback{
			func(ctx agent.Context, tl tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
				if err != nil || tl == nil || ctx.Actions() == nil {
					return nil, nil
				}
				switch tl.Name() {
				case "qa_approve":
					ctx.Actions().Escalate = true
				case "qa_request_changes":
					// dogfooding achado #13: QA girando com a entrega imutável
					// queimava tokens sem backpressure (6 reprovações na T3).
					if msg := r.recordQAStall(); msg != "" {
						ctx.Actions().Escalate = true
						if ctx.Actions().StateDelta == nil {
							ctx.Actions().StateDelta = map[string]any{}
						}
						ctx.Actions().StateDelta["qa_feedback"] = msg
						if r.Out != nil {
							fmt.Fprintf(r.Err, "aviso: %s\n", msg)
						}
					}
				}
				return nil, nil
			},
		}
	}
	return llmagent.New(cfg)
}

// recordQAStall conta reprovações consecutivas do QA sem nenhuma alteração na
// entrega desde a reprovação anterior. Na terceira reprovação com a entrega
// imutável, devolve a mensagem que encerra o loop Dev→QA (o Chefe de fecho
// recebe via state qa_feedback e o CLI imprime no stderr).
func (r *Runner) recordQAStall() string {
	if r.Git == nil || r.deliveryRoot == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := r.Git.Status(ctx, r.deliveryRoot)
	if err != nil {
		return ""
	}
	defer func() { r.qaLastStatus = status }()
	if status != r.qaLastStatus {
		r.qaStallCount = 0
		return ""
	}
	r.qaStallCount++
	if r.qaStallCount < 2 {
		return ""
	}
	r.qaStallCount = 0
	return "O QA reprovou de novo sem nenhuma alteração nova na entrega desde a reprovação anterior; o ciclo Dev→QA foi interrompido para não queimar mais chamadas. Revise manualmente o feedback do QA e o estado da entrega."
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
	if strings.EqualFold(strings.TrimSpace(a.Provider), "jev") {
		return NewJevModelFactory(JevFactoryOptions{
			Workspace: r.Workspace,
			Approve:   r.Approve,
			OnEvent:   r.OnEvent,
		})(ctx, a)
	}
	return modelx.New(ctx, a.Provider, a.Model)
}

func (r *Runner) openSession(ctx context.Context, id, objective, key, runMode string) (session.Session, error) {
	consumePendingPlan := runMode == "squad"
	if id != "" {
		got, err := r.Sessions.Get(ctx, &session.GetRequest{AppName: sessionx.AppName, UserID: sessionx.UserID, SessionID: id})
		if err != nil {
			return nil, fmt.Errorf("retomar sessão %s: %w", id, err)
		}
		pendingFlag, _ := got.Session.State().Get("plan_pending_approval")
		isPendingPlan := strings.TrimSpace(fmt.Sprint(pendingFlag)) == "1"
		docsFlag, _ := got.Session.State().Get("sdd_docs_pending")
		isDocsPending := strings.TrimSpace(fmt.Sprint(docsFlag)) == "1"
		if runMode == "plan:sdd-jira" && !isDocsPending {
			return nil, fmt.Errorf("não há docs SDD pendentes de aprovação nesta sessão; execute primeiro o modo Planejar · Projeto SDD (plan:sdd) e aprove o Gate 1 (Docs & Arquitetura)")
		}
		if consumePendingPlan && isPendingPlan {
			if prevPlan, err := got.Session.State().Get("chief_plan"); err == nil && prevPlan != nil {
				r.chiefPlan = strings.TrimSpace(fmt.Sprint(prevPlan))
			}
			if prevBrief, err := got.Session.State().Get("chief_context_brief"); err == nil && prevBrief != nil {
				r.chiefContextBrief = strings.TrimSpace(fmt.Sprint(prevBrief))
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
		priorCtx := summarizePriorSessionContext(got.Session)
		keepPlan := (consumePendingPlan && isPendingPlan && r.chiefPlan != "") ||
			(runMode == "plan:sdd") ||
			(runMode == "plan:sdd-jira" && isDocsPending)
		resetMap := map[string]any{
			"prior_session_context": priorCtx,
			"qa_feedback":           "",
			"qa_report":             "",
			"qa_approval_note":      "",
			"qa_approved":           "",
			"integrate_status":      "",
			"plan_pending_approval": "",
			"dev_slices_joined":     "",
			"pr_number":             "",
			"pr_branch":             "",
			"pr_reviewers":          "",
		}
		if runMode == "plan:sdd" || runMode == "plan:sdd-jira" {
			resetMap["sdd_stage"] = map[bool]string{true: "docs", false: "backlog"}[runMode == "plan:sdd"]
			if runMode == "plan:sdd" {
				resetMap["sdd_docs_pending"] = ""
			}
		} else {
			resetMap["sdd_docs_pending"] = ""
			resetMap["sdd_stage"] = ""
		}
		if !keepPlan {
			resetMap["chief_plan"] = ""
			resetMap["chief_context_brief"] = ""
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
			"prior_session_context": "",
			"qa_feedback":           "",
			"qa_report":             "",
			"qa_approval_note":      "",
			"qa_approved":           "",
			"integrate_status":      "",
			"chief_plan":            "",
			"chief_context_brief":   "",
			"dev_slices_joined":     "",
			"pr_number":             "",
			"pr_branch":             "",
			"pr_reviewers":          "",
			"plan_pending_approval": "",
			"sdd_stage":             "",
			"sdd_docs_pending":      "",
		},
	})
	if err != nil {
		return nil, err
	}
	return created.Session, nil
}

func (r *Runner) finish(ctx context.Context, sess session.Session, result *Result, devs []devEnv, key, objective string) error {
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

	// Governance gate: a delivery is only committed/pushed/opened as PR after an
	// explicit qa_approve from the QA agent. Without it, the code stays in the
	// delivery worktree and the run is reported as pending QA. This is what makes
	// "QA honesto" a product rule instead of a prompt instruction.
	if !qaApproved(sess) {
		result.PendingQA = true
		msg := "entrega NÃO aprovada pelo QA: nenhum commit, push ou PR foi feito; as alterações ficaram na worktree de entrega"
		fmt.Fprintf(r.Err, "aviso: %s\n", msg)
		if r.OnEvent != nil {
			r.OnEvent(Event{Kind: "qa_pending", Stage: "qa", Text: msg})
		}
		return nil
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

// qaApproved reports whether the QA agent explicitly approved this delivery.
// qa_approved is the authoritative flag; qa_approval_note is accepted as a
// fallback for sessions created before the flag existed.
func qaApproved(sess session.Session) bool {
	if sess == nil {
		return false
	}
	return qaApprovedReadonly(sess.State())
}

func qaApprovedReadonly(st session.ReadonlyState) bool {
	if st == nil {
		return false
	}
	if v, err := st.Get("qa_approved"); err == nil && strings.TrimSpace(fmt.Sprint(v)) == "1" {
		return true
	}
	if v, err := st.Get("qa_approval_note"); err == nil && strings.TrimSpace(fmt.Sprint(v)) != "" {
		return true
	}
	return false
}

func isClosingPublishTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "git_commit",
		"git_push",
		"github_open_pr",
		"github_create_pr",
		"github_comment_pr",
		"github_reply_review_comment",
		"github_request_review",
		"github_merge_pr",
		"github_review_pr":
		return true
	default:
		return false
	}
}

// summarizePriorSessionContext extracts a compact digest of prior user prompts and
// final agent summaries from a resumed session so pipeline agents using
// IncludeContentsNone still see earlier requirements and decisions without
// replaying bulky tool-call history.
func summarizePriorSessionContext(sess session.Session) string {
	if sess == nil || sess.Events() == nil || sess.Events().Len() == 0 {
		return ""
	}
	type entry struct {
		who  string
		text string
	}
	var items []entry
	for ev := range sess.Events().All() {
		if ev == nil || ev.Partial || ev.LLMResponse.Content == nil {
			continue
		}
		var parts []string
		for _, p := range ev.LLMResponse.Content.Parts {
			if p == nil || p.Thought || strings.TrimSpace(p.Text) == "" {
				continue
			}
			parts = append(parts, strings.TrimSpace(p.Text))
		}
		if len(parts) == 0 {
			continue
		}
		who := strings.TrimSpace(ev.Author)
		if ev.LLMResponse.Content.Role == genai.RoleUser || who == "" || strings.EqualFold(who, "user") {
			who = "Usuário"
		}
		joined := strings.Join(parts, "\n")
		if len(joined) > 600 {
			joined = joined[:600] + "…"
		}
		items = append(items, entry{who: who, text: joined})
	}
	if len(items) == 0 {
		return ""
	}
	const maxItems = 8
	if len(items) > maxItems {
		items = items[len(items)-maxItems:]
	}
	var b strings.Builder
	const maxTotal = 3600
	for _, it := range items {
		line := fmt.Sprintf("- %s: %s\n", it.who, it.text)
		if b.Len()+len(line) > maxTotal {
			break
		}
		b.WriteString(line)
	}
	return strings.TrimSpace(b.String())
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

// teamMemorySnippet devolve a memória compartilhada do time (.hivemind/MEMORY.md)
// para injeção em todo run do CLI — paridade com a IDE (dogfooding achado #7:
// conhecimento caro descoberto num run não ajudava o próximo no CLI). Cria o
// arquivo com orientação inicial quando ainda não existe. O objetivo em si
// (r.objectiveText) permanece limpo para nomes de branch e commits.
func (r *Runner) teamMemorySnippet() string {
	target, err := workspace.ResolveWithOptions(r.Workspace, ".hivemind/MEMORY.md", workspace.Options{AllowMemoryRead: true})
	if err != nil {
		return ""
	}
	if _, statErr := os.Stat(target); os.IsNotExist(statErr) {
		starter := "# Memória Compartilhada do Time (HiveMind)\n\n" +
			"Anote aqui decisões arquiteturais, convenções de código, comandos específicos e restrições do projeto.\n" +
			"Todos os agentes da equipe recebem este contexto automaticamente sem precisar repetir no chat.\n\n" +
			"## Convenções & Arquitetura\n- \n\n" +
			"## Comandos & Ambiente\n- \n"
		_ = os.MkdirAll(filepath.Dir(target), 0o700)
		_ = os.WriteFile(target, []byte(starter), 0o600)
		return ""
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return ""
	}
	txt := strings.TrimSpace(string(data))
	if txt == "" {
		return ""
	}
	txt = strx.TruncateBytes(txt, 4096, "\n…")
	return "\n[Memória Compartilhada do Time (.hivemind/MEMORY.md)]\n" + txt + "\n"
}

func (r *Runner) printUsageSummary(result Result, w io.Writer, prefix string) {
	line := usageSummaryLine(result)
	if line == "" || w == nil {
		return
	}
	fmt.Fprintf(w, "%s%s\n", prefix, line)
}

// usageSummaryLine formats the per-run token usage printed by the CLI at the
// end of a run (dogfooding achado #8: cost was invisible outside the IDE).
func usageSummaryLine(result Result) string {
	if result.TotalTokens <= 0 {
		return ""
	}
	return fmt.Sprintf("Tokens: ↑%d entrada · ↓%d saída · %d total", result.InputTokens, result.OutputTokens, result.TotalTokens)
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
	switch name {
	case "read_file":
		if p, ok := args["path"]; ok && p != nil {
			s := strings.TrimSpace(fmt.Sprint(p))
			if s != "" {
				start := parseIntArg(args["start_line"])
				end := parseIntArg(args["end_line"])
				if start > 0 && end > 0 {
					s = fmt.Sprintf("%s:%d-%d", s, start, end)
				} else if start > 0 {
					s = fmt.Sprintf("%s:%d", s, start)
				}
				return strx.TruncateRunes(strings.ReplaceAll(s, "\n", " "), 120, "…")
			}
		}
	case "search_text":
		q := strings.TrimSpace(fmt.Sprint(args["query"]))
		if q != "" && q != "<nil>" {
			if g := strings.TrimSpace(fmt.Sprint(args["glob"])); g != "" && g != "<nil>" {
				q += " (" + g + ")"
			} else if p := strings.TrimSpace(fmt.Sprint(args["path"])); p != "" && p != "<nil>" {
				q += " em " + p
			}
			return strx.TruncateRunes(strings.ReplaceAll(q, "\n", " "), 120, "…")
		}
	case "find_files":
		pat := strings.TrimSpace(fmt.Sprint(args["pattern"]))
		if pat != "" && pat != "<nil>" {
			if p := strings.TrimSpace(fmt.Sprint(args["path"])); p != "" && p != "<nil>" {
				pat += " em " + p
			}
			return strx.TruncateRunes(strings.ReplaceAll(pat, "\n", " "), 120, "…")
		}
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
	for _, key := range []string{"path", "query", "pattern", "command", "message", "title", "number", "key", "jql", "ref", "note", "notes"} {
		if v, ok := args[key]; ok && v != nil {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" {
				return strx.TruncateRunes(strings.ReplaceAll(s, "\n", " "), 120, "…")
			}
		}
	}
	return ""
}

func parseIntArg(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	default:
		return 0
	}
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

// nativeSkillGuide injeta os corpos das skills NATIVAS da IDE pedidas por nome
// (usado pelas fases SDD/Planejamento, que contam com skills nativas semeadas).
// Nomes inexistentes são ignorados silenciosamente (o usuário pode ter excluído
// a skill do catálogo nativo).
func (r *Runner) nativeSkillGuide(names ...string) string {
	if len(names) == 0 || r.NativeSkillsRoot == "" {
		return ""
	}
	var picked []skillx.Skill
	for _, n := range names {
		if hit := skillx.Find(r.NativeSkills, n); hit != nil {
			picked = append(picked, *hit)
		}
	}
	if len(picked) == 0 {
		return ""
	}
	return skillx.GuidanceBlock(r.Workspace, r.NativeSkillsRoot, nil, picked, 0, 0)
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
	Plan             string   `json:"plan" jsonschema:"Plano técnico detalhado para a equipe executar."`
	ContextBrief     string   `json:"context_brief,omitempty" jsonschema:"Dossiê técnico com os pontos importantes que você JÁ leu (regras RN exatas, assinaturas de classes/interfaces/métodos, campos de DTO, códigos HTTP/erro, rotas, caminhos de arquivos e faixas de linha) para o Dev e o QA consultarem direto sem reler spec.md/plan.md/tasks.md inteiros."`
	DevTasks         []string `json:"dev_tasks" jsonschema:"Lista dinâmica de 1 até max_dynamic_devs subtarefas SEM sobreposição de arquivos. Cada item dispara 1 desenvolvedor paralelo em worktree isolada (use 1 item para tarefas pequenas/focadas, ou 2 a 5 itens para entregas grandes e paralelizáveis)."`
	PRNumber         int      `json:"pr_number,omitempty" jsonschema:"Número do PR existente quando a tarefa é corrigir mudanças solicitadas em revisão."`
	PRBranch         string   `json:"pr_branch,omitempty" jsonschema:"Branch headRef do PR existente, quando houver."`
	Reviewers        []string `json:"reviewers,omitempty" jsonschema:"Logins dos revisores para pedir nova revisão após corrigir (ex: quem pediu mudança)."`
	Skills           []string `json:"skills,omitempty" jsonschema:"Opcional: nomes exatos das skills do catálogo .agents/skills do projeto que a equipe deve seguir (apenas as relevantes para a tarefa; vazio significa nenhuma)."`
	NativeSkills     []string `json:"native_skills,omitempty" jsonschema:"Opcional: nomes das skills NATIVAS da IDE que a equipe deve seguir. Use quando a nativa parecer mais atual ou melhor que a versão do projeto; se houver conflito de nome, o usuário será consultado para autorizar."`
	PauseForApproval bool     `json:"pause_for_approval,omitempty" jsonschema:"No modo Auto: use true quando a tarefa for grande, arriscada ou de escopo amplo e você julgar que o usuário deve revisar e aprovar o plano ANTES de disparar o Squad. false (padrão) executa direto."`
}

type delegateResult struct {
	Status string `json:"status"`
}

func (r *Runner) resolveTriageChief() team.Agent {
	chief := r.primaryChief()
	if r.autoReviewMode {
		if arm := strings.TrimSpace(r.Project.Settings.AutoReview.Model); arm != "" {
			chief.Model = arm
			chief.Provider = team.NormalizeProvider("", arm)
			return chief
		}
	}
	if r.reviewRequested {
		if rm := strings.TrimSpace(r.Project.Settings.ReviewModel); rm != "" {
			chief.Model = rm
			chief.Provider = team.NormalizeProvider("", rm)
		}
	}
	return chief
}

func (r *Runner) runTriageChief(ctx context.Context, sess session.Session, msg *genai.Content, jiraKey string, mode agent.StreamingMode, runMode string) (bool, string, error) {
	chief := r.resolveTriageChief()

	sddDocs := runMode == "plan:sdd"
	sddJira := runMode == "plan:sdd-jira"
	isAutoReview := runMode == "auto-review"

	toolRoot := r.Workspace
	if isAutoReview && strings.TrimSpace(r.ReviewRoot) != "" {
		toolRoot = r.ReviewRoot
	}
	triageCtx := modelx.WithAgentDir(ctx, toolRoot)
	triageCtx = withJevRouteHint(triageCtx, jevRouteHint{
		RunMode:      runMode,
		Step:         "triage_chief",
		ObjectiveLen: len(r.objectiveText),
		InputTokens:  jev.EstimateFirstStepInputTokens(len(r.objectiveText), len(r.teamMemorySnippet()), true),
		HasImages:    r.hasImages || hasImageContent(msg),
		NeedsTools:   true,
		QAStall:      r.qaStallCount,
		Workspace:    r.Workspace,
		RepoKey:      r.jevRepo,
		Author:       chief.Name,
		Label:        roleLabel(chief.Role) + " · " + chief.Name,
		Stage:        "chief",
		Approve:      r.Approve,
		OnEvent:      r.OnEvent,
	})
	llm, err := r.modelFor(triageCtx, chief)
	if err != nil {
		return false, "", fmt.Errorf("modelo de %s: %w", chief.Name, err)
	}

	// Triage Chief uses RoleProjectManager with DisableGitWrite=true so it never edits or commits on main workspace,
	// while retaining full PR inspection, comment, review, request-review, and merge governance tools.
	// In auto-review mode, DisableMerge=true physically omits github_merge_pr (hard-gate).
	// SDD phases narrow this further: DocsOnlyWrite blocks code and Jira/GitHub writes until the gates are approved.
	baseTools, err := tools.ForRole(team.RoleProjectManager, tools.Deps{
		Root:                   toolRoot,
		Settings:               r.Project.Settings,
		DisableGitWrite:        true,
		DisableMerge:           isAutoReview,
		ReviewCommentOnly:      isAutoReview && r.Project.Settings.AutoReview.EffectiveReviewAction() == "comment_only",
		AllowReadOnlyTestShell: isAutoReview && r.Project.Settings.AutoReview.EffectiveRunTests(),
		DocsOnlyWrite:          sddDocs || sddJira,
		ReviewRequestedInObj:   r.reviewRequested || isAutoReview,
		Allowlist:              r.Project.Settings.ShellAllowlist,
		Confirm:                r.Confirm,
		Approve:                r.Approve,
		Git:                    r.Git,
		GitHub:                 r.GitHub,
		Jira:                   r.Jira,
		JiraKey:                jiraKey,
		JiraProject:            r.Project.Settings.JiraProject,
		BaseBranch:             r.Project.Settings.BaseBranch,
		MCP:                    r.MCP,
	})
	if err != nil {
		return false, "", err
	}
	if sddDocs {
		// Fase SDD de descoberta/docs: sem Jira, sem GitHub e sem delegate — apenas leitura + write_doc + gate.
		baseTools, err = tools.ForRole(team.RoleProjectManager, tools.Deps{
			Root:                 r.Workspace,
			Settings:             r.Project.Settings,
			DisableGitWrite:      true,
			DocsOnlyWrite:        true,
			ReviewRequestedInObj: r.reviewRequested,
			Confirm:              r.Confirm,
			Approve:              r.Approve,
			Git:                  r.Git,
			GitHub:               githubx.Disabled{Reason: "fase SDD de docs não opera GitHub"},
			Jira:                 nil,
			JiraKey:              jiraKey,
			JiraProject:          r.Project.Settings.JiraProject,
			BaseBranch:           r.Project.Settings.BaseBranch,
			MCP:                  r.MCP,
		})
		if err != nil {
			return false, "", err
		}
	}
	if sddJira {
		// Fase SDD de backlog: Jira habilitado para criar Epics/Tasks/Subtasks + write_doc para registrar as chaves na spec.
		baseTools, err = tools.ForRole(team.RoleProjectManager, tools.Deps{
			Root:                 r.Workspace,
			Settings:             r.Project.Settings,
			DisableGitWrite:      true,
			DocsOnlyWrite:        true,
			ReviewRequestedInObj: r.reviewRequested,
			Confirm:              r.Confirm,
			Approve:              r.Approve,
			Git:                  r.Git,
			GitHub:               githubx.Disabled{Reason: "fase SDD de backlog não opera GitHub"},
			Jira:                 r.Jira,
			JiraKey:              jiraKey,
			JiraProject:          r.Project.Settings.JiraProject,
			BaseBranch:           r.Project.Settings.BaseBranch,
			MCP:                  r.MCP,
		})
		if err != nil {
			return false, "", err
		}
	}

	baseDevs := r.Team.ByRole(team.RoleDeveloper)
	maxDevs := r.maxAllowedDevs()

	var delegated bool
	var planCardEmitted bool
	allTools := baseTools

	if sddDocs {
		docsReady, err := functiontool.New(functiontool.Config{
			Name:        "sdd_docs_ready",
			Description: "Encerra a fase SDD de Discovery & Docs: registra o resumo da especificação gravada e pausa no Gate 1 para o usuário aprovar antes de qualquer criação no Jira ou código.",
		}, func(tctx agent.Context, args struct {
			Summary string `json:"summary" jsonschema:"Resumo executivo: artefatos SDD gravados (specs/docs/ADRs), principais decisões e próximos passos."`
		}) (delegateResult, error) {
			text := strings.TrimSpace(args.Summary)
			if text == "" {
				return delegateResult{}, fmt.Errorf("informe o resumo dos docs SDD gravados")
			}
			_ = tctx.State().Set("sdd_docs_pending", "1")
			_ = tctx.State().Set("chief_plan", text)
			if actions := tctx.Actions(); actions != nil {
				if actions.StateDelta == nil {
					actions.StateDelta = map[string]any{}
				}
				actions.StateDelta["sdd_docs_pending"] = "1"
				actions.StateDelta["chief_plan"] = text
			}
			if r.OnEvent != nil {
				r.OnEvent(Event{
					Kind:   "sdd_gate",
					Stage:  "chief",
					Author: chief.Name,
					Gate:   "docs",
					Label:  "Gate 1 · Docs & Arquitetura SDD prontos",
					Text:   text,
				})
			}
			return delegateResult{Status: "docs SDD registrados; aguardando aprovação do usuário no Gate 1 (Criar Tasks no Jira)"}, nil
		})
		if err != nil {
			return false, "", err
		}
		allTools = append(baseTools, docsReady)
	}

	if runMode != "direct" && runMode != "auto-review" && !sddDocs {
		delegateTool, err := functiontool.New(functiontool.Config{
			Name:        "delegate_to_squad",
			Description: fmt.Sprintf("Registra o plano técnico e dispara dinamicamente de 1 até %d desenvolvedores paralelos em worktrees separadas (Dev(s) → QA → Chefe).", maxDevs),
		}, func(tctx agent.Context, args delegateArgs) (delegateResult, error) {
			delegated = true
			r.chiefPlan = strings.TrimSpace(args.Plan)
			r.chiefContextBrief = strings.TrimSpace(args.ContextBrief)
			if args.PauseForApproval && (runMode == "" || runMode == "auto") {
				r.pauseForApproval = true
			}

			cleaned := make([]string, 0, len(args.DevTasks))
			seenTasks := make(map[string]bool, len(args.DevTasks))
			for _, s := range args.DevTasks {
				trimmed := strings.TrimSpace(s)
				if trimmed == "" {
					continue
				}
				// dogfooding achado #4: fatias idênticas disparavam 2 devs na MESMA
				// tarefa (~261k tokens desperdiçados na T1). Duplicatas exatas caem.
				key := strings.ToLower(trimmed)
				if seenTasks[key] {
					continue
				}
				seenTasks[key] = true
				cleaned = append(cleaned, trimmed)
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
			if runMode == "plan:sdd-jira" {
				// Fase SDD: devs e QA seguem as skills nativas de execução/verificação SDD por padrão.
				for _, n := range []string{"sdd-dev-executor", "sdd-qa-verifier"} {
					if hit := skillx.Find(r.NativeSkills, n); hit != nil &&
						skillx.Find(r.selectedNative, n) == nil && skillx.Find(r.selectedSkills, n) == nil {
						r.selectedNative = append(r.selectedNative, *hit)
					}
				}
			}
			skillNames := skillx.Names(r.selectedSkills)
			nativeNames := skillx.Names(r.selectedNative)
			joinedSlices := strings.Join(r.devSlices, "\n|||\n")
			_ = tctx.State().Set("chief_plan", r.chiefPlan)
			_ = tctx.State().Set("chief_context_brief", r.chiefContextBrief)
			_ = tctx.State().Set("dev_slices_joined", joinedSlices)
			_ = tctx.State().Set("selected_skills", strings.Join(skillNames, "\n"))
			_ = tctx.State().Set("selected_native_skills", strings.Join(nativeNames, "\n"))
			if actions := tctx.Actions(); actions != nil {
				if actions.StateDelta == nil {
					actions.StateDelta = map[string]any{}
				}
				actions.StateDelta["chief_plan"] = r.chiefPlan
				actions.StateDelta["chief_context_brief"] = r.chiefContextBrief
				actions.StateDelta["dev_slices_joined"] = joinedSlices
				actions.StateDelta["selected_skills"] = strings.Join(skillNames, "\n")
				actions.StateDelta["selected_native_skills"] = strings.Join(nativeNames, "\n")
			}
			r.writeHandoffDossier(sess.ID())

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
				if runMode == "plan:sdd-jira" {
					r.OnEvent(Event{
						Kind:   "sdd_gate",
						Stage:  "chief",
						Author: chief.Name,
						Gate:   "jira",
						Label:  fmt.Sprintf("Gate 2 · Backlog Jira criado (%s)", devCountLabel),
						Text:   strings.TrimSpace(hb.String()),
					})
				} else if runMode == "plan" || r.pauseForApproval {
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
			if runMode == "plan:sdd-jira" {
				return delegateResult{Status: fmt.Sprintf("backlog Jira registrado e %d onda(s) de desenvolvimento definidas; aguardando aprovação do usuário para disparar o Squad", len(resolved))}, nil
			}
			if r.pauseForApproval {
				return delegateResult{Status: fmt.Sprintf("plano registrado com pausa para aprovação (%d dev(s) previstos); o usuário revisará antes de disparar o Squad", len(resolved))}, nil
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
  • Tarefa grande com módulos/arquivos independentes: divida em 2, 3, 4 ou até %d itens em "dev_tasks" (cada item dispara um dev paralelo em sua própria worktree isolada, SEM sobreposição de arquivos).
- CADA fatia deve caber em poucas edições: o dev tem orçamento rígido por rodada (%d chamadas e %d leituras/buscas antes da 1ª edição; estouro aborta a execução). Fatias ambíguas ou grandes demais fazem o dev girar em leitura sem editar e queimam milhões de tokens — prefira fatiar mais fino e listar os arquivos/caminhos exatos em cada dev_tasks.`, maxDevs, maxDevs, maxDevs, r.Project.Settings.EffectiveMaxDevCalls(), r.Project.Settings.EffectiveMaxDevExploreCalls())

	govRules := `GOVERNANÇA GIT & PULL REQUESTS:
- Toda alteração de código roda em worktree separada via delegate_to_squad; apenas Chefe ou Arquiteto comitam e abrem PR.
- ANTES de opinar, comentar ou revisar qualquer PR, chame github_get_pr (o conector nativo GitHub retorna autoria, revisões formais, comentários inline no código e comentários gerais) e confira o campo "Papel neste PR":
  • Se o PR for NOSSO (autor = usuário autenticado): NUNCA atue como revisor nem emita parecer revisando o próprio PR. Leia todos os comentários gerais e inline deixados pelos revisores externos, faça a triagem técnica e despache para a equipe com delegate_to_squad informando pr_number, pr_branch e reviewers; ao terminar, o Chefe comita, responde ponto a ponto (github_comment_pr / github_reply_review_comment) e pede nova revisão (github_request_review).
  • Se o PR for DE TERCEIRO: só aprove/revise/comente quando houver pedido de revisão; use github_pr_files/github_pr_diff para inspecionar o diff completo e github_review_pr para enviar o parecer.
- Só mescle (github_merge_pr) APÓS aprovação do revisor: em homolog pode mesclar direto; em main a ferramenta pede permissão ao usuário.
- INVESTIGAÇÃO ECONÔMICA NO PROJETO: prefira search_text e find_files em vez de múltiplas chamadas list_dir, e use read_file com start_line/end_line ao inspecionar arquivos grandes.
- DOSSIÊ DE HANDOFF PARA O DEV (context_brief): sempre que chamar delegate_to_squad, preencha OBRIGATORIAMENTE "context_brief" com os fatos concretos que você JÁ leu (assinaturas exatas de classes/interfaces/métodos, campos de DTO, códigos HTTP/erro, regras RN críticas e caminhos de arquivos). Esse dossiê é salvo em .hivemind/handoff/ e entregue direto ao Dev e ao QA para evitar que eles releiam a spec/plano inteiros.`

	if r.reviewRequested {
		govRules += `

PROTOCOLO DE REVISÃO PROFUNDA & ECONÔMICA DE PR:
1. Comece por github_get_pr + github_pr_files (ou github_pr_diff) + github_ci_status em paralelo.
2. Quando houver dúvida fora do diff (DTOs, rotas, permissões, contratos de API, catracas ou testes), investigue o projeto inteiro livremente — mas use search_text e find_files para localizar símbolos/arquivos em 1 chamada e read_file com start_line/end_line para ler apenas os trechos relevantes.
3. Agrupe leituras independentes em chamadas paralelas no mesmo turno.
4. Após cada bloco de leituras, sintetize em 1-2 linhas o que confirmou antes de seguir, preservando as conclusões mesmo quando leituras antigas forem compactadas automaticamente.`
	}

	var triageInstr string
	switch runMode {
	case "auto-review":
		testRule := "Execução de testes desabilitada nesta configuração (faça auditoria estática completa no diff e no projeto)."
		if r.Project.Settings.AutoReview.EffectiveRunTests() {
			testRule = "Testes habilitados em worktree isolada da branch do PR: você PODE rodar comandos de teste/build da stack via run_shell (ex.: testes afetados pelo PR) e citar os resultados objetivos no parecer. Operações de commit, push ou merge via shell são bloqueadas."
		}
		actionRule := "Publique o parecer adequado com github_review_pr (APPROVE, REQUEST_CHANGES ou COMMENT conforme os achados técnicos)."
		if r.Project.Settings.AutoReview.EffectiveReviewAction() == "comment_only" {
			actionRule = "Modo SOMENTE COMENTAR ativo: publique obrigatoriamente com github_review_pr usando event=\"COMMENT\" (APPROVE e REQUEST_CHANGES estão bloqueados)."
		}
		triageInstr = fmt.Sprintf(`%s

Você é o Chefe da equipe (%s) operando em MODO REVISÃO AUTOMÁTICA DE FLUXO (autônomo, SEM MERGE).
REGRAS ABSOLUTAS DESTE MODO:
1. NUNCA tente mesclar o PR (github_merge_pr foi fisicamente removida; o merge é decisão humana).
2. Inspecione o PR com github_get_pr + github_pr_files + github_pr_diff + github_ci_status.
3. Quando houver dúvida fora do diff, investigue o repositório inteiro livremente usando search_text, find_files e read_file(start_line, end_line).
4. %s
5. %s
6. Ao final, devolva um resumo curto e objetivo do veredito registrado no GitHub.
%s`,
			chief.Instructions, chief.Name, testRule, actionRule, govRules)
	case "direct":
		triageInstr = fmt.Sprintf(`%s

Você é o Chefe da equipe (%s) operando em MODO DIRETO (1 agente, sem delegar ao squad).
Inspecione arquivos/git/PRs se necessário e responda diretamente ao usuário com clareza, profundidade técnica e objetividade.
%s`,
			chief.Instructions, chief.Name, govRules)
	case "plan":
		triageInstr = fmt.Sprintf(`%s

Você é o Chefe da equipe (%s) operando em MODO PLANEJAMENTO · FEATURE / TASK GRANDE (Human-in-the-Loop).
1. Inspecione os arquivos/PRs relevantes do projeto com read_file/list_dir/git_status/github_get_pr.
2. Elabore um plano arquitetural claro (mapa de impacto, divisão de arquivos, riscos e testes) e chame delegate_to_squad preenchendo "plan" e "dev_tasks" (escolhendo dinamicamente de 1 até %d desenvolvedores conforme o tamanho da demanda).
3. Apresente um resumo objetivo do plano ao usuário informando quantos devs serão disparados e que a execução iniciará em worktree separada assim que ele aprovar.
%s
%s`,
			chief.Instructions, chief.Name, maxDevs, scaleGuidance, govRules)
	case "plan:sdd":
		triageInstr = fmt.Sprintf(`%s

Você é o Chefe da equipe (%s) operando em MODO PLANEJAMENTO SDD · PROJETO DO ZERO (Spec-Driven Development com gates — Human-in-the-Loop).

Este modo inicia um projeto do zero: NENHUM código é criado e NENHUMA issue é aberta até a documentação SDD completa ser aprovada pelo usuário.

FLUXO TRAVADO POR GATES (você NÃO pula etapa):
- ETAPA 1 — ENTREVISTA / DISCOVERY: faça rodadas objetivas de perguntas ao usuário até zerar ambiguidades materiais: problema de negócio, usuários/personas, requisitos funcionais e não funcionais (latência, throughput, consistência, disponibilidade, custo, segurança), stack preferida, integrações, regras de domínio, restrições e deploy. Pergunte em blocos curtos (máx. 5-8 perguntas por rodada) e NÃO avance com dúvida relevante em aberto. Respostas incompletas do usuário: registre como premissa explícita e sinalize.
- ETAPA 2 — DOCS & ARQUITETURA SDD: só depois do escopo confirmado, grave APENAS documentação com write_doc (não existe write_file nesta fase):
  • specs/01-spec.md — visão, escopo, NÃO-escopo, personas, requisitos funcionais/não funcionais e critérios de aceite
  • specs/02-arquitetura.md — componentes, modelo de dados, segurança e observabilidade
  • docs/adr/NNNN-titulo.md — um ADR curto (Contexto, Decisão, Consequências) para cada decisão relevante (stack, persistência, mensageria, etc.)
  • specs/03-contratos.md — contratos de API/eventos/esquemas de dados
  • specs/04-plano.md — ondas de entrega, dependências e riscos
  • specs/05-tarefas.md — backlog completo: Epics → Tasks → Subtasks, cada task com critérios de aceite e referência à spec
- Ao concluir, chame sdd_docs_ready com um resumo executivo (artefatos gravados + principais decisões) e PARE. Gate 1: o usuário aprovou os docs antes de qualquer Jira/código.

REGRAS:
- write_doc aceita SOMENTE .md em docs/, specs/, .specify/ ou README.md; tentativas de gravar código serão recusadas pela ferramenta.
- Se o usuário pedir ajustes após o gate, revise os docs e chame sdd_docs_ready novamente.
- Siga as skills nativas sdd-discovery (entrevista) e sdd-architecture-docs (geração dos docs); leia-as com read_file quando precisar de diretrizes detalhadas.`,
			chief.Instructions, chief.Name)
	case "plan:sdd-jira":
		triageInstr = fmt.Sprintf(`%s

Você é o Chefe da equipe (%s) na FASE SDD — CRIAÇÃO DO BACKLOG NO JIRA (Gate 1 já aprovado pelo usuário).

1. Leia specs/05-tarefas.md e os demais docs SDD aprovados (specs/, docs/adr/) com read_file.
2. Espelhe o backlog no Jira com jira_create_issue (Epic por épico/onda; Task ou Story com os critérios de aceite da spec na description; parent para vincular ao Epic) e jira_create_subtask quando houver quebra fina. Sem project informado, o projeto padrão do time/conector é usado.
3. Grave as chaves Jira geradas (PROJ-xxx) de volta na spec com write_doc (atualize o mapeamento tarefa→chave em specs/05-tarefas.md) para rastreabilidade.
4. Chame delegate_to_squad com "plan" (resumo das ondas de desenvolvimento extraído de specs/04-plano.md) e "dev_tasks" (1 item por onda/desenvolvedor paralelo, SEM sobreposição de arquivos) e PARE. Gate 2: o usuário aprova o backlog e dispara o Squad.
- Se o Jira não estiver configurado/habilitado, informe claramente o que falta (Conector Jira Nativo na IDE + jira_enabled no time) e NÃO invente chaves nem avance para o desenvolvimento.
%s`,
			chief.Instructions, chief.Name, scaleGuidance)
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
3. AUTONOMIA PARA PAUSAR: se a tarefa for grande, arriscada (migração, refatoração ampla, mudança de contrato, dados sensíveis) ou de escopo ambíguo, chame delegate_to_squad com pause_for_approval=true — o plano será apresentado ao usuário para aprovação ANTES de disparar o Squad. Use essa autonomia com critério: tarefas pequenas e claras executam direto.
%s
%s`,
			chief.Instructions, chief.Name, maxDevs, scaleGuidance, govRules)
	}

	if catalogBlock := skillx.CatalogBlock(r.skills, r.NativeSkills); catalogBlock != "" {
		triageInstr += "\n\n" + catalogBlock +
			"SELEÇÃO DE SKILLS:\n" +
			"- Ao delegar com delegate_to_squad, avalie quais skills acima cercam ou aceleram a tarefa e passe os nomes exatos em \"skills\" (apenas as relevantes; vazio = nenhuma).\n" +
			"- Se a versão NATIVA da IDE parecer mais atual ou melhor que a versão do projeto, passe o nome em \"native_skills\"; se o projeto tiver uma skill de mesmo nome, o usuário será consultado para autorizar a substituição.\n" +
			"- Skills na seção OPCIONAIS DESLIGADAS só entram em native_skills se o usuário pediu explicitamente essa skill pelo nome.\n" +
			"- Em modo direto/consulta você mesmo pode seguir uma skill: leia o arquivo SKILL.md indicado com read_file."
	}
	if note := r.pinnedSkillsNote(); note != "" {
		triageInstr += note
	}
	if chiefGuide := r.guidanceForRole(team.SkillTargetChief, false); chiefGuide != "" {
		triageInstr += chiefGuide
	}
	// Skills SDD/Planejamento nativas injetadas automaticamente na fase correspondente.
	switch runMode {
	case "plan:sdd":
		triageInstr += r.nativeSkillGuide("sdd-discovery", "sdd-architecture-docs")
	case "plan:sdd-jira":
		triageInstr += r.nativeSkillGuide("sdd-jira-planner")
	case "plan":
		triageInstr += r.nativeSkillGuide("plan-feature-architect")
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
		BeforeModelCallbacks: []llmagent.BeforeModelCallback{
			newInTurnCompactionCallback(r.Project.Settings.EffectiveInTurnKeepToolResults()),
		},
		BeforeToolCallbacks: []llmagent.BeforeToolCallback{
			newRepeatReadGuardCallback(),
		},
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
			tokenLimit := r.Project.Settings.MaxTokens
			if isAutoReview {
				tokenLimit = r.Project.Settings.AutoReview.EffectiveMaxTokensPerPR(tokenLimit)
			}
			if tokenLimit > 0 && r.turnTotalTokens > tokenLimit {
				return false, "", fmt.Errorf("limite de tokens por execução atingido (%d > %d tokens)", r.turnTotalTokens, tokenLimit)
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
	if delegated && r.chiefContextBrief != "" {
		_ = sess.State().Set("chief_context_brief", r.chiefContextBrief)
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

// writeHandoffDossier persists the Chief's extracted contracts, plan, and task slices
// under .hivemind/handoff/ (already gitignored) so Devs, QA, and the user have a single
// durable reference without forcing Devs to re-read entire spec/plan files from scratch.
func (r *Runner) writeHandoffDossier(sessionID string) {
	if strings.TrimSpace(r.Workspace) == "" || (r.chiefPlan == "" && r.chiefContextBrief == "") {
		return
	}
	dir := filepath.Join(r.Workspace, ".hivemind", "handoff")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	var b strings.Builder
	b.WriteString("# Dossiê de Handoff do Chefe\n\n")
	if sessionID != "" {
		fmt.Fprintf(&b, "- **Sessão:** `%s`\n", sessionID)
	}
	if r.issueKey != "" {
		fmt.Fprintf(&b, "- **Issue Jira:** `%s`\n", r.issueKey)
	}
	fmt.Fprintf(&b, "- **Gerado em:** %s\n\n", time.Now().Format(time.RFC3339))
	if r.chiefPlan != "" {
		b.WriteString("## Plano Técnico\n\n")
		b.WriteString(r.chiefPlan)
		b.WriteString("\n\n")
	}
	if r.chiefContextBrief != "" {
		b.WriteString("## Contratos & Pontos Importantes (Context Brief)\n\n")
		b.WriteString(r.chiefContextBrief)
		b.WriteString("\n\n")
	}
	if len(r.devSlices) > 0 {
		b.WriteString("## Subtarefas do Squad\n\n")
		for i, sl := range r.devSlices {
			fmt.Fprintf(&b, "%d. %s\n", i+1, strings.TrimSpace(sl))
		}
		b.WriteString("\n")
	}
	payload := []byte(b.String())
	_ = os.WriteFile(filepath.Join(dir, "latest.md"), payload, 0o644)
	if safeID := strings.TrimSpace(sessionID); safeID != "" {
		safeID = strings.ReplaceAll(safeID, "/", "_")
		safeID = strings.ReplaceAll(safeID, "\\", "_")
		_ = os.WriteFile(filepath.Join(dir, safeID+".md"), payload, 0o644)
	}
}
