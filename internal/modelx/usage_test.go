package modelx

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseOpenCodeUsageJSON(t *testing.T) {
	raw := []byte(`{
		"usage": {
			"rolling": {"status": "active", "percent": 34.5, "resetsAt": "2026-11-18T23:00:00Z"},
			"weekly":  {"status": "active", "percent": 62,   "resetsAt": "2026-11-22T00:00:00Z"},
			"monthly": {"status": "active", "percent": 15,   "resetsAt": "2026-12-01T00:00:00Z"}
		}
	}`)
	q, err := ParseOpenCodeUsageJSON(raw)
	if err != nil {
		t.Fatalf("ParseOpenCodeUsageJSON error: %v", err)
	}
	if !q.Available || !q.Rolling5h.HasData || !q.Weekly.HasData || !q.Monthly.HasData {
		t.Fatalf("expected all windows populated, got %+v", q)
	}
	if q.Rolling5h.Percent != 34.5 || q.Weekly.Percent != 62 || q.Monthly.Percent != 15 {
		t.Fatalf("unexpected percentages: %+v", q)
	}
}

func TestParseAgyUsageJSON(t *testing.T) {
	raw := []byte(`{
		"status": "SUCCESS",
		"num_turns": 0,
		"usage": {"total_tokens": 0},
		"command": {
			"name": "usage",
			"data": {
				"groups": [
					{
						"name": "Gemini Models",
						"description": "Models within this group: Gemini 4 Neon, Gemini 4 Hydrogen",
						"buckets": [
							{"id": "gemini-weekly", "name": "Weekly Limit Remaining", "window": "weekly", "remaining_fraction": 0.5404, "reset_time": "2026-11-25T12:29:14Z"},
							{"id": "gemini-5h", "name": "Five Hour Limit Remaining", "window": "5h", "remaining_fraction": 0.4025, "reset_time": "2026-11-19T02:17:43Z"}
						]
					},
					{
						"name": "Gemini 4 Argon",
						"buckets": [
							{"id": "gemini-argon-wtu-weekly", "name": "Gemini 4 Argon Weekly Limit", "window": "weekly", "remaining_fraction": 0, "reset_time": "2026-11-25T03:16:26Z"}
						]
					}
				]
			}
		}
	}`)
	q, err := ParseAgyUsageJSON(raw)
	if err != nil {
		t.Fatalf("ParseAgyUsageJSON error: %v", err)
	}
	if !q.Available || len(q.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %+v", q)
	}
	// Verify 5h comes before weekly inside the group
	g0 := q.Groups[0]
	if g0.Name != "Gemini Models" || len(g0.Buckets) != 2 {
		t.Fatalf("unexpected group 0: %+v", g0)
	}
	if g0.Buckets[0].Window != "5h" || g0.Buckets[0].RemainingPercent != 40.3 || g0.Buckets[0].UsedPercent != 59.7 {
		t.Fatalf("unexpected 5h bucket: %+v", g0.Buckets[0])
	}
	if g0.Buckets[1].Window != "weekly" || g0.Buckets[1].RemainingPercent != 54 || g0.Buckets[1].UsedPercent != 46 {
		t.Fatalf("unexpected weekly bucket: %+v", g0.Buckets[1])
	}
	g1 := q.Groups[1]
	if g1.Buckets[0].UsedPercent != 100 || g1.Buckets[0].RemainingPercent != 0 {
		t.Fatalf("unexpected argon bucket: %+v", g1.Buckets[0])
	}
}

