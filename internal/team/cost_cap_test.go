package team

import "testing"

func TestEffectiveMaxCostMicroUSD(t *testing.T) {
	cases := []struct {
		name string
		raw  int64
		want int64
	}{
		{"desligado por padrão", 0, 0},
		{"negativo trata como desligado", -5, 0},
		{"piso de um centavo", 1, 10_000},
		{"piso exato preservado", 10_000, 10_000},
		{"valor explicito preservado", 250_000, 250_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Settings{MaxCostMicroUSD: tc.raw}
			if got := s.EffectiveMaxCostMicroUSD(); got != tc.want {
				t.Fatalf("EffectiveMaxCostMicroUSD(%d) = %d, esperado %d", tc.raw, got, tc.want)
			}
		})
	}
}
