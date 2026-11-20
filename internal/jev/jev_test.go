package jev

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestClassifyTable(t *testing.T) {
	tests := []struct {
		name      string
		in        ClassifyInput
		wantClass Class
		wantComp  Complexity
	}{
		{
			name:      "triage short without image is trivial low",
			in:        ClassifyInput{Role: team.RoleArchitect, Step: "triage_chief", ObjectiveLen: 120, InputTokens: 3000},
			wantClass: ClassTrivial,
			wantComp:  ComplexityLow,
		},
		{
			name:      "triage with image becomes vision keeping low complexity",
			in:        ClassifyInput{Role: team.RoleArchitect, Step: "triage_chief", ObjectiveLen: 120, InputTokens: 3000, HasImage: true},
			wantClass: ClassVision,
			wantComp:  ComplexityLow,
		},
		{
			name:      "pm planning long objective is planning medium",
			in:        ClassifyInput{Role: team.RoleProjectManager, Step: "pm_plan", ObjectiveLen: 1200, InputTokens: 15_000},
			wantClass: ClassPlanning,
			wantComp:  ComplexityMedium,
		},
		{
			name:      "architect planning over 100k tokens is planning high",
			in:        ClassifyInput{Role: team.RoleArchitect, Step: "architect_plan", ObjectiveLen: 400, InputTokens: 120_000},
			wantClass: ClassPlanning,
			wantComp:  ComplexityHigh,
		},
		{
			name:      "dev short objective is coding low",
			in:        ClassifyInput{Role: team.RoleDeveloper, Step: "dev_report", ObjectiveLen: 300, InputTokens: 8000},
			wantClass: ClassCoding,
			wantComp:  ComplexityLow,
		},
		{
			name:      "dev with qa stall escalates to coding high",
			in:        ClassifyInput{Role: team.RoleDeveloper, Step: "dev_report", ObjectiveLen: 300, InputTokens: 8000, QAStall: 1},
			wantClass: ClassCoding,
			wantComp:  ComplexityHigh,
		},
		{
			name:      "qa normal is qa low",
			in:        ClassifyInput{Role: team.RoleQA, Step: "qa_report", ObjectiveLen: 500, InputTokens: 12_000},
			wantClass: ClassQA,
			wantComp:  ComplexityLow,
		},
		{
			name:      "auto review is qa low",
			in:        ClassifyInput{Role: team.RoleArchitect, RunMode: "auto-review", Step: "triage_chief", ObjectiveLen: 500, InputTokens: 12_000},
			wantClass: ClassQA,
			wantComp:  ComplexityLow,
		},
		{
			name:      "qa with stall is qa high",
			in:        ClassifyInput{Role: team.RoleQA, Step: "qa_report", ObjectiveLen: 500, InputTokens: 12_000, QAStall: 1},
			wantClass: ClassQA,
			wantComp:  ComplexityHigh,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotClass, gotComp := Classify(tc.in)
			if gotClass != tc.wantClass || gotComp != tc.wantComp {
				t.Fatalf("Classify() = (%s, %s), want (%s, %s)", gotClass, gotComp, tc.wantClass, tc.wantComp)
			}
		})
	}
}

