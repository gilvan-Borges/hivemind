package loopx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type scriptExec struct {
	summary string
	cost    int64
	tokens  int64
	human   bool
	err     error
	calls   int
	last    ExecRequest
}

func (s *scriptExec) Run(_ context.Context, req ExecRequest) (ExecResult, error) {
	s.calls++
	s.last = req
	if s.err != nil {
		return ExecResult{NeedsHuman: s.human, Summary: s.summary}, s.err
	}
	return ExecResult{Summary: s.summary, CostMicroUSD: s.cost, Tokens: s.tokens}, nil
}

type scriptCmd struct{ exit int }

func (s scriptCmd) Run(context.Context, string, string, time.Duration) (int, string, error) {
	if s.exit == 0 {
		return 0, "ok", nil
	}
	return s.exit, "falhou", nil
}

func TestGoalLoopStopsWhenChecksPass(t *testing.T) {
	ws := t.TempDir()
	exec := &scriptExec{summary: "teste verde"}
	var kinds []string
	c := &Controller{
		Executor: exec,
		Commands: scriptCmd{},
		Full:     func(string, bool, string) bool { return true },
		OnEvent:  func(ev Event) { kinds = append(kinds, ev.Kind) },
	}
	off := false
	spec := Spec{
		ID: "fix-auth", Kind: KindGoal, Objective: "go test passa",
		Checks: []Check{{Name: "unit", Run: "go test ./..."}},
		Judge:  JudgeSpec{Enabled: &off},
		Limits: LimitSpec{MaxIterations: 5},
	}
	if _, err := c.Start(ws, spec, "conv", "sess"); err != nil {
		t.Fatal(err)
	}
	st, err := c.Run(context.Background(), ws, "fix-auth", RunOptions{Accompanied: true, ConversationID: "conv"})
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != StatusFinished || st.Outcome != OutcomeDone || st.Iteration != 1 {
		t.Fatalf("estado: %+v", st)
	}
	if exec.calls != 1 || !exec.last.Full || !strings.Contains(exec.last.Prompt, "go test passa") {
		t.Fatalf("exec: calls=%d full=%v prompt=%q", exec.calls, exec.last.Full, exec.last.Prompt)
	}
	if !containsAll(kinds, "loop_started", "loop_iteration", "loop_check", "loop_verdict", "loop_finished") {
		t.Fatalf("eventos: %v", kinds)
	}
	rows, err := ReadIterations(ws, "fix-auth", 0)
	if err != nil || len(rows) != 1 || !rows[0].ChecksOK {
		t.Fatalf("jsonl: %+v %v", rows, err)
	}
}

