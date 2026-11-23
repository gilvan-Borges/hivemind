package loopx

import (
	"testing"
	"time"
)

func limits() Limits {
	return Limits{
		MaxIterations:   10,
		MaxDuration:     2 * time.Hour,
		MaxCostMicroUSD: 5_000_000,
		StallAfter:      3,
	}
}

func TestDecideDoneRequiresHostChecks(t *testing.T) {
	s := Snapshot{ChecksOK: true, IntegrityOK: true, Judge: JudgeSkipped, Limits: limits(), Iterations: 1}
	if got := Decide(s); got != OutcomeDone {
		t.Fatalf("done = %s", got)
	}
	s.ChecksOK = false
	if got := Decide(s); got != OutcomeContinue {
		t.Fatalf("sem check = %s", got)
	}
}

func TestDecideDoneBeatsBudget(t *testing.T) {
	s := Snapshot{
		ChecksOK: true, IntegrityOK: true, Judge: JudgeSkipped,
		Iterations: 10, Limits: limits(),
	}
	if got := Decide(s); got != OutcomeDone {
		t.Fatalf("feito no teto = %s", got)
	}
}

func TestDecideBudget(t *testing.T) {
	base := Snapshot{IntegrityOK: true, Judge: JudgeSkipped, Limits: limits()}
	cases := []Snapshot{
		func() Snapshot { s := base; s.Iterations = 10; return s }(),
		func() Snapshot { s := base; s.Elapsed = 2 * time.Hour; return s }(),
		func() Snapshot { s := base; s.SpentMicroUSD = 5_000_000; return s }(),
		func() Snapshot {
			s := base
			s.Limits.MaxTokens = 100
			s.SpentTokens = 100
			return s
		}(),
	}
	for i, s := range cases {
		if got := Decide(s); got != OutcomeBudgetExhausted {
			t.Fatalf("caso %d = %s", i, got)
		}
	}
}

func TestDecideStalledAfterQuietIterations(t *testing.T) {
	s := Snapshot{IntegrityOK: true, Judge: JudgeSkipped, ConsecutiveNoProgress: 3, Iterations: 3, Limits: limits()}
	if got := Decide(s); got != OutcomeStalled {
		t.Fatalf("stalled = %s", got)
	}
	s.ConsecutiveNoProgress = 2
	if got := Decide(s); got != OutcomeContinue {
		t.Fatalf("ainda anda = %s", got)
	}
}

func TestDecideImpossibleNeedsTwo(t *testing.T) {
	s := Snapshot{IntegrityOK: true, ChecksOK: true, Judge: JudgeImpossible, JudgeRequired: true, ConsecutiveImpossible: 1, Limits: limits()}
	if got := Decide(s); got != OutcomeContinue {
		t.Fatalf("uma vez = %s", got)
	}
	s.ConsecutiveImpossible = 2
	if got := Decide(s); got != OutcomeImpossible {
		t.Fatalf("duas vezes = %s", got)
	}
}

func TestDecideBlockedAndJudgeGate(t *testing.T) {
	s := Snapshot{ChecksOK: true, IntegrityOK: true, Blocked: true, Limits: limits()}
	if got := Decide(s); got != OutcomeBlocked {
		t.Fatalf("blocked = %s", got)
	}
	s.Blocked = false
	s.JudgeRequired = true
	s.Judge = JudgeNotMet
	if got := Decide(s); got != OutcomeContinue {
		t.Fatalf("juiz recusou = %s", got)
	}
	s.Judge = JudgeMet
	if got := Decide(s); got != OutcomeDone {
		t.Fatalf("juiz aceitou = %s", got)
	}
}

func TestNoteProgress(t *testing.T) {
	var st State
	NoteProgress(&st, "a", true, 0)
	if st.ConsecutiveNoProgress != 0 {
		t.Fatalf("primeira marca de árvore conta como progresso: %+v", st)
	}
	NoteProgress(&st, "a", true, 0)
	NoteProgress(&st, "a", true, 0)
	if st.ConsecutiveNoProgress != 2 {
		t.Fatalf("sem mudança = %d", st.ConsecutiveNoProgress)
	}
	NoteProgress(&st, "a", true, 1)
	if st.ConsecutiveNoProgress != 0 || st.PassingChecks != 1 {
		t.Fatalf("check novo deveria zerar: %+v", st)
	}
}
