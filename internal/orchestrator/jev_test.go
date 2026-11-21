package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestJevModelFactorySkipsBusyHalfOpenSlots(t *testing.T) {
	pol := jev.DefaultPolicy()
	tmp := t.TempDir()
	catOnly := jev.NewCatalogStore(tmp+"/catalog2.json", nil)
	catOnly.MergeSource("openrouter", []jev.ModelEntry{
		{Provider: "openrouter", Model: "healthy-e", ContextLength: 128_000, PromptMicroUSD: 0.2, CompletionMicroUSD: 0.4, Quality: 0.70, Capabilities: jev.Capabilities{Tools: true}, DataClass: jev.DataClassHTTPAPI, Tier: jev.TierCheap},
		{Provider: "openrouter", Model: "ho-1", ContextLength: 128_000, PromptMicroUSD: 0.1, CompletionMicroUSD: 0.1, Quality: 0.99, Capabilities: jev.Capabilities{Tools: true}, DataClass: jev.DataClassHTTPAPI, Tier: jev.TierCheap},
		{Provider: "openrouter", Model: "ho-2", ContextLength: 128_000, PromptMicroUSD: 0.1, CompletionMicroUSD: 0.1, Quality: 0.98, Capabilities: jev.Capabilities{Tools: true}, DataClass: jev.DataClassHTTPAPI, Tier: jev.TierCheap},
		{Provider: "openrouter", Model: "ho-3", ContextLength: 128_000, PromptMicroUSD: 0.1, CompletionMicroUSD: 0.1, Quality: 0.97, Capabilities: jev.Capabilities{Tools: true}, DataClass: jev.DataClassHTTPAPI, Tier: jev.TierCheap},
		{Provider: "openrouter", Model: "ho-4", ContextLength: 128_000, PromptMicroUSD: 0.1, CompletionMicroUSD: 0.1, Quality: 0.96, Capabilities: jev.Capabilities{Tools: true}, DataClass: jev.DataClassHTTPAPI, Tier: jev.TierCheap},
	})
	var builtModel string
	// First 4 calls hit half_open busy (simulated by build returning transient outage without consuming all attempts when >3? Wait: buildFails caps at 3!).
	// Verify real HealthStore half_open contention: seed 4 models into half_open via outage, wait for Snapshot transition, claim their slots, then factory skips all 4!
	hs2 := jev.NewHealthStore(tmp+"/health2.json", jev.CooldownConfig{Quota: "1ms", RateLimit: "1ms", Outage: "1ms"})
	for _, m := range []string{"ho-1", "ho-2", "ho-3", "ho-4"} {
		hs2.RecordFailure("openrouter", m, jev.ErrClassOutage, time.Time{})
	}
	time.Sleep(15 * time.Millisecond)
	for _, m := range []string{"ho-1", "ho-2", "ho-3", "ho-4"} {
		if !hs2.TryAcquireHalfOpen("openrouter", m) {
			t.Fatalf("expected %s to acquire half-open probe slot", m)
		}
	}
	factory := NewJevModelFactory(JevFactoryOptions{
		Policy:      &pol,
		Catalog:     catOnly,
		HealthStore: hs2,
		Creds:       func() map[string]bool { return map[string]bool{"openrouter": true} },
		BuildModel: func(_ context.Context, prov, mod string) (model.LLM, error) {
			builtModel = prov + "/" + mod
			return &modelx.Scripted{}, nil
		},
	})
	ag := team.Agent{Name: "qa", Role: team.RoleQA, Provider: "jev", Model: "auto"}
	if _, err := factory(context.Background(), ag); err != nil {
		t.Fatalf("expected factory to skip all 4 busy half_open models and pick healthy-e, got %v", err)
	}
	if builtModel != "openrouter/healthy-e" {
		t.Fatalf("expected openrouter/healthy-e, got %q", builtModel)
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

func TestClassifierStaysOffUnlessEnabled(t *testing.T) {
	long := strings.Repeat("lorem ipsum dolor sit amet ", 40)
	ctx := withJevRouteHint(context.Background(), jevRouteHint{
		Step: "dev_report", ObjectiveLen: len(long), Objective: long, InputTokens: 3000,
	})
	newFactory := func(enabled bool) (func(context.Context, team.Agent) (model.LLM, error), *int) {
		pol := jev.DefaultPolicy()
		pol.ClassifyEnabled = enabled
		cat := jev.NewCatalogStore(t.TempDir()+"/catalog.json", nil)
		cat.MergeSource("openrouter", []jev.ModelEntry{{
			Provider:           "openrouter",
			Model:              "deepseek/deepseek-chat",
			ContextLength:      128_000,
			PromptMicroUSD:     0.14,
			CompletionMicroUSD: 0.28,
			Capabilities:       jev.Capabilities{Tools: true},
			Tier:               jev.TierCheap,
			DataClass:          jev.DataClassHTTPAPI,
		}})
		calls := 0
		factory := NewJevModelFactory(JevFactoryOptions{
			Policy:  &pol,
			Catalog: cat,
			Health: func() jev.HealthSnapshot {
				return jev.HealthSnapshot{Credentials: map[string]bool{"openrouter": true}, AgyRemainingPct: -1}
			},
			BuildModel: func(context.Context, string, string) (model.LLM, error) {
				return &modelx.Scripted{}, nil
			},
			Classify: func(_ context.Context, objective string) (string, error) {
				calls++
				if strings.Contains(objective, "policy.json") || strings.Contains(objective, "sk-") {
					t.Fatal("classificador recebeu policy ou chave")
				}
				return "compre o modelo caro", nil
			},
		})
		return factory, &calls
	}
	off, offCalls := newFactory(false)
	if _, err := off(ctx, team.Agent{Name: "n", Role: "analyst", Provider: "jev", Model: "auto"}); err != nil {
		t.Fatal(err)
	}
	if *offCalls != 0 {
		t.Fatalf("flag desligada fez %d chamadas", *offCalls)
	}
	on, onCalls := newFactory(true)
	if _, err := on(ctx, team.Agent{Name: "dev", Role: team.RoleDeveloper, Provider: "jev", Model: "auto"}); err != nil {
		t.Fatal(err)
	}
	if *onCalls != 0 {
		t.Fatalf("papel claro não deveria classificar, calls=%d", *onCalls)
	}
	if _, err := on(ctx, team.Agent{Name: "n", Role: "analyst", Provider: "jev", Model: "auto"}); err != nil {
		t.Fatal(err)
	}
	if *onCalls != 1 {
		t.Fatalf("confiança baixa deveria chamar uma vez, calls=%d", *onCalls)
	}
}

func TestClassifierDefaultOnAndFailureKeepsHeuristic(t *testing.T) {
	if !jev.DefaultPolicy().ClassifyEnabled {
		t.Fatal("padrão do classificador deveria ser ligado")
	}
	long := strings.Repeat("lorem ipsum dolor sit amet ", 40)
	ctx := withJevRouteHint(context.Background(), jevRouteHint{
		Step: "dev_report", ObjectiveLen: len(long), Objective: long, InputTokens: 3000,
	})
	agent := team.Agent{Name: "n", Role: "analyst", Provider: "jev", Model: "auto"}
	route := func(pol jev.Policy, classify func(context.Context, string) (string, error)) string {
		t.Helper()
		cat := jev.NewCatalogStore(t.TempDir()+"/catalog.json", nil)
		cat.MergeSource("openrouter", []jev.ModelEntry{{
			Provider: "openrouter", Model: "deepseek/deepseek-chat",
			ContextLength: 128_000, PromptMicroUSD: 0.14, CompletionMicroUSD: 0.28,
			Capabilities: jev.Capabilities{Tools: true}, Tier: jev.TierCheap, DataClass: jev.DataClassHTTPAPI,
		}})
		var got string
		factory := NewJevModelFactory(JevFactoryOptions{
			Policy:  &pol,
			Catalog: cat,
			Health: func() jev.HealthSnapshot {
				return jev.HealthSnapshot{Credentials: map[string]bool{"openrouter": true}, AgyRemainingPct: -1}
			},
			BuildModel: func(_ context.Context, provider, modelName string) (model.LLM, error) {
				if modelName == "deepseek-v4.1-flash" {
					return nil, errors.New("classificador indisponível")
				}
				got = provider + "/" + modelName
				return &modelx.Scripted{}, nil
			},
			Classify: classify,
		})
		if _, err := factory(ctx, agent); err != nil {
			t.Fatalf("rota não pode cair se o classificador falha: %v", err)
		}
		return got
	}
	off := jev.DefaultPolicy()
	off.ClassifyEnabled = false
	want := route(off, func(context.Context, string) (string, error) {
		t.Fatal("flag desligada não chama o classificador")
		return "", nil
	})
	if want == "" {
		t.Fatal("rota heurística vazia")
	}
	on := jev.DefaultPolicy()
	if got := route(on, func(context.Context, string) (string, error) {
		return "", errors.New("indisponível")
	}); got != want {
		t.Fatalf("erro do classificador mudou a rota: %s vs %s", got, want)
	}
	if got := route(on, func(context.Context, string) (string, error) {
		return "duas palavras invalidas demais", nil
	}); got != want {
		t.Fatalf("texto inválido mudou a rota: %s vs %s", got, want)
	}
	if got := route(on, nil); got != want {
		t.Fatalf("modelo do classificador indisponível mudou a rota: %s vs %s", got, want)
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
