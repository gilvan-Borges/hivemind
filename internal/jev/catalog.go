package jev

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// CatalogTTL is the per-source refresh interval matching OpenRouter's 30 min cache.
const CatalogTTL = 30 * time.Minute

// SourceFetcher retrieves fresh ModelEntry items for one catalog source outside the hot path.
type SourceFetcher func(ctx context.Context) ([]ModelEntry, error)

// CuratedOpenRouterSeedEntries returns the 21 curated OpenRouter coding/agentic models
// across Cheap (low difficulty), Mid (medium difficulty), and Strong (high difficulty) tiers.
func CuratedOpenRouterSeedEntries() []ModelEntry {
	return []ModelEntry{
		// 1. DeepSeek V4.1 Flash (1017)
		{
			Provider:           "openrouter",
			Model:              "deepseek/deepseek-v4.1-flash-1017",
			DisplayName:        "DeepSeek V4.1 Flash (1017)",
			ContextLength:      1_050_000,
			PromptMicroUSD:     0.05,
			CompletionMicroUSD: 0.15,
			Capabilities:       Capabilities{Tools: true, Vision: true, Stream: true},
			LatencyP50Ms:       720,
			Quality:            0.83,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierCheap,
			Source:             "static",
		},
		// 2. DeepSeek V4.1 Flash
		{
			Provider:           "openrouter",
			Model:              "deepseek/deepseek-v4.1-flash",
			DisplayName:        "DeepSeek V4.1 Flash",
			ContextLength:      1_050_000,
			PromptMicroUSD:     0.05,
			CompletionMicroUSD: 0.15,
			Capabilities:       Capabilities{Tools: true, Stream: true},
			LatencyP50Ms:       740,
			Quality:            0.81,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierCheap,
			Source:             "static",
		},
		// 3. DeepSeek V4 Pro
		{
			Provider:           "openrouter",
			Model:              "deepseek/deepseek-v4-pro",
			DisplayName:        "DeepSeek V4 Pro",
			ContextLength:      1_000_000,
			PromptMicroUSD:     0.90,
			CompletionMicroUSD: 1.50,
			Capabilities:       Capabilities{Tools: true, Stream: true},
			LatencyP50Ms:       1100,
			Quality:            0.91,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierMid,
			Source:             "static",
		},
		// 4. Tencent Hy4 Flash
		{
			Provider:           "openrouter",
			Model:              "tencent/hy4-flash",
			DisplayName:        "Tencent Hy4 Flash",
			ContextLength:      1_050_000,
			PromptMicroUSD:     0.06,
			CompletionMicroUSD: 0.18,
			Capabilities:       Capabilities{Tools: true, Stream: true},
			LatencyP50Ms:       730,
			Quality:            0.78,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierCheap,
			Source:             "static",
		},
		// 5. Tencent Hy4
		{
			Provider:           "openrouter",
			Model:              "tencent/hy4",
			DisplayName:        "Tencent Hy4",
			ContextLength:      1_050_000,
			PromptMicroUSD:     0.45,
			CompletionMicroUSD: 1.20,
			Capabilities:       Capabilities{Tools: true, Vision: true, Stream: true},
			LatencyP50Ms:       950,
			Quality:            0.86,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierMid,
			Source:             "static",
		},
		// 6. Google Gemini 4 Neon
		{
			Provider:           "openrouter",
			Model:              "google/gemini-4-neon",
			DisplayName:        "Google Gemini 4 Neon",
			ContextLength:      1_048_576,
			PromptMicroUSD:     0.60,
			CompletionMicroUSD: 2.00,
			Capabilities:       Capabilities{Tools: true, Vision: true, Stream: true},
			LatencyP50Ms:       780,
			Quality:            0.89,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierMid,
			Source:             "static",
		},
		// 7. Google Gemini 4 Carbon (-50% discount)
		{
			Provider:           "openrouter",
			Model:              "google/gemini-4-carbon",
			DisplayName:        "Google Carbon",
			ContextLength:      1_048_576,
			PromptMicroUSD:     1.00,
			CompletionMicroUSD: 2.20,
			DiscountPct:        50,
			Capabilities:       Capabilities{Tools: true, Vision: true, Stream: true},
			LatencyP50Ms:       880,
			Quality:            0.94,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierMid,
			Source:             "static",
		},
		// 8. Google Gemini 4 Argon
		{
			Provider:           "openrouter",
			Model:              "google/gemini-4-argon",
			DisplayName:        "Google Gemini 4 Argon",
			ContextLength:      1_048_576,
			PromptMicroUSD:     1.20,
			CompletionMicroUSD: 2.40,
			Capabilities:       Capabilities{Tools: true, Vision: true, Stream: true},
			LatencyP50Ms:       1150,
			Quality:            0.97,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierStrong,
			Source:             "static",
		},
		// 9. Google Gemini 3.8 Flash
		{
			Provider:           "openrouter",
			Model:              "google/gemini-3.8-flash",
			DisplayName:        "Google Gemini 3.8 Flash",
			ContextLength:      1_000_000,
			PromptMicroUSD:     0.12,
			CompletionMicroUSD: 0.45,
			Capabilities:       Capabilities{Tools: true, Vision: true, Stream: true},
			LatencyP50Ms:       680,
			Quality:            0.81,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierCheap,
			Source:             "static",
		},
		// 10. OpenAI GPT-6.1 Sol
		{
			Provider:           "openrouter",
			Model:              "openai/gpt-6.1-sol",
			DisplayName:        "OpenAI GPT-6.1 Sol",
			ContextLength:      1_000_000,
			PromptMicroUSD:     1.20,
			CompletionMicroUSD: 2.40,
			Capabilities:       Capabilities{Tools: true, Vision: true, Stream: true},
			LatencyP50Ms:       1050,
			Quality:            0.96,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierStrong,
			Source:             "static",
		},
		// 11. OpenAI GPT-6.1 Terra
		{
			Provider:           "openrouter",
			Model:              "openai/gpt-6.1-terra",
			DisplayName:        "OpenAI GPT-6.1 Terra",
			ContextLength:      1_000_000,
			PromptMicroUSD:     0.55,
			CompletionMicroUSD: 1.80,
			Capabilities:       Capabilities{Tools: true, Vision: true, Stream: true},
			LatencyP50Ms:       860,
			Quality:            0.89,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierMid,
			Source:             "static",
		},
		// 12. OpenAI GPT-6 Luna
		{
			Provider:           "openrouter",
			Model:              "openai/gpt-6-luna",
			DisplayName:        "OpenAI GPT-6 Luna",
			ContextLength:      1_000_000,
			PromptMicroUSD:     0.10,
			CompletionMicroUSD: 0.35,
			Capabilities:       Capabilities{Tools: true, Stream: true},
			LatencyP50Ms:       700,
			Quality:            0.79,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierCheap,
			Source:             "static",
		},
		// 13. Claude Opus 5.5
		{
			Provider:           "openrouter",
			Model:              "anthropic/claude-opus-5.5",
			DisplayName:        "Claude Opus 5.5",
			ContextLength:      1_000_000,
			PromptMicroUSD:     1.50,
			CompletionMicroUSD: 2.50,
			Capabilities:       Capabilities{Tools: true, Vision: true, Stream: true},
			LatencyP50Ms:       1200,
			Quality:            0.98,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierStrong,
			Source:             "static",
		},
		// 14. Claude Haiku 5.5
		{
			Provider:           "openrouter",
			Model:              "anthropic/claude-haiku-5.5",
			DisplayName:        "Claude Haiku 5.5",
			ContextLength:      200_000,
			PromptMicroUSD:     0.15,
			CompletionMicroUSD: 0.60,
			Capabilities:       Capabilities{Tools: true, Vision: true, Stream: true},
			LatencyP50Ms:       650,
			Quality:            0.80,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierCheap,
			Source:             "static",
		},
		// 15. Grok 4.7
		{
			Provider:           "openrouter",
			Model:              "x-ai/grok-4.7",
			DisplayName:        "Grok 4.7",
			ContextLength:      1_000_000,
			PromptMicroUSD:     0.70,
			CompletionMicroUSD: 1.90,
			Capabilities:       Capabilities{Tools: true, Vision: true, Stream: true},
			LatencyP50Ms:       950,
			Quality:            0.91,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierMid,
			Source:             "static",
		},
		// 16. Z.ai GLM 5.3
		{
			Provider:           "openrouter",
			Model:              "z-ai/glm-5.3",
			DisplayName:        "Z.ai GLM 5.3",
			ContextLength:      1_048_576,
			PromptMicroUSD:     0.07,
			CompletionMicroUSD: 1.40,
			Capabilities:       Capabilities{Tools: true, Stream: true},
			LatencyP50Ms:       820,
			Quality:            0.88,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierMid,
			Source:             "static",
		},
		// 17. Z.ai GLM 5.3 Flash
		{
			Provider:           "openrouter",
			Model:              "z-ai/glm-5.3-flash",
			DisplayName:        "Z.ai GLM 5.3 Flash",
			ContextLength:      1_048_576,
			PromptMicroUSD:     0.04,
			CompletionMicroUSD: 0.08,
			Capabilities:       Capabilities{Tools: true, Stream: true},
			LatencyP50Ms:       690,
			Quality:            0.79,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierCheap,
			Source:             "static",
		},
		// 18. Moonshot Kimi K3.1 Pro
		{
			Provider:           "openrouter",
			Model:              "moonshotai/kimi-k3.1-pro",
			DisplayName:        "Moonshot Kimi K3.1 Pro",
			ContextLength:      1_048_576,
			PromptMicroUSD:     0.35,
			CompletionMicroUSD: 1.40,
			Capabilities:       Capabilities{Tools: true, Stream: true},
			LatencyP50Ms:       890,
			Quality:            0.88,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierMid,
			Source:             "static",
		},
		// 19. Qwen3 Coder 480B A35B
		{
			Provider:           "openrouter",
			Model:              "qwen/qwen3-coder",
			DisplayName:        "Qwen3 Coder 480B A35B",
			ContextLength:      1_000_000,
			PromptMicroUSD:     0.25,
			CompletionMicroUSD: 1.00,
			Capabilities:       Capabilities{Tools: true, Stream: true},
			LatencyP50Ms:       840,
			Quality:            0.88,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierMid,
			Source:             "static",
		},
		// 20. Particle-1 (Gratuito)
		{
			Provider:      "openrouter",
			Model:         "particle-ai/particle-1:free",
			DisplayName:   "Particle-1 (Gratuito)",
			ContextLength: 1_050_000,
			Capabilities:  Capabilities{Tools: true, Stream: true},
			LatencyP50Ms:  820,
			Quality:       0.77,
			DataClass:     DataClassHTTPAPI,
			Tier:          TierCheap,
			Source:        "static",
			Free:          true,
		},
		// 21. Cohere North Mini Code (Gratuito)
		{
			Provider:      "openrouter",
			Model:         "cohere/north-mini-code:free",
			DisplayName:   "Cohere North Mini Code (Gratuito)",
			ContextLength: 256_000,
			Capabilities:  Capabilities{Tools: true, Stream: true},
			LatencyP50Ms:  710,
			Quality:       0.75,
			DataClass:     DataClassHTTPAPI,
			Tier:          TierCheap,
			Source:        "static",
			Free:          true,
		},
	}
}

