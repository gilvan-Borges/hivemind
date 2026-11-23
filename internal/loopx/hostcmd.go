package loopx

import (
	"context"
	"runtime"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/sandboxx"
	"github.com/gilvan-Borges/HiveMind/internal/shellx"
)

type hostFullKey struct{}

// WithHostFull marks a check context as allowed to run on the host.
// Governed loops stay in the sandbox and fail closed when it is missing.
func WithHostFull(ctx context.Context, full bool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, hostFullKey{}, full)
}

func hostFull(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(hostFullKey{}).(bool)
	return v
}

// HostRunner runs a spec check. Full uses the host process. Governed uses the OS sandbox.
type HostRunner struct {
	Sandbox sandboxx.Runner
}

// Run executes one shell command from the spec. The command is not chosen by the model.
func (h HostRunner) Run(ctx context.Context, dir, command string, timeout time.Duration) (int, string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return -1, "", errEmptyCommand
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	argv := shellArgv(command)
	if hostFull(ctx) {
		stdout, stderr, exit, err := shellx.RunDirect(runCtx, dir, argv)
		return exit, Tail(stdout+"\n"+stderr, 8000), ignoreExit(err)
	}
	sb := h.Sandbox
	if sb == nil {
		sb = sandboxx.NewOSRunner()
	}
	if !sb.Probe().Available {
		return -1, "", errSandboxClosed
	}
	res, err := sb.Run(runCtx, sandboxx.Options{
		Argv:         argv,
		WorkDir:      dir,
		ReadRoots:    []string{dir},
		WriteRoots:   []string{dir},
		AllowNetwork: true,
		Timeout:      timeout,
	})
	return res.ExitCode, Tail(res.Stdout+"\n"+res.Stderr, 8000), ignoreExit(err)
}

func shellArgv(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/C", command}
	}
	return []string{"sh", "-c", command}
}

func ignoreExit(err error) error {
	if err == nil {
		return nil
	}
	// A non-zero exit is a check result, not a runner failure.
	if _, ok := err.(interface{ ExitCode() int }); ok {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "exit status") {
		return nil
	}
	return err
}

type cmdError string

func (e cmdError) Error() string { return string(e) }

const (
	errEmptyCommand  cmdError = "comando de check vazio"
	errSandboxClosed cmdError = "check do host recusado: sandbox indisponível no modo governado"
)
