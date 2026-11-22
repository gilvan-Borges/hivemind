package modelx

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

type fakeAccountLLM struct {
	accountID string
	modelName string
	failErr   error
}

func (f *fakeAccountLLM) Name() string {
	return "agy:" + f.modelName
}

func (f *fakeAccountLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if f.failErr != nil {
			yield(nil, f.failErr)
			return
		}
		yield(&model.LLMResponse{
			Content: &genai.Content{
				Role:  genai.RoleModel,
				Parts: []*genai.Part{{Text: fmt.Sprintf("ok:%s:%s", f.accountID, f.modelName)}},
			},
		}, nil)
	}
}

func TestPerAccountPerModelQuotaMatrixAndFailover(t *testing.T) {
	// Isolate account pool state for test.
	accountPoolMu.Lock()
	accountPoolLoaded = true
	accountPoolState = accountPoolHealthFile{
		Version: 1,
		Slots:   map[string]accountModelSlotState{},
	}
	accountPoolMu.Unlock()

	origProfilesPathFn := profilesFilePathFn
	tmpDir := t.TempDir()
	profilesFilePathFn = func() (string, error) {
		return filepath.Join(tmpDir, "provider-profiles.json"), nil
	}
	defer func() { profilesFilePathFn = origProfilesPathFn }()

	pool := []ProviderProfile{
		{ID: "agy-1", Provider: "agy", Name: "Conta 1 (Empresa)", IsDefault: true},
		{ID: "agy-2", Provider: "agy", Name: "Conta 2 (Particular)"},
		{ID: "agy-3", Provider: "agy", Name: "Conta 3 (Reserva)"},
	}

	// Simulate that Conta 1 has exhausted quota for "argon", while "gemini-4-neon-high" is still healthy on Conta 1.
	buildRaw := func(ctx context.Context, provider, modelName string) (model.LLM, error) {
		accID := profileOverrideFromContext(ctx, provider)
		bucket := NormalizeModelQuotaBucket(provider, modelName)
		if accID == "agy-1" && bucket == "argon" {
			return &fakeAccountLLM{
				accountID: accID,
				modelName: bucket,
				failErr:   fmt.Errorf("agy: resource_exhausted: 429 quota exceeded for model argon"),
			}, nil
		}
		return &fakeAccountLLM{
			accountID: accID,
			modelName: bucket,
		}, nil
	}

	// 1. Agent A requests "agy:argon". It starts on Conta 1 ("agy-1"), hits 429 quota on "argon",
	// and must transparently fail over "argon" to Conta 2 ("agy-2") on the same call!
	argonModel := &pooledAccountLLM{
		baseCtx:     context.Background(),
		provider:    "agy",
		rawProvider: "agy",
		modelName:   "agy:argon",
		pool:        pool,
		buildRaw:    buildRaw,
		activeIdx:   -1,
	}

	var gotArgon string
	for resp, err := range argonModel.GenerateContent(context.Background(), &model.LLMRequest{}, false) {
		if err != nil {
			t.Fatalf("expected transparent failover from agy-1 to agy-2 for argon, got error: %v", err)
		}
		if resp != nil && resp.Content != nil && len(resp.Content.Parts) > 0 {
			gotArgon = resp.Content.Parts[0].Text
		}
	}
	if gotArgon != "ok:agy-2:argon" {
		t.Fatalf("expected argon to fail over to Conta 2 (agy-2), got %q", gotArgon)
	}

	// Verify (agy, agy-1, argon) is now marked unhealthy, while (agy, agy-1, gemini-4-neon-high) is STILL healthy!
	if IsAccountModelSlotHealthy("agy", "agy-1", "agy:argon") {
		t.Fatalf("expected (agy-1, argon) slot to be in cooldown after 429 quota error")
	}
	if !IsAccountModelSlotHealthy("agy", "agy-1", "agy:gemini-4-neon-high") {
		t.Fatalf("expected (agy-1, gemini-4-neon-high) to remain healthy on Conta 1!")
	}

	// 2. Agent B requests "agy:gemini-4-neon-high". Because gemini-4-neon-high still has quota on Conta 1,
	// it MUST stay on Conta 1 ("agy-1")!
	geminiModel := &pooledAccountLLM{
		baseCtx:     context.Background(),
		provider:    "agy",
		rawProvider: "agy",
		modelName:   "agy:gemini-4-neon-high",
		pool:        pool,
		buildRaw:    buildRaw,
		activeIdx:   -1,
	}
	var gotGemini string
	for resp, err := range geminiModel.GenerateContent(context.Background(), &model.LLMRequest{}, false) {
		if err != nil {
			t.Fatalf("unexpected error on geminiModel: %v", err)
		}
		if resp != nil && resp.Content != nil && len(resp.Content.Parts) > 0 {
			gotGemini = resp.Content.Parts[0].Text
		}
	}
	if gotGemini != "ok:agy-1:gemini-4-neon-high" {
		t.Fatalf("expected gemini-4-neon-high to stay on Conta 1 (agy-1), got %q", gotGemini)
	}
}

