package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/shellx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestReadAndSearchSkipSensitiveFiles(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".env", "DATABASE_URL=postgres://app:s3cret@localhost/db\n")
	write(".env.local", "TOKEN=s3cret-local\n")
	write(".env.example", "DATABASE_URL=postgres://localhost/db\n")
	write(".ssh/id_rsa", "-----BEGIN PRIVATE KEY-----\ns3cret-key\n")
	write("certs/app.pem", "-----BEGIN CERT-----\ns3cret-pem\n")
	write("src/main.go", "package main\nfunc tokenHint() {}\n")
	write(".hivemind/MEMORY.md", "memoria do time\n")

	for _, rel := range []string{".env", ".env.local", ".ssh/id_rsa", "certs/app.pem"} {
		_, err := readFileRange(root, readFileArgs{Path: rel})
		if err == nil || !strings.Contains(err.Error(), "sensível") {
			t.Fatalf("read %s: %v", rel, err)
		}
	}
	example, err := readFileRange(root, readFileArgs{Path: ".env.example"})
	if err != nil || !strings.Contains(example, "postgres://localhost") {
		t.Fatalf("example deveria ser legível: %v %q", err, example)
	}
	code, err := readFileRange(root, readFileArgs{Path: "src/main.go"})
	if err != nil || !strings.Contains(code, "tokenHint") {
		t.Fatalf("código: %v %q", err, code)
	}

	out, err := runSearchText(context.Background(), root, searchTextArgs{Query: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, ".env") || strings.Contains(out, "id_rsa") || strings.Contains(out, "PRIVATE KEY") || strings.Contains(out, "postgres://app") || strings.Contains(out, ".pem") {
		t.Fatalf("busca vazou segredo ou caminho sensível:\n%s", out)
	}
	_, err = runSearchText(context.Background(), root, searchTextArgs{Query: "s3cret", Path: ".env"})
	if err == nil || !strings.Contains(err.Error(), "sensível") {
		t.Fatalf("busca direta em .env: %v", err)
	}
	out, err = runSearchText(context.Background(), root, searchTextArgs{Query: "tokenHint"})
	if err != nil || !strings.Contains(out, "src/main.go") {
		t.Fatalf("busca em código: %v\n%s", err, out)
	}
}

func TestAutoEditShellMetaRequiresConfirm(t *testing.T) {
	sh := &gateShell{}
	settings := team.DefaultSettings()
	settings.PermissionMode = team.PermissionAutoEdit
	asked := 0
	sb := &recordSandbox{}
	deps := applyShellPolicy(Deps{
		Root:      t.TempDir(),
		Settings:  settings,
		Shell:     sh,
		Sandbox:   sb,
		Allowlist: []string{"go", "git"},
		Confirm: func(command string) bool {
			asked++
			return strings.Contains(command, "SEM SANDBOX")
		},
	})
	if _, err := deps.runShell(context.Background(), "go test ./...", "", false); err != nil {
		t.Fatalf("comando allowlisted: %v", err)
	}
	if asked != 0 || sb.runs != 1 {
		t.Fatalf("sandbox=%d confirmações=%d", sb.runs, asked)
	}
	if _, err := deps.runShell(context.Background(), "go test ./... && git status", "", false); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("metacaractere na sandbox: %v", err)
	}
	if asked != 0 || sh.ran != "" {
		t.Fatalf("linha de shell não pode rodar na sandbox asked=%d ran=%q", asked, sh.ran)
	}
	if _, err := deps.runShell(context.Background(), "go test ./... && git status", "", true); err == nil || !strings.Contains(err.Error(), "perigoso") {
		t.Fatalf("metacaractere sem sandbox ainda pede confirmação: %v", err)
	}
	if sh.ran != "" {
		t.Fatalf("comando com metacaractere rodou: %q", sh.ran)
	}

	headless := applyShellPolicy(Deps{
		Root:      t.TempDir(),
		Settings:  settings,
		Shell:     sh,
		Sandbox:   sb,
		Allowlist: []string{"go"},
	})
	if _, err := headless.runShell(context.Background(), "go test ./... | echo x", "", true); err == nil {
		t.Fatal("headless em auto_edit não pode rodar metacaractere sem sandbox")
	}
}

func TestFullAccessShellMetaRunsAndAudits(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	restore := shellx.SetShellAuditPathForTest(auditPath)
	t.Cleanup(restore)

	sh := &gateShell{}
	sb := &recordSandbox{}
	settings := team.DefaultSettings()
	settings.PermissionMode = team.PermissionFullAccess
	settings.ShellUnsandboxed = true
	deps := applyShellPolicy(Deps{
		Root:     t.TempDir(),
		Settings: settings,
		Shell:    sh,
		Sandbox:  sb,
		Confirm:  func(string) bool { return false },
	})
	secret := "super-secret-token"
	cmd := "go test ./... && echo " + secret
	out, err := deps.runShell(context.Background(), cmd, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if sh.ran != cmd || sb.runs != 0 {
		t.Fatalf("full_access sem sandbox deveria executar no host: ran=%q sandbox=%d", sh.ran, sb.runs)
	}
	if !strings.Contains(out, "SEM SANDBOX") {
		t.Fatalf("saída sem aviso: %q", out)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, secret) || strings.Contains(text, "go test") {
		t.Fatalf("auditoria vazou o comando: %s", text)
	}
	if !strings.Contains(text, "full_access") || !strings.Contains(text, "run_shell") {
		t.Fatalf("auditoria: %s", text)
	}
}
