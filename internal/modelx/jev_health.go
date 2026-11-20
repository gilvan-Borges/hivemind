package modelx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/jev"
)

var (
	jevHealthOnce sync.Once
	jevHealthInst *jev.HealthStore
)

// DefaultJevHealthStore returns the process-shared Jev HealthStore backed by health.json.
func DefaultJevHealthStore() *jev.HealthStore {
	jevHealthOnce.Do(func() {
		pol, err := jev.LoadPolicy()
		if err != nil {
			pol = jev.DefaultPolicy()
		}
		jevHealthInst = jev.NewHealthStore("", pol.Cooldown)
		jevHealthInst.SyncCredentialFingerprints(JevCredentialFingerprints())
	})
	return jevHealthInst
}

// JevCredentialFingerprints returns SHA-256 prefixes of each provider's credential
// state (never raw secrets) so auth-opened circuits auto-reset when keys change.
func JevCredentialFingerprints() map[string]string {
	hashVal := func(parts ...string) string {
		h := sha256.New()
		for _, p := range parts {
			h.Write([]byte(strings.TrimSpace(p)))
			h.Write([]byte{0})
		}
		sum := h.Sum(nil)
		return hex.EncodeToString(sum[:8])
	}
	bg := context.Background()
	ocKey, ocBase, _ := LoadOpenCodeCredentials()
	agyBin, _ := LookAgy()
	curBin, _ := LookCursor()
	return map[string]string{
		"agy":              hashVal(agyBin),
		"cursor":           hashVal(curBin, resolveEnvForContext(bg, "cursor", "CURSOR_API_KEY")),
		"opencode":         hashVal(ocKey, ocBase),
		"openrouter":       hashVal(resolveEnvForContext(bg, "openrouter", "OPENROUTER_API_KEY"), resolveEnvForContext(bg, "openrouter", "OPENROUTER_BASE_URL")),
		"gemini":           hashVal(resolveEnvForContext(bg, "gemini", "GOOGLE_API_KEY", "GEMINI_API_KEY")),
		"vertex":           hashVal(resolveEnvForContext(bg, "gemini", "GOOGLE_CLOUD_PROJECT"), resolveEnvForContext(bg, "gemini", "GOOGLE_CLOUD_LOCATION")),
		"anthropic":        hashVal(resolveEnvForContext(bg, "anthropic", "ANTHROPIC_API_KEY"), os.Getenv("ANTHROPIC_BACKEND")),
		"openai":           hashVal(resolveEnvForContext(bg, "openai", "OPENAI_API_KEY"), resolveEnvForContext(bg, "openai", "OPENAI_BASE_URL")),
		"openai-responses": hashVal(resolveEnvForContext(bg, "openai", "OPENAI_API_KEY"), resolveEnvForContext(bg, "openai", "OPENAI_BASE_URL")),
	}
}

// ProbeProviderRecovery runs the existing non-invasive modelx probe for one provider
// and updates HealthStore without opening outage circuits on probe errors.
func ProbeProviderRecovery(ctx context.Context, hs *jev.HealthStore, provider string) {
	if hs == nil {
		return
	}
	hs.SyncCredentialFingerprints(JevCredentialFingerprints())
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "agy":
		q := fetchAgyUsageFn(ctx)
		if !q.Available {
			hs.UpdateQuotaProbe("agy", -1, time.Time{}, errProbeUnavailable)
			return
		}
		minRem := 100.0
		var earliestReset time.Time
		hasBucket := false
		for _, g := range q.Groups {
			for _, b := range g.Buckets {
				hasBucket = true
				if b.RemainingPercent < minRem {
					minRem = b.RemainingPercent
				}
				if b.ResetTime != "" {
					if ts, err := time.Parse(time.RFC3339, b.ResetTime); err == nil {
						if earliestReset.IsZero() || ts.Before(earliestReset) {
							earliestReset = ts
						}
					}
				}
			}
		}
		if !hasBucket {
			minRem = -1
		}
		hs.UpdateQuotaProbe("agy", minRem, earliestReset, nil)

	case "opencode":
		q := fetchOpenCodeUsageFn(ctx)
		if !q.Available {
			hs.UpdateQuotaProbe("opencode", -1, time.Time{}, errProbeUnavailable)
			return
		}
		maxUsed := 0.0
		var resetsAt time.Time
		for _, w := range []RemoteQuotaWindow{q.Rolling5h, q.Weekly, q.Monthly} {
			if !w.HasData {
				continue
			}
			if w.Percent > maxUsed {
				maxUsed = w.Percent
				if w.ResetsAt != "" {
					if ts, err := time.Parse(time.RFC3339, w.ResetsAt); err == nil {
						resetsAt = ts
					}
				}
			}
		}
		rem := 100.0 - maxUsed
		if rem < 0 {
			rem = 0
		}
		hs.UpdateQuotaProbe("opencode", rem, resetsAt, nil)

	case "openrouter":
		err := ProbeOpenRouterAuth(ctx)
		hs.UpdateQuotaProbe("openrouter", -1, time.Time{}, err)

	case "cursor":
		st := ProbeCursor()
		if !st.Found || !st.HasKey {
			hs.UpdateQuotaProbe("cursor", -1, time.Time{}, errProbeUnavailable)
			return
		}
		hs.UpdateQuotaProbe("cursor", -1, time.Time{}, nil)
	}
}

// ProbeAllProvidersHealth refreshes recovery probes for any non-closed or quota-tracked
// providers (agy, opencode, openrouter, cursor) outside the routing hot path.
func ProbeAllProvidersHealth(ctx context.Context, hs *jev.HealthStore, policy jev.Policy) []jev.ProviderHealthView {
	if hs == nil {
		hs = DefaultJevHealthStore()
	}
	hs.SyncCredentialFingerprints(JevCredentialFingerprints())
	creds := JevCredentialPresence()
	snap := hs.Snapshot(creds, nil)

	targets := map[string]bool{}
	for prov, st := range snap.Circuits {
		if st.Circuit != "" && st.Circuit != jev.CircuitClosed {
			targets[prov] = true
		}
	}
	if creds["agy"] {
		targets["agy"] = true
	}
	if creds["opencode"] {
		targets["opencode"] = true
	}

	var wg sync.WaitGroup
	for prov := range targets {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			pCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			ProbeProviderRecovery(pCtx, hs, p)
		}(prov)
	}
	wg.Wait()
	return hs.Summary(policy, JevCredentialPresence())
}

var errProbeUnavailable = context.DeadlineExceeded
