package jev

import "testing"

func TestComputeCostMicroUSD(t *testing.T) {
	entry := ModelEntry{
		Provider:           "openrouter",
		Model:              "test/model",
		PromptMicroUSD:     2.0, // $2 / 1M input tokens
		CompletionMicroUSD: 8.0, // $8 / 1M output tokens
	}

	// 1000 fresh in * 2 + 500 out * 8 = 2000 + 4000 = 6000 microUSD
	if got := ComputeCostMicroUSD(entry, 1000, 500, 0, 0); got != 6000 {
		t.Fatalf("expected 6000 microUSD, got %d", got)
	}

	// Cached read tokens are billed at 10% of prompt price:
	// 200 fresh * 2 + 800 cached * 0.2 + 500 out * 8 = 400 + 160 + 4000 = 4560 microUSD
	if got := ComputeCostMicroUSD(entry, 1000, 500, 800, 0); got != 4560 {
		t.Fatalf("expected 4560 microUSD with cache, got %d", got)
	}

	// Discount 50% halves total:
	if got := ComputeCostMicroUSD(entry, 1000, 500, 800, 50); got != 2280 {
		t.Fatalf("expected 2280 microUSD at 50%% discount, got %d", got)
	}

	// Clamp negative & cached > inTok:
	if got := ComputeCostMicroUSD(entry, -10, -5, -1, 200); got != 0 {
		t.Fatalf("expected 0 for negative tokens, got %d", got)
	}
	if got := ComputeCostMicroUSD(entry, 100, 0, 500, 0); got != 20 {
		t.Fatalf("expected clamped cache to 100 (20 microUSD), got %d", got)
	}
}

func TestFormatMicroUSD(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "$0.00"},
		{-10, "$0.00"},
		{500, "$0.0005"},
		{12345, "$0.0123"},
		{1_250_000, "$1.25"},
	}
	for _, tc := range cases {
		if got := FormatMicroUSD(tc.in); got != tc.want {
			t.Errorf("FormatMicroUSD(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
