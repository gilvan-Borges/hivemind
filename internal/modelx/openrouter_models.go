package modelx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/jev"
)

// OpenRouterModel is one entry of the OpenRouter public model catalog.
type OpenRouterModel struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	ContextLength   int64   `json:"contextLength,omitempty"`
	PromptPrice     float64 `json:"promptPrice,omitempty"`
	CompletionPrice float64 `json:"completionPrice,omitempty"`
	DiscountPct     int     `json:"discountPct,omitempty"`
	DetailsPath     string  `json:"-"`
	SupportsTools   bool    `json:"supportsTools,omitempty"`
	SupportsVision  bool    `json:"supportsVision,omitempty"`
}

const openRouterModelsTTL = 30 * time.Minute

var (
	orModelsMu        sync.Mutex
	orModelsCache     []OpenRouterModel
	orModelsCachedAt  time.Time
	orBalanceMu       sync.Mutex
	orBalanceUSD      float64
	orBalanceKnown    bool
	orBalanceCachedAt time.Time
)

const openRouterBalanceTTL = 2 * time.Minute

// GetOpenRouterBalanceUSD queries OpenRouter's /credits (and /key) endpoint to check
// the remaining account balance in USD, caching the result for 2 minutes.
// When known is true and balanceUSD <= 1.00, Jev preserves that $1.00 reserve and
// routes work to other active providers (agy, cursor, opencode, gemini).
func GetOpenRouterBalanceUSD(ctx context.Context) (balanceUSD float64, known bool) {
	key := strings.TrimSpace(ResolveEnv(ctx, "openrouter", "OPENROUTER_API_KEY"))
	if key == "" {
		return 0, false
	}
	orBalanceMu.Lock()
	if orBalanceKnown && time.Since(orBalanceCachedAt) < openRouterBalanceTTL {
		b, k := orBalanceUSD, orBalanceKnown
		orBalanceMu.Unlock()
		return b, k
	}
	orBalanceMu.Unlock()

	base := strings.TrimSpace(ResolveEnv(ctx, "openrouter", "OPENROUTER_BASE_URL"))
	if base == "" {
		base = "https://openrouter.ai/api/v1"
	}
	base = strings.TrimRight(base, "/")

	reqCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 4 * time.Second}

	// 1. Check GET /credits (account-wide total_credits - total_usage).
	if req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base+"/credits", nil); err == nil {
		req.Header.Set("Authorization", "Bearer "+key)
		if resp, err := client.Do(req); err == nil {
			var credRaw struct {
				Data struct {
					TotalCredits *float64 `json:"total_credits"`
					TotalUsage   *float64 `json:"total_usage"`
				} `json:"data"`
			}
			decErr := json.NewDecoder(resp.Body).Decode(&credRaw)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && decErr == nil && credRaw.Data.TotalCredits != nil {
				used := 0.0
				if credRaw.Data.TotalUsage != nil {
					used = *credRaw.Data.TotalUsage
				}
				rem := *credRaw.Data.TotalCredits - used
				if rem < 0 {
					rem = 0
				}
				orBalanceMu.Lock()
				orBalanceUSD = rem
				orBalanceKnown = true
				orBalanceCachedAt = time.Now()
				orBalanceMu.Unlock()
				return rem, true
			}
		}
	}

	// 2. Fallback to GET /key (key-scoped limit_remaining).
	if req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base+"/key", nil); err == nil {
		req.Header.Set("Authorization", "Bearer "+key)
		if resp, err := client.Do(req); err == nil {
			var keyRaw struct {
				Data struct {
					LimitRemaining *float64 `json:"limit_remaining"`
				} `json:"data"`
			}
			decErr := json.NewDecoder(resp.Body).Decode(&keyRaw)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && decErr == nil && keyRaw.Data.LimitRemaining != nil {
				rem := *keyRaw.Data.LimitRemaining
				if rem < 0 {
					rem = 0
				}
				orBalanceMu.Lock()
				orBalanceUSD = rem
				orBalanceKnown = true
				orBalanceCachedAt = time.Now()
				orBalanceMu.Unlock()
				return rem, true
			}
		}
	}

	return 0, false
}

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
			ID                  string   `json:"id"`
			Name                string   `json:"name"`
			ContextLength       int64    `json:"context_length"`
			SupportedParameters []string `json:"supported_parameters"`
			Architecture        struct {
				Modality        string   `json:"modality"`
				InputModalities []string `json:"input_modalities"`
			} `json:"architecture"`
			Pricing struct {
				Prompt     string  `json:"prompt"`
				Completion string  `json:"completion"`
				Discount   float64 `json:"discount"`
			} `json:"pricing"`
			Links struct {
				Details string `json:"details"`
			} `json:"links"`
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
		supportsTools := len(m.SupportedParameters) == 0
		for _, p := range m.SupportedParameters {
			if strings.EqualFold(strings.TrimSpace(p), "tools") {
				supportsTools = true
				break
			}
		}
		supportsVision := strings.Contains(strings.ToLower(m.Architecture.Modality), "image")
		for _, mod := range m.Architecture.InputModalities {
			if strings.EqualFold(strings.TrimSpace(mod), "image") {
				supportsVision = true
				break
			}
		}
		discPct := 0
		if m.Pricing.Discount > 0 && m.Pricing.Discount <= 1.0 {
			discPct = int(m.Pricing.Discount*100 + 0.5)
		} else if m.Pricing.Discount > 1.0 && m.Pricing.Discount <= 100.0 {
			discPct = int(m.Pricing.Discount + 0.5)
		}
		out = append(out, OpenRouterModel{
			ID:              id,
			Name:            strings.TrimSpace(m.Name),
			ContextLength:   m.ContextLength,
			PromptPrice:     parseORPrice(m.Pricing.Prompt),
			CompletionPrice: parseORPrice(m.Pricing.Completion),
			DiscountPct:     discPct,
			DetailsPath:     strings.TrimSpace(m.Links.Details),
			SupportsTools:   supportsTools,
			SupportsVision:  supportsVision,
		})
	}
	enrichOpenRouterDailyDiscounts(reqCtx, base, out)
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

