package resident

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/tools"
)

const (
	// Turn budgets from docs/SPEC_HIVE_AGENT.md §5.
	MaxModelCallsPerTurn  = 40
	MaxConsecutiveReads   = 8
	MaxInputTokensPerTurn = 80000
)

// Runner executes Phase 1 read-only turns of the Hive resident agent.
// It never opens network sockets, never spawns MCP servers, and routes every
// tool invocation through the fail-closed Broker.
type Runner struct {
	BaseDir      string
	Config       Config
	Audit        AuditWriter
	Memory       *MemoryStore
	ListProjects func() []ProjectRef
	Approve      ApprovalFunc
	OnEvent      func(Event)
	// Model optionally injects a pre-built model.LLM (e.g. modelx.Scripted in tests).
	Model model.LLM
	// Streaming defaults to agent.StreamingModeSSE when zero.
	Streaming agent.StreamingMode
	// CustomHandlers optionally overrides broker read handlers in unit tests.
	CustomHandlers map[string]ToolHandler
	// LastBroker exposes the broker used in the most recent RunTurn (useful in tests).
	LastBroker *Broker
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
	promptBuildMs := time.Since(promptStart).Milliseconds()

	// Resolve LLM (injected in tests or constructed via internal/modelx).
	llm := r.Model
	prov, mod := ResolveModelRoute(r.Config)
	if llm == nil {
		var err error
		llm, prov, mod, err = BuildLLM(ctx, r.Config)
		if err != nil {
			return TurnResult{}, err
		}
	}

	broker := NewBroker(BrokerConfig{
		Workspace:      req.Workspace,
		Workstream:     ws,
		Origin:         origin,
		AccountID:      accountID,
		DeviceID:       req.DeviceID,
		Audit:          audit,
		Memory:         mem,
		ListProjects:   r.ListProjects,
		Approve:        r.Approve,
		OnToolEvent:    r.emit,
		CustomHandlers: r.CustomHandlers,
	})
	r.LastBroker = broker

	adkTools, err := tools.ForResidentReadOnly(broker)
	if err != nil {
		return TurnResult{}, fmt.Errorf("registrar ferramentas somente leitura do residente: %w", err)
	}

	ag, err := llmagent.New(llmagent.Config{
		Name:        "hive_resident",
		Model:       llm,
		Description: "Assistente residente pessoal Hive (Fase 1 · Somente Leitura).",
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

func (r *Runner) emit(ev Event) {
	if r.OnEvent == nil {
		return
	}
	ev.Text = RedactSecrets(ev.Text)
	ev.Detail = RedactSecrets(ev.Detail)
	r.OnEvent(ev)
}