func TestGetAIUsageReportListsEachAgyAccount(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "ai-usage.json")
	prevPath := usageFilePathFn
	prevAgy := fetchAgyUsageFn
	prevOC := fetchOpenCodeUsageFn
	prevList := listAgyProfilesUsageFn
	prevAct := activateAgyProfileFn
	prevRead := readLiveAgyCredFn
	prevWrite := writeLiveAgyCredFn
	prevHas := hasAgyVaultSessionFn
	prevRest := restoreLiveAgyCredFn
	usageFilePathFn = func() (string, error) { return tmp, nil }
	listAgyProfilesUsageFn = func() []ProviderProfileSummary {
		return []ProviderProfileSummary{
			{ID: "agy-a", Provider: "agy", Name: "Conta A"},
			{ID: "agy-b", Provider: "agy", Name: "Conta B"},
			{ID: "agy-c", Provider: "agy", Name: "Conta C"},
		}
	}
	calls := 0
	activateAgyProfileFn = func(id string) error { return nil }
	readLiveAgyCredFn = func() ([]byte, string, error) { return []byte("prev"), "prev@x", nil }
	wrote := ""
	writeLiveAgyCredFn = func(raw []byte) error { wrote = string(raw); return nil }
	hasAgyVaultSessionFn = func(id string) (bool, string) { return true, id + "@ex.com" }
	restoreLiveAgyCredFn = func() {}
	fetchAgyUsageFn = func(context.Context) AgyQuota {
		calls++
		return AgyQuota{Available: true, Detail: "ok"}
	}
	fetchOpenCodeUsageFn = func(context.Context) OpenCodeQuota { return OpenCodeQuota{} }
	t.Cleanup(func() {
		FlushAIUsage()
		resetUsageMemForTest()
		usageFilePathFn = prevPath
		fetchAgyUsageFn = prevAgy
		fetchOpenCodeUsageFn = prevOC
		listAgyProfilesUsageFn = prevList
		activateAgyProfileFn = prevAct
		readLiveAgyCredFn = prevRead
		writeLiveAgyCredFn = prevWrite
		hasAgyVaultSessionFn = prevHas
		restoreLiveAgyCredFn = prevRest
	})

	rep := GetAIUsageReport(context.Background())
	if len(rep.AgyAccounts) != 3 {
		t.Fatalf("agy accounts: %+v", rep.AgyAccounts)
	}
	if calls != 3 {
		t.Fatalf("usage fetches = %d", calls)
	}
	if wrote != "prev" {
		t.Fatalf("live cred not restored: %q", wrote)
	}
	if rep.AgyAccounts[0].Name != "Conta A" || rep.AgyAccounts[2].Email != "agy-c@ex.com" {
		t.Fatalf("account labels: %+v", rep.AgyAccounts)
	}
}