type orDiscountItem struct {
	DiscountPct     int     `json:"discountPct"`
	PromptPrice     float64 `json:"promptPrice,omitempty"`
	CompletionPrice float64 `json:"completionPrice,omitempty"`
}

type orDailyDiscountFile struct {
	UpdatedAt time.Time                 `json:"updatedAt"`
	Discounts map[string]orDiscountItem `json:"discounts"`
}

func orDailyDiscountCachePath() (string, error) {
	dir, err := jev.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "openrouter-discounts.json"), nil
}

// enrichOpenRouterDailyDiscounts checks OpenRouter's per-model endpoints API once per day
// for the curated catalog models to detect active daily promotions (e.g. Google Carbon -50%)
// and applies the discount percentage + promotional pricing.
func enrichOpenRouterDailyDiscounts(ctx context.Context, baseURL string, models []OpenRouterModel) {
	if len(models) == 0 {
		return
	}
	cachePath, _ := orDailyDiscountCachePath()
	var cached orDailyDiscountFile
	cacheFresh := false
	if cachePath != "" {
		if data, err := os.ReadFile(cachePath); err == nil {
			if json.Unmarshal(data, &cached) == nil && len(cached.Discounts) > 0 && time.Since(cached.UpdatedAt) < 12*time.Hour {
				cacheFresh = true
			}
		}
	}

	if !cacheFresh {
		// Probe OpenRouter endpoints for curated models to detect active daily discounts.
		type probeTarget struct {
			id          string
			detailsPath string
		}
		var targets []probeTarget
		for _, m := range models {
			if _, _, _, _, isCurated := jev.CuratedModelMeta("openrouter", m.ID); isCurated && m.DetailsPath != "" {
				targets = append(targets, probeTarget{id: m.ID, detailsPath: m.DetailsPath})
			}
		}
		if len(targets) > 0 {
			probeCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()
			origin := "https://openrouter.ai"
			if strings.HasPrefix(baseURL, "http") {
				if idx := strings.Index(baseURL, "/api/"); idx > 0 {
					origin = baseURL[:idx]
				}
			}
			var mu sync.Mutex
			discovered := make(map[string]orDiscountItem, len(targets))
			sem := make(chan struct{}, 6)
			var wg sync.WaitGroup
			client := &http.Client{Timeout: 5 * time.Second}

			for _, tgt := range targets {
				wg.Add(1)
				go func(t probeTarget) {
					defer wg.Done()
					select {
					case sem <- struct{}{}:
						defer func() { <-sem }()
					case <-probeCtx.Done():
						return
					}
					epURL := origin + t.detailsPath
					if strings.HasPrefix(t.detailsPath, "http") {
						epURL = t.detailsPath
					}
					req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, epURL, nil)
					if err != nil {
						return
					}
					resp, err := client.Do(req)
					if err != nil {
						return
					}
					defer resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						return
					}
					var payload struct {
						Data struct {
							Endpoints []struct {
								Pricing struct {
									Prompt     string  `json:"prompt"`
									Completion string  `json:"completion"`
									Discount   float64 `json:"discount"`
								} `json:"pricing"`
							} `json:"endpoints"`
						} `json:"data"`
					}
					if json.NewDecoder(resp.Body).Decode(&payload) != nil {
						return
					}
					bestPct := 0
					var bestPrompt, bestComp float64
					for _, ep := range payload.Data.Endpoints {
						d := ep.Pricing.Discount
						pct := 0
						if d > 0 && d <= 1.0 {
							pct = int(d*100 + 0.5)
						} else if d > 1.0 && d <= 100.0 {
							pct = int(d + 0.5)
						}
						if pct > bestPct {
							bestPct = pct
							bestPrompt = parseORPrice(ep.Pricing.Prompt)
							bestComp = parseORPrice(ep.Pricing.Completion)
						}
					}
					if bestPct > 0 {
						mu.Lock()
						discovered[strings.ToLower(t.id)] = orDiscountItem{
							DiscountPct:     bestPct,
							PromptPrice:     bestPrompt,
							CompletionPrice: bestComp,
						}
						mu.Unlock()
					}
				}(tgt)
			}
			wg.Wait()
			if len(discovered) > 0 {
				cached = orDailyDiscountFile{
					UpdatedAt: time.Now().UTC(),
					Discounts: discovered,
				}
				if cachePath != "" {
					_ = os.MkdirAll(filepath.Dir(cachePath), 0o700)
					if rawBytes, err := json.MarshalIndent(cached, "", "  "); err == nil {
						_ = os.WriteFile(cachePath, rawBytes, 0o600)
					}
				}
			}
		}
	}

	for i := range models {
		lowID := strings.ToLower(strings.TrimSpace(models[i].ID))
		if item, ok := cached.Discounts[lowID]; ok && item.DiscountPct > 0 {
			models[i].DiscountPct = item.DiscountPct
			if item.PromptPrice > 0 {
				models[i].PromptPrice = item.PromptPrice
			}
			if item.CompletionPrice > 0 {
				models[i].CompletionPrice = item.CompletionPrice
			}
		}
		if disp, _, _, baseDisc, ok := jev.CuratedModelMeta("openrouter", lowID); ok {
			if models[i].Name == "" {
				models[i].Name = disp
			}
			if models[i].DiscountPct <= 0 && baseDisc > 0 {
				models[i].DiscountPct = baseDisc
			}
		}
	}
}
