package idecore

import (
	"context"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/loopx"
	"github.com/gilvan-Borges/HiveMind/internal/resident"
)

type okExec struct{}

func (okExec) Run(context.Context, loopx.ExecRequest) (loopx.ExecResult, error) {
	return loopx.ExecResult{Summary: "feito"}, nil
}

type okCmd struct{}

func (okCmd) Run(context.Context, string, string, time.Duration) (int, string, error) {
	return 0, "ok", nil
}

func TestGoalWithoutCheckIsRefused(t *testing.T) {
	m := NewResidentManager(ResidentManagerOptions{BaseDir: t.TempDir()})
	t.Cleanup(m.Close)
	ws := t.TempDir()
	_, err := m.RunTurn(context.Background(), ws, "geral", "/goal ficar verde", resident.OriginLocal)
	if err == nil {
		t.Fatal("sem check do host o loop não começa")
	}
}

func TestGoalCommandFinishesOnHostCheck(t *testing.T) {
	m := NewResidentManager(ResidentManagerOptions{BaseDir: t.TempDir()})
	t.Cleanup(m.Close)
	m.testLoopExec = okExec{}
	m.testLoopCmd = okCmd{}
	ws := t.TempDir()
	res, err := m.RunTurn(context.Background(), ws, "geral", `/goal ficar verde --max 2 --check "go test ./..."`, resident.OriginLocal)
	if err != nil {
		t.Fatal(err)
	}
	if res.FinalText == "" {
		t.Fatal("resposta vazia")
	}
	deadline := time.Now().Add(2 * time.Second)
	var st loopx.State
	for time.Now().Before(deadline) {
		st, err = loopx.LoadState(ws, "ficar-verde")
		if err == nil && st.Status == loopx.StatusFinished {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if st.Outcome != loopx.OutcomeDone || st.Iteration != 1 {
		t.Fatalf("loop: %+v err=%v", st, err)
	}
}