func TestScoreCheaperWinsWithinCheapTier(t *testing.T) {
	cat := Catalog{
		Entries: []ModelEntry{
			{
				Provider:           "openrouter",
				Model:              "expensive/cheap-tier",
				ContextLength:      128_000,
				PromptMicroUSD:     0.50,
				CompletionMicroUSD: 1.50,
				Capabilities:       Capabilities{Tools: true},
				LatencyP50Ms:       800,
				Quality:            0.90,
				DataClass:          DataClassHTTPAPI,
				Tier:               TierCheap,
			},
			{
				Provider:           "openrouter",
				Model:              "budget/flash",
				ContextLength:      128_000,
				PromptMicroUSD:     0.08,
				CompletionMicroUSD: 0.25,
				Capabilities:       Capabilities{Tools: true},
				LatencyP50Ms:       800,
				Quality:            0.60,
				DataClass:          DataClassHTTPAPI,
				Tier:               TierCheap,
			},
			{
				Provider:           "openrouter",
				Model:              "strong/opus",
				ContextLength:      200_000,
				PromptMicroUSD:     15.0,
				CompletionMicroUSD: 75.0,
				Capabilities:       Capabilities{Tools: true},
				LatencyP50Ms:       600,
				Quality:            0.99,
				DataClass:          DataClassHTTPAPI,
				Tier:               TierStrong,
			},
		},
	}
	pol := DefaultPolicy()
	health := HealthSnapshot{
		Credentials:     map[string]bool{"openrouter": true},
		AgyRemainingPct: -1,
	}
	task := Task{Role: team.RoleDeveloper, Class: ClassCoding, Complexity: ComplexityLow, InputTokens: 5000, NeedsTools: true}
	choice, ref := Score(task, cat, pol, health, AgentPin{}, RepoPolicy{})
	if ref.Code != "" {
		t.Fatalf("unexpected refusal: %+v", ref)
	}
	if choice.Model != "budget/flash" {
		t.Fatalf("expected cheaper model budget/flash to win despite lower quality, got %s/%s", choice.Provider, choice.Model)
	}
}

func TestScorePinPrecedence(t *testing.T) {
	cat := DefaultStaticCatalog()
	pol := DefaultPolicy()
	pol.Pins = map[string]RolePin{
		team.RoleDeveloper: {Provider: "opencode", Model: "opencode-go:deepseek-v4.1-flash"},
	}
	health := HealthSnapshot{
		Credentials:     map[string]bool{"gemini": true, "opencode": true, "anthropic": true},
		AgyRemainingPct: -1,
	}
	task := Task{Role: team.RoleDeveloper, Class: ClassCoding, Complexity: ComplexityLow, InputTokens: 1000, NeedsTools: true}

	// 1. team.yaml agent pin beats policy role pin and automatic selection.
	choice, ref := Score(task, cat, pol, health, AgentPin{Provider: "anthropic", Model: "claude-sonnet-4-5"}, RepoPolicy{})
	if ref.Code != "" {
		t.Fatalf("unexpected refusal: %+v", ref)
	}
	if choice.Provider != "anthropic" || choice.Model != "claude-sonnet-4-5" || !choice.Pinned {
		t.Fatalf("team.yaml pin must win, got %+v", choice)
	}

	// 2. When team.yaml uses jev/auto, policy.json role pin beats automatic selection.
	choice, ref = Score(task, cat, pol, health, AgentPin{Provider: "jev", Model: "auto"}, RepoPolicy{})
	if ref.Code != "" {
		t.Fatalf("unexpected refusal: %+v", ref)
	}
	if choice.Provider != "opencode" || choice.Model != "opencode-go:deepseek-v4.1-flash" || !choice.Pinned {
		t.Fatalf("policy role pin must win over auto, got %+v", choice)
	}
}

func TestScoreFiltersContextVisionAllowlistAndBudget(t *testing.T) {
	cat := Catalog{
		Entries: []ModelEntry{
			{
				Provider:           "openrouter",
				Model:              "small/ctx",
				ContextLength:      16_000,
				PromptMicroUSD:     0.01,
				CompletionMicroUSD: 0.01,
				Capabilities:       Capabilities{Tools: true, Vision: true},
				DataClass:          DataClassHTTPAPI,
				Tier:               TierCheap,
			},
			{
				Provider:           "openrouter",
				Model:              "big/expensive",
				ContextLength:      200_000,
				PromptMicroUSD:     5.0,
				CompletionMicroUSD: 25.0,
				Capabilities:       Capabilities{Tools: true, Vision: true},
				DataClass:          DataClassHTTPAPI,
				Tier:               TierCheap,
			},
			{
				Provider:      "cursor",
				Model:         "auto",
				ContextLength: 200_000,
				Capabilities:  Capabilities{Tools: false, Vision: true, WorkspaceAgent: true},
				DataClass:     DataClassWorkspaceAgent,
				Tier:          TierCheap,
			},
		},
	}
	pol := DefaultPolicy()
	pol.AutoEscalateUSD = 0.02 // $0.02 = 20,000 microUSD
	health := HealthSnapshot{
		Credentials:     map[string]bool{"openrouter": true, "cursor": true},
		AgyRemainingPct: -1,
	}

	// InputTokens = 20,000 requires context >= 24,000 (1.2x), eliminating small/ctx.
	// With repo denying cursor and big/expensive costing > $0.02, Score MUST refuse with budget!
	task := Task{Role: team.RoleDeveloper, Class: ClassCoding, Complexity: ComplexityLow, InputTokens: 20_000, NeedsTools: true}
	_, ref := Score(task, cat, pol, health, AgentPin{}, RepoPolicy{DenyProviders: []string{"cursor"}})
	if ref.Code != "budget" || ref.Need == nil || ref.Need.Model != "big/expensive" {
		t.Fatalf("expected budget refusal pointing to big/expensive, got %+v", ref)
	}

	// With pending ADK tool call, cursor (WorkspaceAgent) is ineligible even if not denied.
	taskPending := task
	taskPending.HasPendingToolCall = true
	_, ref = Score(taskPending, cat, pol, health, AgentPin{}, RepoPolicy{})
	if ref.Code != "budget" {
		t.Fatalf("expected WorkspaceAgent skipped on pending ADK tool call, got %+v", ref)
	}
}

