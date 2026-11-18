package shellx

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Runner executes a command line inside dir after the policy check.
type Runner interface {
	Run(ctx context.Context, dir, command string, allowlist []string, confirm Confirmer) (string, error)
}

// Local is the process runner used by the CLI.
type Local struct{}

// Run executes command. Unknown binaries are refused. Dangerous commands run
// only when confirm returns true.
func (Local) Run(ctx context.Context, dir, command string, allowlist []string, confirm Confirmer) (string, error) {
	dec := Evaluate(command, allowlist)
	if !dec.Allowed {
		return "", fmt.Errorf("comando recusado: %s", dec.Reason)
	}
	if dec.Dangerous {
		if confirm == nil || !confirm(command) {
			return "", fmt.Errorf("comando perigoso recusado: %s", dec.Reason)
		}
	}
	var cmd *exec.Cmd
	if dec.UseShell {
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command)
		} else {
			cmd = exec.CommandContext(ctx, "sh", "-c", command)
		}
	} else {
		cmd = exec.CommandContext(ctx, dec.Argv[0], dec.Argv[1:]...)
	}
	cmd.Dir = dir
	hideWindow(cmd)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := truncate(buf.String(), 16*1024)
	if err != nil {
		return out, fmt.Errorf("%s: %w", strings.TrimSpace(out), err)
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…(saída truncada)"
}
