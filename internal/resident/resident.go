package resident

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/sandboxx"
	"github.com/gilvan-Borges/HiveMind/internal/secretx"
	"github.com/gilvan-Borges/HiveMind/internal/tools"
)

const (
	// Turn budgets from docs/SPEC_HIVE_AGENT.md §5.
	MaxModelCallsPerTurn  = 40
	MaxConsecutiveReads   = 8
	MaxInputTokensPerTurn = 80000
)

// Runner executes Phase 2 resident turns with fail-closed OS sandboxing,
// vault secret protection, and human-gated tool execution via Broker.
type Runner struct {
	BaseDir         string
	Config          Config
	Policy          *Policy
	Sandbox         sandboxx.Runner
	Vault           secretx.Vault
	HTTPClient      *http.Client
	ApprovalTimeout time.Duration
	Audit           AuditWriter
	Memory          *MemoryStore
	ListProjects    func() []ProjectRef
	Approve         ApprovalFunc
	ApproveDetailed ApprovalDetailFunc
	OnEvent         func(Event)
	// Model optionally injects a pre-built model.LLM (e.g. modelx.Scripted in tests).
	Model model.LLM
	// Streaming defaults to agent.StreamingModeSSE when zero.
	Streaming agent.StreamingMode
	// CustomHandlers optionally overrides broker handlers in unit tests.
	CustomHandlers map[string]ToolHandler
	// LastBroker exposes the broker used in the most recent RunTurn (useful in tests).
	LastBroker *Broker
	// Child is set on a spawned subagent. A child cannot spawn, schedule or hand off.
	Child bool
	// SkillsDir is the native skill catalog. Project skills are never read.
	SkillsDir string
	// AllowedTiers narrows a routine or a subagent. Empty means no extra narrow.
	AllowedTiers []Tier
	// ParentTier is the class ceiling inherited by a subagent.
	ParentTier Tier
	// PermissionMode reads team.yaml. The model cannot supply this value.
	PermissionMode func(workspace string) string
	// SquadHandoff starts a squad lane after the host sanitizes the package.
	SquadHandoff func(ctx context.Context, h SquadHandoff) (string, error)
	// OnRoutineSaved rearms the in-process timer after cron_add.
	OnRoutineSaved func()
	gateOnce       sync.Once
	gate           *spawnGate
}

