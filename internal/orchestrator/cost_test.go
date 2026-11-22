package orchestrator

import (
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestUsageSummaryLineReportsCacheReadAndHitPct(t *testing.T) {
	line := usageSummaryLine(Result{
		InputTokens:      10_000,
		OutputTokens:     500,
		TotalTokens:      10_500,
		CachedReadTokens: 6_000,
	})
	if !strings.Contains(line, "cache read: 6000 (60%)") {
		t.Fatalf("linha de uso deve reportar cache read com %% de acerto, got %q", line)
	}
	noCache := usageSummaryLine(Result{InputTokens: 100, OutputTokens: 10, TotalTokens: 110})
	if strings.Contains(noCache, "cache read") {
		t.Fatalf("sem cached read a linha não deve mencionar cache, got %q", noCache)
	}
}

func TestRunnerCostAccumulationByRunAgentAndRole(t *testing.T) {
	t.Setenv("HIVEMIND_DATA_DIR", t.TempDir())

	r := &Runner{
		Workspace: t.TempDir(),
		Team: team.Team{
			Name: "cost-test",
			Agents: []team.Agent{
				{Name: "pm", Role: team.RoleProjectManager, Provider: "gemini", Model: "gemini-flash-latest"},
				{Name: "dev_1", Role: team.RoleDeveloper, Provider: "gemini", Model: "gemini-flash-latest"},
				{Name: "qa", Role: team.RoleQA, Provider: "gemini", Model: "gemini-flash-latest"},
			},
		},
	}

	var events []Event
	r.OnEvent = func(ev Event) { events = append(events, ev) }

	labels := map[string]string{
		"pm":    "Gerente · pm",
		"dev_1": "Dev · dev_1",
		"qa":    "QA · qa",
	}

	evPM := &session.Event{Author: "pm"}
	evPM.LLMResponse = model.LLMResponse{
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     1000,
			CandidatesTokenCount: 200,
			TotalTokenCount:      1200,
		},
	}
	evDev := &session.Event{Author: "dev_1"}
	evDev.LLMResponse = model.LLMResponse{
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:        2000,
			CandidatesTokenCount:    500,
			CachedContentTokenCount: 1000,
			TotalTokenCount:         2500,
		},
	}
	evQA := &session.Event{Author: "qa"}
	evQA.LLMResponse = model.LLMResponse{
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     500,
			CandidatesTokenCount: 100,
			TotalTokenCount:      600,
		},
	}

	r.recordUsage(evPM, labels)
	r.recordUsage(evDev, labels)
	r.recordUsage(evQA, labels)

	var res Result
	r.populateUsageMetrics(&res)

	if res.TotalTokens != 4300 {
		t.Fatalf("expected TotalTokens 4300, got %d", res.TotalTokens)
	}
	if res.CostMicroUSD <= 0 {
		t.Fatalf("expected non-zero accumulated CostMicroUSD for priced Gemini catalog entries, got %d", res.CostMicroUSD)
	}
	if len(res.CostByRole) != 3 {
		t.Fatalf("expected 3 roles (Chefe, Dev, QA), got %#v", res.CostByRole)
	}
	if res.CostByRole[0].Role != "Chefe" || res.CostByRole[1].Role != "Dev" || res.CostByRole[2].Role != "QA" {
		t.Fatalf("unexpected role order: %#v", res.CostByRole)
	}
	if len(res.CostByAgent) != 3 {
		t.Fatalf("expected 3 agents in CostByAgent, got %#v", res.CostByAgent)
	}
	if len(events) != 3 || events[1].CachedReadTokens != 1000 || events[1].Role != "Dev" || events[1].CostMicroUSD <= 0 {
		t.Fatalf("expected enriched usage events with cache/role/cost, got %#v", events)
	}
	summary := usageSummaryLine(res)
	if summary == "" || summary == "Tokens: ↑3500 entrada · ↓800 saída · 4300 total" {
		t.Fatalf("expected USD cost in summary line, got %q", summary)
	}
}
