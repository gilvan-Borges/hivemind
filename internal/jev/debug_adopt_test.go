package jev

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDebugSaveAdopt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "health.json")
	hs := NewHealthStore(path, DefaultPolicy().Cooldown)

	foreign := healthFile{
		Version: 1,
		Providers: map[string]*providerStateRecord{
			"foreignprov": {circuitRecord: circuitRecord{Circuit: CircuitOpen, Reason: "quota"}, DailyDate: time.Now().UTC().Format("2006-01-02"), QuotaRemainingPct: -1},
		},
	}
	raw, _ := json.Marshal(foreign)
	os.WriteFile(path, raw, 0o600)

	hs.RecordFailure("ownprov", "", ErrClassRateLimit, time.Time{})

	hs.mu.Lock()
	t.Logf("in-memory providers: %v", keysOf(hs.state.Providers))
	hs.mu.Unlock()
	data, _ := os.ReadFile(path)
	t.Logf("file: %s", string(data))
}

func keysOf[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
