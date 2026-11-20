package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestJevModelFactoryLegacyTeamUnchanged(t *testing.T) {
	pol := jev.DefaultPolicy() // Enabled: false
	var calledProvider, calledModel string
	factory := NewJevModelFactory(JevFactoryOptions{
		Policy: &pol,
		BuildModel: func(_ context.Context, provider, modelName string) (model.LLM, error) {
			calledProvider = provider
			calledModel = modelName
			return &modelx.Scripted{}, nil
		},
	})
	ag := team.Agent{Name: "dev", Role: team.RoleDeveloper, Provider: "gemini", Model: "gemini-2.5-pro"}
	if _, err := factory(context.Background(), ag); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calledProvider != "gemini" || calledModel != "gemini-2.5-pro" {
		t.Fatalf("legacy team must call BuildModel directly, got %s/%s", calledProvider, calledModel)
	}
}

func TestJevModelFactoryOptInEmitsModelRouteAndRedactsSecrets(t *testing.T) {
	pol := jev.DefaultPolicy() // Enabled: false; opt-in via provider: jev
	cat := jev.NewCatalogStore(t.TempDir()+"/catalog.json", nil)
	cat.MergeSource("openrouter", []jev.ModelEntry{
		{
			Provider:           "openrouter",
			Model:              "deepseek/deepseek-chat",
			ContextLength:      128_000,
			PromptMicroUSD:     0.14,
			CompletionMicroUSD: 0.28,
			Capabilities:       jev.Capabilities{Tools: true},
			Tier:               jev.TierCheap,
		},
	})
	var events []Event
	var calledProvider, calledModel string
	factory := NewJevModelFactory(JevFactoryOptions{
		Policy:  &pol,
		Catalog: cat,
		Health: func() jev.HealthSnapshot {
			return jev.HealthSnapshot{
				Credentials:     map[string]bool{"openrouter": true},
				AgyRemainingPct: -1,
			}
		},
		BuildModel: func(_ context.Context, provider, modelName string) (model.LLM, error) {
			calledProvider = provider
			calledModel = modelName
			return &modelx.Scripted{}, nil
		},
		OnEvent: func(ev Event) {
			events = append(events, ev)
		},
	})

	// Plant a secret-shaped token in agent name to verify event redaction.
	ag := team.Agent{Name: "dev-sk-plantsecret999", Role: team.RoleDeveloper, Provider: "jev", Model: "auto"}
	ctx := withJevRouteHint(context.Background(), jevRouteHint{
		Step:         "dev_report",
		ObjectiveLen: 200,
		InputTokens:  3000,
		NeedsTools:   true,
		Author:       ag.Name,
		Label:        "Desenvolvedor · " + ag.Name,
		Stage:        "dev",
	})
	if _, err := factory(ctx, ag); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calledProvider != "openrouter" || calledModel != "deepseek/deepseek-chat" {
		t.Fatalf("expected openrouter/deepseek/deepseek-chat, got %s/%s", calledProvider, calledModel)
	}
	if len(events) != 1 || events[0].Kind != "model_route" {
		t.Fatalf("expected 1 model_route event, got %+v", events)
	}
	ev := events[0]
	if ev.Provider != "openrouter" || ev.Model != "deepseek/deepseek-chat" || ev.Tier != "cheap" || ev.DecisionMicros <= 0 {
		t.Fatalf("unexpected model_route payload: %+v", ev)
	}
	if strings.Contains(ev.Author, "sk-plantsecret999") || strings.Contains(ev.Label, "sk-plantsecret999") || strings.Contains(ev.Text, "sk-plantsecret999") {
		t.Fatalf("secret leaked in model_route event: %+v", ev)
	}
}

