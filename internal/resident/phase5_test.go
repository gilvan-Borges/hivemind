package resident

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
)

func TestRetrieve5000LinesStaysWithin4KiB(t *testing.T) {
	dir := t.TempDir()
	mem := NewMemoryStore(dir, "local")
	if err := os.MkdirAll(mem.AccountDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		fact := MemoryFact{
			ID:         "m" + itoa(i),
			Workstream: "geral",
			Text:       "fato " + itoa(i) + " sobre arquitetura do residente e do roteador",
			Source:     "user",
		}
		raw, err := json.Marshal(fact)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(mem.FilePath(), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	ret, err := mem.Retrieve("geral", "arquitetura residente")
	if err != nil {
		t.Fatal(err)
	}
	if len(ret.Matches) == 0 || len(ret.Matches) > MaxMemoryItems {
		t.Fatalf("%d trechos, teto %d", len(ret.Matches), MaxMemoryItems)
	}
	n := 0
	for _, fact := range ret.Matches {
		n += len(fact.Text)
	}
	if n > MaxMemoryBytes || ret.TotalBytes > MaxMemoryBytes {
		t.Fatalf("injetou %d bytes de texto e %d no total, teto %d", n, ret.TotalBytes, MaxMemoryBytes)
	}
}

func TestResidentRouteGoesThroughJev(t *testing.T) {
	low, lowComp := adjustResidentRoute(1000)
	if low != jev.ClassTrivial || lowComp != jev.ComplexityLow {
		t.Fatalf("rota baixa: %s %s", low, lowComp)
	}
	high, highComp := adjustResidentRoute(120_000)
	if high != jev.ClassCoding || highComp != jev.ComplexityHigh {
		t.Fatalf("rota alta: %s %s", high, highComp)
	}

	cat := jev.Catalog{Entries: []jev.ModelEntry{{
		Provider:           "openrouter",
		Model:              "openrouter/auto",
		ContextLength:      128_000,
		PromptMicroUSD:     0.1,
		CompletionMicroUSD: 0.2,
		Capabilities:       jev.Capabilities{Tools: true, Stream: true},
		DataClass:          jev.DataClassHTTPAPI,
		Tier:               jev.TierCheap,
	}}}
	pol := jev.DefaultPolicy()
	health := func() jev.HealthSnapshot {
		return jev.HealthSnapshot{Credentials: map[string]bool{"openrouter": true}, AgyRemainingPct: -1}
	}
	calls := 0
	llm, prov, mod, err := buildThroughJev(context.Background(), Config{}, jevRouteDeps{
		catalog: &cat,
		policy:  &pol,
		tokens:  1000,
		health:  health,
		build: func(_ context.Context, p, m string) (model.LLM, error) {
			calls++
			if p != "openrouter" || m != "openrouter/auto" {
				t.Fatalf("builder recebeu %s/%s", p, m)
			}
			return &modelx.Scripted{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || llm == nil || prov != "openrouter" || mod != "openrouter/auto" {
		t.Fatalf("rota %s/%s chamadas=%d", prov, mod, calls)
	}

	calls = 0
	empty := jev.Catalog{}
	_, _, _, err = buildThroughJev(context.Background(), Config{}, jevRouteDeps{
		catalog: &empty,
		policy:  &pol,
		health: func() jev.HealthSnapshot {
			return jev.HealthSnapshot{Credentials: map[string]bool{}, AgyRemainingPct: -1}
		},
		build: func(context.Context, string, string) (model.LLM, error) {
			calls++
			return &modelx.Scripted{}, nil
		},
	})
	if err == nil || calls != 0 {
		t.Fatalf("recusa deveria impedir o construtor, calls=%d err=%v", calls, err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d [12]byte
	i := len(d)
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d[i:])
}