func TestScoreAgyQuotaBelow5Percent(t *testing.T) {
	cat := DefaultStaticCatalog()
	pol := DefaultPolicy()
	health := HealthSnapshot{
		Credentials:     map[string]bool{"agy": true, "gemini": true},
		AgyRemainingPct: 4.0,
	}
	task := Task{Role: team.RoleDeveloper, Class: ClassCoding, Complexity: ComplexityLow, InputTokens: 2000, NeedsTools: true}
	choice, ref := Score(task, cat, pol, health, AgentPin{}, RepoPolicy{})
	if ref.Code != "" {
		t.Fatalf("unexpected refusal: %+v", ref)
	}
	if choice.Provider != "gemini" {
		t.Fatalf("expected cheap HTTP gemini to beat agy when agy quota < 5%%, got %+v", choice)
	}
}

func TestCurated21OpenRouterModelsAndPromotionDifficultyRouting(t *testing.T) {
	cat := DefaultStaticCatalog()
	if len(CuratedOpenRouterSeedEntries()) != 21 {
		t.Fatalf("expected 21 curated OpenRouter models, got %d", len(CuratedOpenRouterSeedEntries()))
	}
	for _, seed := range CuratedOpenRouterSeedEntries() {
		if !IsTrustedOpenRouterModel(seed.Model) {
			t.Fatalf("curated model %s must be trusted by IsTrustedOpenRouterModel", seed.Model)
		}
		if _, ok := cat.Lookup("openrouter", seed.Model); !ok {
			t.Fatalf("curated model %s missing from DefaultStaticCatalog", seed.Model)
		}
	}

	pol := DefaultPolicy()
	health := HealthSnapshot{
		Credentials:     map[string]bool{"openrouter": true},
		AgyRemainingPct: -1,
	}

	// Medium difficulty coding task must route to TierMid and prioritize Google Carbon (-50% discount).
	medTask := Task{
		Role:         team.RoleDeveloper,
		Class:        ClassCoding,
		Complexity:   ComplexityMedium,
		InputTokens:  6000,
		NeedsTools:   true,
		OnlyProvider: "openrouter",
	}
	choice, ref := Score(medTask, cat, pol, health, AgentPin{}, RepoPolicy{})
	if ref.Code != "" {
		t.Fatalf("unexpected refusal on medium task: %+v", ref)
	}
	if choice.Model != "google/gemini-4-carbon" || choice.DiscountPct != 50 || choice.DisplayName != "Google Carbon" {
		t.Fatalf("expected google/gemini-4-carbon (-50%%) to win on medium difficulty coding task, got %+v", choice)
	}

	// Reserve rule ($1.00 floor): when OpenRouter balance is <= $1.00 (e.g. $0.95 or $1.00),
	// Jev MUST NOT select OpenRouter (preserving that $1.00 for Jev itself) and MUST NOT break
	// even if OnlyProvider was "openrouter" — it automatically falls back to agy / cursor!
	reserveHealth := HealthSnapshot{
		Credentials:            map[string]bool{"openrouter": true, "agy": true, "cursor": true},
		AgyRemainingPct:        80,
		OpenRouterBalanceUSD:   1.00,
		OpenRouterBalanceKnown: true,
	}
	fallbackChoice, fallbackRef := Score(medTask, cat, pol, reserveHealth, AgentPin{Provider: "openrouter", Model: "openrouter:auto"}, RepoPolicy{})
	if fallbackRef.Code != "" {
		t.Fatalf("expected Jev not to break when OpenRouter has <= $1.00, got refusal: %+v", fallbackRef)
	}
	if fallbackChoice.Provider == "openrouter" {
		t.Fatalf("expected Jev to preserve the $1.00 OpenRouter reserve and pick agy/cursor, got %+v", fallbackChoice)
	}
	if fallbackChoice.Provider != "agy" && fallbackChoice.Provider != "cursor" {
		t.Fatalf("expected Jev to fall back to agy or cursor when OpenRouter <= $1.00, got %+v", fallbackChoice)
	}
}