// StaticSeedEntries returns the baseline cheap, mid, and strong model seeds from SPEC_JEV_ORQUESTRADOR §6
// used when no network snapshot exists yet or when a remote refresh fails.
func StaticSeedEntries() []ModelEntry {
	base := []ModelEntry{
		{
			Provider:      "opencode",
			Model:         "opencode-go:deepseek-v4.1-flash",
			DisplayName:   "OpenCode Go · DeepSeek V4.1 Flash",
			ContextLength: 128_000,
			Capabilities:  Capabilities{Tools: true, Stream: true},
			LatencyP50Ms:  900,
			Quality:       0.68,
			DataClass:     DataClassHTTPAPI,
			Tier:          TierCheap,
			Source:        "static",
		},
		{
			Provider:      "opencode",
			Model:         "opencode:big-pickle",
			DisplayName:   "OpenCode Zen · Big Pickle (Free)",
			ContextLength: 128_000,
			Capabilities:  Capabilities{Tools: true, Stream: true},
			LatencyP50Ms:  950,
			Quality:       0.62,
			DataClass:     DataClassHTTPAPI,
			Tier:          TierCheap,
			Source:        "static",
			Free:          true,
		},
		{
			Provider:      "agy",
			Model:         "agy",
			DisplayName:   "Antigravity (agy)",
			ContextLength: 200_000,
			Capabilities:  Capabilities{Tools: true, Vision: true},
			LatencyP50Ms:  2200,
			Quality:       0.76,
			DataClass:     DataClassCLIAccount,
			Tier:          TierCheap,
			Source:        "static",
		},
		{
			Provider:      "agy",
			Model:         "agy:gemini-4-neon-low",
			DisplayName:   "Antigravity · Gemini 4 Neon (Low)",
			ContextLength: 1_000_000,
			Capabilities:  Capabilities{Tools: true, Vision: true},
			LatencyP50Ms:  1600,
			Quality:       0.80,
			DataClass:     DataClassCLIAccount,
			Tier:          TierCheap,
			Source:        "static",
		},
		{
			Provider:      "agy",
			Model:         "agy:gemini-4-neon-high",
			DisplayName:   "Antigravity · Gemini 4 Neon (High)",
			ContextLength: 1_000_000,
			Capabilities:  Capabilities{Tools: true, Vision: true},
			LatencyP50Ms:  2100,
			Quality:       0.91,
			DataClass:     DataClassCLIAccount,
			Tier:          TierMid,
			Source:        "static",
		},
		{
			Provider:      "agy",
			Model:         "agy:carbon",
			DisplayName:   "Antigravity · Carbon",
			ContextLength: 1_000_000,
			Capabilities:  Capabilities{Tools: true, Vision: true},
			LatencyP50Ms:  2300,
			Quality:       0.93,
			DataClass:     DataClassCLIAccount,
			Tier:          TierMid,
			Source:        "static",
		},
		{
			Provider:      "agy",
			Model:         "agy:argon",
			DisplayName:   "Antigravity · Argon",
			ContextLength: 1_000_000,
			Capabilities:  Capabilities{Tools: true, Vision: true},
			LatencyP50Ms:  2600,
			Quality:       0.97,
			DataClass:     DataClassCLIAccount,
			Tier:          TierStrong,
			Source:        "static",
		},
		{
			Provider:      "cursor",
			Model:         "auto",
			DisplayName:   "Cursor Auto",
			ContextLength: 200_000,
			Capabilities:  Capabilities{Tools: false, Vision: true, WorkspaceAgent: true},
			LatencyP50Ms:  2800,
			Quality:       0.78,
			DataClass:     DataClassWorkspaceAgent,
			Tier:          TierCheap,
			Source:        "static",
		},
		{
			Provider:      "cursor",
			Model:         "sonnet-4.5",
			DisplayName:   "Cursor · Claude Sonnet",
			ContextLength: 200_000,
			Capabilities:  Capabilities{Tools: false, Vision: true, WorkspaceAgent: true},
			LatencyP50Ms:  3000,
			Quality:       0.90,
			DataClass:     DataClassWorkspaceAgent,
			Tier:          TierMid,
			Source:        "static",
		},
		{
			Provider:           "gemini",
			Model:              "gemini-flash-latest",
			DisplayName:        "Gemini Flash",
			ContextLength:      1_000_000,
			PromptMicroUSD:     0.15,
			CompletionMicroUSD: 0.60,
			Capabilities:       Capabilities{Tools: true, Vision: true, Stream: true},
			LatencyP50Ms:       700,
			Quality:            0.72,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierCheap,
			Source:             "static",
		},
	}
	return append(base, CuratedOpenRouterSeedEntries()...)
}

