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
		// If a previous session opened the global "agy" quota circuit while reserve
		// accounts still exist in the pool, clear the provider-level lock immediately
		// so pooledAccountLLM can route to healthy accounts.
		if pool := ListAccountPoolForProvider(context.Background(), "agy", ""); len(pool) > 1 {
			snap := jevHealthInst.Snapshot(map[string]bool{"agy": true}, nil)
			if st, ok := snap.Circuits["agy"]; ok && st.Circuit == jev.CircuitOpen && st.Reason == string(jev.ErrClassQuota) {
				jevHealthInst.UpdateQuotaProbe("agy", 100, time.Time{}, nil)
			}
		}
	})
	return jevHealthInst
}

// JevCredentialFingerprints returns SHA-256 prefixes of each provider's credential
// state (never raw secrets) so auth-opened circuits auto-reset when keys change.
func JevCredentialFingerprints() map[string]string {
	return JevCredentialFingerprintsForContext(context.Background())
}

// JevCredentialFingerprintsForContext computes credential fingerprints scoped to
// ctx's active workspace/organization.
func JevCredentialFingerprintsForContext(ctx context.Context) map[string]string {
	if ctx == nil {
		ctx = context.Background()
	}
	hashVal := func(parts ...string) string {
		h := sha256.New()
		for _, p := range parts {
			h.Write([]byte(strings.TrimSpace(p)))
			h.Write([]byte{0})
		}
		sum := h.Sum(nil)
		return hex.EncodeToString(sum[:8])
	}
	ocKey, ocBase, _ := LoadOpenCodeCredentialsForContext(ctx)
	agyBin, _ := LookAgy()
	curBin, _ := LookCursor()
	return map[string]string{
		"typesafe":         hashVal(resolveEnvForContext(ctx, "typesafe", "TYPESAFE_API_KEY"), resolveEnvForContext(ctx, "typesafe", "TYPESAFE_BASE_URL")),
		"agy":              hashVal(agyBin),
		"cursor":           hashVal(curBin, resolveEnvForContext(ctx, "cursor", "CURSOR_API_KEY")),
		"opencode":         hashVal(ocKey, ocBase),
		"openrouter":       hashVal(resolveEnvForContext(ctx, "openrouter", "OPENROUTER_API_KEY"), resolveEnvForContext(ctx, "openrouter", "OPENROUTER_BASE_URL")),
		"gemini":           hashVal(resolveEnvForContext(ctx, "gemini", "GOOGLE_API_KEY", "GEMINI_API_KEY")),
		"vertex":           hashVal(resolveEnvForContext(ctx, "gemini", "GOOGLE_CLOUD_PROJECT"), resolveEnvForContext(ctx, "gemini", "GOOGLE_CLOUD_LOCATION")),
		"anthropic":        hashVal(resolveEnvForContext(ctx, "anthropic", "ANTHROPIC_API_KEY"), os.Getenv("ANTHROPIC_BACKEND")),
		"openai":           hashVal(resolveEnvForContext(ctx, "openai", "OPENAI_API_KEY"), resolveEnvForContext(ctx, "openai", "OPENAI_BASE_URL")),
		"openai-responses": hashVal(resolveEnvForContext(ctx, "openai", "OPENAI_API_KEY"), resolveEnvForContext(ctx, "openai", "OPENAI_BASE_URL")),
	}
}

func agyQuotaSummary(q AgyQuota) (maxRem float64, earliestReset time.Time, hasBucket bool) {
	if !q.Available {
		return -1, time.Time{}, false
	}
	maxRem = -1
	for _, g := range q.Groups {
		for _, b := range g.Buckets {
			hasBucket = true
			if b.RemainingPercent > maxRem {
				maxRem = b.RemainingPercent
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
	return maxRem, earliestReset, hasBucket
}

// ProbeProviderRecovery runs the existing non-invasive modelx probe for one provider
// and updates HealthStore without opening outage circuits on probe errors.
func ProbeProviderRecovery(ctx context.Context, hs *jev.HealthStore, provider string) {
	if hs == nil {
		return
	}
	hs.SyncCredentialFingerprints(JevCredentialFingerprintsForContext(ctx))
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "agy":
		q := fetchAgyUsageFn(ctx)
		bestRem, earliestReset, hasBucket := agyQuotaSummary(q)

		// Never close the global "agy" provider circuit solely because 1 model group or
		// 1 account is exhausted when other accounts remain available in the multi-account pool.
		pool := ListAccountPoolForProvider(ctx, "agy", "")
		if len(pool) > 1 && (!q.Available || (hasBucket && bestRem < 5)) {
			hasReserveAccount := false
			for _, acc := range pool {
				if IsAccountModelSlotHealthy("agy", acc.ID, "__all__") {
					hasReserveAccount = true
					break
				}
			}
			if hasReserveAccount {
				// Probe up to one healthy reserve account quickly via lease if budget allows,
				// or keep the provider eligible (>= 5%) so pooledAccountLLM can route cleanly.
				if !q.Available || bestRem < 5 {
					bestRem = 100
				}
				hs.UpdateQuotaProbe("agy", bestRem, earliestReset, nil)
				return
			}
		}

		if !q.Available {
			hs.UpdateQuotaProbe("agy", -1, time.Time{}, errProbeUnavailable)
			return
		}
		if !hasBucket {
			bestRem = -1
		}
		hs.UpdateQuotaProbe("agy", bestRem, earliestReset, nil)

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

	case "typesafe":
		_, err := ProbeTypeSafeJev(ctx)
		hs.UpdateQuotaProbe("typesafe", -1, time.Time{}, err)

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
	hs.SyncCredentialFingerprints(JevCredentialFingerprintsForContext(ctx))
	creds := JevCredentialPresenceForContext(ctx)
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
	if creds["typesafe"] {
		targets["typesafe"] = true
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
	return hs.Summary(policy, JevCredentialPresenceForContext(ctx))
}

var errProbeUnavailable = context.DeadlineExceeded