func TestAccountPoolNeverCrossesOrganizationBoundaries(t *testing.T) {
	tmp := t.TempDir()
	prev := profilesFilePathFn
	profilesFilePathFn = func() (string, error) {
		return filepath.Join(tmp, "provider-profiles.json"), nil
	}
	t.Cleanup(func() { profilesFilePathFn = prev })

	wsAlpha := filepath.Join(tmp, "org-alpha-web")
	wsAcme := filepath.Join(tmp, "acme-api")
	for _, pair := range [][2]string{
		{wsAlpha, "git@github.com:org-alpha/web.git"},
		{wsAcme, "git@github.com:acme-corp/api.git"},
	} {
		gitDir := filepath.Join(pair[0], ".git")
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[remote \"origin\"]\n\turl = "+pair[1]+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	profiles := []SaveProfileRequest{
		{
			ID: "or-alpha-1", Provider: "openrouter", Name: "Org Alpha 1",
			OrgPatterns: []string{"org-alpha"}, Env: map[string]string{"OPENROUTER_API_KEY": "sk-alpha-1"},
		},
		{
			ID: "or-alpha-2", Provider: "openrouter", Name: "Org Alpha 2",
			OrgPatterns: []string{"org-alpha"}, Env: map[string]string{"OPENROUTER_API_KEY": "sk-alpha-2"},
		},
		{
			ID: "or-acme", Provider: "openrouter", Name: "Acme",
			OrgPatterns: []string{"acme-corp"}, Env: map[string]string{"OPENROUTER_API_KEY": "sk-acme"},
		},
		{
			ID: "or-personal", Provider: "openrouter", Name: "Pessoal",
			IsDefault: true, Env: map[string]string{"OPENROUTER_API_KEY": "sk-personal"},
		},
	}
	for _, req := range profiles {
		if _, err := SaveProviderProfile(req); err != nil {
			t.Fatal(err)
		}
	}

	a1Pool := ListAccountPoolForProvider(WithWorkspace(context.Background(), wsAlpha), "openrouter", "")
	var alphaIDs []string
	for _, p := range a1Pool {
		alphaIDs = append(alphaIDs, p.ID)
	}
	if strings.Join(alphaIDs, ",") != "or-alpha-1,or-alpha-2" {
		t.Fatalf("org-alpha pool leaked external accounts: %v", alphaIDs)
	}

	aPool := ListAccountPoolForProvider(WithWorkspace(context.Background(), wsAcme), "openrouter", "")
	if len(aPool) != 1 || aPool[0].ID != "or-acme" {
		t.Fatalf("acme pool leaked external accounts: %+v", aPool)
	}
}

func TestAccountPoolAgyQuotaCooldownAndEffortFamily(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("AppData", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)

	accountPoolMu.Lock()
	accountPoolLoaded = true
	accountPoolState = accountPoolHealthFile{
		Version: 1,
		Slots:   map[string]accountModelSlotState{},
	}
	accountPoolMu.Unlock()

	errMsg := errors.New("agy esgotou a quota/créditos (429 RESOURCE_EXHAUSTED). Your AI credits balance is too low to continue.")
	bucket, cooldown, shouldFailover := RecordAccountModelFailure("agy", "acc-alpha", "Conta Alpha", "agy:claude-opus-5-5-medium", errMsg)
	if !shouldFailover {
		t.Fatal("expected shouldFailover=true on AGY credits exhaustion")
	}
	if bucket != "claude-opus-5-5-medium" {
		t.Fatalf("expected bucket claude-opus-5-5-medium, got %q", bucket)
	}
	if cooldown != defaultAccountModelQuotaCooldown {
		t.Fatalf("expected 15m quota cooldown even when 429 is in text, got %v", cooldown)
	}

	if IsAccountModelSlotHealthy("agy", "acc-alpha", "agy:claude-opus-5-5-medium") {
		t.Fatal("exact exhausted model must be unhealthy")
	}
	if IsAccountModelSlotHealthy("agy", "acc-alpha", "agy:claude-opus-5-5-high") {
		t.Fatal("sibling effort variant claude-opus-5-5-high must be unhealthy on acc-alpha")
	}
	if IsAccountModelSlotHealthy("agy", "acc-alpha", "agy:claude-sonnet-4-6-high") {
		t.Fatal("claude credits family must block claude-sonnet-4-6-high on acc-alpha")
	}
	if !IsAccountModelSlotHealthy("agy", "acc-alpha", "agy:gemini-4-neon-high") {
		t.Fatal("gemini-4-neon-high on acc-alpha must remain healthy")
	}
	if !IsAccountModelSlotHealthy("agy", "acc-beta", "agy:claude-opus-5-5-high") {
		t.Fatal("acc-beta must remain healthy")
	}

	_, rateCooldown, ok := RecordAccountModelFailure("agy", "acc-beta", "Conta Beta", "agy:gemini-4-neon-high", errors.New("HTTP 429 Too Many Requests"))
	if !ok || rateCooldown != defaultAccountModelRateCooldown {
		t.Fatalf("expected 2m rate-limit cooldown for pure 429, got ok=%v cooldown=%v", ok, rateCooldown)
	}
	if IsAccountModelSlotHealthy("agy", "acc-beta", "agy:gemini-4-neon-high") {
		t.Fatal("acc-beta gemini-4-neon-high must be in cooldown")
	}
	if !IsAccountModelSlotHealthy("agy", "acc-beta", "agy:gemini-4-neon-low") {
		t.Fatal("pure rate-limit must stay scoped to exact bucket without blocking sibling effort")
	}
}
