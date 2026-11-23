package idecore

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/loopx"
	"github.com/gilvan-Borges/HiveMind/internal/resident"
)

type captureExec struct {
	mu    sync.Mutex
	full  bool
	calls int
}

func (c *captureExec) Run(_ context.Context, req loopx.ExecRequest) (loopx.ExecResult, error) {
	c.mu.Lock()
	c.full = req.Full
	c.calls++
	c.mu.Unlock()
	return loopx.ExecResult{Summary: "ok"}, nil
}

func (c *captureExec) snapshot() (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, c.full
}

func waitSettled(t *testing.T, ws, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st, err := loopx.LoadState(ws, id)
		if err == nil && st.Status != loopx.StatusRunning && st.Iteration > 0 {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	st, _ := loopx.LoadState(ws, id)
	t.Fatalf("loop não assentou: %+v", st)
}

func waitCalls(t *testing.T, c *captureExec, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := c.snapshot()
		if got >= n {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	got, full := c.snapshot()
	t.Fatalf("chamadas=%d full=%v", got, full)
}

func waitingLoop(t *testing.T, m *ResidentManager, ws, id, kind string, events []string) {
	t.Helper()
	off := false
	spec := loopx.Spec{
		ID: id, Kind: kind, Objective: "vigiar",
		Checks:  []loopx.Check{{Name: "unit", Run: "go test"}},
		Judge:   loopx.JudgeSpec{Enabled: &off},
		Trigger: loopx.TriggerSpec{Events: events},
		Limits:  loopx.LimitSpec{MaxIterations: 4, StallAfter: 10},
	}
	if _, err := m.loopController().Start(ws, spec, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := loopx.UpdateState(ws, id, func(st *loopx.State) error {
		st.Status = loopx.StatusWaiting
		st.Outcome = ""
		st.StopRequested = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUnattendedFullNeedsTheSettingsSwitch(t *testing.T) {
	m := NewResidentManager(ResidentManagerOptions{BaseDir: t.TempDir()})
	t.Cleanup(m.Close)
	ws := t.TempDir()
	cap := &captureExec{}
	m.testLoopExec = cap
	m.testLoopCmd = okCmd{}
	if _, err := m.SetFullAccess(true); err != nil {
		t.Fatal(err)
	}
	waitingLoop(t, m, ws, "cron", loopx.KindTime, nil)
	if err := m.WakeLoop(ws, "cron"); err != nil {
		t.Fatal(err)
	}
	waitCalls(t, m.testLoopExec.(*captureExec), 1)
	waitSettled(t, ws, "cron")
	if _, full := cap.snapshot(); full {
		t.Fatal("cron em Full sem allow_full_unattended deve ficar governado")
	}
	if _, err := loopx.UpdateState(ws, "cron", func(st *loopx.State) error {
		st.Status = loopx.StatusWaiting
		st.Outcome = ""
		st.StopRequested = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetAllowFullUnattended(true); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); !st.AllowFullUnattended {
		t.Fatal("o interruptor não gravou")
	}
	if err := m.WakeLoop(ws, "cron"); err != nil {
		t.Fatal(err)
	}
	waitCalls(t, cap, 2)
	if _, full := cap.snapshot(); !full {
		t.Fatal("com o interruptor o cron acompanha o Full")
	}
}

func TestEventWakeIsOptIn(t *testing.T) {
	m := NewResidentManager(ResidentManagerOptions{BaseDir: t.TempDir()})
	t.Cleanup(m.Close)
	ws := t.TempDir()
	cap := &captureExec{}
	m.testLoopExec = cap
	m.testLoopCmd = okCmd{}
	m.projectsFn = func() []resident.ProjectRef { return []resident.ProjectRef{{Root: ws}} }
	waitingLoop(t, m, ws, "ao-salvar", loopx.KindProactive, []string{"arquivo"})
	m.NoteHostEvent("arquivo")
	time.Sleep(40 * time.Millisecond)
	if calls, _ := cap.snapshot(); calls != 0 {
		t.Fatalf("gatilho desligado acordou o loop: %d", calls)
	}
	if _, err := m.SetEventTriggers(true); err != nil {
		t.Fatal(err)
	}
	m.NoteHostEvent("lane")
	time.Sleep(40 * time.Millisecond)
	if calls, _ := cap.snapshot(); calls != 0 {
		t.Fatalf("evento fora da spec acordou o loop: %d", calls)
	}
	m.NoteHostEvent("arquivo")
	waitCalls(t, cap, 1)
}

func TestLoopCommandArmsARoutine(t *testing.T) {
	base := t.TempDir()
	m := NewResidentManager(ResidentManagerOptions{BaseDir: base})
	t.Cleanup(m.Close)
	m.testLoopExec = okExec{}
	m.testLoopCmd = okCmd{}
	m.testLoopJudge = metJudge{}
	ws := t.TempDir()
	_, err := m.RunTurn(context.Background(), ws, "geral", `/loop 10m vigiar o CI --check "go test ./..." --no-judge`, resident.OriginLocal)
	if err != nil {
		t.Fatal(err)
	}
	list, err := resident.LoadRoutines(base)
	if err != nil || len(list) != 1 || list[0].LoopID == "" || list[0].Expr != "*/10 * * * *" || list[0].Workspace != ws {
		t.Fatalf("rotina: %+v %v", list, err)
	}
}
