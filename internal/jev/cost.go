package jev

import (
	"fmt"
	"math"
)

// ComputeCostMicroUSD returns the integer microUSD cost (1e-6 USD) for one LLM
// call given its catalog pricing, token counts, and active discount percentage.
// Cached prompt tokens are billed at 10% of PromptMicroUSD matching OpenRouter
// and Anthropic prompt-cache read pricing.
func ComputeCostMicroUSD(entry ModelEntry, inTok, outTok, cachedReadTok int64, discountPct int) int64 {
	if inTok < 0 {
		inTok = 0
	}
	if outTok < 0 {
		outTok = 0
	}
	if cachedReadTok < 0 {
		cachedReadTok = 0
	}
	if cachedReadTok > inTok {
		cachedReadTok = inTok
	}
	if inTok == 0 && outTok == 0 {
		return 0
	}
	if discountPct < 0 {
		discountPct = 0
	}
	if discountPct > 100 {
		discountPct = 100
	}
	disc := 1.0 - float64(discountPct)/100.0
	fresh := float64(inTok - cachedReadTok)
	cached := float64(cachedReadTok)
	cost := disc*(fresh*entry.PromptMicroUSD+float64(outTok)*entry.CompletionMicroUSD) + disc*0.1*cached*entry.PromptMicroUSD
	if cost <= 0 {
		return 0
	}
	return int64(math.Round(cost))
}

// FormatMicroUSD formats microUSD (1e-6 USD) concisely for CLI, TUI and IDE.
func FormatMicroUSD(microUSD int64) string {
	if microUSD <= 0 {
		return "$0.00"
	}
	usd := float64(microUSD) / 1_000_000.0
	if usd < 1.0 {
		return fmt.Sprintf("$%.4f", usd)
	}
	return fmt.Sprintf("$%.2f", usd)
}
