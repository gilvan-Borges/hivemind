package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/sandboxx"
	"github.com/gilvan-Borges/HiveMind/internal/shellx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

type recordSandbox struct {
	runs int
	last sandboxx.Options
}

func (r *recordSandbox) Probe() sandboxx.ProbeResult {
	return sandboxx.ProbeResult{Available: true, Mechanism: "test"}
}

func (r *recordSandbox) Run(_ context.Context, opts sandboxx.Options) (sandboxx.Result, error) {
	r.runs++
	r.last = opts
	return sandboxx.Result{Stdout: "ok", Mechanism: "test"}, nil
}

func TestSquadShellDefaultsToSandbox(t *testing.T) {
	sh := &gateShell{}
	sb := &recordSandbox{}
	deps := applyShellPolicy(Deps{
		Root:      t.TempDir(),
		Settings:  team.DefaultSettings(),
		Shell:     sh,
		Sandbox:   sb,
		Allowlist: []string{"go"},
		Confirm:   func(string) bool { return true },
	})
	out, err := deps.runShell(context.Background(), "go test ./...", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if out != "ok" || sb.runs != 1 || sh.ran != "" {
		t.Fatalf("padrão deveria ser a sandbox: out=%q runs=%d host=%q", out, sb.runs, sh.ran)
	}
	if len(sb.last.ExtraEnv) != 0 || !sb.last.AllowNetwork {
		t.Fatalf("sandbox: extra=%v rede=%v", sb.last.ExtraEnv, sb.last.AllowNetwork)
	}
	if _, err := deps.runShell(context.Background(), "go test && go vet", "", false); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("metacaractere no padrão: %v", err)
	}
	if sh.ran != "" || sb.runs != 1 {
		t.Fatalf("metacaractere não entra na sandbox host=%q runs=%d", sh.ran, sb.runs)
	}
}

func TestSquadShellUnsandboxedNeedsConfirmOrFullAccess(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	restore := shellx.SetShellAuditPathForTest(auditPath)
	t.Cleanup(restore)

	sh := &gateShell{}
	sb := &recordSandbox{}
	settings := team.DefaultSettings()
	asked := 0
	deps := applyShellPolicy(Deps{
		Root:     t.TempDir(),
		Settings: settings,
		Shell:    sh,
		Sandbox:  sb,
		Confirm: func(command string) bool {
			asked++
			if !strings.Contains(command, "SEM SANDBOX") {
				t.Fatalf("confirmação sem aviso: %s", command)
			}
			return false
		},
	})
	if _, err := deps.runShell(context.Background(), "go test ./...", "", true); err == nil || !strings.Contains(err.Error(), "sem sandbox") {
		t.Fatalf("governed sem confirmação: %v", err)
	}
	if asked != 1 || sh.ran != "" || sb.runs != 0 {
		t.Fatalf("não podia executar asked=%d host=%q sandbox=%d", asked, sh.ran, sb.runs)
	}

	settings.PermissionMode = team.PermissionFullAccess
	full := applyShellPolicy(Deps{
		Root:     t.TempDir(),
		Settings: settings,
		Shell:    sh,
		Sandbox:  sb,
		Confirm: func(string) bool {
			t.Fatal("Full Acesso não pede o aviso de novo")
			return false
		},
	})
	out, err := full.runShell(context.Background(), "go test ./...", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if sh.ran != "go test ./..." || !strings.Contains(out, "SEM SANDBOX") {
		t.Fatalf("host=%q out=%q", sh.ran, out)
	}
	raw, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "go test") {
		t.Fatalf("auditoria vazou o comando: %s", text)
	}
	if !strings.Contains(text, "SEM sandbox") || !strings.Contains(text, "unisolated") {
		t.Fatalf("auditoria: %s", text)
	}
}

type bigStdoutSandbox struct {
	out string
}

func (b *bigStdoutSandbox) Probe() sandboxx.ProbeResult {
	return sandboxx.ProbeResult{Available: true, Mechanism: "test"}
}

func (b *bigStdoutSandbox) Run(_ context.Context, _ sandboxx.Options) (sandboxx.Result, error) {
	return sandboxx.Result{Stdout: b.out, Mechanism: "test"}, nil
}

func TestRunShellSandboxedSavesFullOutputOnTruncate(t *testing.T) {
	full := strings.Repeat("HEAD\n", 1200) + strings.Repeat("MID\n", 5000) + "\nFAIL: exact-tail-error\n"
	deps := applyShellPolicy(Deps{
		Root:      t.TempDir(),
		Settings:  team.DefaultSettings(),
		Sandbox:   &bigStdoutSandbox{out: full},
		Allowlist: []string{"go"},
	})
	out, err := deps.runShell(context.Background(), "go test ./...", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "FAIL: exact-tail-error") {
		t.Fatalf("expected tail preserved, got: %s", out)
	}
	const marker = "[saída completa salva em: "
	idx := strings.Index(out, marker)
	if idx < 0 {
		t.Fatalf("expected saved log path in truncated output, got: %s", out)
	}
	rest := out[idx+len(marker):]
	end := strings.IndexByte(rest, ']')
	if end <= 0 {
		t.Fatalf("malformed log path line: %q", rest)
	}
	logPath := rest[:end]
	t.Cleanup(func() { _ = os.Remove(logPath) })
	saved, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading saved shell log %s: %v", logPath, err)
	}
	if string(saved) != full {
		t.Fatalf("saved shell log mismatch: len=%d want=%d", len(saved), len(full))
	}
}
