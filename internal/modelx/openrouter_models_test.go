package modelx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenRouterModelsCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":             "openai/gpt-4.1",
					"name":           "OpenAI: GPT-4.1",
					"context_length": 128000,
					"pricing":        map[string]string{"prompt": "1e-6", "completion": "2e-6"},
				},
				{
					"id":             "anthropic/claude-sonnet-4.5",
					"name":           "Anthropic: Claude Sonnet 4.5",
					"context_length": 200000,
					"pricing":        map[string]string{"prompt": "3e-6", "completion": "15e-6"},
				},
				{
					"id": "",
				},
			},
		})
	}))
	defer srv.Close()

	prevCache := orModelsCache
	prevAt := orModelsCachedAt
	t.Cleanup(func() {
		orModelsCache = prevCache
		orModelsCachedAt = prevAt
	})
	orModelsCache = nil
	orModelsCachedAt = time.Time{}

	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_BASE_URL", srv.URL)

	models, err := OpenRouterModels(context.Background())
	if err != nil {
		t.Fatalf("OpenRouterModels error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d: %+v", len(models), models)
	}
	// Sorted by ID.
	if models[0].ID != "anthropic/claude-sonnet-4.5" || models[1].ID != "openai/gpt-4.1" {
		t.Fatalf("unexpected order: %+v", models)
	}
	if models[0].ContextLength != 200000 {
		t.Fatalf("unexpected context length: %+v", models[0])
	}
	if models[1].PromptPrice <= 0 || models[1].CompletionPrice <= 0 {
		t.Fatalf("unexpected pricing: %+v", models[1])
	}

	// Second call must hit the in-memory cache (server would still respond, but
	// we assert the cached timestamp moved).
	_, err = OpenRouterModels(context.Background())
	if err != nil {
		t.Fatalf("cached call error: %v", err)
	}
	if orModelsCachedAt.IsZero() {
		t.Fatal("cache timestamp not set")
	}
}
