package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
)

// fakeStatusGit devolve status programável para exercitar o backpressure do
// loop do QA sem precisar de um repositório real.
type fakeStatusGit struct {
	gitx.Git
	status string
}

func (f fakeStatusGit) Status(ctx context.Context, dir string) (string, error) {
	return f.status, nil
}

// TestRecordQAStallStopsSpinningLoop valida o achado #13: reprovações
// consecutivas do QA sem nenhuma alteração na entrega encerram o loop na
// terceira, em vez de girar até o max_qa_loops.
func TestRecordQAStallStopsSpinningLoop(t *testing.T) {
	r := &Runner{Git: fakeStatusGit{status: " M a.go"}, deliveryRoot: "wt"}

	// 1ª reprovação: registra o estado, ainda não para.
	if got := r.recordQAStall(context.Background()); got != "" {
		t.Fatalf("primeira reprovação não deveria parar o loop: %q", got)
	}
	// 2ª reprovação consecutiva sem mudanças: uma chance a mais para os devs.
	if got := r.recordQAStall(context.Background()); got != "" {
		t.Fatalf("segunda reprovação não deveria parar o loop: %q", got)
	}
	// 3ª consecutiva com a entrega imutável: para e explica.
	got := r.recordQAStall(context.Background())
	if !strings.Contains(got, "sem nenhuma alteração nova") {
		t.Fatalf("terceira reprovação deveria encerrar o loop com motivo; got = %q", got)
	}
	if !strings.Contains(got, "interrompido") {
		t.Fatalf("mensagem deveria explicar a interrupção; got = %q", got)
	}

	// Devs entregaram algo novo: contador zera e o ciclo recomeça limpo.
	r.Git = fakeStatusGit{status: " M a.go\n M b.go"}
	if got := r.recordQAStall(context.Background()); got != "" {
		t.Fatalf("com entrega nova não deveria parar: %q", got)
	}
	r.Git = fakeStatusGit{status: " M a.go\n M b.go"}
	if got := r.recordQAStall(context.Background()); got != "" {
		t.Fatalf("primeira re-reprovação pós-mudança não deveria parar: %q", got)
	}
}

// TestRecordQAStallNeedsDeliveryRoot garante que sem worktree de entrega
// (ex.: workspace sem git) o backpressure não interfere.
func TestRecordQAStallNeedsDeliveryRoot(t *testing.T) {
	r := &Runner{Git: fakeStatusGit{status: "x"}, deliveryRoot: ""}
	if got := r.recordQAStall(context.Background()); got != "" {
		t.Fatalf("sem deliveryRoot não deveria agir: %q", got)
	}
}

func TestRecordQAStallEmptyWorktreeGivesOneRetryThenStops(t *testing.T) {
	r := &Runner{Git: fakeStatusGit{status: ""}, deliveryRoot: "wt"}

	// 1ª reprovação com worktree vazia: registra estado inicial e dá 1 chance ao Dev.
	if got := r.recordQAStall(context.Background()); got != "" {
		t.Fatalf("primeira reprovação com worktree vazia deveria dar 1 chance: %q", got)
	}
	if r.qaStallFired {
		t.Fatal("qaStallFired não deve disparar na 1ª reprovação")
	}

	// 2ª reprovação consecutiva ainda vazia: interrompe na hora em vez de girar 3 rodadas vazias.
	got := r.recordQAStall(context.Background())
	if !strings.Contains(got, "sem nenhuma alteração nova") || !strings.Contains(got, "interrompido") {
		t.Fatalf("segunda reprovação consecutiva ainda vazia deveria interromper o ciclo: %q", got)
	}
	if !r.qaStallFired {
		t.Fatal("qaStallFired deve estar true após interrupção")
	}
}