func TestRecordAIUsageAndLimits(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "ai-usage.json")
	prevPath := usageFilePathFn
	prevAgy := fetchAgyUsageFn
	prevOC := fetchOpenCodeUsageFn
	prevList := listAgyProfilesUsageFn
	usageFilePathFn = func() (string, error) { return tmp, nil }
	listAgyProfilesUsageFn = func() []ProviderProfileSummary { return nil }
	fetchAgyUsageFn = func(context.Context) AgyQuota { return AgyQuota{Available: true} }
	fetchOpenCodeUsageFn = func(context.Context) OpenCodeQuota { return OpenCodeQuota{Available: true} }
	t.Cleanup(func() {
		FlushAIUsage()
		resetUsageMemForTest()
		usageFilePathFn = prevPath
		fetchAgyUsageFn = prevAgy
		fetchOpenCodeUsageFn = prevOC
		listAgyProfilesUsageFn = prevList
	})

	RecordAIUsage("agy", "agy:gemini-4-neon-high", 1200, 300, 1500)
	RecordAIUsage("cursor", "auto", 400, 100, 500)
	RecordAIUsage("opencode", "opencode-go:glm-5.3", 2000, 500, 2500)
	RecordAIUsage("openrouter", "anthropic/claude-sonnet-4-5", 100, 50, 150)
	if err := SaveAIUsageLimits("agy", 500000, 2000000); err != nil {
		t.Fatalf("SaveAIUsageLimits error: %v", err)
	}

	t.Setenv("OPENCODE_API_KEY", "")
	t.Setenv("OPENCODE_ZEN_API_KEY", "")
	rep := GetAIUsageReport(context.Background())

	var agyFound, cursorFound, goFound, zenFound, orFound *AIProviderUsage
	for i := range rep.Providers {
		p := &rep.Providers[i]
		switch p.Provider {
		case "agy":
			agyFound = p
		case "cursor":
			cursorFound = p
		case "opencode-go":
			goFound = p
		case "opencode":
			zenFound = p
		case "openrouter":
			orFound = p
		}
	}
	if agyFound == nil || agyFound.Window5h.TotalTokens != 1500 || agyFound.Window5h.Calls != 1 {
		t.Fatalf("unexpected agy usage: %+v", agyFound)
	}
	if cursorFound == nil || cursorFound.Window5h.TotalTokens != 500 || cursorFound.Window5h.Calls != 1 {
		t.Fatalf("unexpected cursor usage (must not fall into agy): %+v", cursorFound)
	}
	if agyFound.Limits.FiveHourTokens != 500000 || agyFound.Limits.WeeklyTokens != 2000000 {
		t.Fatalf("unexpected agy limits: %+v", agyFound.Limits)
	}
	if goFound == nil || goFound.Window5h.TotalTokens != 2500 || goFound.WindowWeekly.TotalTokens != 2500 {
		t.Fatalf("unexpected opencode-go usage: %+v", goFound)
	}
	if zenFound != nil {
		t.Fatalf("unused opencode (Zen) must not be shown when it has zero usage: %+v", zenFound)
	}
	if orFound == nil || orFound.Window5h.TotalTokens != 150 || orFound.Window5h.Calls != 1 {
		t.Fatalf("unexpected openrouter usage: %+v", orFound)
	}
}

func useUsageFile(t *testing.T, path string) {
	t.Helper()
	prevPath := usageFilePathFn
	prevAgy := fetchAgyUsageFn
	prevOC := fetchOpenCodeUsageFn
	prevList := listAgyProfilesUsageFn
	prevInterval := usageFlushInterval
	usageFilePathFn = func() (string, error) { return path, nil }
	listAgyProfilesUsageFn = func() []ProviderProfileSummary { return nil }
	fetchAgyUsageFn = func(context.Context) AgyQuota { return AgyQuota{} }
	fetchOpenCodeUsageFn = func(context.Context) OpenCodeQuota { return OpenCodeQuota{} }
	usageFlushInterval = time.Hour
	resetUsageMemForTest()
	t.Cleanup(func() {
		FlushAIUsage()
		resetUsageMemForTest()
		usageFilePathFn = prevPath
		fetchAgyUsageFn = prevAgy
		fetchOpenCodeUsageFn = prevOC
		listAgyProfilesUsageFn = prevList
		usageFlushInterval = prevInterval
	})
}

func TestRecordAIUsageBuffersUntilFlush(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "ai-usage.json")
	useUsageFile(t, tmp)
	t.Setenv("OPENCODE_API_KEY", "")
	t.Setenv("OPENCODE_ZEN_API_KEY", "")

	RecordAIUsage("agy", "agy:gemini-4-neon-high", 10, 1, 11)
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("a chamada não deveria regravar o JSON: %v", err)
	}
	rep := GetAIUsageReport(context.Background())
	var agy *AIProviderUsage
	for i := range rep.Providers {
		if rep.Providers[i].Provider == "agy" {
			agy = &rep.Providers[i]
		}
	}
	if agy == nil || agy.Window5h.TotalTokens != 11 {
		t.Fatalf("o relatório tem de ver o buffer antes do flush: %+v", agy)
	}

	FlushAIUsage()
	info, err := os.Stat(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("gravação atômica deixou o arquivo temporário")
	}
	RecordAIUsage("agy", "agy:gemini-4-neon-high", 3, 1, 4)
	info2, err := os.Stat(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !info2.ModTime().Equal(info.ModTime()) || info2.Size() != info.Size() {
		t.Fatal("a segunda chamada regravou o arquivo inteiro")
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `"tot":`) != 1 {
		t.Fatalf("disco ainda deveria ter só o primeiro evento: %s", data)
	}
	rep = GetAIUsageReport(context.Background())
	agy = nil
	for i := range rep.Providers {
		if rep.Providers[i].Provider == "agy" {
			agy = &rep.Providers[i]
		}
	}
	if agy == nil || agy.Window5h.TotalTokens != 15 {
		t.Fatalf("memória deveria somar o intervalo ainda não gravado: %+v", agy)
	}

	FlushAIUsage()
	resetUsageMemForTest()
	rep = GetAIUsageReport(context.Background())
	agy = nil
	for i := range rep.Providers {
		if rep.Providers[i].Provider == "agy" {
			agy = &rep.Providers[i]
		}
	}
	if agy == nil || agy.Window5h.TotalTokens != 15 || agy.Window5h.Calls != 2 {
		t.Fatalf("releitura do JSON compatível: %+v", agy)
	}
}

