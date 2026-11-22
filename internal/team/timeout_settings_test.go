package team

import (
	"testing"
	"time"
)

func TestEffectiveModelCallTimeout(t *testing.T) {
	cases := []struct {
		set  int
		want time.Duration
	}{
		{0, 600 * time.Second},      // default
		{-5, 600 * time.Second},     // inválido -> default
		{30, 60 * time.Second},      // piso
		{90, 90 * time.Second},      // valor válido
		{99999, 7200 * time.Second}, // teto
	}
	for _, c := range cases {
		s := Settings{ModelCallTimeoutSeconds: c.set}
		if got := s.EffectiveModelCallTimeout(); got != c.want {
			t.Fatalf("ModelCallTimeoutSeconds=%d: esperava %v, veio %v", c.set, c.want, got)
		}
	}
}
