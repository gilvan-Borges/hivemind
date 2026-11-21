package modelx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/jev"
)

func TestJevOpenRouterCatalogFetchAndPreserveOnHTTP500(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if status != http.StatusOK {
			http.Error(w, "upstream error", status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":                   "deepseek/deepseek-chat",
					"name":                 "DeepSeek Marketing Description Ignore Me",
					"context_length":       128000,
					"supported_parameters": []string{"tools", "temperature"},
					"architecture":         map[string]any{"modality": "text->text", "input_modalities": []string{"text"}},
					"pricing":              map[string]string{"prompt": "0.00000014", "completion": "0.00000028"},
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

	t.Setenv("OPENROUTER_BASE_URL", srv.URL)

	catPath := filepath.Join(t.TempDir(), "catalog.json")
	cs := jev.NewCatalogStore(catPath, map[string]jev.SourceFetcher{
		"openrouter": fetchJevOpenRouter,
	})
	cs.RefreshNow(context.Background())

	snap := cs.Snapshot()
	entry, ok := snap.Lookup("openrouter", "deepseek/deepseek-chat")
	if !ok {
		t.Fatal("expected deepseek/deepseek-chat in Jev catalog")
	}
	if entry.ContextLength != 128000 || !entry.Capabilities.Tools || entry.PromptMicroUSD <= 0 {
		t.Fatalf("unexpected entry fields: %+v", entry)
	}
	if _, ok := snap.Lookup("openrouter", "deepseek/deepseek-v4.1-flash"); ok {
		t.Fatal("id que /models não devolveu não pode ser candidato")
	}

	// Simulate TTL expiry and HTTP 500 from OpenRouter: previous snapshot must remain intact.
	status = http.StatusInternalServerError
	orModelsCache = nil
	orModelsCachedAt = time.Time{}
	_, err := fetchJevOpenRouter(context.Background())
	if err == nil {
		t.Fatal("expected error on HTTP 500")
	}
	cs.RefreshNow(context.Background())
	if _, ok := cs.Snapshot().Lookup("openrouter", "deepseek/deepseek-chat"); !ok {
		t.Fatal("HTTP 500 must preserve previous snapshot")
	}

	// Verify failed custom fetcher preserves snapshot too.
	csFail := jev.NewCatalogStore(catPath, map[string]jev.SourceFetcher{
		"openrouter": func(context.Context) ([]jev.ModelEntry, error) {
			return nil, errors.New("boom")
		},
	})
	csFail.RefreshNow(context.Background())
	if _, ok := csFail.Snapshot().Lookup("openrouter", "deepseek/deepseek-chat"); !ok {
		t.Fatal("failed refresh must preserve persisted catalog.json snapshot")
	}
}
