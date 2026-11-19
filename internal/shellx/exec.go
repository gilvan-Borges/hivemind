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

	"github.com/gilvan-Borges/HiveMind/internal/procx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
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
		argv := append([]string(nil), dec.Argv...)
		argv[0] = resolveLocalExecutable(dir, argv[0])
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
	}
	cmd.Dir = dir
	procx.ConfigureContext(cmd)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := strx.TruncateBytes(buf.String(), 16*1024, "\n…(saída truncada)")
	if err != nil {
		return out, fmt.Errorf("%s: %w", strings.TrimSpace(out), err)
	}
	return out, nil
}

// resolveLocalExecutable lets an allowlisted bare name (ex.: gradlew.bat,
// ./gradlew, run_tests) execute the file of the same name that lives in the
// working directory. Go resolves executables against PATH (and the process
// cwd), never against cmd.Dir — so wrappers like gradlew.bat failed with
// "executable file not found" even when sitting right next to the agent
// (dogfooding achado #5: ~6 tentativas e 436k tokens só para rodar o build).
// On Windows a name without extension also probes .bat/.cmd/.exe variants.
func resolveLocalExecutable(dir, name string) string {
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