func TestRouteRejectsNonCatalogIDAndAsyncRefresh(t *testing.T) {
	dir := t.TempDir()
	catPath := filepath.Join(dir, "catalog.json")
	var refreshCalls atomic.Int32
	cs := NewCatalogStore(catPath, map[string]SourceFetcher{
		"openrouter": func(context.Context) ([]ModelEntry, error) {
			refreshCalls.Add(1)
			return []ModelEntry{
				{
					Provider:           "openrouter",
					Model:              "deepseek/deepseek-chat",
					ContextLength:      128_000,
					PromptMicroUSD:     0.14,
					CompletionMicroUSD: 0.28,
					Capabilities:       Capabilities{Tools: true},
					Tier:               TierCheap,
				},
				{
					Provider: "openrouter",
					Model:    "bad model with spaces\nrm -rf",
					Tier:     TierCheap,
				},
			}, nil
		},
	})
	// Refresh synchronously once to verify snapshot persistence and sanitization.
	cs.RefreshNow(context.Background())
	if refreshCalls.Load() != 1 {
		t.Fatalf("expected 1 refresh call, got %d", refreshCalls.Load())
	}
	snap := cs.Snapshot()
	if _, ok := snap.Lookup("openrouter", "bad model with spaces\nrm -rf"); ok {
		t.Fatal("unsafe model ID with spaces/newline must not enter catalog")
	}
	if _, ok := snap.Lookup("openrouter", "deepseek/deepseek-chat"); !ok {
		t.Fatal("expected deepseek/deepseek-chat in catalog")
	}
	if _, ok := snap.Lookup("openrouter", "injected/not-in-catalog"); ok {
		t.Fatal("non-catalog model ID must never be found")
	}
	// Verify disk persistence permissions and reload.
	reloaded, err := LoadCatalogFrom(catPath)
	if err != nil {
		t.Fatalf("LoadCatalogFrom: %v", err)
	}
	if _, ok := reloaded.Lookup("openrouter", "deepseek/deepseek-chat"); !ok {
		t.Fatal("persisted catalog.json missing deepseek/deepseek-chat")
	}
}

func TestRepoPolicyIgnoresBudgetAndWeights(t *testing.T) {
	ws := t.TempDir()
	hmDir := filepath.Join(ws, ".hivemind")
	if err := os.MkdirAll(hmDir, 0o700); err != nil {
		t.Fatal(err)
	}
	yamlContent := `
deny_providers: [cursor, openrouter]
dailyUsd:
  openrouter: 999.0
weights:
  quality: 1.0
allow: [workspace_agent]
pins:
  developer:
    provider: openai
    model: gpt-5
`
	if err := os.WriteFile(filepath.Join(hmDir, "jev.yaml"), []byte(yamlContent), 0o600); err != nil {
		t.Fatal(err)
	}
	rp, warnings, err := LoadRepoPolicy(ws)
	if err != nil {
		t.Fatalf("LoadRepoPolicy: %v", err)
	}
	if len(rp.DenyProviders) != 2 || rp.DenyProviders[0] != "cursor" || rp.DenyProviders[1] != "openrouter" {
		t.Fatalf("unexpected deny_providers: %#v", rp.DenyProviders)
	}
	if len(warnings) < 4 {
		t.Fatalf("expected warnings for ignored security fields, got %#v", warnings)
	}
	if key := RepoKey(ws, "git@github.com:org/repo.git"); key != "github.com/org/repo" {
		t.Fatalf("unexpected RepoKey for SSH remote: %q", key)
	}
	localKey := RepoKey(ws, "")
	if !strings.HasPrefix(localKey, "local:") || strings.Contains(localKey, filepath.Base(ws)) {
		t.Fatalf("local RepoKey must be hashed, not plaintext path: %q", localKey)
	}
}

