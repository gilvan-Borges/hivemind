//go:build !windows && !linux && !darwin

package sandboxx

import (
	"context"
	"fmt"
	"runtime"
)

func probePlatform() ProbeResult {
	return ProbeResult{
		Available: false,
		OS:        runtime.GOOS,
		Mechanism: "unsupported",
		Reason:    fmt.Sprintf("sistema operacional %s não possui sandbox nativa implementada", runtime.GOOS),
	}
}

func runPlatform(_ context.Context, _ Options) (Result, error) {
	return Result{Mechanism: "unsupported"}, fmt.Errorf("%w: sistema %s não suportado", ErrSandboxUnavailable, runtime.GOOS)
}
