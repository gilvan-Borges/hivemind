package shellx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/procx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

// DefaultTimeout bounds a single shell command when the caller's context has no
// deadline. Without it, a hung build or a command waiting for input blocks the
// whole team run (and its parallel developers) indefinitely.
const DefaultTimeout = 20 * time.Minute

const (
	maxCapturedOutput = 1 << 20 // keep at most 1 MiB in memory
	maxReturnedOutput = 16 << 10
)

// Runner executes a command line inside dir after the policy check.
type Runner interface {
	Run(ctx context.Context, dir, command string, allowlist []string, confirm Confirmer) (string, error)
}

// Local is the process runner used by the CLI.
type Local struct{}

// Run executes command. Unknown binaries are refused. Dangerous commands run
// only when confirm returns true. The child process receives a sanitized
// environment (no provider/Jira/GitHub credentials) and a bounded runtime.
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

	runCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}

	var cmd *exec.Cmd
	if dec.UseShell {
		if runtime.GOOS == "windows" {
			psCmd := sanitizeWindowsPowerShellCommand(command)
			cmd = exec.CommandContext(runCtx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psCmd)
		} else {
			cmd = exec.CommandContext(runCtx, "sh", "-c", command)
		}
	} else {
		argv := append([]string(nil), dec.Argv...)
		argv[0] = ResolveLocalExecutable(dir, argv[0])
		cmd = exec.CommandContext(runCtx, argv[0], argv[1:]...)
	}
	cmd.Dir = dir
	cmd.Env = SanitizedEnv()
	procx.ConfigureContext(cmd)

	out := &cappedBuffer{limit: maxCapturedOutput}
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	text := strx.TruncateHeadTailBytes(out.String(), 4<<10, 12<<10)
	if runCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		return text, fmt.Errorf("comando excedeu o tempo limite de %s: %s", DefaultTimeout, strings.TrimSpace(text))
	}
	if err != nil {
		return text, fmt.Errorf("%s: %w", strings.TrimSpace(text), err)
	}
	return text, nil
}

// sanitizeWindowsPowerShellCommand strips redundant `2>&1` redirections (which
// cause PowerShell 5.1 to wrap native stderr lines like `git push` progress in
// NativeCommandError/RemoteException) since Stdout and Stderr are already merged
// into cappedBuffer, translates POSIX `&&` outside quotes into PowerShell 5.1
// conditional execution, and preserves the native $LASTEXITCODE.
func sanitizeWindowsPowerShellCommand(command string) string {
	cleaned := strings.ReplaceAll(command, " 2>&1", "")
	cleaned = strings.ReplaceAll(cleaned, "2>&1", "")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return command
	}
	if parts := splitOutsideQuotes(cleaned, "&&"); len(parts) > 1 {
		var b strings.Builder
		b.WriteString("$global:LASTEXITCODE = 0; ")
		b.WriteString(strings.TrimSpace(parts[0]))
		for _, p := range parts[1:] {
			b.WriteString("; if ($? -and ($null -eq $LASTEXITCODE -or $LASTEXITCODE -eq 0)) { ")
			b.WriteString(strings.TrimSpace(p))
		}
		for i := 1; i < len(parts); i++ {
			b.WriteString(" }")
		}
		cleaned = b.String()
	}
	return "$ErrorActionPreference = 'Continue'; " + cleaned + "; if ($LASTEXITCODE -is [int]) { exit $LASTEXITCODE }"
}

func splitOutsideQuotes(s, sep string) []string {
	var (
		parts   []string
		start   int
		inS     bool
		inD     bool
		escaped bool
	)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '`' && inD {
			escaped = true
			continue
		}
		if ch == '\'' && !inD {
			inS = !inS
			continue
		}
		if ch == '"' && !inS {
			inD = !inD
			continue
		}
		if !inS && !inD && strings.HasPrefix(s[i:], sep) {
			part := strings.TrimSpace(s[start:i])
			if part != "" {
				parts = append(parts, part)
			}
			i += len(sep) - 1
			start = i + 1
		}
	}
	if tail := strings.TrimSpace(s[start:]); tail != "" {
		parts = append(parts, tail)
	}
	return parts
}

// cappedBuffer is an io.Writer safe for the concurrent stdout/stderr writers
// used by os/exec. It keeps at most limit bytes so a command that prints
// gigabytes cannot exhaust the process memory.
type cappedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	remaining := c.limit - c.buf.Len()
	if remaining <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = c.buf.Write(p[:remaining])
		c.truncated = true
		return len(p), nil
	}
	_, _ = c.buf.Write(p)
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.truncated {
		return c.buf.String() + "\n…(saída excedeu o limite de captura)"
	}
	return c.buf.String()
}

// resolveLocalExecutable lets an allowlisted bare name (ex.: gradlew.bat,
// ./gradlew, run_tests) execute the file of the same name that lives in the
// working directory. Go resolves executables against PATH (and the process
// cwd), never against cmd.Dir — so wrappers like gradlew.bat failed with
// "executable file not found" even when sitting right next to the agent
// (dogfooding achado #5: ~6 tentativas e 436k tokens só para rodar o build).
// On Windows a name without extension also probes .bat/.cmd/.exe variants.
// ResolveLocalExecutable finds an allowlisted bare name inside dir.
func ResolveLocalExecutable(dir, name string) string {
	if dir == "" || name == "" {
		return name
	}
	// aceita "gradlew.bat", "./gradlew" e ".\gradlew.bat" — mas não caminhos
	// com diretório no meio (tools/x.bat), que seguem para o PATH/relativo.
	clean := strings.ReplaceAll(strings.ReplaceAll(name, `\`, "/"), "./", "")
	if clean == "" || strings.ContainsRune(clean, '/') {
		return name
	}
	if runtime.GOOS == "windows" {
		for _, cand := range []string{clean, clean + ".bat", clean + ".cmd", clean + ".exe"} {
			if p := filepath.Join(dir, cand); isRegularFile(p) {
				return p
			}
		}
		return name
	}
	if p := filepath.Join(dir, clean); isRegularFile(p) {
		return p
	}
	return name
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// RunDirect executes argv with the process environment and no allowlist.
// The Hive Full permission mode is the caller. Governed paths keep using Run.
func RunDirect(ctx context.Context, dir string, argv []string) (stdout, stderr string, exitCode int, err error) {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return "", "", -1, fmt.Errorf("comando vazio")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	procx.ConfigureContext(cmd)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	exitCode = -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return outBuf.String(), errBuf.String(), exitCode, err
}