// DefaultStaticCatalog returns a Catalog populated with the static seed entries.
func DefaultStaticCatalog() Catalog {
	return Catalog{
		Version: 1,
		Entries: StaticSeedEntries(),
		Sources: map[string]time.Time{},
	}
}

// Lookup reports whether (provider, model) exists in the catalog.
func (c Catalog) Lookup(provider, model string) (ModelEntry, bool) {
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.TrimSpace(model)
	for _, e := range c.Entries {
		if strings.EqualFold(e.Provider, p) && e.Model == m {
			return e, true
		}
	}
	return ModelEntry{}, false
}

// CatalogStore keeps the active Catalog snapshot in memory, persists updates to
// catalog.json, and runs non-blocking background refreshes via injected fetchers.
type CatalogStore struct {
	mu         sync.RWMutex
	catalog    Catalog
	index      map[string]int
	path       string
	fetchers   map[string]SourceFetcher
	refreshing map[string]bool
	nowFn      func() time.Time
}

// CatalogPath returns the default disk snapshot path (UserConfigDir/HiveMind/jev/catalog.json).
func CatalogPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "catalog.json"), nil
}

// NewCatalogStore loads any persisted catalog snapshot (falling back to static seeds)
// and configures optional per-source refresh callbacks.
func NewCatalogStore(path string, fetchers map[string]SourceFetcher) *CatalogStore {
	if path == "" {
		if def, err := CatalogPath(); err == nil {
			path = def
		}
	}
	cs := &CatalogStore{
		catalog:    DefaultStaticCatalog(),
		path:       path,
		fetchers:   fetchers,
		refreshing: map[string]bool{},
		nowFn:      time.Now,
	}
	if path != "" {
		if loaded, err := LoadCatalogFrom(path); err == nil && len(loaded.Entries) > 0 {
			if len(fetchers) > 0 {
				filtered := make([]ModelEntry, 0, len(loaded.Entries))
				for _, e := range loaded.Entries {
					if strings.EqualFold(e.Provider, "openrouter") && !IsTrustedOpenRouterModel(e.Model) {
						continue
					}
					filtered = append(filtered, e)
				}
				loaded.Entries = filtered
			}
			cs.catalog = mergeWithStaticSeeds(loaded)
		}
	}
	cs.rebuildIndexLocked()
	return cs
}

