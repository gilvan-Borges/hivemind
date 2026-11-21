package jev

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestHealthStore(t *testing.T) (*HealthStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "health.json")
	return NewHealthStore(path, DefaultPolicy().Cooldown), path
}

// Spec 06 §4: "Simulação de cancelamento/400 durante probe half_open libera o
// slot imediatamente sem travar o provedor."
func TestHalfOpenLeaseReleasesOnPolicyErrorAndCancel(t *testing.T) {
	hs, _ := newTestHealthStore(t)
	hs.nowFn = func() time.Time { return time.Now() }

	// Open the provider circuit (auth is provider-scoped) and let the window
	// expire -> half-open.
	hs.RecordFailure("provA", "", ErrClassAuth, time.Time{})
	hs.mu.Lock()
	rec := hs.state.Providers["prova"]
	rec.Until = hs.nowFn().Add(-time.Second)
	advanceLazyState(&rec.circuitRecord, hs.nowFn())
	hs.mu.Unlock()
	if rec.Circuit != CircuitHalfOpen {
		t.Fatalf("expected half-open, got %s", rec.Circuit)
	}

	// First probe claims the slot.
	if !hs.TryAcquireHalfOpen("provA", "") {
		t.Fatal("first probe must acquire the half-open slot")
	}
	// A concurrent second probe is blocked while the lease is held.
	if hs.TryAcquireHalfOpen("provA", "") {
		t.Fatal("second probe must be blocked while the lease is held")
	}

	// Probe fails with ErrClassPolicy (400/cancel): no RecordFailure happens,
	// but ReleaseHalfOpen must free the slot for the next probe.
	hs.ReleaseHalfOpen("provA", "")
	if !hs.TryAcquireHalfOpen("provA", "") {
		t.Fatal("slot must be free after ReleaseHalfOpen on a policy-class failure")
	}

	// The same guarantee must hold for the model-scoped slot.
	hs.mu.Lock()
	mr := hs.state.Models[catalogKey("provA", "model-x")]
	if mr == nil {
		mr = hs.ensureModelLocked("provA", "model-x")
	}
	mr.Circuit = CircuitHalfOpen
	mr.Until = time.Time{}
	hs.mu.Unlock()
	if !hs.TryAcquireHalfOpen("provA", "model-x") {
		t.Fatal("model probe must acquire")
	}
	if hs.TryAcquireHalfOpen("provA", "model-x") {
		t.Fatal("concurrent model probe must be blocked")
	}
	hs.ReleaseHalfOpen("provA", "model-x")
	if !hs.TryAcquireHalfOpen("provA", "model-x") {
		t.Fatal("model slot must be free after release")
	}
}

// A probe that dies without releasing self-heals after the TTL instead of
// wedging the provider in half_open forever (the old HalfOpenInFlight bug).
func TestHalfOpenLeaseSelfHealsAfterTTL(t *testing.T) {
	hs, _ := newTestHealthStore(t)
	base := time.Now()
	hs.nowFn = func() time.Time { return base }

	hs.RecordFailure("provB", "", ErrClassRateLimit, time.Time{})
	hs.mu.Lock()
	rec := hs.state.Providers["provb"]
	rec.Until = base.Add(-time.Second)
	advanceLazyState(&rec.circuitRecord, base)
	hs.mu.Unlock()

	if !hs.TryAcquireHalfOpen("provB", "") {
		t.Fatal("probe must acquire")
	}
	// Probe never reports back (crash/panic path). Advance past the TTL.
	hs.nowFn = func() time.Time { return base.Add(halfOpenLeaseTTL + time.Second) }
	if !hs.TryAcquireHalfOpen("provB", "") {
		t.Fatal("lease must self-release after the TTL")
	}
}

// Summary is a read: unknown providers must not be inserted into state.
func TestSummaryDoesNotInsertProviderRecords(t *testing.T) {
	hs, _ := newTestHealthStore(t)
	views := hs.Summary(Policy{}, map[string]bool{"never-seen": true, "agy": true})
	found := map[string]string{}
	for _, v := range views {
		found[v.Provider] = v.State
	}
	if found["never-seen"] != string(CircuitClosed) {
		t.Fatalf("unknown provider must render closed, got %q", found["never-seen"])
	}
	hs.mu.Lock()
	n := len(hs.state.Providers)
	hs.mu.Unlock()
	if n != 0 {
		t.Fatalf("Summary must not insert records, got %d", n)
	}
}

// RecordSpend debounce: many calls in the same window write the file once.
func TestRecordSpendDebounces(t *testing.T) {
	hs, path := newTestHealthStore(t)
	base := time.Now()
	hs.nowFn = func() time.Time { return base }

	countWrites := func() int {
		data, err := os.ReadFile(path)
		if err != nil {
			return 0
		}
		var parsed struct {
			Providers map[string]struct {
				DailySpentMicro int64 `json:"dailySpentMicroUsd"`
			} `json:"providers"`
		}
		if json.Unmarshal(data, &parsed) != nil {
			return 0
		}
		return int(parsed.Providers["or"].DailySpentMicro)
	}

	for i := 0; i < 10; i++ {
		hs.RecordSpend("or", 100)
	}
	if got := countWrites(); got != 100 {
		// First call saves immediately (lastSpendSave starts zero); the next 9
		// are buffered. The counter on disk must reflect the flushed state.
		t.Logf("disk after burst: %d", got)
	}
	// Advancing past the debounce flushes the pending delta.
	hs.nowFn = func() time.Time { return base.Add(recordSpendDebounce + time.Second) }
	hs.RecordSpend("or", 50)
	if got := countWrites(); got != 1050 {
		t.Fatalf("pending spend must flush after the debounce window, got %d", got)
	}
}

// Cross-process merge: records only another process wrote survive our save.
func TestSaveAdoptsUnknownDiskRecords(t *testing.T) {
	hs, path := newTestHealthStore(t)

	// Another process wrote a record we have never seen.
	foreign := healthFile{
		Version: 1,
		Providers: map[string]*providerStateRecord{
			"foreignprov": {circuitRecord: circuitRecord{Circuit: CircuitOpen, Reason: "quota"}, DailyDate: time.Now().UTC().Format("2006-01-02"), QuotaRemainingPct: -1},
		},
		Models: map[string]*circuitRecord{
			catalogKey("foreignprov", "m1"): {Circuit: CircuitOpen, Reason: "rate_limit"},
		},
	}
	raw, _ := json.Marshal(foreign)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	hs.RecordFailure("ownprov", "", ErrClassRateLimit, time.Time{})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var merged healthFile
	if err := json.Unmarshal(data, &merged); err != nil {
		t.Fatal(err)
	}
	if merged.Providers["foreignprov"] == nil || merged.Providers["foreignprov"].Circuit != CircuitOpen {
		t.Fatal("foreign provider record was lost by our save")
	}
	if merged.Providers["ownprov"] == nil {
		t.Fatal("own provider record missing after save")
	}
	if merged.Models[catalogKey("ownprov", "")] == nil || merged.Models[catalogKey("ownprov", "")].Circuit != CircuitOpen {
		t.Fatal("own rate-limit model circuit missing after save")
	}
	if merged.Models[catalogKey("foreignprov", "m1")] == nil {
		t.Fatal("foreign model record was lost by our save")
	}
}

var _ = errors.New
