// Package orchestrator runs a HiveMind team with ADK sequential, parallel and loop agents.
package orchestrator

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"

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
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/ui"
	"github.com/gilvan-Borges/HiveMind/internal/workerx"
)

// Event is a structured live update emitted by Runner when OnEvent is set.
type Event struct {
	Kind              string                   `json:"kind"` // "stage", "handoff", "devs_scaled", "usage", "text", "tool_call", "tool_result", "finish", "error", "plan_ready", "approval_required", "sdd_gate", "qa_pending", "model_route", "provider_failover", "provider_exhausted", "compact", "worker_usage"
	Stage             string                   `json:"stage,omitempty"`
	Author            string                   `json:"author,omitempty"`
	Label             string                   `json:"label,omitempty"`
	Text              string                   `json:"text,omitempty"`
	ToolName          string                   `json:"toolName,omitempty"`
	Detail            string                   `json:"detail,omitempty"`
	Devs              []string                 `json:"devs,omitempty"`
	Gate              string                   `json:"gate,omitempty"`       // "docs" | "jira" | "budget" | "worktree"
	ApprovalID        string                   `json:"approvalId,omitempty"` // identificador único da aprovação pendente (permite aprovações concorrentes na mesma lane)
	Partial           bool                     `json:"partial,omitempty"`
	Thought           bool                     `json:"thought,omitempty"`
	InputTokens       int32                    `json:"inputTokens,omitempty"`
	OutputTokens      int32                    `json:"outputTokens,omitempty"`
	Tokens            int32                    `json:"tokens,omitempty"`
	CachedReadTokens  int32                    `json:"cachedReadTokens,omitempty"`
	CacheHitPct       int32                    `json:"cacheHitPct,omitempty"` // cachedReadTokens/promptTokens por chamada (0 quando o provedor não reporta)
	CostMicroUSD      int64                    `json:"costMicroUsd,omitempty"`
	Role              string                   `json:"role,omitempty"`
	Provider          string                   `json:"provider,omitempty"`
	Model             string                   `json:"model,omitempty"`
	FromProvider      string                   `json:"fromProvider,omitempty"`
	FromModel         string                   `json:"fromModel,omitempty"`
	Tier              string                   `json:"tier,omitempty"`
	Reasons           []string                 `json:"reasons,omitempty"`
	DiscountPct       int                      `json:"discountPct,omitempty"`
	EstimatedMicroUSD int64                    `json:"estimatedMicroUsd,omitempty"`
	DecisionMicros    int64                    `json:"decisionMicros,omitempty"`
	Health            []jev.ProviderHealthView `json:"health,omitempty"`
	FromRole          string                   `json:"fromRole,omitempty"`
	ToRole            string                   `json:"toRole,omitempty"`
	Origin            string                   `json:"origin,omitempty"`
}

// clampInt32 bounds one int64 token counter to the event stream's int32
// fields without silently wrapping around on pathological values.
func clampInt32(v int64) int32 {
	const maxI32 = int64(1<<31 - 1)
	if v > maxI32 {
		return int32(maxI32)
	}
	if v < -maxI32 {
		return int32(-maxI32)
	}
	return int32(v)
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
	MCP                *mcpx.Host
	Out                ioWriter
	Err                ioWriter
	Streaming          agent.StreamingMode
	ExternalWorkers    *workerx.AdapterRegistry
	OnEvent            func(Event)
	EnableTriage       bool
	EnableClosingChief bool
	ReviewRoot         string
	LaneID             string
	NativeSkills       []skillx.Skill
	NativeSkillsRoot   string
	Catalog            *jev.CatalogStore
	LiveRoundtableModel model.LLM
	RaceCandidateExec  func(ctx context.Context, idx int, wtDir, objective string) (summary string, costMicroUSD int64, totalTokens int64, err error)

	autoReviewMode    bool
	resumedSession    bool
	roundtableMu      sync.Mutex
	roundtableCalls   int
	integration       string
	deliveryRoot      string
	deliveryBranch    string
	nestedWorktrees   []gitx.NestedWorktree
	issueKey          string
	objectiveText     string
	reviewRequested   bool
	prNumber          int
	prBranch          string
	prReviewers       []string
	devSlices         []string
	devOwnedPaths     [][]string
	devSequential     bool
	sharedDevWorktree bool
	sharedBoard       *TeamBoard
	chiefPlan         string
	chiefContextBrief string
	pauseForApproval  bool
	skills            []skillx.Skill
	selectedSkills    []skillx.Skill
	selectedNative    []skillx.Skill
	turnInputTokens   int64
	turnOutputTokens  int64
	turnTotalTokens   int64
	turnCachedRead    int64
	costMu            sync.Mutex
	turnCostMicroUSD  int64
	agentCosts        map[string]*AgentCost
	roleCosts         map[string]*RoleCost
	qaLastStatus      string
	qaHasLastStatus   bool
	qaStallCount      int
	qaStallFired      bool
	qaLoopIter        int
	handoffMu         sync.Mutex
	handoffTo         string
	handoffHash       string
	handoffEdges      []handoffEdge
	hasImages         bool
	jevRepo           string
	devBudgets        map[string]*devTurnBudget
	resolvedChoiceMu  sync.Mutex
	lastWorkingRoute  team.Agent
	routedModels      map[string][2]string
	routedDiscount    map[string]int
}