func (s *CatalogStore) rebuildIndexLocked() {
	idx := make(map[string]int, len(s.catalog.Entries))
	for i, e := range s.catalog.Entries {
		idx[catalogKey(e.Provider, e.Model)] = i
	}
	s.index = idx
}

func catalogKey(provider, model string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "\x00" + strings.TrimSpace(model)
}

// Snapshot returns an immutable view of the current in-memory catalog.
// Never mutates entries after publishing and never performs disk or network I/O.
func (s *CatalogStore) Snapshot() Catalog {
	s.mu.RLock()
	cat := s.catalog
	s.mu.RUnlock()
	return cat
}

// RefreshIfStale triggers non-blocking background refreshes for any source whose
// snapshot is older than CatalogTTL. Errors preserve the previous snapshot.
func (s *CatalogStore) RefreshIfStale(ctx context.Context) {
	if s == nil || len(s.fetchers) == 0 {
		return
	}
	now := s.nowFn()
	s.mu.Lock()
	var due []string
	for src := range s.fetchers {
		if s.refreshing[src] {
			continue
		}
		last := s.catalog.Sources[src]
		if last.IsZero() || now.Sub(last) >= CatalogTTL {
			s.refreshing[src] = true
			due = append(due, src)
		}
	}
	s.mu.Unlock()

	for _, src := range due {
		fetcher := s.fetchers[src]
		go func(source string, fn SourceFetcher) {
			defer func() {
				s.mu.Lock()
				delete(s.refreshing, source)
				s.mu.Unlock()
			}()
			bgCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			entries, err := fn(bgCtx)
			if err != nil || len(entries) == 0 {
				return
			}
			s.MergeSource(source, entries)
		}(src, fetcher)
	}
}

