package orchestrator

import (
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
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
