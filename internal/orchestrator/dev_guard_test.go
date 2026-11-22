package orchestrator

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func devToolEvent(author string, toolNames ...string) *session.Event {
	parts := make([]*genai.Part, 0, len(toolNames))
	for _, n := range toolNames {
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: "call-" + n, Name: n}})
	}
	return &session.Event{
		Author:      author,
		LLMResponse: model.LLMResponse{Content: &genai.Content{Parts: parts}},
	}
}

func devUsageEvent(author string, promptTokens int32) *session.Event {
	return &session.Event{
		Author:      author,
		LLMResponse: model.LLMResponse{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: promptTokens}},
	}
}

func TestDevGuardAbortsExploreSpiralWithoutEdits(t *testing.T) {
	r := &Runner{Project: team.File{Settings: team.DefaultSettings()}}
	r.resetDevBudgets()
	labels := map[string]string{"dev_1": "Desenvolvedor · dev_1"}

	max := r.Project.Settings.EffectiveMaxDevExploreCalls()
	for i := 0; i < max-1; i++ {
		if err := r.checkDevGuard(devToolEvent("dev_1", "read_file"), labels); err != nil {
			t.Fatalf("unexpected abort before limit at read %d: %v", i+1, err)
		}
	}
	err := r.checkDevGuard(devToolEvent("dev_1", "read_file"), labels)
	if err == nil || !strings.Contains(err.Error(), "sem editar NENHUM arquivo") {
		t.Fatalf("expected explore-spiral abort at %d reads, got %v", max, err)
	}

	// Any edit resets the explore counter: a fresh spiral budget begins.
	if err := r.checkDevGuard(devToolEvent("dev_1", "edit_file"), labels); err != nil {
		t.Fatalf("edit after abort must not fail: %v", err)
	}
	if err := r.checkDevGuard(devToolEvent("dev_1", "read_file"), labels); err != nil {
		t.Fatalf("read after edit should start a fresh budget: %v", err)
	}
}

func TestDevGuardAbortsCallCap(t *testing.T) {
	cfg := team.DefaultSettings()
	cfg.MaxDevCalls = 20
	r := &Runner{Project: team.File{Settings: cfg}}
	r.resetDevBudgets()
	labels := map[string]string{"dev_2": "Desenvolvedor · dev_2"}

	for i := 0; i < 20; i++ {
		if err := r.checkDevGuard(devToolEvent("dev_2", "edit_file"), labels); err != nil {
			t.Fatalf("unexpected abort before call cap at %d: %v", i+1, err)
		}
	}
	if err := r.checkDevGuard(devToolEvent("dev_2", "edit_file"), labels); err == nil {
		t.Fatal("expected call-cap abort after 21st call")
	}
}

func TestDevGuardAbortsInputTokenBudgetAndResetsOnQA(t *testing.T) {
	cfg := team.DefaultSettings()
	cfg.MaxDevInputTokens = 250_000
	r := &Runner{Project: team.File{Settings: cfg}}
	r.resetDevBudgets()
	labels := map[string]string{
		"dev_3": "Desenvolvedor · dev_3",
		"qa":    "QA · qa",
	}

	if err := r.checkDevGuard(devUsageEvent("dev_3", 130_000), labels); err != nil {
		t.Fatalf("unexpected token abort at 130k: %v", err)
	}
	if err := r.checkDevGuard(devUsageEvent("dev_3", 130_000), labels); err == nil {
		t.Fatal("expected input-token budget abort after 260k")
	}

	// QA taking over resets the round budgets.
	if err := r.checkDevGuard(devUsageEvent("qa", 10_000), labels); err != nil {
		t.Fatalf("qa event must reset budgets, got %v", err)
	}
	max := cfg.EffectiveMaxDevExploreCalls()
	for i := 0; i < max-1; i++ {
		if err := r.checkDevGuard(devToolEvent("dev_3", "search_text"), labels); err != nil {
			t.Fatalf("dev budget must be fresh after QA: read %d -> %v", i+1, err)
		}
	}
}

