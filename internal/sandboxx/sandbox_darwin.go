//go:build darwin

package sandboxx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const mechanismDarwin = "seatbelt"

func probePlatform() ProbeResult {
	path, err := exec.LookPath("sandbox-exec")
	if err != nil || strings.TrimSpace(path) == "" {
		return ProbeResult{
			Available: false,
			OS:        "darwin",
			Mechanism: mechanismDarwin,
			Reason:    "sandbox-exec indisponível no macOS",
		}
	}
	return ProbeResult{
		Available: true,
		OS:        "darwin",
		Mechanism: mechanismDarwin,
	}
}

func runPlatform(ctx context.Context, opts Options) (Result, error) {
	sbPath, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return Result{Mechanism: mechanismDarwin}, fmt.Errorf("%w: sandbox-exec ausente: %v", ErrSandboxUnavailable, err)
	}
	resolvedExe, err := exec.LookPath(opts.Argv[0])
	if err != nil {
		return Result{Mechanism: mechanismDarwin}, fmt.Errorf("executável %q não encontrado: %w", opts.Argv[0], err)
	}

	profile := buildSeatbeltProfile(opts)
	runCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	args := append([]string{"-p", profile, resolvedExe}, opts.Argv[1:]...)
	cmd := exec.CommandContext(runCtx, sbPath, args...)
	cmd.Dir = opts.WorkDir
	cmd.Env = sanitizedEnv(opts.ExtraEnv)

	var stdout, stderr cappedBuffer
	stdout.max = opts.MaxOutputBytes
	stderr.max = opts.MaxOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	started := time.Now()
	err = cmd.Run()
	res := Result{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		Duration:  time.Since(started),
		TimedOut:  errors.Is(runCtx.Err(), context.DeadlineExceeded),
		Mechanism: mechanismDarwin,
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, fmt.Errorf("processo na sandbox encerrou com código %d", res.ExitCode)
		}
		res.ExitCode = -1
		return res, fmt.Errorf("falha na sandbox Seatbelt: %w", err)
	}
	return res, nil
}

func buildSeatbeltProfile(opts Options) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(deny default)\n")
	b.WriteString("(allow process-exec process-fork signal (target self))\n")
	b.WriteString("(allow sysctl-read mach-lookup)\n")
	b.WriteString("(allow file-read* (subpath \"/usr/lib\") (subpath \"/usr/bin\") (subpath \"/bin\") (subpath \"/System/Library\") (subpath \"/dev\"))\n")
	b.WriteString("(allow file-write* (literal \"/dev/null\") (subpath \"/private/var/tmp\") (subpath \"/tmp\"))\n")
	for _, r := range opts.ReadRoots {
		fmt.Fprintf(&b, "(allow file-read* (subpath %q))\n", r)
	}
	for _, w := range opts.WriteRoots {
		fmt.Fprintf(&b, "(allow file-read* file-write* (subpath %q))\n", w)
	}
	if !opts.AllowNetwork {
		b.WriteString("(deny network*)\n")
	} else {
		b.WriteString("(allow network-outbound)\n")
	}
	return b.String()
}

type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.max <= 0 {
		return len(p), nil
	}
	rem := c.max - c.buf.Len()
	if rem > 0 {
		if len(p) <= rem {
			c.buf.Write(p)
		} else {
			c.buf.Write(p[:rem])
		}
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	return c.buf.String()
}