// RunTurn executes one read-only resident turn and returns the final response
// and usage telemetry.
func (r *Runner) RunTurn(ctx context.Context, req TurnRequest) (TurnResult, error) {
	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		return TurnResult{}, fmt.Errorf("mensagem vazia")
	}
	origin := req.Origin
	if origin == "" {
		origin = OriginLocal
	}
	req.Origin = origin

	baseDir := strings.TrimSpace(r.BaseDir)
	if baseDir == "" {
		baseDir = DefaultBaseDir()
	}

	accountID := SanitizeAccountID(req.AccountID)
	req.AccountID = accountID

	ws := SanitizeWorkstream(req.Workstream)
	if req.Workstream == "" && r.Config.Workstream != "" {
		ws = SanitizeWorkstream(r.Config.Workstream)
	}
	req.Workstream = ws

	mem := r.Memory
	if mem == nil {
		mem = NewMemoryStore(baseDir, accountID)
	}

	audit := r.Audit
	if audit == nil {
		audit = NewFileAuditLogger(filepath.Join(baseDir, "audit.jsonl"))
	}

	// Build bounded prompt BEFORE opening the model stream (§5.1, §13: < 50 ms budget).
	promptStart := time.Now()
	userPrompt, err := BuildUserPrompt(req, mem)
	if err != nil {
		return TurnResult{}, err
	}
	if !r.Child {
		if block := SkillBlock(r.SkillsDir, baseDir); block != "" {
			userPrompt += "\n\n" + block
		}
	}
	promptBuildMs := time.Since(promptStart).Milliseconds()

	// Resolve LLM (injected in tests or constructed via internal/modelx).
	llm := r.Model
	prov, mod := ResolveModelRoute(r.Config)
	if llm == nil {
		var err error
		llm, prov, mod, err = BuildLLM(ctx, baseDir, r.Config)
		if err != nil {
			return TurnResult{}, err
		}
	}

	pol := Policy{}
	if r.Policy != nil {
		pol = *r.Policy
	} else if loadedPol, polErr := LoadPolicy(baseDir); polErr == nil {
		pol = loadedPol
	}

	approvalTimeout := r.ApprovalTimeout
	if origin == OriginRoutine && approvalTimeout == 0 {
		approvalTimeout = 15 * time.Minute
	}
	parentTier := r.ParentTier
	broker := NewBroker(BrokerConfig{
		Workspace:              req.Workspace,
		Workstream:             ws,
		Origin:                 origin,
		ParentTier:             parentTier,
		AccountID:              accountID,
		DeviceID:               req.DeviceID,
		Policy:                 pol,
		Sandbox:                r.Sandbox,
		Vault:                  r.Vault,
		HTTPClient:             r.HTTPClient,
		ApprovalTimeout:        approvalTimeout,
		Audit:                  audit,
		Memory:                 mem,
		ListProjects:           r.ListProjects,
		Approve:                r.Approve,
		ApproveDetailed:        r.ApproveDetailed,
		OnToolEvent:            r.emit,
		CustomHandlers:         r.CustomHandlers,
		Phase3:                 !r.Child,
		AllowedTiers:           append([]Tier(nil), r.AllowedTiers...),
		PermissionMode:         r.PermissionMode,
		Spawn:                  r.runSpawn,
		SquadHandoff:           r.SquadHandoff,
		BaseDir:                baseDir,
		OnRoutineSaved:         r.OnRoutineSaved,
		ExperimentalUnisolated: r.Config.ExperimentalUnisolated,
	})
	r.LastBroker = broker

	buildTools := tools.ForResidentPhase2
	if !r.Child {
		buildTools = tools.ForResidentPhase3
	}
	adkTools, err := buildTools(broker)
	if err != nil {
		return TurnResult{}, fmt.Errorf("registrar ferramentas do residente: %w", err)
	}

	ag, err := llmagent.New(llmagent.Config{
		Name:        "hive_resident",
		Model:       llm,
		Description: "Assistente residente pessoal Hive (Fase 2 · Ações na Sandbox).",
		Instruction: SystemPrompt(),
		Tools:       adkTools,
		BeforeModelCallbacks: []llmagent.BeforeModelCallback{
			newInTurnCompactionCallback(residentKeepToolResults),
		},
	})
	if err != nil {
		return TurnResult{}, fmt.Errorf("criar agente residente: %w", err)
	}

	sessSvc := session.InMemoryService()
	sessID := newShortID("res_sess")
	if _, err := sessSvc.Create(ctx, &session.CreateRequest{
		AppName:   "hivemind_resident",
		UserID:    accountID,
		SessionID: sessID,
	}); err != nil {
		return TurnResult{}, fmt.Errorf("criar sessão do residente: %w", err)
	}

	adkRunner, err := runner.New(runner.Config{
		AppName:        "hivemind_resident",
		Agent:          ag,
		SessionService: sessSvc,
	})
	if err != nil {
		return TurnResult{}, fmt.Errorf("criar runner do residente: %w", err)
	}

	streamMode := r.Streaming
	if streamMode == "" {
		streamMode = agent.StreamingModeSSE
	}

	r.emit(Event{
		Kind:   "stage",
		Author: "hive",
		Label:  "Hive Residente",
		Text:   "pensando",
	})

	var (
		finalParts       []string
		streamedText     strings.Builder
		totalIn          int64
		totalOut         int64
		totalTok         int64
		modelCalls       int
		consecutiveReads int
	)

	runCfg := agent.RunConfig{
		StreamingMode: streamMode,
		MaxLLMCalls:   MaxModelCallsPerTurn,
	}

	userContent := &genai.Content{
		Role:  genai.RoleUser,
		Parts: []*genai.Part{{Text: userPrompt}},
	}

	for ev, err := range adkRunner.Run(ctx, accountID, sessID, userContent, runCfg) {
		if err != nil {
			safeErr := RedactSecrets(err.Error())
			r.emit(Event{Kind: "error", Author: "hive", Label: "Hive Residente", Text: safeErr})
			return TurnResult{}, fmt.Errorf("%s", safeErr)
		}
		if ev == nil {
			continue
		}

		if !ev.Partial {
			if ev.UsageMetadata != nil {
				inTok := ev.UsageMetadata.PromptTokenCount
				outTok := ev.UsageMetadata.CandidatesTokenCount
				totTok := ev.UsageMetadata.TotalTokenCount
				if totTok == 0 {
					totTok = inTok + outTok
				}
				totalIn += int64(inTok)
				totalOut += int64(outTok)
				totalTok += int64(totTok)
				r.emit(Event{
					Kind:         "usage",
					Author:       "hive",
					Label:        "Hive Residente",
					InputTokens:  inTok,
					OutputTokens: outTok,
					Tokens:       totTok,
				})
				if totalIn > MaxInputTokensPerTurn {
					abortErr := fmt.Errorf("orçamento do turno excedido: %d tokens de entrada (teto %d)", totalIn, MaxInputTokensPerTurn)
					r.emit(Event{Kind: "error", Author: "hive", Label: "Hive Residente", Text: abortErr.Error()})
					return TurnResult{}, abortErr
				}
			}

			if ev.Content != nil {
				hasCall := false
				hasText := false
				for _, p := range ev.Content.Parts {
					if p == nil {
						continue
					}
					if p.FunctionCall != nil {
						hasCall = true
					}
					if p.Text != "" && !p.Thought {
						hasText = true
					}
				}
				if hasCall || hasText {
					modelCalls++
					if modelCalls > MaxModelCallsPerTurn {
						abortErr := fmt.Errorf("orçamento do turno excedido: mais de %d chamadas de modelo", MaxModelCallsPerTurn)
						r.emit(Event{Kind: "error", Author: "hive", Label: "Hive Residente", Text: abortErr.Error()})
						return TurnResult{}, abortErr
					}
				}
				if hasCall {
					consecutiveReads++
					if consecutiveReads > MaxConsecutiveReads {
						abortErr := fmt.Errorf("orçamento do turno excedido: %d leituras seguidas sem resposta final (teto %d)", consecutiveReads, MaxConsecutiveReads)
						r.emit(Event{Kind: "error", Author: "hive", Label: "Hive Residente", Text: abortErr.Error()})
						return TurnResult{}, abortErr
					}
				} else if hasText {
					consecutiveReads = 0
				}
			}
		}

		if ev.Content != nil {
			for _, p := range ev.Content.Parts {
				if p == nil || p.Text == "" {
					continue
				}
				safeText := RedactSecrets(p.Text)
				if p.Thought {
					r.emit(Event{
						Kind:    "text",
						Author:  "hive",
						Label:   "Hive Residente",
						Text:    safeText,
						Partial: ev.Partial,
						Thought: true,
					})
					continue
				}
				if ev.Partial {
					streamedText.WriteString(safeText)
					r.emit(Event{
						Kind:    "text",
						Author:  "hive",
						Label:   "Hive Residente",
						Text:    safeText,
						Partial: true,
					})
				} else {
					finalParts = append(finalParts, safeText)
					if streamedText.Len() == 0 {
						r.emit(Event{
							Kind:    "text",
							Author:  "hive",
							Label:   "Hive Residente",
							Text:    safeText,
							Partial: false,
						})
					}
				}
			}
		}
	}

	finalText := strings.TrimSpace(strings.Join(finalParts, "\n"))
	if finalText == "" {
		finalText = strings.TrimSpace(streamedText.String())
	}

	// Persist short-term turn locally per account (never long-term facts from tool output, §5.8).
	_ = mem.AppendSessionTurn(ws, msg, finalText)

	r.emit(Event{
		Kind:   "finish",
		Author: "hive",
		Label:  "Hive Residente",
		Text:   finalText,
	})

	return TurnResult{
		SessionID:     sessID,
		Workstream:    ws,
		FinalText:     finalText,
		Provider:      prov,
		Model:         mod,
		InputTokens:   totalIn,
		OutputTokens:  totalOut,
		TotalTokens:   totalTok,
		ToolCalls:     broker.CallCount(),
		PromptBuildMs: promptBuildMs,
	}, nil
}

