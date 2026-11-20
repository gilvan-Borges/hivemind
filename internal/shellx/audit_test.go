package shellx

import (
	"os"
	"strings"
	"testing"
)

func TestAuditFullAccessShellOmitsCommandText(t *testing.T) {
	path := t.TempDir() + "/audit.jsonl"
	prev := shellAuditPathFn
	shellAuditPathFn = func() (string, error) { return path, nil }
	t.Cleanup(func() { shellAuditPathFn = prev })

	secret := "super-secret-token"
	AuditFullAccessShell("go test ./... && echo " + secret)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, secret) || strings.Contains(text, "go test") {
		t.Fatalf("auditoria gravou o comando: %s", text)
	}
	if !strings.Contains(text, `"tool":"run_shell"`) || !strings.Contains(text, "full_access") || !strings.Contains(text, `"decision":"allow"`) {
		t.Fatalf("linha de auditoria: %s", text)
	}
}