func TestGoalLoopExhaustsBudgetWhenChecksFail(t *testing.T) {
	ws := t.TempDir()
	exec := &scriptExec{summary: "ainda vermelho"}
	c := &Controller{Executor: exec, Commands: scriptCmd{exit: 1}}
	off := false
	spec := Spec{
		ID: "impossivel", Kind: KindGoal, Objective: "não tem como",
		Checks: []Check{{Name: "unit", Run: "go test ./..."}},
		Judge:  JudgeSpec{Enabled: &off},
		Limits: LimitSpec{MaxIterations: 2, StallAfter: 10},
	}
	if _, err := c.Start(ws, spec, "", ""); err != nil {
		t.Fatal(err)
	}
	st, err := c.Run(context.Background(), ws, "impossivel", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Outcome != OutcomeBudgetExhausted || st.Iteration != 2 || exec.calls != 2 {
		t.Fatalf("estado: %+v calls=%d", st, exec.calls)
	}
}

func TestGoalLoopStallsWithoutProgress(t *testing.T) {
	ws := t.TempDir()
	c := &Controller{Executor: &scriptExec{summary: "igual"}, Commands: scriptCmd{exit: 1}}
	off := false
	spec := Spec{
		ID: "parado", Kind: KindGoal, Objective: "sem mudança",
		Checks: []Check{{Name: "unit", Run: "go test ./..."}},
		Judge:  JudgeSpec{Enabled: &off},
		Limits: LimitSpec{MaxIterations: 10, StallAfter: 3},
	}
	if _, err := c.Start(ws, spec, "", ""); err != nil {
		t.Fatal(err)
	}
	st, err := c.Run(context.Background(), ws, "parado", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Outcome != OutcomeStalled || st.Iteration != 3 {
		t.Fatalf("estado: %+v", st)
	}
}

func TestLoopBlocksWhenSpecChanges(t *testing.T) {
	ws := t.TempDir()
	c := &Controller{Executor: &scriptExec{}, Commands: scriptCmd{}}
	off := false
	spec := Spec{
		ID: "hash", Kind: KindGoal, Objective: "um",
		Checks: []Check{{Name: "unit", Run: "go test ./..."}},
		Judge:  JudgeSpec{Enabled: &off},
	}
	if _, err := c.Start(ws, spec, "", ""); err != nil {
		t.Fatal(err)
	}
	spec.Objective = "outro"
	if _, err := SaveSpec(ws, spec); err != nil {
		t.Fatal(err)
	}
	st, err := c.Run(context.Background(), ws, "hash", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Outcome != OutcomeBlocked || !strings.Contains(st.LastReason, "spec") {
		t.Fatalf("estado: %+v", st)
	}
}

func TestLoopCostCeiling(t *testing.T) {
	ws := t.TempDir()
	c := &Controller{
		Executor: &scriptExec{summary: "caro", cost: 6_000_000},
		Commands: scriptCmd{exit: 1},
	}
	off := false
	spec := Spec{
		ID: "caro", Kind: KindGoal, Objective: "estoura",
		Checks: []Check{{Name: "unit", Run: "go test ./..."}},
		Judge:  JudgeSpec{Enabled: &off},
		Limits: LimitSpec{MaxIterations: 10, MaxCostUSD: 5, StallAfter: 10},
	}
	if _, err := c.Start(ws, spec, "", ""); err != nil {
		t.Fatal(err)
	}
	st, err := c.Run(context.Background(), ws, "caro", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Outcome != OutcomeBudgetExhausted || st.Iteration != 1 {
		t.Fatalf("estado: %+v", st)
	}
}

func TestLoopBlocksForAPerson(t *testing.T) {
	ws := t.TempDir()
	c := &Controller{Executor: &scriptExec{human: true, err: errors.New("aprovação humana negada ou expirada")}, Commands: scriptCmd{}}
	off := false
	spec := Spec{
		ID: "espera", Kind: KindGoal, Objective: "precisa de alguém",
		Checks: []Check{{Name: "unit", Run: "go test ./..."}},
		Judge:  JudgeSpec{Enabled: &off},
	}
	if _, err := c.Start(ws, spec, "", ""); err != nil {
		t.Fatal(err)
	}
	st, err := c.Run(context.Background(), ws, "espera", RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Outcome != OutcomeBlocked {
		t.Fatalf("estado: %+v", st)
	}
}

func TestPauseRunningSurvivesRestart(t *testing.T) {
	ws := t.TempDir()
	c := &Controller{Executor: &scriptExec{summary: "ok"}, Commands: scriptCmd{}}
	off := false
	spec := Spec{
		ID: "vivo", Kind: KindGoal, Objective: "no meio",
		Checks: []Check{{Name: "unit", Run: "true"}},
		Judge:  JudgeSpec{Enabled: &off},
	}
	if _, err := c.Start(ws, spec, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateState(ws, "vivo", func(st *State) error {
		st.Status = StatusRunning
		st.Iteration = 2
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	n, err := PauseRunning(ws)
	if err != nil || n != 1 {
		t.Fatalf("pause %d %v", n, err)
	}
	st, err := LoadState(ws, "vivo")
	if err != nil || st.Status != StatusPaused || st.Iteration != 2 {
		t.Fatalf("não retomou sozinho: %+v %v", st, err)
	}
}

func TestFullFlagFollowsPermission(t *testing.T) {
	ws := t.TempDir()
	exec := &scriptExec{summary: "ok"}
	c := &Controller{
		Executor: exec,
		Commands: scriptCmd{},
		Full: func(string, bool, string) bool {
			return ApplyPermission("inherit", true, false, false)
		},
	}
	off := false
	spec := Spec{
		ID: "cron", Kind: KindGoal, Objective: "desacompanhado",
		Checks: []Check{{Name: "unit", Run: "go test"}},
		Judge:  JudgeSpec{Enabled: &off},
	}
	if _, err := c.Start(ws, spec, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), ws, "cron", RunOptions{Accompanied: false}); err != nil {
		t.Fatal(err)
	}
	if exec.last.Full {
		t.Fatal("loop desacompanhado não herda Full sem o interruptor")
	}
}

func containsAll(got []string, want ...string) bool {
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}