type spawnGate struct {
	mu sync.Mutex
	n  int
}

func (g *spawnGate) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.n >= 2 {
		return false
	}
	g.n++
	return true
}

func (g *spawnGate) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.n > 0 {
		g.n--
	}
}

func (r *Runner) slots() *spawnGate {
	r.gateOnce.Do(func() {
		if r.gate == nil {
			r.gate = &spawnGate{}
		}
	})
	return r.gate
}

func (r *Runner) runSpawn(ctx context.Context, objective string, roots, classes []string) (string, error) {
	if r.Child {
		return "", fmt.Errorf("subagente não pode criar outro subagente")
	}
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return "", fmt.Errorf("objetivo do subagente vazio")
	}
	root, err := spawnRoot(r.lastWorkspace(), roots)
	if err != nil {
		return "", err
	}
	allowed, err := parseClassList(classes)
	if err != nil {
		return "", err
	}
	if !r.slots().enter() {
		return "", fmt.Errorf("limite de 2 subagentes ao mesmo tempo")
	}
	defer r.slots().leave()

	child := &Runner{
		BaseDir:         r.BaseDir,
		Config:          r.Config,
		Policy:          r.Policy,
		Sandbox:         r.Sandbox,
		Vault:           r.Vault,
		HTTPClient:      r.HTTPClient,
		ApprovalTimeout: r.ApprovalTimeout,
		Audit:           r.Audit,
		Memory:          r.Memory,
		ListProjects:    r.ListProjects,
		Approve:         r.Approve,
		ApproveDetailed: r.ApproveDetailed,
		OnEvent:         r.OnEvent,
		Model:           r.Model,
		Streaming:       agent.StreamingModeNone,
		Child:           true,
		ParentTier:      ceilingTier(allowed),
		AllowedTiers:    allowed,
		gate:            r.slots(),
	}
	res, err := child.RunTurn(ctx, TurnRequest{
		Message:    objective,
		Workspace:  root,
		Workstream: r.lastWorkstream(),
		Origin:     OriginSubagent,
		AccountID:  r.lastAccount(),
	})
	if err != nil {
		return "", err
	}
	return res.FinalText, nil
}