func TestJevModelFactoryPinWinsOverAuto(t *testing.T) {
	pol := jev.DefaultPolicy()
	pol.Enabled = true
	cat := jev.NewCatalogStore(t.TempDir()+"/catalog.json", nil)
	var calledProvider, calledModel string
	var events []Event
	factory := NewJevModelFactory(JevFactoryOptions{
		Policy:  &pol,
		Catalog: cat,
		Health: func() jev.HealthSnapshot {
			return jev.HealthSnapshot{
				Credentials:     map[string]bool{"gemini": true, "anthropic": true},
				AgyRemainingPct: -1,
			}
		},
		BuildModel: func(_ context.Context, provider, modelName string) (model.LLM, error) {
			calledProvider = provider
			calledModel = modelName
			return &modelx.Scripted{}, nil
		},
		OnEvent: func(ev Event) { events = append(events, ev) },
	})

	ag := team.Agent{Name: "chefe", Role: team.RoleArchitect, Provider: "anthropic", Model: "claude-sonnet-4-5"}
	if _, err := factory(context.Background(), ag); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calledProvider != "anthropic" || calledModel != "claude-sonnet-4-5" {
		t.Fatalf("pin in team.yaml must win, got %s/%s", calledProvider, calledModel)
	}
	if len(events) != 1 || events[0].Kind != "model_route" || len(events[0].Reasons) == 0 || events[0].Reasons[0] != "pin" {
		t.Fatalf("expected model_route with pin reason, got %+v", events)
	}
}

func TestJevModelFactoryBudgetApprovalGate(t *testing.T) {
	pol := jev.DefaultPolicy()
	pol.AutoEscalateUSD = 0.02
	cat := jev.Catalog{
		Version: 1,
		Entries: []jev.ModelEntry{
			{
				Provider:           "openrouter",
				Model:              "expensive/model",
				ContextLength:      200_000,
				PromptMicroUSD:     10.0,
				CompletionMicroUSD: 50.0,
				Capabilities:       jev.Capabilities{Tools: true},
				DataClass:          jev.DataClassHTTPAPI,
				Tier:               jev.TierCheap,
				Source:             "openrouter",
			},
		},
	}
	cs := jev.NewCatalogStore(t.TempDir()+"/catalog.json", nil)
	cs.MergeSource("openrouter", cat.Entries)

	buildCalls := 0
	approved := false
	factory := NewJevModelFactory(JevFactoryOptions{
		Policy:  &pol,
		Catalog: cs,
		Health: func() jev.HealthSnapshot {
			return jev.HealthSnapshot{Credentials: map[string]bool{"openrouter": true}, AgyRemainingPct: -1}
		},
		BuildModel: func(_ context.Context, _, _ string) (model.LLM, error) {
			buildCalls++
			return &modelx.Scripted{}, nil
		},
		Approve: func(prompt string) bool {
			if !strings.Contains(prompt, "expensive/model") {
				t.Fatalf("expected approval prompt to name expensive model, got %q", prompt)
			}
			return approved
		},
	})

	ag := team.Agent{Name: "dev", Role: team.RoleDeveloper, Provider: "jev", Model: "auto"}
	ctx := withJevRouteHint(context.Background(), jevRouteHint{Step: "dev_report", InputTokens: 50_000, NeedsTools: true})

	// Denied: expensive model MUST NOT be constructed.
	if _, err := factory(ctx, ag); err == nil {
		t.Fatal("expected budget refusal when approval denied")
	}
	if buildCalls != 0 {
		t.Fatalf("expensive model must never be built without approval, got %d calls", buildCalls)
	}

	// Approved: expensive model is built once.
	approved = true
	if _, err := factory(ctx, ag); err != nil {
		t.Fatalf("expected approved escalation to succeed, got %v", err)
	}
	if buildCalls != 1 {
		t.Fatalf("expected 1 build call after approval, got %d", buildCalls)
	}
}