func TestDevGuardInputTokenBudgetDisabledByDefault(t *testing.T) {
	r := &Runner{Project: team.File{Settings: team.DefaultSettings()}}
	r.resetDevBudgets()
	labels := map[string]string{"dev_1": "Desenvolvedor · dev_1"}

	for i := 0; i < 30; i++ {
		if err := r.checkDevGuard(devUsageEvent("dev_1", 100_000), labels); err != nil {
			t.Fatalf("default settings must not abort dev by raw input-token sum (call %d): %v", i+1, err)
		}
	}
}

func TestDevGuardIgnoresNonDevStages(t *testing.T) {
	cfg := team.DefaultSettings()
	cfg.MaxDevExploreCalls = 8
	r := &Runner{Project: team.File{Settings: cfg}}
	r.resetDevBudgets()
	labels := map[string]string{"chefe": "Gerente de Projeto (Chefe) · chefe"}

	for i := 0; i < 30; i++ {
		if err := r.checkDevGuard(devToolEvent("chefe", "read_file"), labels); err != nil {
			t.Fatalf("chief exploration must not be limited by dev guard: read %d -> %v", i+1, err)
		}
	}
}

func TestDevExploreBudgetCallbacksWarnSoftBlockAndResetOnWrite(t *testing.T) {
	newStub := func(name string) tool.Tool {
		built, err := functiontool.New(functiontool.Config{
			Name:        name,
			Description: name,
		}, func(_ agent.Context, _ struct{}) (map[string]any, error) {
			return map[string]any{"text": "ok"}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return built
	}
	readTool := newStub("read_file")
	searchTool := newStub("search_text")
	editTool := newStub("edit_file")
	writeTool := newStub("write_file")

	round := 0
	// maxExplore=20 -> softBlockAt=8 (20-12=8), softWarnAt=3 (3..8 warn, >8 block)
	before, after := newDevExploreBudgetCallbacks(20, func() int { return round })

	for i := 1; i <= 2; i++ {
		res, err := before(nil, readTool, nil)
		if err != nil || res != nil {
			t.Fatalf("read %d before softWarnAt should not block: res=%+v err=%v", i, res, err)
		}
		ares, aerr := after(nil, readTool, nil, map[string]any{"text": "conteudo"}, nil)
		if aerr != nil || ares != nil {
			t.Fatalf("read %d before softWarnAt should not attach warning: res=%+v err=%v", i, ares, aerr)
		}
	}

	for i := 3; i <= 8; i++ {
		tl := readTool
		if i%2 == 0 {
			tl = searchTool
		}
		res, err := before(nil, tl, nil)
		if err != nil || res != nil {
			t.Fatalf("explore %d <= softBlockAt should pass: res=%+v err=%v", i, res, err)
		}
		ares, aerr := after(nil, tl, nil, map[string]any{"text": "conteudo"}, nil)
		if aerr != nil || ares == nil || !strings.Contains(fmt.Sprint(ares["text"]), "[AVISO DE ORÇAMENTO DO DEV") {
			t.Fatalf("explore %d should append budget warning in AfterToolCallback, got res=%+v err=%v", i, ares, aerr)
		}
	}

	// 9th explore call exceeds softBlockAt=8 -> BeforeToolCallback blocks cleanly.
	blocked, err := before(nil, readTool, nil)
	if err != nil || blocked == nil || !strings.Contains(fmt.Sprint(blocked["text"]), "ORÇAMENTO DE EXPLORAÇÃO DO DEV ATINGIDO") {
		t.Fatalf("expected soft-block redirect after softBlockAt, got res=%+v err=%v", blocked, err)
	}

	// Editing resets exploration counter and unlocks subsequent reads.
	if res, err := before(nil, editTool, nil); err != nil || res != nil {
		t.Fatalf("edit_file must pass and reset: res=%+v err=%v", res, err)
	}
	if res, err := before(nil, readTool, nil); err != nil || res != nil {
		t.Fatalf("read_file after edit_file must be unlocked: res=%+v err=%v", res, err)
	}

	// Exhaust again and confirm round increment (new QA loop round) resets budget too.
	_, _ = before(nil, writeTool, nil) // writes > 0 stays unlocked within same round, next round resets cleanly
	round = 1
	for i := 1; i <= 8; i++ {
		if res, err := before(nil, readTool, nil); err != nil || res != nil {
			t.Fatalf("new round read %d <= softBlockAt must be allowed: res=%+v err=%v", i, res, err)
		}
	}
	blocked2, _ := before(nil, searchTool, nil)
	if blocked2 == nil || !strings.Contains(fmt.Sprint(blocked2["text"]), "ORÇAMENTO DE EXPLORAÇÃO DO DEV ATINGIDO") {
		t.Fatalf("expected soft-block again in round 1 after 8 explores, got %+v", blocked2)
	}

	// Test total-calls soft wrap-up (maxCalls=20 -> softWrapCallsAt=16):
	// When the Dev has already written files and approaches maxCalls, BeforeToolCallback
	// soft-stops further tool calls so the Dev outputs its final summary and hands off to QA
	// instead of crashing at checkDevGuard's hard cap.
	round = 2
	beforeWrap, afterWrap := newDevExploreBudgetCallbacks(40, func() int { return round }, 20)
	if res, err := beforeWrap(nil, writeTool, nil); err != nil || res != nil {
		t.Fatalf("initial write must pass: res=%+v err=%v", res, err)
	}
	var sawWrapWarn bool
	for i := 2; i <= 16; i++ {
		res, err := beforeWrap(nil, readTool, nil)
		if err != nil || res != nil {
			t.Fatalf("call %d <= softWrapCallsAt(16) must pass: res=%+v err=%v", i, res, err)
		}
		ares, _ := afterWrap(nil, readTool, nil, map[string]any{"text": "ok"}, nil)
		if ares != nil && strings.Contains(fmt.Sprint(ares["text"]), "[AVISO DE ENCERRAMENTO DA RODADA") {
			sawWrapWarn = true
		}
	}
	if !sawWrapWarn {
		t.Fatal("expected AfterToolCallback wrap-up warning before softWrapCallsAt")
	}
	wrapBlocked, err := beforeWrap(nil, readTool, nil)
	if err != nil || wrapBlocked == nil || !strings.Contains(fmt.Sprint(wrapBlocked["text"]), "ORÇAMENTO DE FERRAMENTAS DA RODADA CONCLUÍDO") {
		t.Fatalf("17th call (> softWrapCallsAt=16) must soft-stop to hand off to QA, got res=%+v err=%v", wrapBlocked, err)
	}
}

func TestEmitEventOrdersTextBeforeToolCallsInSameResponse(t *testing.T) {
	var kinds []string
	r := &Runner{
		OnEvent: func(ev Event) {
			if ev.Kind == "text" || ev.Kind == "tool_call" {
				kinds = append(kinds, ev.Kind)
			}
		},
	}
	// Mirrors agyEnvelope.toResponse: FunctionCall parts precede the final Text part.
	ev := &session.Event{
		Author: "dev",
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{
					{FunctionCall: &genai.FunctionCall{ID: "agy-1-1", Name: "read_file", Args: map[string]any{"path": "a.go"}}},
					{FunctionCall: &genai.FunctionCall{ID: "agy-1-2", Name: "read_file", Args: map[string]any{"path": "b.go"}}},
					{Text: "Vou ler a.go e b.go."},
				},
			},
		},
	}
	r.emitEvent(ev, map[string]string{"dev": "Desenvolvedor · dev"})
	if strings.Join(kinds, ",") != "text,tool_call,tool_call" {
		t.Fatalf("expected text event before tool_call events to prevent duplicate/concatenated bubbles, got %v", kinds)
	}
}