// RefreshNow runs all configured fetchers synchronously (useful in tests or explicit refresh).
func (s *CatalogStore) RefreshNow(ctx context.Context) {
	if s == nil || len(s.fetchers) == 0 {
		return
	}
	var wg sync.WaitGroup
	for src, fn := range s.fetchers {
		wg.Add(1)
		go func(source string, fetch SourceFetcher) {
			defer wg.Done()
			entries, err := fetch(ctx)
			if err != nil || len(entries) == 0 {
				return
			}
			s.MergeSource(source, entries)
		}(src, fn)
	}
	wg.Wait()
}

// MergeSource replaces entries for source with valid sanitized entries and persists catalog.json.
func (s *CatalogStore) MergeSource(source string, entries []ModelEntry) {
	now := s.nowFn().UTC()
	cleaned := sanitizeEntries(source, entries, now)
	if len(cleaned) == 0 {
		return
	}

	s.mu.Lock()
	kept := make([]ModelEntry, 0, len(s.catalog.Entries)+len(cleaned))
	for _, e := range s.catalog.Entries {
		if strings.EqualFold(e.Source, source) || (strings.EqualFold(e.Source, "static") && strings.EqualFold(e.Provider, source)) {
			continue
		}
		kept = append(kept, e)
	}
	kept = append(kept, cleaned...)
	sourcesCopy := make(map[string]time.Time, len(s.catalog.Sources)+1)
	for k, v := range s.catalog.Sources {
		sourcesCopy[k] = v
	}
	sourcesCopy[source] = now
	merged := mergeWithStaticSeeds(Catalog{
		Version: 1,
		Entries: kept,
		Sources: sourcesCopy,
	})
	s.catalog = merged
	s.rebuildIndexLocked()
	path := s.path
	snapshot := merged
	s.mu.Unlock()

	if path != "" {
		_ = SaveCatalogTo(path, snapshot)
	}
}

