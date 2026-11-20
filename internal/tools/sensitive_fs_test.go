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
	deps := applyShellPolicy(Deps{
		Root:      t.TempDir(),
		Settings:  settings,
		Shell:     sh,
		Allowlist: []string{"go", "git"},
		Confirm: func(string) bool {
			asked++
			return false
		},
	})
	if _, err := deps.runShell(context.Background(), "go test ./...", ""); err != nil {
		t.Fatalf("comando allowlisted: %v", err)
	}
	if asked != 0 {
		t.Fatalf("confirmações indevidas: %d", asked)
	}
	if _, err := deps.runShell(context.Background(), "go test ./... && git status", ""); err == nil || !strings.Contains(err.Error(), "perigoso") {
		t.Fatalf("metacaractere recusado: %v", err)
	}
	if asked != 1 {
		t.Fatalf("confirmações = %d", asked)
	}
	if strings.Contains(sh.ran, "&&") {
		t.Fatalf("comando com metacaractere rodou: %q", sh.ran)
	}

	headless := applyShellPolicy(Deps{
		Root:      t.TempDir(),
		Settings:  settings,
		Shell:     sh,
		Allowlist: []string{"go"},
	})
	if _, err := headless.runShell(context.Background(), "go test ./... | echo x", ""); err == nil {
		t.Fatal("headless em auto_edit não pode rodar metacaractere")
	}
}

func TestFullAccessShellMetaRunsAndAudits(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	restore := shellx.SetShellAuditPathForTest(auditPath)
	t.Cleanup(restore)

	sh := &gateShell{}
	settings := team.DefaultSettings()
	settings.PermissionMode = team.PermissionFullAccess
	deps := applyShellPolicy(Deps{
		Root:     t.TempDir(),
		Settings: settings,
		Shell:    sh,
		Confirm:  func(string) bool { return false },
	})
	secret := "super-secret-token"
	cmd := "go test ./... && echo " + secret
	if _, err := deps.runShell(context.Background(), cmd, ""); err != nil {
		t.Fatal(err)
	}
	if sh.ran != cmd {
		t.Fatalf("full_access deveria executar: %q", sh.ran)
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