func TestJevModelFactoryBuildErrorRedacted(t *testing.T) {
	pol := jev.DefaultPolicy()
	cs := jev.NewCatalogStore(t.TempDir()+"/catalog.json", nil)
	factory := NewJevModelFactory(JevFactoryOptions{
		Policy:  &pol,
		Catalog: cs,
		Health: func() jev.HealthSnapshot {
			return jev.HealthSnapshot{Credentials: map[string]bool{"gemini": true}, AgyRemainingPct: -1}
		},
		BuildModel: func(_ context.Context, _, _ string) (model.LLM, error) {
			return nil, errors.New("falha auth com sk-secretvalue99999")
		},
	})
	ag := team.Agent{Name: "dev", Role: team.RoleDeveloper, Provider: "jev", Model: "auto"}
	_, err := factory(context.Background(), ag)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "sk-secretvalue99999") {
		t.Fatalf("secret leaked in returned error: %v", err)
	}
}

func TestJevModelFactoryProviderScopedOpenRouterAuto(t *testing.T) {
	pol := jev.DefaultPolicy() // Enabled: false; opt-in via openrouter:auto or jev:openrouter
	cat := jev.NewCatalogStore(t.TempDir()+"/catalog.json", nil)
	cat.MergeSource("openrouter", []jev.ModelEntry{
		{
			Provider:           "openrouter",
			Model:              "deepseek/deepseek-chat",
			ContextLength:      128_000,
			PromptMicroUSD:     0.14,
			CompletionMicroUSD: 0.28,
			Capabilities:       jev.Capabilities{Tools: true},
			Tier:               jev.TierCheap,
		},
	})
	for _, spec := range []team.Agent{
		{Name: "dev", Role: team.RoleDeveloper, Provider: "openrouter", Model: "openrouter:auto"},
		{Name: "dev", Role: team.RoleDeveloper, Provider: "jev", Model: "jev:openrouter"},
	} {
		var calledProvider, calledModel string
		factory := NewJevModelFactory(JevFactoryOptions{
			Policy:  &pol,
			Catalog: cat,
			Health: func() jev.HealthSnapshot {
				return jev.HealthSnapshot{
					Credentials:     map[string]bool{"agy": true, "gemini": true, "openrouter": true},
					AgyRemainingPct: -1,
				}
			},
			BuildModel: func(_ context.Context, provider, modelName string) (model.LLM, error) {
				calledProvider = provider
				calledModel = modelName
				return &modelx.Scripted{}, nil
			},
		})
		ctx := withJevRouteHint(context.Background(), jevRouteHint{
			Step:         "dev_report",
			ObjectiveLen: 200,
			InputTokens:  3000,
			NeedsTools:   true,
		})
		if _, err := factory(ctx, spec); err != nil {
			t.Fatalf("spec %+v unexpected error: %v", spec, err)
		}
		if calledProvider != "openrouter" || calledModel != "deepseek/deepseek-chat" {
			t.Fatalf("spec %+v expected Jev to pick openrouter/deepseek/deepseek-chat (ignoring static agy/gemini seeds), got %s/%s", spec, calledProvider, calledModel)
		}
	}
}

func TestRepoDenyCursorSkipsBuildEvenWhenPinned(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".hivemind"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("deny_providers: [cursor]\n")
	if err := os.WriteFile(filepath.Join(ws, ".hivemind", "jev.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	pol := jev.DefaultPolicy()
	built := 0
	factory := NewJevModelFactory(JevFactoryOptions{
		Policy: &pol,
		BuildModel: func(context.Context, string, string) (model.LLM, error) {
			built++
			return &modelx.Scripted{}, nil
		},
	})
	ctx := withJevRouteHint(context.Background(), jevRouteHint{Workspace: ws, Step: "dev_report"})
	_, err := factory(ctx, team.Agent{Name: "dev", Role: team.RoleDeveloper, Provider: "cursor", Model: "auto"})
	if err == nil {
		t.Fatal("pin cursor com deny_providers deveria falhar")
	}
	if built != 0 {
		t.Fatalf("NewCursor não pode ser construído com o provedor negado, build=%d", built)
	}
}