func TestRedactSecrets(t *testing.T) {
	raw := "erro ao chamar provedor sk-testsecret12345 e crsr_abcdef12345 e AIzaSyTestKey1234567 e hmlan_abcdefghijklmnopqrstuvwxyz"
	clean := RedactSecrets(raw, "minha-chave-secreta")
	for _, secret := range []string{"sk-testsecret12345", "crsr_abcdef12345", "AIzaSyTestKey1234567", "hmlan_abcdefghijklmnopqrstuvwxyz"} {
		if strings.Contains(clean, secret) {
			t.Fatalf("secret %q leaked in redacted text: %q", secret, clean)
		}
	}
	withExtra := RedactSecrets("falha com minha-chave-secreta", "minha-chave-secreta")
	if strings.Contains(withExtra, "minha-chave-secreta") {
		t.Fatalf("extra secret leaked: %q", withExtra)
	}
}

func TestPackagePurityNoSecretsNetworkShellOrIDE(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	forbiddenTokens := []string{
		"Listen",
		"api-keys.json",
		"credentials.json",
		"EnvOrStored",
	}
	forbiddenImports := []string{
		"/ide/",
		"net/http",
		"os/exec",
		"internal/modelx",
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(src)
		for _, tok := range forbiddenTokens {
			if strings.Contains(text, tok) {
				t.Fatalf("file %s must not reference %q", name, tok)
			}
		}
		f, err := parser.ParseFile(fset, name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbiddenImports {
				if strings.Contains(path, bad) {
					t.Fatalf("file %s must not import %q", name, path)
				}
			}
		}
		_ = ast.File{}
	}
}

func build2000ModelCatalog() Catalog {
	entries := make([]ModelEntry, 0, 2000)
	for i := 0; i < 2000; i++ {
		tier := TierCheap
		pMicro := 0.10 + float64(i%50)*0.01
		cMicro := 0.30 + float64(i%50)*0.02
		if i%3 == 1 {
			tier = TierMid
			pMicro = 1.5
			cMicro = 4.0
		} else if i%3 == 2 {
			tier = TierStrong
			pMicro = 15.0
			cMicro = 60.0
		}
		entries = append(entries, ModelEntry{
			Provider:           "openrouter",
			Model:              fmt.Sprintf("vendor-%d/model-%d", i%20, i),
			ContextLength:      128_000,
			PromptMicroUSD:     pMicro,
			CompletionMicroUSD: cMicro,
			Capabilities:       Capabilities{Tools: true, Vision: i%5 == 0, Stream: true},
			LatencyP50Ms:       int64(400 + i%1500),
			Quality:            0.55 + float64(i%40)*0.01,
			DataClass:          DataClassHTTPAPI,
			Tier:               tier,
			Source:             "openrouter",
		})
	}
	return Catalog{Version: 1, Entries: entries}
}

