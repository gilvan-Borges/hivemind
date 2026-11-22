package team

import "testing"

func TestEffectiveSubagentSettings(t *testing.T) {
	cases := []struct {
		name string
		s    Settings
		want int
		fn   func(Settings) int
	}{
		{"default spawns", Settings{}, 6, Settings.EffectiveMaxSubagentCalls},
		{"custom spawns", Settings{MaxSubagentCalls: 10}, 10, Settings.EffectiveMaxSubagentCalls},
		{"clamped spawns", Settings{MaxSubagentCalls: 99}, 24, Settings.EffectiveMaxSubagentCalls},
		{"default scout llm calls", Settings{}, 8, Settings.EffectiveMaxSubagentLLMCalls},
		{"floor scout llm calls", Settings{MaxSubagentLLMCalls: 1}, 3, Settings.EffectiveMaxSubagentLLMCalls},
		{"clamped scout llm calls", Settings{MaxSubagentLLMCalls: 99}, 24, Settings.EffectiveMaxSubagentLLMCalls},
	}
	for _, tc := range cases {
		if got := tc.fn(tc.s); got != tc.want {
			t.Fatalf("%s: esperava %d, veio %d", tc.name, tc.want, got)
		}
	}
}
