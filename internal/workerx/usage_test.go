package workerx

import (
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/modelx"
)

func TestUsageReportFrom(t *testing.T) {
	if got := usageReportFrom("m1", nil); got != nil {
		t.Fatalf("nil usage must yield nil report, got %+v", got)
	}
	cost := 0.01234
	u := &modelx.CursorUsage{
		InputTokens:     1200,
		OutputTokens:    300,
		CacheReadTokens: 800,
		CostUSD:         &cost,
	}
	rep := usageReportFrom("modelo-beta", u)
	if rep == nil {
		t.Fatalf("usage must convert to a report")
	}
	if rep.Model != "modelo-beta" || rep.InputTokens != 1200 || rep.OutputTokens != 300 || rep.CacheReadTokens != 800 {
		t.Fatalf("fields wrong: %+v", rep)
	}
	if rep.CostMicroUSD != 12340 {
		t.Fatalf("cost conversion wrong: %d", rep.CostMicroUSD)
	}
}

func TestUsageReportFromWithoutCost(t *testing.T) {
	u := &modelx.CursorUsage{InputTokens: 10}
	rep := usageReportFrom("", u)
	if rep.CostMicroUSD != 0 {
		t.Fatalf("missing cost must stay zero, got %d", rep.CostMicroUSD)
	}
}