type ioWriter = interface {
	Write(p []byte) (n int, err error)
}

// Request is one user turn.
type Request struct {
	Objective   string
	JiraKey     string
	SessionID   string
	LaneID      string
	RunMode     string // "", "auto", "direct", "plan", "plan:sdd", "plan:sdd-jira", "squad", "race"
	Attachments []Attachment
}

// NestedDelivery reports worktree, branch, and optional PR metadata for a
// nested independent Git repository mounted inside the conversation worktree.
type NestedDelivery struct {
	RelPath     string               `json:"relPath"`
	RepoDir     string               `json:"repoDir"`
	WorktreeDir string               `json:"worktreeDir"`
	Branch      string               `json:"branch"`
	BaseBranch  string               `json:"baseBranch,omitempty"`
	PullRequest *githubx.PullRequest `json:"pullRequest,omitempty"`
}

// Result is what the CLI prints after the team finishes.
type Result struct {
	SessionID         string
	Summary           string
	Branches          []string
	IntegrationBranch string `json:"integrationBranch,omitempty"`
	DeliveryBranch    string `json:"deliveryBranch,omitempty"`
	WorktreeDir       string `json:"worktreeDir,omitempty"`
	PullRequests      []githubx.PullRequest
	NestedDeliveries  []NestedDelivery `json:"nestedDeliveries,omitempty"`
	InputTokens       int64            `json:"inputTokens"`
	OutputTokens      int64            `json:"outputTokens"`
	TotalTokens        int64            `json:"totalTokens"`
	CachedReadTokens  int64            `json:"cachedReadTokens,omitempty"`
	CostMicroUSD      int64            `json:"costMicroUsd,omitempty"`
	CostByAgent       []AgentCost      `json:"costByAgent,omitempty"`
	CostByRole        []RoleCost       `json:"costByRole,omitempty"`
	PendingQA         bool             `json:"pendingQa,omitempty"`
}

type devEnv struct {
	Agent      team.Agent
	ADK        string
	Root       string
	Branch     string
	OwnedPaths []string
	Nested     []gitx.NestedWorktree
}

func (r *Runner) setStateChecked(st session.State, key string, val any) {
	if st == nil {
		return
	}
	if err := st.Set(key, val); err != nil && r.Err != nil {
		fmt.Fprintf(r.Err, "aviso: falha ao gravar estado %q: %v\n", key, err)
	}
}

