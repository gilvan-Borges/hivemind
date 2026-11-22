package jev

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestSanitizeDropsCursorVendorModelID(t *testing.T) {
	out := sanitizeEntries("cursor", []ModelEntry{
		{Provider: "cursor", Model: "auto"},
		{Provider: "cursor", Model: "composer-2.5"},
		{Provider: "cursor", Model: "openrouter/particle-ai/particle-1:free"},
		{Provider: "cursor", Model: "deepseek/deepseek-v4.1-flash"},
	}, time.Now())
	for _, e := range out {
		if strings.Contains(e.Model, "/") {
			t.Fatalf("id de outro provedor permaneceu no catálogo cursor: %s", e.Model)
		}
	}
	if len(out) != 2 {
		t.Fatalf("esperava auto e composer, veio %+v", out)
	}
}

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ErrorClass
	}{
		{"nil", nil, ErrClassNone},
		{"canceled", context.Canceled, ErrClassPolicy},
		{"deadline", context.DeadlineExceeded, ErrClassOutage},
		{"400 bad request", errors.New("HTTP 400 Bad Request: invalid_argument"), ErrClassPolicy},
		{"orphaned tool", errors.New("OpenAI API error: 400 Bad Request: missing tool_call_id"), ErrClassPolicy},
		{"cursor foreign model", errors.New("cursor/openrouter/particle-ai/particle-1:free: modelo incompatível com o provedor Cursor: OpenAI API error: 400 Bad Request: model is not a Cursor model"), ErrClassModel},
		{"stripped openai 400", errors.New("OpenAI API error: 400 Bad Request"), ErrClassPolicy},
		{"invalid model id", errors.New("OpenAI API error: 400 Bad Request: openrouter:deepseek/deepseek-v4.1-flash is not a valid model ID"), ErrClassModel},
		{"cursor stripped 400", errors.New("cursor/auto: o Cursor CLI terminou com erro: OpenAI API error: 400 Bad Request"), ErrClassModel},
		{"jail refusal", errors.New("caminho recusado fora do workspace"), ErrClassPolicy},
		{"402 quota", errors.New("OpenRouter respondeu 402: créditos insuficientes"), ErrClassQuota},
		{"agy resource exhausted", errors.New("agy esgotou a quota/créditos (429 RESOURCE_EXHAUSTED)"), ErrClassQuota},
		{"agy ai credits low", errors.New("Your AI credits balance is too low to continue."), ErrClassQuota},
		{"401 auth", errors.New("OpenRouter rejeitou a chave (HTTP 401 Unauthorized)"), ErrClassAuth},
		{"403 auth", errors.New("HTTP 403 forbidden"), ErrClassAuth},
		{"429 rate limit", errors.New("HTTP 429 Too Many Requests"), ErrClassRateLimit},
		{"503 outage", errors.New("HTTP 503 Service Unavailable"), ErrClassOutage},
		{"timeout", errors.New("agy excedeu o tempo (10m)"), ErrClassOutage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyError(tc.err); got != tc.want {
				t.Fatalf("ClassifyError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestHealthStoreCircuitLifecycleAndBackoff(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	hs := NewHealthStore(filepath.Join(t.TempDir(), "health.json"), CooldownConfig{
		Quota:     "15m",
		RateLimit: "2m",
		Outage:    "1m",
	})
	hs.nowFn = func() time.Time { return now }

	// 400 / policy error must NOT open circuit.
	hs.RecordFailure("openrouter", "deepseek/deepseek-chat", ErrClassPolicy, time.Time{})
	snap := hs.Snapshot(map[string]bool{"openrouter": true}, nil)
	if snap.Circuits["openrouter"].Circuit != CircuitClosed && snap.Circuits["openrouter"].Circuit != "" {
		t.Fatalf("policy error must not open provider circuit: %+v", snap.Circuits["openrouter"])
	}

	// 429 opens model only, NOT entire provider.
	scope, st := hs.RecordFailure("openrouter", "model-a", ErrClassRateLimit, time.Time{})
	if scope != "model" || st != CircuitOpen {
		t.Fatalf("expected model circuit open, got scope=%s state=%s", scope, st)
	}
	snap = hs.Snapshot(map[string]bool{"openrouter": true}, nil)
	if snap.ModelCircuits[catalogKey("openrouter", "model-a")].Circuit != CircuitOpen {
		t.Fatalf("model-a should be open")
	}
	if snap.Circuits["openrouter"].Circuit == CircuitOpen {
		t.Fatalf("429 on one model must not open whole openrouter provider")
	}

	// Advance past 2m cooldown -> transitions to half_open with 1 slot.
	now = now.Add(2*time.Minute + time.Second)
	if !hs.TryAcquireHalfOpen("openrouter", "model-a") {
		t.Fatal("first half_open slot should succeed")
	}
	if hs.TryAcquireHalfOpen("openrouter", "model-a") {
		t.Fatal("second concurrent half_open slot must be denied")
	}

	// Failure in half_open re-opens with exponential backoff (4m for failure #2).
	hs.RecordFailure("openrouter", "model-a", ErrClassRateLimit, time.Time{})
	snap = hs.Snapshot(map[string]bool{"openrouter": true}, nil)
	mc := snap.ModelCircuits[catalogKey("openrouter", "model-a")]
	if mc.Circuit != CircuitOpen || mc.Until.Sub(now) != 4*time.Minute {
		t.Fatalf("expected 4m backoff on second failure, got %+v", mc)
	}

	// Advance past 4m and succeed -> closes circuit.
	now = now.Add(4*time.Minute + time.Second)
	hs.RecordSuccess("openrouter", "model-a")
	snap = hs.Snapshot(map[string]bool{"openrouter": true}, nil)
	if snap.ModelCircuits[catalogKey("openrouter", "model-a")].Circuit != CircuitClosed {
		t.Fatalf("success must close circuit: %+v", snap.ModelCircuits[catalogKey("openrouter", "model-a")])
	}
}

func TestHealthStoreQuotaAuthOutageEscalationAndSpike(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	hs := NewHealthStore(filepath.Join(t.TempDir(), "health.json"), DefaultPolicy().Cooldown)
	hs.nowFn = func() time.Time { return now }

	// 402 quota opens entire provider.
	scope, _ := hs.RecordFailure("openrouter", "m1", ErrClassQuota, time.Time{})
	if scope != "provider" {
		t.Fatalf("quota must open whole provider, got %q", scope)
	}
	snap := hs.Snapshot(map[string]bool{"openrouter": true}, nil)
	if snap.Circuits["openrouter"].Circuit != CircuitOpen {
		t.Fatal("openrouter must be open after 402")
	}

	// 401 auth opens provider until credential fingerprint changes.
	hs.SyncCredentialFingerprints(map[string]string{"openai": "fp-old"})
	scope, _ = hs.RecordFailure("openai", "gpt-4.1", ErrClassAuth, time.Time{})
	if scope != "provider" {
		t.Fatalf("auth must open provider, got %q", scope)
	}
	hs.SyncCredentialFingerprints(map[string]string{"openai": "fp-old"})
	if hs.Snapshot(nil, nil).Circuits["openai"].Circuit != CircuitOpen {
		t.Fatal("unchanged key must keep openai auth circuit open")
	}
	hs.SyncCredentialFingerprints(map[string]string{"openai": "fp-new"})
	if hs.Snapshot(nil, nil).Circuits["openai"].Circuit != CircuitClosed {
		t.Fatal("updated key fingerprint must reset openai auth circuit")
	}

	// 5 consecutive outages on a provider escalate to provider-wide open.
	for i := 0; i < 4; i++ {
		sc, _ := hs.RecordFailure("gemini", "gemini-flash-latest", ErrClassOutage, time.Time{})
		if sc != "model" {
			t.Fatalf("outage #%d should be model-scoped, got %s", i+1, sc)
		}
	}
	sc, _ := hs.RecordFailure("gemini", "gemini-flash-latest", ErrClassOutage, time.Time{})
	if sc != "provider" {
		t.Fatalf("5th consecutive outage must open provider, got %s", sc)
	}

	// Spike detection: >50% failure in last 10 calls within 2 min with >=4 failures.
	hs.RecordSuccess("opencode", "m-spike")
	hs.RecordSuccess("opencode", "m-spike")
	for i := 0; i < 4; i++ {
		hs.RecordFailure("opencode", "m-spike", ErrClassOutage, time.Time{})
	}
	mr := hs.Snapshot(nil, nil).ModelCircuits[catalogKey("opencode", "m-spike")]
	if mr.Circuit != CircuitOpen {
		t.Fatalf("expected spike to open model circuit, got %+v", mr)
	}
}

func TestHealthStoreQuotaProbeAndDailyRollover(t *testing.T) {
	now := time.Date(2026, 11, 20, 23, 55, 0, 0, time.UTC)
	hs := NewHealthStore(filepath.Join(t.TempDir(), "health.json"), DefaultPolicy().Cooldown)
	hs.nowFn = func() time.Time { return now }

	// Quota probe with 4% remaining opens agy quota circuit.
	hs.UpdateQuotaProbe("agy", 4.0, now.Add(30*time.Minute), nil)
	snap := hs.Snapshot(map[string]bool{"agy": true}, nil)
	if snap.Circuits["agy"].Circuit != CircuitOpen || snap.AgyRemainingPct != 4.0 {
		t.Fatalf("expected agy open at 4%% quota, got %+v", snap.Circuits["agy"])
	}

	// Failed probe must NOT mark outage; keeps last quota with QuotaStale = true.
	hs.UpdateQuotaProbe("agy", -1, time.Time{}, errors.New("cli timeout"))
	sum := hs.Summary(DefaultPolicy(), map[string]bool{"agy": true})
	var agyView ProviderHealthView
	for _, v := range sum {
		if v.Provider == "agy" {
			agyView = v
		}
	}
	if !agyView.QuotaStale || agyView.Reason != string(ErrClassQuota) {
		t.Fatalf("probe failure must set stale without overwriting reason with outage: %+v", agyView)
	}

	// Daily spend resets at UTC midnight.
	hs.RecordSpend("openrouter", 500_000)
	if got := hs.Snapshot(nil, nil).DailySpentMicroUSD["openrouter"]; got != 500_000 {
		t.Fatalf("expected 500000 microUSD, got %d", got)
	}
	now = now.Add(10 * time.Minute) // 00:05 UTC next day
	if got := hs.Snapshot(nil, nil).DailySpentMicroUSD["openrouter"]; got != 0 {
		t.Fatalf("expected UTC midnight rollover to reset spend, got %d", got)
	}
}

func TestScoreExcludesCursorMidStepAndOpenModelCircuits(t *testing.T) {
	cat := DefaultStaticCatalog()
	pol := DefaultPolicy()
	health := HealthSnapshot{
		Credentials: map[string]bool{"cursor": true, "gemini": true, "agy": true},
		ModelCircuits: map[string]ProviderHealth{
			catalogKey("gemini", "gemini-flash-latest"): {Circuit: CircuitOpen},
		},
		AgyRemainingPct: -1,
	}
	// InStep = true must exclude cursor (WorkspaceAgent) even if it's cheap & healthy.
	task := Task{
		Role:        team.RoleDeveloper,
		Class:       ClassCoding,
		Complexity:  ComplexityLow,
		InputTokens: 2000,
		NeedsTools:  true,
		InStep:      true,
	}
	choice, ref := Score(task, cat, pol, health, AgentPin{}, RepoPolicy{})
	if ref.Code != "" {
		t.Fatalf("unexpected refusal: %+v", ref)
	}
	if choice.Provider != "agy" {
		t.Fatalf("expected agy when gemini model circuit is open and cursor is blocked InStep, got %+v", choice)
	}
}

func TestHealthStoreModelQuotaAndAgyFamilyIsolation(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	hs := NewHealthStore(filepath.Join(t.TempDir(), "health.json"), CooldownConfig{
		Quota:     "15m",
		RateLimit: "2m",
		Outage:    "1m",
	})
	hs.nowFn = func() time.Time { return now }

	scope, st := hs.RecordFailure("agy", "agy:claude-opus-5-5-medium", ErrClassModelQuota, time.Time{})
	if scope != "model" || st != CircuitOpen {
		t.Fatalf("expected model circuit open for ErrClassModelQuota, got scope=%q state=%q", scope, st)
	}

	snap := hs.Snapshot(map[string]bool{"agy": true}, nil)
	if snap.Circuits["agy"].Circuit == CircuitOpen {
		t.Fatalf("ErrClassModelQuota must NOT open whole agy provider: %+v", snap.Circuits["agy"])
	}
	mc := snap.ModelCircuits[catalogKey("agy", "agy:claude-opus-5-5-medium")]
	if mc.Circuit != CircuitOpen || mc.Until.Sub(now) != 15*time.Minute {
		t.Fatalf("expected 15m quota cooldown on exact model, got %+v", mc)
	}
	if isPairCircuitEligible("agy", "agy:claude-opus-5-5-high", snap) {
		t.Fatal("sibling effort variant agy:claude-opus-5-5-high must be ineligible")
	}
	if !isPairCircuitEligible("agy", "agy:gemini-4-neon-high", snap) {
		t.Fatal("unrelated agy:gemini-4-neon-high must remain eligible")
	}
	if !isPairCircuitEligible("agy", "agy", snap) {
		t.Fatal("base agy route must remain eligible")
	}

	cat := Catalog{
		Version: 1,
		Entries: []ModelEntry{
			{Provider: "agy", Model: "agy:claude-opus-5-5-medium", ContextLength: 200_000, Quality: 0.99, Capabilities: Capabilities{Tools: true}, DataClass: DataClassCLIAccount, Tier: TierMid},
			{Provider: "agy", Model: "agy:claude-opus-5-5-high", ContextLength: 200_000, Quality: 0.97, Capabilities: Capabilities{Tools: true}, DataClass: DataClassCLIAccount, Tier: TierMid},
			{Provider: "agy", Model: "agy:gemini-4-neon-high", ContextLength: 1_000_000, Quality: 0.91, Capabilities: Capabilities{Tools: true}, DataClass: DataClassCLIAccount, Tier: TierMid},
		},
	}
	choice, ref := Score(Task{
		Role: team.RoleDeveloper, Class: ClassCoding, Complexity: ComplexityMedium, InputTokens: 2000, NeedsTools: true,
	}, cat, DefaultPolicy(), snap, AgentPin{}, RepoPolicy{})
	if ref.Code != "" {
		t.Fatalf("unexpected score refusal: %+v", ref)
	}
	if choice.Model != "agy:gemini-4-neon-high" {
		t.Fatalf("expected Score to skip claude sibling family and choose agy:gemini-4-neon-high, got %+v", choice)
	}

	hs.RecordFailure("agy", "__family__:claude", ErrClassModelQuota, time.Time{})
	snap = hs.Snapshot(map[string]bool{"agy": true}, nil)
	if isPairCircuitEligible("agy", "agy:claude-sonnet-4-6-high", snap) {
		t.Fatal("claude credits family must block other claude variants on agy")
	}
	if !isPairCircuitEligible("agy", "agy:gemini-4-neon-high", snap) {
		t.Fatal("claude credits family must NOT block gemini models on agy")
	}

	now = now.Add(15*time.Minute + time.Second)
	if !hs.TryAcquireHalfOpen("agy", "agy:claude-opus-5-5-high") {
		t.Fatal("first half_open probe after 15m quota window should succeed")
	}
	if hs.TryAcquireHalfOpen("agy", "agy:claude-opus-5-5-high") {
		t.Fatal("concurrent second half_open probe on same family must be blocked")
	}
	hs.ReleaseHalfOpen("agy", "agy:claude-opus-5-5-high")
	if !hs.TryAcquireHalfOpen("agy", "agy:claude-opus-5-5-high") {
		t.Fatal("half_open probe slot should reopen after ReleaseHalfOpen")
	}

	hs.RecordSuccess("agy", "agy:claude-opus-5-5-high")
	snap = hs.Snapshot(map[string]bool{"agy": true}, nil)
	if !isPairCircuitEligible("agy", "agy:claude-opus-5-5-high", snap) || !isPairCircuitEligible("agy", "agy:claude-sonnet-4-6-high", snap) {
		t.Fatalf("RecordSuccess must close family circuits too: %+v", snap.ModelCircuits)
	}
}
