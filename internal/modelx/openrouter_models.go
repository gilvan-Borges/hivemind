package modelx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// OpenRouterModel is one entry of the OpenRouter public model catalog.
type OpenRouterModel struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	ContextLength   int64   `json:"contextLength,omitempty"`
	PromptPrice     float64 `json:"promptPrice,omitempty"`
	CompletionPrice float64 `json:"completionPrice,omitempty"`
}

const openRouterModelsTTL = 30 * time.Minute

var (
	orModelsMu       sync.Mutex
	orModelsCache    []OpenRouterModel
	orModelsCachedAt time.Time
)

// OpenRouterModels lists every model available on OpenRouter (GET /models).
// The catalog is public and does not require the API key. Results are cached
// in memory for openRouterModelsTTL to avoid hammering the gateway.
func OpenRouterModels(ctx context.Context) ([]OpenRouterModel, error) {
	base := EnvOrStored("OPENROUTER_BASE_URL")
	if base == "" {
		base = "https://openrouter.ai/api/v1"
	}

	orModelsMu.Lock()
	if len(orModelsCache) > 0 && time.Since(orModelsCachedAt) < openRouterModelsTTL {
		out := append([]OpenRouterModel(nil), orModelsCache...)
		orModelsMu.Unlock()
		return out, nil
	}
	orModelsMu.Unlock()

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	url := strings.TrimRight(base, "/") + "/models"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 18 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("buscar catálogo OpenRouter em %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catálogo OpenRouter respondeu HTTP %d", resp.StatusCode)
	}
	var raw struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int64  `json:"context_length"`
			Pricing       struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decodificar catálogo OpenRouter: %w", err)
	}
	out := make([]OpenRouterModel, 0, len(raw.Data))
	for _, m := range raw.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		out = append(out, OpenRouterModel{
			ID:              id,
			Name:            strings.TrimSpace(m.Name),
			ContextLength:   m.ContextLength,
			PromptPrice:     parseORPrice(m.Pricing.Prompt),
			CompletionPrice: parseORPrice(m.Pricing.Completion),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	orModelsMu.Lock()
	orModelsCache = out
	orModelsCachedAt = time.Now()
	orModelsMu.Unlock()
	return out, nil
}

func parseORPrice(raw string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0
	}
	return f
}