func (r *Runner) updateStateChecked(sess session.Session, vals map[string]any) {
	if sess == nil || len(vals) == 0 {
		return
	}
	if err := sessionx.UpdateState(sess, vals); err != nil && r.Err != nil {
		fmt.Fprintf(r.Err, "aviso: falha ao persistir estado da sessão: %v\n", err)
	}
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
	if strings.TrimSpace(req.LaneID) != "" {
		r.LaneID = strings.TrimSpace(req.LaneID)
	}
	if r.GitHub == nil && r.Git.IsRepo(ctx, r.Workspace) {
		r.GitHub = r.detectGitHub(ctx)
	}
	if r.Jira == nil {
		if r.Project.Settings.JiraEnabled {
			r.Jira = jirax.FromWorkspace(r.Workspace)
		} else {
			r.Jira = jirax.Disabled{Reason: "Jira desabilitado"}
		}
	}
	if r.Sessions == nil {
		store, err := sessionx.Open(r.Workspace)
		if err != nil {
			return result, err
		}
		r.Sessions = store
	}
	defer modelx.FlushAIUsage()

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
	if objective == "" && len(req.Attachments) == 0 {
		return result, fmt.Errorf("informe um objetivo ou uma chave Jira")
	}
	if objective == "" && len(req.Attachments) > 0 {
		objective = "Analise a(s) imagem(ns) anexada(s) e execute o que for pedido."
	}
	r.Project.Normalize()
	ctx = modelx.WithPermissionMode(ctx, r.Project.Settings.EffectivePermissionMode())
	r.resetTurnState(key, objective, req.Attachments, ctx)

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

	if raceCount, isRace := ParseRaceCount(runMode); isRace {
		return r.runRace(ctx, req, sess, objective, key, raceCount)
	}

	mode := r.Streaming
	if mode == "" {
		mode = agent.StreamingModeSSE
	}
	useTriage := ((runMode == "" && r.EnableTriage) ||
		runMode == "auto" ||
		runMode == "direct" ||
		runMode == "auto-review" ||
		runMode == "plan" ||
		runMode == "plan:sdd" ||
		runMode == "plan:sdd-jira" ||
		(runMode == "squad" && r.EnableTriage)) &&
		(r.chiefPlan == "" || runMode == "plan" || runMode == "plan:sdd" || runMode == "plan:sdd-jira" || runMode == "direct" || runMode == "auto-review")

	userObjective := objective
	if useTriage {
		userObjective += r.teamMemorySnippet()
	}
	// Defesa de contexto do prompt inicial: objetivo/anexos acima do teto
	// deixam o miolo em .hivemind/intake/ em vez de estourar a janela do
	// Chefe na primeira chamada.
	userObjective, boundedAtts := r.boundIntake(userObjective, req.Attachments)
	msg := buildUserContent(userObjective, boundedAtts)
	if !useTriage && r.chiefPlan != "" {
		if r.OnEvent != nil {
			r.OnEvent(Event{
				Kind:  "handoff",
				Stage: "dev",
				Label: "Retomando direto no Desenvolvedor (plano do Chefe preservado)",
				Text:  "O plano técnico e a divisão de tarefas do Chefe já estão prontos nesta sessão — pulando nova triagem e retomando direto na worktree do Desenvolvedor.",
			})
		}
		msg = genai.NewContentFromText("Prossigam com a implementação e validação conforme o plano e divisão de tarefas do Chefe.", genai.RoleUser)
	}
	if useTriage {
		delegated, summary, err := r.runTriageChief(ctx, sess, msg, key, mode, runMode)
		r.populateUsageMetrics(&result)
		if err != nil {
			if r.OnEvent != nil {
				r.OnEvent(Event{Kind: "error", Text: err.Error()})
			}
			return result, err
		}
		if handled, outRes := r.handlePostTriageReturn(runMode, delegated, summary, sess, result); handled {
			return outRes, nil
		}
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
			r.populateUsageMetrics(&result)
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
				r.populateUsageMetrics(&result)
				r.printUsageSummary(result, r.Err, "uso até a interrupção: ")
				if r.OnEvent != nil {
					r.OnEvent(Event{Kind: "error", Text: guardErr.Error()})
				}
				return result, guardErr
			}
			if r.Project.Settings.MaxTokens > 0 && r.turnTotalTokens > r.Project.Settings.MaxTokens {
				budgetErr := fmt.Errorf("limite de tokens por execução atingido (%d > %d tokens)", r.turnTotalTokens, r.Project.Settings.MaxTokens)
				r.populateUsageMetrics(&result)
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

	r.populateUsageMetrics(&result)
	if err := r.finish(ctx, sess, &result, devs, key, objective); err != nil {
		if r.OnEvent != nil {
			r.OnEvent(Event{Kind: "error", Text: err.Error()})
		}
		return result, err
	}
	if err := sessionx.CompactOldToolOutputs(sess, 12); err != nil && r.Err != nil {
		fmt.Fprintf(r.Err, "aviso: falha ao compactar outputs da sessão: %v\n", err)
	}
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
			CostMicroUSD: result.CostMicroUSD,
		})
	}
	return result, nil
}

