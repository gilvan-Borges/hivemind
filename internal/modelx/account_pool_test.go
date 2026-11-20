package modelx

import (
	"context"
	"fmt"
	"iter"
	"path/filepath"
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