func TestRouteAndScoreLatency2000Models(t *testing.T) {
	cat := build2000ModelCatalog()
	pol := DefaultPolicy()
	pol.Enabled = true
	health := HealthSnapshot{
		Credentials:     map[string]bool{"openrouter": true},
		AgyRemainingPct: -1,
	}
	task := Task{
		Role:        team.RoleDeveloper,
		Class:       ClassCoding,
		Complexity:  ComplexityLow,
		InputTokens: 4000,
		NeedsTools:  true,
		RepoKey:     "github.com/exemplo/repo",
	}

	// Warmup to avoid first-call cold cache jitter during parallel package tests.
	for i := 0; i < 5; i++ {
		_, _ = Score(task, cat, pol, health, AgentPin{}, RepoPolicy{})
	}
	const runs = 500
	scoreTimes := make([]int64, 0, runs)
	for i := 0; i < runs; i++ {
		start := time.Now()
		choice, ref := Score(task, cat, pol, health, AgentPin{}, RepoPolicy{})
		elapsedUs := time.Since(start).Microseconds()
		if ref.Code != "" || choice.Model == "" {
			t.Fatalf("unexpected refusal: %+v", ref)
		}
		scoreTimes = append(scoreTimes, elapsedUs)
	}

	cs := &CatalogStore{
		catalog: cat,
		nowFn:   time.Now,
	}
	cs.rebuildIndexLocked()
	router := &Router{
		Catalog: cs,
		Policy:  pol,
		Health:  func() HealthSnapshot { return health },
	}
	routeTimes := make([]int64, 0, runs)
	for i := 0; i < runs; i++ {
		start := time.Now()
		choice, ref := router.Route(context.Background(), RouteInput{Task: task})
		elapsedUs := time.Since(start).Microseconds()
		if ref.Code != "" || choice.Model == "" {
			t.Fatalf("unexpected route refusal: %+v", ref)
		}
		routeTimes = append(routeTimes, elapsedUs)
	}
	sort.Slice(scoreTimes, func(i, j int) bool { return scoreTimes[i] < scoreTimes[j] })
	sort.Slice(routeTimes, func(i, j int) bool { return routeTimes[i] < routeTimes[j] })
	p99Idx := int(float64(runs)*0.99) - 1
	if p99Idx < 0 {
		p99Idx = 0
	}
	p99Score := scoreTimes[p99Idx]
	p99Route := routeTimes[p99Idx]
	// Spec §13: Score < 2ms (2000us) p99, Route < 5ms (5000us) p99.
	if p99Score > 2000 {
		t.Fatalf("Score p99 took %d us (> 2000 us limit) for 2000 models", p99Score)
	}
	if p99Route > 5000 {
		t.Fatalf("Route p99 took %d us (> 5000 us limit) for 2000 models", p99Route)
	}
	t.Logf("2000 models latency: Score p99=%d us (max=%d us), Route p99=%d us (max=%d us)", p99Score, scoreTimes[runs-1], p99Route, routeTimes[runs-1])
}

func BenchmarkScore2000(b *testing.B) {
	cat := build2000ModelCatalog()
	pol := DefaultPolicy()
	health := HealthSnapshot{
		Credentials:     map[string]bool{"openrouter": true},
		AgyRemainingPct: -1,
	}
	task := Task{
		Role:        team.RoleDeveloper,
		Class:       ClassCoding,
		Complexity:  ComplexityLow,
		InputTokens: 4000,
		NeedsTools:  true,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c, ref := Score(task, cat, pol, health, AgentPin{}, RepoPolicy{})
		if ref.Code != "" || c.Model == "" {
			b.Fatalf("unexpected refusal: %+v", ref)
		}
	}
}

func BenchmarkRoute2000(b *testing.B) {
	cat := build2000ModelCatalog()
	pol := DefaultPolicy()
	health := HealthSnapshot{
		Credentials:     map[string]bool{"openrouter": true},
		AgyRemainingPct: -1,
	}
	cs := &CatalogStore{
		catalog: cat,
		nowFn:   time.Now,
	}
	cs.rebuildIndexLocked()
	router := &Router{
		Catalog: cs,
		Policy:  pol,
		Health:  func() HealthSnapshot { return health },
	}
	in := RouteInput{
		Task: Task{
			Role:        team.RoleDeveloper,
			Class:       ClassCoding,
			Complexity:  ComplexityLow,
			InputTokens: 4000,
			NeedsTools:  true,
		},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c, ref := router.Route(context.Background(), in)
		if ref.Code != "" || c.Model == "" {
			b.Fatalf("unexpected refusal: %+v", ref)
		}
	}
}
