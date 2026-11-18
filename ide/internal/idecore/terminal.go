package idecore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// CommandOutput captures the result of running a shell command in the integrated IDE terminal.
type CommandOutput struct {
	Command    string `json:"command"`
	Output     string `json:"output"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
}

// RunWorkspaceCommand runs a user-initiated command inside workspace with CREATE_NO_WINDOW.
func RunWorkspaceCommand(ctx context.Context, workspace, command string) (CommandOutput, error) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return CommandOutput{}, fmt.Errorf("informe um comando para executar")
	}
	absRoot, err := filepath.Abs(workspace)
	if err != nil {
		return CommandOutput{}, err
	}

	start := time.Now()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", trimmed)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", trimmed)
	}
	hideWindow(cmd)
	cmd.Dir = absRoot

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	runErr := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	out := buf.String()
	const maxBytes = 64 * 1024
	if len(out) > maxBytes {
		out = out[:maxBytes] + "\n…(saída truncada em 64 KB)"
	}

	return CommandOutput{
		Command:    trimmed,
		Output:     out,
		ExitCode:   exitCode,
		DurationMs: elapsed,
	}, nil
}
