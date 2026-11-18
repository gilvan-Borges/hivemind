package idecore

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/procx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
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
	return RunWorkspaceCommandStream(ctx, workspace, command, nil)
}

// RunWorkspaceCommandStream runs a user-initiated command inside workspace, streaming
// incremental output lines through onChunk (when non-nil) and returning the final output.
func RunWorkspaceCommandStream(ctx context.Context, workspace, command string, onChunk func(chunk string)) (CommandOutput, error) {
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
	procx.ConfigureContext(cmd)
	cmd.Dir = absRoot

	if onChunk == nil {
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf

		runErr := cmd.Run()
		elapsed := time.Since(start).Milliseconds()

		exitCode := resolveExitCode(runErr)
		const maxBytes = 64 * 1024
		out := strx.TruncateBytes(buf.String(), maxBytes, "\n…(saída truncada em 64 KB)")
		return CommandOutput{
			Command:    trimmed,
			Output:     out,
			ExitCode:   exitCode,
			DurationMs: elapsed,
		}, nil
	}

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return CommandOutput{}, err
	}

	var buf bytes.Buffer
	var bufMu sync.Mutex
	const maxBytes = 64 * 1024
	doneRead := make(chan struct{})
	go func() {
		defer close(doneRead)
		reader := bufio.NewReaderSize(pr, 4096)
		for {
			line, rErr := reader.ReadString('\n')
			if len(line) > 0 {
				bufMu.Lock()
				if buf.Len() < maxBytes+16 {
					buf.WriteString(line)
				}
				bufMu.Unlock()
				onChunk(line)
			}
			if rErr != nil {
				break
			}
		}
	}()

	runErr := cmd.Wait()
	_ = pw.Close()
	<-doneRead
	_ = pr.Close()

	elapsed := time.Since(start).Milliseconds()
	exitCode := resolveExitCode(runErr)

	bufMu.Lock()
	rawOut := buf.String()
	bufMu.Unlock()
	out := strx.TruncateBytes(rawOut, maxBytes, "\n…(saída truncada em 64 KB)")

	return CommandOutput{
		Command:    trimmed,
		Output:     out,
		ExitCode:   exitCode,
		DurationMs: elapsed,
	}, nil
}

func resolveExitCode(runErr error) int {
	if runErr == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
