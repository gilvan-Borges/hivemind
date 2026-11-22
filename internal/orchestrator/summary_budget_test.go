package orchestrator

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/contextx"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestSummarizeCompactionDeltaReturnsModelText(t *testing.T) {
	scripted := &modelx.Scripted{Fn: func(req *model.LLMRequest, call int) *model.LLMResponse {
		if req.Config == nil || req.Config.SystemInstruction == nil {
			t.Fatal("summarizer prompt must carry the fixed system instruction")
		}
		var user string
		for _, c := range req.Contents {
			for _, p := range c.Parts {
				user += p.Text
			}
		}
		if !strings.Contains(user, "read_file") {
			t.Fatal("summarizer user turn must carry the compacted payload")
		}
		return modelx.Text("## Ações concluídas\n- leu módulo 01")
	}}
	out, err := summarizeCompactionDelta(context.Background(), scripted, "", []contextx.CompactedEntry{
		{ID: "call-1", Tool: "read_file", Label: `read_file("src/module_01.ts")`, Text: "conteúdo do módulo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Ações concluídas") {
		t.Fatalf("unexpected summary: %q", out)
	}

	// Iterative update: the running summary rides the next user turn.
	scriptedIter := &modelx.Scripted{Fn: func(req *model.LLMRequest, _ int) *model.LLMResponse {
		var user string
		for _, c := range req.Contents {
			for _, p := range c.Parts {
				user += p.Text
			}
		}
		if !strings.Contains(user, "RESUMO ATUAL") || !strings.Contains(user, "v1") {
			t.Fatalf("iterative pass must preserve the running summary, got: %q", user)
		}
		return modelx.Text("v2")
	}}
	out, err = summarizeCompactionDelta(context.Background(), scriptedIter, "v1", []contextx.CompactedEntry{
		{ID: "call-2", Tool: "read_file", Label: "read_file", Text: "novo payload"},
	})
	if err != nil || out != "v2" {
		t.Fatalf("iterative summary = %q, %v", out, err)
	}
}

func TestSummarizeCompactionDeltaRejectsEmptyReply(t *testing.T) {
	scripted := &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
		return modelx.Text("   ")
	}}
	if _, err := summarizeCompactionDelta(context.Background(), scripted, "", []contextx.CompactedEntry{{ID: "c", Text: "x"}}); err == nil {
		t.Fatal("empty reply must fail (contextx cools down instead of injecting nothing useful)")
	}
}

func TestNewCompactionSummarizerUsesInjectedModel(t *testing.T) {
	scripted := &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
		return modelx.Text("resumo ok")
	}}
	r := &Runner{CompactionModel: scripted}
	summarizer := r.newCompactionSummarizer("dev_1", "Dev · dev_1", t.TempDir())
	out, err := summarizer(context.Background(), "", []contextx.CompactedEntry{{ID: "c1", Tool: "read_file", Text: "payload"}})
	if err != nil || out != "resumo ok" {
		t.Fatalf("injected model must serve the summary, got %q, %v", out, err)
	}
}

func newBudgetRunner(t *testing.T, settings team.Settings) *Runner {
	squad := team.Team{
		Name: "budget-test",
		Agents: []team.Agent{
			{Name: "pm", Role: team.RoleProjectManager, Provider: "gemini", Model: "gemini-flash-latest"},
		},
	}
	return &Runner{
		Workspace: t.TempDir(),
		Team:      squad,
		Project:   team.File{Settings: settings},
	}
}

func pricedUsageEvent() *session.Event {
	ev := &session.Event{Author: "pm"}
	ev.LLMResponse = model.LLMResponse{
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     200_000,
			CandidatesTokenCount: 50_000,
			TotalTokenCount:      250_000,
		},
	}
	return ev
}

func TestCheckRunBudgetDisabledByDefault(t *testing.T) {
	r := newBudgetRunner(t, team.Settings{})
	r.recordUsage(pricedUsageEvent(), map[string]string{"pm": "Gerente · pm"})
	if err := r.checkRunBudget(); err != nil {
		t.Fatalf("no ceiling configured must never trip, got %v", err)
	}
}

func TestCheckRunBudgetTokenCeiling(t *testing.T) {
	r := newBudgetRunner(t, team.Settings{MaxTokens: 100_000})
	r.recordUsage(pricedUsageEvent(), map[string]string{"pm": "Gerente · pm"})
	err := r.checkRunBudget()
	if err == nil || !strings.Contains(err.Error(), "limite de tokens") {
		t.Fatalf("token ceiling must trip, got %v", err)
	}
}

func TestCheckRunBudgetUSDCeiling(t *testing.T) {
	// One expensive Gemini call on the priced catalog lands well above a
	// one-cent ceiling; tokens stay unlimited so only the USD gate can trip.
	r := newBudgetRunner(t, team.Settings{MaxRunCostUSD: 0.01})
	r.recordUsage(pricedUsageEvent(), map[string]string{"pm": "Gerente · pm"})
	err := r.checkRunBudget()
	if err == nil || !strings.Contains(err.Error(), "orçamento de custo") {
		t.Fatalf("USD ceiling must trip, got %v", err)
	}

	// A generous ceiling stays silent.
	r2 := newBudgetRunner(t, team.Settings{MaxRunCostUSD: 50})
	r2.recordUsage(pricedUsageEvent(), map[string]string{"pm": "Gerente · pm"})
	if err := r2.checkRunBudget(); err != nil {
		t.Fatalf("generous ceiling must not trip, got %v", err)
	}
}

func TestCheckRunBudgetUSDFloorProtectsTinyTypos(t *testing.T) {
	s := team.Settings{MaxRunCostUSD: 0.0001}
	if got := s.EffectiveMaxRunCostUSD(); got != 10_000 {
		t.Fatalf("sub-cent values must floor to one cent (10000 micro-USD), got %d", got)
	}
}