// isAllowedFreeOpenRouterModel returns true for the 2 curated free coding/terminal
// models on OpenRouter explicitly approved for Jev routing.
func isAllowedFreeOpenRouterModel(modelID string) bool {
	m := strings.ToLower(strings.TrimSpace(modelID))
	return m == "particle-ai/particle-1:free" ||
		m == "cohere/north-mini-code:free" ||
		strings.HasPrefix(m, "particle-ai/particle-1") ||
		strings.HasPrefix(m, "cohere/north-mini-code")
}

// isBlockedOpenRouterEndpoint reports whether an OpenRouter model ID is an unapproved
// ":free" trial endpoint or an experimental/chat-only family known to fail with HTTP 400
// on multi-tool agent schemas.
func isBlockedOpenRouterEndpoint(modelID string) bool {
	m := strings.ToLower(strings.TrimSpace(modelID))
	if isAllowedFreeOpenRouterModel(m) {
		return false
	}
	return strings.HasSuffix(m, ":free") ||
		strings.HasSuffix(m, ":batch") ||
		strings.Contains(m, "/ernie") ||
		strings.HasPrefix(m, "baidu/") ||
		strings.HasPrefix(m, "dots-studio/") ||
		strings.Contains(m, "gemma") ||
		strings.Contains(m, "llama-3.1-8b") ||
		strings.Contains(m, "llama-3.2-") ||
		strings.Contains(m, "vision-free") ||
		strings.Contains(m, "roleplay")
}

// IsTrustedOpenRouterModel reports whether an OpenRouter model ID belongs to the
// curated coding and tool-calling catalog suitable for autonomous HiveMind agents.
func IsTrustedOpenRouterModel(modelID string) bool {
	m := strings.ToLower(strings.TrimSpace(modelID))
	if m == "" || isBlockedOpenRouterEndpoint(m) {
		return false
	}
	if isAllowedFreeOpenRouterModel(m) {
		return true
	}
	trustedPrefixes := []string{
		"deepseek/deepseek-v4.1-flash-1017",
		"deepseek/deepseek-v4.1-flash",
		"deepseek/deepseek-v4-pro",
		"deepseek/deepseek-v4",
		"deepseek/deepseek-chat",
		"deepseek/deepseek-v3",
		"deepseek/deepseek-coder",
		"deepseek/deepseek-r1",
		"tencent/hy4-flash",
		"tencent/hy4",
		"google/gemini-4-neon",
		"google/gemini-4-carbon",
		"google/gemini-4-argon",
		"google/gemini-3.8-flash",
		"google/gemini-2.5-flash",
		"google/gemini-2.5-pro",
		"google/gemini-3",
		"openai/gpt-6.1-sol",
		"openai/gpt-6.1-terra",
		"openai/gpt-6-luna",
		"openai/gpt-6",
		"openai/gpt-4o",
		"openai/gpt-4.1",
		"openai/gpt-5",
		"openai/o3",
		"openai/o4",
		"anthropic/claude-opus-5.5",
		"anthropic/claude-haiku-5.5",
		"anthropic/claude-",
		"x-ai/grok-4.7",
		"x-ai/grok-4",
		"x-ai/grok-3",
		"x-ai/grok-code",
		"z-ai/glm-5.3-flash",
		"z-ai/glm-5.3",
		"z-ai/glm-5",
		"z-ai/glm-4.5",
		"moonshotai/kimi-k3.1-pro",
		"moonshotai/kimi-k3",
		"moonshotai/kimi-k2",
		"qwen/qwen3-coder",
		"qwen/qwen-2.5-coder",
		"qwen/qwen3-235b",
		"qwen/qwen3-32b",
		"qwen/qwen3-30b",
		"mistralai/codestral",
		"mistralai/devstral",
	}
	for _, pref := range trustedPrefixes {
		if strings.HasPrefix(m, pref) {
			return true
		}
	}
	return false
}

// CuratedModelMeta returns the curated display name, difficulty tier, quality score,
// and baseline discount percentage for any of the 21 flagship OpenRouter models.
func CuratedModelMeta(provider, modelID string) (displayName string, tier Tier, quality float64, baselineDiscountPct int, ok bool) {
	if !strings.EqualFold(strings.TrimSpace(provider), "openrouter") {
		return "", "", 0, 0, false
	}
	low := strings.ToLower(strings.TrimSpace(modelID))
	for _, seed := range CuratedOpenRouterSeedEntries() {
		if strings.EqualFold(seed.Model, low) {
			return seed.DisplayName, seed.Tier, seed.Quality, seed.DiscountPct, true
		}
	}
	return "", "", 0, 0, false
}

