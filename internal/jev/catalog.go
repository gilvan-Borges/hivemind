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

// StaticSeedEntries returns the baseline cheap model seeds from SPEC_JEV_ORQUESTRADOR §6
// used when no network snapshot exists yet or when a remote refresh fails.
func StaticSeedEntries() []ModelEntry {
	return []ModelEntry{
		{
			Provider:      "opencode",
			Model:         "opencode-go:deepseek-v4.1-flash",
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
			ContextLength: 200_000,
			Capabilities:  Capabilities{Tools: true, Vision: true},
			LatencyP50Ms:  2500,
			Quality:       0.72,
			DataClass:     DataClassCLIAccount,
			Tier:          TierCheap,
			Source:        "static",
		},
		{
			Provider:      "cursor",
			Model:         "auto",
			ContextLength: 200_000,
			Capabilities:  Capabilities{Tools: false, Vision: true, WorkspaceAgent: true},
			LatencyP50Ms:  3000,
			Quality:       0.75,
			DataClass:     DataClassWorkspaceAgent,
			Tier:          TierCheap,
			Source:        "static",
		},
		{
			Provider:           "gemini",
			Model:              "gemini-flash-latest",
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
			entries, err := fn(ctx)
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
		if strings.EqualFold(e.Source, source) {
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

func sanitizeEntries(source string, entries []ModelEntry, now time.Time) []ModelEntry {
	seen := make(map[string]bool, len(entries))
	out := make([]ModelEntry, 0, len(entries))
	for _, e := range entries {
		prov := strings.ToLower(strings.TrimSpace(e.Provider))
		mod := strings.TrimSpace(e.Model)
		if prov == "" || mod == "" || !isSafeCatalogID(mod) {
			continue
		}
		key := catalogKey(prov, mod)
		if seen[key] {
			continue
		}
		seen[key] = true
		e.Provider = prov
		e.Model = mod
		e.Source = source
		if e.RefreshedAt.IsZero() {
			e.RefreshedAt = now
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
	for _, e := range cat.Entries {
		seen[catalogKey(e.Provider, e.Model)] = true
	}
	for _, seed := range StaticSeedEntries() {
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
