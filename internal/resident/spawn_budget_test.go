package resident

import (
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/toolspill"
)

func TestBudgetChildSummaryShortReportUnchanged(t *testing.T) {
	text := "resultado: ok\n- arquivo.go alterado"
	if got := budgetChildSummary(text, 24_000, toolspill.NewStore(t.TempDir())); got != text {
		t.Fatalf("short report must stay verbatim, got %q", got)
	}
}

func TestBudgetChildSummaryTrimsAndArchives(t *testing.T) {
	root := t.TempDir()
	text := strings.Repeat("HEAD-LINE\n", 300) + strings.Repeat("MID\n", 20_000) + "FINAL-OUTCOME: sucesso\n"
	got := budgetChildSummary(text, 2_000, toolspill.NewStore(root))
	if len(got) >= len(text) {
		t.Fatalf("summary must be bounded: %d", len(got))
	}
	if !strings.Contains(got, "HEAD-LINE") {
		t.Fatal("summary must keep the head (lead with outcomes)")
	}
	if !strings.Contains(got, "FINAL-OUTCOME: sucesso") {
		t.Fatal("summary must keep the tail (final outcome)")
	}
	if !strings.Contains(got, "texto completo") {
		t.Fatal("summary must carry the archive pointer")
	}
}

func TestBudgetChildSummaryFailClosedWithoutStore(t *testing.T) {
	text := strings.Repeat("x", 50_000)
	got := budgetChildSummary(text, 2_000, toolspill.NewStore(""))
	if len(got) >= len(text) {
		t.Fatalf("fail-closed summary must still be bounded: %d", len(got))
	}
	if strings.Contains(got, "texto completo") {
		t.Fatal("fail-closed summary must not reference a lost archive")
	}
}

func TestSpawnGateConfigurableConcurrency(t *testing.T) {
	g := newSpawnGate(3)
	if !g.enter() || !g.enter() || !g.enter() {
		t.Fatal("three slots must fit the configured cap")
	}
	if g.enter() {
		t.Fatal("fourth entry must be rejected")
	}
	g.leave()
	if !g.enter() {
		t.Fatal("freed slot must be reusable")
	}

	floor := newSpawnGate(0)
	if !floor.enter() {
		t.Fatal("floor of 1 must always allow one child")
	}
	if floor.enter() {
		t.Fatal("floor of 1 must reject a second child")
	}
}

func TestConfigEffectiveDelegationKnobs(t *testing.T) {
	var zero Config
	if zero.EffectiveMaxConcurrentSubagents() != 2 {
		t.Fatalf("default concurrency = %d", zero.EffectiveMaxConcurrentSubagents())
	}
	if zero.EffectiveMaxSubagentSummaryChars() != 24_000 {
		t.Fatalf("default summary cap = %d", zero.EffectiveMaxSubagentSummaryChars())
	}
	custom := Config{MaxConcurrentSubagents: 5, MaxSubagentSummaryChars: 50_000}
	if custom.EffectiveMaxConcurrentSubagents() != 5 {
		t.Fatal("custom concurrency must win")
	}
	if custom.EffectiveMaxSubagentSummaryChars() != 50_000 {
		t.Fatal("custom summary cap must win")
	}
	tiny := Config{MaxSubagentSummaryChars: 100}
	if tiny.EffectiveMaxSubagentSummaryChars() != 24_000 {
		t.Fatal("summary cap below the 2.000 floor must fall back to default")
	}
}

func TestChildSummaryCapTightensWithParentHeadroom(t *testing.T) {
	r := &Runner{BaseDir: t.TempDir()}
	cap := r.childSummaryCap()
	if cap < 2_000 || cap > 24_000 {
		t.Fatalf("cap out of range: %d", cap)
	}
}
