package modelx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/jev"
)

func TestProbeProviderRecovery_AgySingleGroupExhaustedOrPoolHealthy(t *testing.T) {
	tmp := t.TempDir()
	hs := jev.NewHealthStore(filepath.Join(tmp, "health.json"), jev.CooldownConfig{
		Quota:     "15m",
		RateLimit: "2m",
		Outage:    "1m",
	})

	// Seed an open quota circuit on agy, simulating legacy minRem=0 behavior.
	hs.UpdateQuotaProbe("agy", 0, time.Now().Add(2*time.Hour), nil)

	prevFetch := fetchAgyUsageFn
	fetchAgyUsageFn = func(context.Context) AgyQuota {
		return AgyQuota{
			Available: true,
			Groups: []AgyQuotaGroup{
				{Name: "Claude", Buckets: []AgyQuotaBucket{{Window: "5h", RemainingPercent: 0}}},
				{Name: "Gemini", Buckets: []AgyQuotaBucket{{Window: "5h", RemainingPercent: 85}}},
			},
		}
	}
	t.Cleanup(func() { fetchAgyUsageFn = prevFetch })

	ProbeProviderRecovery(context.Background(), hs, "agy")
	snap := hs.Snapshot(map[string]bool{"agy": true}, nil)
	if snap.Circuits["agy"].Circuit != jev.CircuitClosed {
		t.Fatalf("expected agy circuit closed when Gemini group still has 85%% quota, got %+v", snap.Circuits["agy"])
	}

	// Now simulate ALL groups on the active account at 0%, but two healthy reserve vault profiles exist.
	origProfilesPathFn := profilesFilePathFn
	profilesFilePathFn = func() (string, error) {
		return filepath.Join(tmp, "provider-profiles.json"), nil
	}
	t.Cleanup(func() { profilesFilePathFn = origProfilesPathFn })

	for _, id := range []string{"agy-acc1", "agy-acc2"} {
		vpath, _ := AgyProfileVaultPath(id)
		_ = os.MkdirAll(filepath.Dir(vpath), 0o700)
		_ = os.WriteFile(vpath, []byte(`{"id_token_email":"`+id+`@example.com"}`), 0o600)
	}
	profilesMu.Lock()
	_ = saveProfilesLocked(storedProfilesFile{
		Profiles: []ProviderProfile{
			{ID: "agy-acc1", Provider: "agy", Name: "Conta 1", AuthMode: "vault", IsDefault: true},
			{ID: "agy-acc2", Provider: "agy", Name: "Conta 2", AuthMode: "vault"},
		},
	})
	profilesMu.Unlock()

	hs.UpdateQuotaProbe("agy", 0, time.Now().Add(2*time.Hour), nil)
	fetchAgyUsageFn = func(context.Context) AgyQuota {
		return AgyQuota{
			Available: true,
			Groups: []AgyQuotaGroup{
				{Name: "Claude", Buckets: []AgyQuotaBucket{{Window: "5h", RemainingPercent: 0}}},
				{Name: "Gemini", Buckets: []AgyQuotaBucket{{Window: "5h", RemainingPercent: 0}}},
			},
		}
	}

	ProbeProviderRecovery(context.Background(), hs, "agy")
	snap2 := hs.Snapshot(map[string]bool{"agy": true}, nil)
	if snap2.Circuits["agy"].Circuit != jev.CircuitClosed {
		t.Fatalf("expected agy circuit closed when multi-account pool still has reserve accounts, got %+v", snap2.Circuits["agy"])
	}
}