func (r *Runner) lastWorkspace() string {
	if r.LastBroker != nil {
		return r.LastBroker.cfg.Workspace
	}
	return ""
}

func (r *Runner) lastWorkstream() string {
	if r.LastBroker != nil {
		return r.LastBroker.cfg.Workstream
	}
	return ""
}

func (r *Runner) lastAccount() string {
	if r.LastBroker != nil {
		return r.LastBroker.cfg.AccountID
	}
	return DefaultAccountID
}

func spawnRoot(parent string, roots []string) (string, error) {
	if len(roots) == 0 || strings.TrimSpace(roots[0]) == "" {
		return parent, nil
	}
	root := strings.TrimSpace(roots[0])
	if strings.Contains(root, "..") {
		return "", fmt.Errorf("raiz do subagente inválida")
	}
	if !filepath.IsAbs(root) {
		if parent == "" {
			return "", fmt.Errorf("raiz do subagente inválida")
		}
		root = filepath.Join(parent, root)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func ceilingTier(list []Tier) Tier {
	if len(list) == 0 {
		return ""
	}
	best := list[0]
	for _, t := range list[1:] {
		if tierRank(t) > tierRank(best) {
			best = t
		}
	}
	return best
}

func (r *Runner) emit(ev Event) {
	if r.OnEvent == nil {
		return
	}
	ev.Text = RedactSecrets(ev.Text)
	ev.Detail = RedactSecrets(ev.Detail)
	r.OnEvent(ev)
}