func sanitizeEntries(source string, entries []ModelEntry, now time.Time) []ModelEntry {
	seen := make(map[string]bool, len(entries))
	out := make([]ModelEntry, 0, len(entries))
	for _, e := range entries {
		prov := strings.ToLower(strings.TrimSpace(e.Provider))
		mod := strings.TrimSpace(e.Model)
		if prov == "" || mod == "" || !isSafeCatalogID(mod) {
			continue
		}
		if prov == "openrouter" && isBlockedOpenRouterEndpoint(mod) {
			continue
		}
		key := catalogKey(prov, mod)
		if seen[key] {
			continue
		}
		seen[key] = true
		e.Provider = prov
		e.Model = mod
		if source != "" {
			e.Source = source
		}
		if e.RefreshedAt.IsZero() {
			e.RefreshedAt = now
		}
		if disp, curTier, curQual, baseDisc, ok := CuratedModelMeta(prov, mod); ok {
			if e.DisplayName == "" {
				e.DisplayName = disp
			}
			if curTier != "" {
				e.Tier = curTier
			}
			if curQual > e.Quality {
				e.Quality = curQual
			}
			if e.DiscountPct <= 0 && baseDisc > 0 {
				e.DiscountPct = baseDisc
			}
		}
		if e.Tier == "" {
			e.Tier = inferTier(e)
		}
		if e.DataClass == "" {
			e.DataClass = defaultDataClass(prov)
		}
		if e.LatencyP50Ms <= 0 {
			e.LatencyP50Ms = 1200
		}
		if e.Quality <= 0 {
			e.Quality = defaultQualityPrior(e.Tier)
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Model < out[j].Model
	})
	return out
}

func isSafeCatalogID(id string) bool {
	if len(id) > 200 {
		return false
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f || r == ' ' || r == '\t' {
			return false
		}
	}
	return true
}

func inferTier(e ModelEntry) Tier {
	if e.Free || (e.PromptMicroUSD == 0 && e.CompletionMicroUSD == 0) {
		return TierCheap
	}
	// Reference call cost on 10k input + 2k output tokens in microUSD:
	refCost := 10_000.0*e.PromptMicroUSD + 2_000.0*e.CompletionMicroUSD
	if refCost <= 12_000 { // <= $0.012
		return TierCheap
	}
	if refCost <= 60_000 { // <= $0.060
		return TierMid
	}
	return TierStrong
}

func defaultDataClass(provider string) DataClass {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "agy":
		return DataClassCLIAccount
	case "cursor":
		return DataClassWorkspaceAgent
	default:
		return DataClassHTTPAPI
	}
}

func defaultQualityPrior(tier Tier) float64 {
	switch tier {
	case TierStrong:
		return 0.90
	case TierMid:
		return 0.80
	default:
		return 0.68
	}
}

func mergeWithStaticSeeds(cat Catalog) Catalog {
	seen := make(map[string]bool, len(cat.Entries))
	hasLiveProvider := make(map[string]bool, len(cat.Sources))
	for _, e := range cat.Entries {
		seen[catalogKey(e.Provider, e.Model)] = true
		if e.Source != "" && !strings.EqualFold(e.Source, "static") {
			hasLiveProvider[strings.ToLower(e.Provider)] = true
		}
	}
	for _, seed := range StaticSeedEntries() {
		if hasLiveProvider[strings.ToLower(seed.Provider)] {
			continue
		}
		if !seen[catalogKey(seed.Provider, seed.Model)] {
			cat.Entries = append(cat.Entries, seed)
		}
	}
	if cat.Version == 0 {
		cat.Version = 1
	}
	if cat.Sources == nil {
		cat.Sources = map[string]time.Time{}
	}
	return cat
}

// LoadCatalogFrom reads a catalog snapshot JSON file from path.
func LoadCatalogFrom(path string) (Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, err
	}
	var cat Catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return Catalog{}, err
	}
	return cat, nil
}

// SaveCatalogTo atomically writes catalog.json with 0700 dir and 0600 file permissions.
func SaveCatalogTo(path string, cat Catalog) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return os.WriteFile(path, data, 0o600)
	}
	return nil
}