func (r *Runner) resetTurnState(key, objective string, atts []Attachment, ctx context.Context) {
	r.resumedSession = false
	r.roundtableMu.Lock()
	r.roundtableCalls = 0
	r.roundtableMu.Unlock()
	r.integration = ""
	r.deliveryRoot = ""
	r.deliveryBranch = ""
	r.nestedWorktrees = nil
	r.issueKey = key
	r.objectiveText = objective
	r.reviewRequested = githubx.IsExplicitReviewObjective(objective)
	r.prNumber = 0
	r.prBranch = ""
	r.prReviewers = nil
	r.devSlices = nil
	r.devOwnedPaths = nil
	r.devSequential = false
	r.sharedDevWorktree = false
	r.sharedBoard = newTeamBoard()
	r.chiefPlan = ""
	r.chiefContextBrief = ""
	r.skills = skillx.Load(r.Workspace)
	r.selectedSkills = nil
	r.selectedNative = nil
	r.turnInputTokens = 0
	r.turnOutputTokens = 0
	r.turnTotalTokens = 0
	r.turnCachedRead = 0
	r.resetTurnCosts()
	r.qaLastStatus = ""
	r.qaHasLastStatus = false
	r.qaStallCount = 0
	r.qaStallFired = false
	r.qaLoopIter = 0
	r.handoffMu.Lock()
	r.handoffTo = ""
	r.handoffHash = ""
	r.handoffEdges = nil
	r.handoffMu.Unlock()
	r.hasImages = hasImageAttachments(atts)
	r.jevRepo = r.resolveRepoKey(ctx)
	r.resetDevBudgets()
}

func (r *Runner) handlePostTriageReturn(runMode string, delegated bool, summary string, sess session.Session, result Result) (bool, Result) {
	if runMode == "plan" || (r.pauseForApproval && (runMode == "" || runMode == "auto") && delegated) {
		result.Summary = summary
		planBody := strings.TrimSpace(r.chiefPlan)
		if planBody == "" {
			planBody = summary
		}
		r.updateStateChecked(sess, map[string]any{
			"chief_plan":            planBody,
			"chief_context_brief":   strings.TrimSpace(r.chiefContextBrief),
			"dev_slices_joined":     strings.Join(r.devSlices, "\n|||\n"),
			"dev_owned_paths_json":  marshalOwnedPaths(r.devOwnedPaths),
			"dev_sequential":        boolFlag(r.devSequential),
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
				CostMicroUSD: result.CostMicroUSD,
			})
		}
		return true, result
	}
	if runMode == "plan:sdd" || runMode == "plan:sdd-jira" {
		result.Summary = summary
		if runMode == "plan:sdd-jira" && delegated && r.chiefPlan != "" {
			r.updateStateChecked(sess, map[string]any{
				"chief_plan":            r.chiefPlan,
				"chief_context_brief":   strings.TrimSpace(r.chiefContextBrief),
				"dev_slices_joined":     strings.Join(r.devSlices, "\n|||\n"),
				"dev_owned_paths_json":  marshalOwnedPaths(r.devOwnedPaths),
				"dev_sequential":        boolFlag(r.devSequential),
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
				CostMicroUSD: result.CostMicroUSD,
			})
		}
		return true, result
	}
	if (!delegated && runMode != "squad") || runMode == "direct" {
		result.Summary = summary
		fmt.Fprintf(r.Out, "\nSessão %s concluída (resposta direta do Chefe).\n", result.SessionID)
		r.printUsageSummary(result, r.Out, "")
		if r.OnEvent != nil {
			r.OnEvent(Event{
				Kind:         "finish",
				Stage:        "chief",
				Text:         fmt.Sprintf("Sessão %s concluída.", result.SessionID),
				InputTokens:  int32(r.turnInputTokens),
				OutputTokens: int32(r.turnOutputTokens),
				Tokens:       int32(r.turnTotalTokens),
				CostMicroUSD: result.CostMicroUSD,
			})
		}
		return true, result
	}
	return false, result
}
