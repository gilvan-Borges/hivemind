package loopx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/sandboxx"
)

type closedSandbox struct{}

func (closedSandbox) Probe() sandboxx.ProbeResult {
	return sandboxx.ProbeResult{Available: false, Reason: "teste"}
}

func (closedSandbox) Run(context.Context, sandboxx.Options) (sandboxx.Result, error) {
	return sandboxx.Result{}, errors.New("não devia rodar")
}

func TestHostRunnerFullRunsAndGovernedFailsClosed(t *testing.T) {
	dir := t.TempDir()
	h := HostRunner{Sandbox: closedSandbox{}}
	ctx := WithHostFull(context.Background(), true)
	exit, _, err := h.Run(ctx, dir, "true", 5*time.Second)
	if err != nil || exit != 0 {
		t.Fatalf("full true: exit=%d err=%v", exit, err)
	}
	exit, _, err = h.Run(context.Background(), dir, "true", 5*time.Second)
	if err == nil {
		t.Fatalf("governado sem sandbox deveria recusar, exit=%d", exit)
	}
}
