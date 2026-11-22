package contextx

import "testing"

func TestStatsUsagePercent(t *testing.T) {
	cases := []struct {
		name   string
		stats  Stats
		wantPct int
	}{
		{"sem janela", Stats{ContextTokens: 1000}, 0},
		{"sem tokens", Stats{ContextWindow: 1000}, 0},
		{"metade da janela", Stats{ContextTokens: 64_000, ContextWindow: 128_000}, 50},
		{"estouro limitado", Stats{ContextTokens: 128_000 * 4, ContextWindow: 128_000}, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.stats.UsagePercent(); got != tc.wantPct {
				t.Fatalf("UsagePercent() = %d, esperado %d", got, tc.wantPct)
			}
		})
	}
}
