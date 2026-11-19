package modelx

import (
	"context"
	"path/filepath"
	"testing"
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

func TestRecordAIUsageAndLimits(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "ai-usage.json")
	prevPath := usageFilePathFn
	prevAgy := fetchAgyUsageFn
	prevOC := fetchOpenCodeUsageFn
	usageFilePathFn = func() (string, error) { return tmp, nil }
	fetchAgyUsageFn = func(context.Context) AgyQuota { return AgyQuota{Available: true} }
	fetchOpenCodeUsageFn = func(context.Context) OpenCodeQuota { return OpenCodeQuota{Available: true} }
	t.Cleanup(func() {
		usageFilePathFn = prevPath
		fetchAgyUsageFn = prevAgy
		fetchOpenCodeUsageFn = prevOC
	})

	RecordAIUsage("agy", "agy:gemini-4-neon-high", 1200, 300, 1500)
	RecordAIUsage("opencode", "opencode-go:glm-5.3", 2000, 500, 2500)
	if err := SaveAIUsageLimits("agy", 500000, 2000000); err != nil {
		t.Fatalf("SaveAIUsageLimits error: %v", err)
	}

	t.Setenv("OPENCODE_API_KEY", "")
	t.Setenv("OPENCODE_ZEN_API_KEY", "")
	rep := GetAIUsageReport(context.Background())

	var agyFound, goFound, zenFound *AIProviderUsage
	for i := range rep.Providers {
		p := &rep.Providers[i]
		switch p.Provider {
		case "agy":
			agyFound = p
		case "opencode-go":
			goFound = p
		case "opencode":
			zenFound = p
		}
	}
	if agyFound == nil || agyFound.Window5h.TotalTokens != 1500 || agyFound.Window5h.Calls != 1 {
		t.Fatalf("unexpected agy usage: %+v", agyFound)
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
}