func TestRecordAIUsagePrunesAfter31Days(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "ai-usage.json")
	old := time.Now().UTC().Add(-40 * 24 * time.Hour)
	raw, err := json.Marshal(usageStoreFile{
		Version: 1,
		Events: []usageEvent{{
			Timestamp: old, Provider: "agy", Model: "agy:old",
			InputTokens: 9, OutputTokens: 1, TotalTokens: 10,
		}},
		Limits: map[string]ProviderLimits{"cursor": {WeeklyTokens: 42}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	useUsageFile(t, tmp)
	RecordAIUsage("agy", "agy:new", 2, 1, 3)
	FlushAIUsage()
	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	var store usageStoreFile
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatal(err)
	}
	if store.Limits["cursor"].WeeklyTokens != 42 {
		t.Fatalf("limites antigos: %+v", store.Limits)
	}
	if len(store.Events) != 1 || store.Events[0].TotalTokens != 3 || strings.Contains(string(data), "agy:old") {
		t.Fatalf("retenção de 31 dias: %+v", store.Events)
	}
}

func TestFlushMergesLedgerWrittenByAnotherProcess(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "ai-usage.json")
	useUsageFile(t, tmp)

	RecordAIUsage("agy", "agy:local", 4, 1, 5)
	other := usageStoreFile{
		Version: 1,
		Events: []usageEvent{{
			Timestamp: time.Now().UTC().Add(-time.Minute), Provider: "cursor", Model: "auto",
			InputTokens: 8, OutputTokens: 2, TotalTokens: 10,
		}},
		Limits: map[string]ProviderLimits{"cursor": {WeeklyTokens: 77}},
	}
	raw, err := json.Marshal(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	FlushAIUsage()

	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	var store usageStoreFile
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatal(err)
	}
	if store.Limits["cursor"].WeeklyTokens != 77 {
		t.Fatalf("limite do outro processo sumiu: %+v", store.Limits)
	}
	var sawLocal, sawOther bool
	for _, ev := range store.Events {
		if ev.Model == "agy:local" && ev.TotalTokens == 5 {
			sawLocal = true
		}
		if ev.Provider == "cursor" && ev.TotalTokens == 10 {
			sawOther = true
		}
	}
	if !sawLocal || !sawOther {
		t.Fatalf("eventos dos dois processos: %+v", store.Events)
	}
}

func BenchmarkRecordAIUsage(b *testing.B) {
	tmp := filepath.Join(b.TempDir(), "ai-usage.json")
	prevPath := usageFilePathFn
	prevInterval := usageFlushInterval
	usageFilePathFn = func() (string, error) { return tmp, nil }
	usageFlushInterval = time.Hour
	resetUsageMemForTest()
	b.Cleanup(func() {
		FlushAIUsage()
		resetUsageMemForTest()
		usageFilePathFn = prevPath
		usageFlushInterval = prevInterval
	})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RecordAIUsage("agy", "agy:bench", 1, 1, 2)
	}
}
