package resident

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFullAccessSkipsGuardsButKeepsAudit(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(secretPath, []byte("TOKEN=fake\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	audit := &recordingAudit{}
	broker := NewBroker(BrokerConfig{
		Origin:     OriginLocal,
		Audit:      audit,
		Workspace:  dir,
		FullAccess: true,
	})

	readOut, err := broker.Execute(context.Background(), "read_file", map[string]any{"path": secretPath})
	if err != nil || !strings.Contains(readOut, "TOKEN=fake") {
		t.Fatalf("leitura full: out=%q err=%v", readOut, err)
	}
	execOut, err := broker.Execute(context.Background(), "exec_command", map[string]any{"command": "echo hive-full", "cwd": dir})
	if err != nil || !strings.Contains(execOut, "hive-full") {
		t.Fatalf("shell full: out=%q err=%v", execOut, err)
	}
	ideOut, err := broker.Execute(context.Background(), "ide_action", map[string]any{"action": "list_sessions"})
	if err != nil || !strings.Contains(ideOut, "sess") {
		// IDE func is nil, so the call is allowed and then reports the IDE is offline.
		if err == nil || !strings.Contains(err.Error(), "não está ligada") {
			t.Fatalf("ide_action full deveria passar da política: out=%q err=%v", ideOut, err)
		}
	}
	allows := 0
	for _, e := range audit.entries {
		if e.Decision == "allow" && strings.Contains(e.Reason, "modo full") {
			allows++
		}
	}
	if allows < 3 {
		t.Fatalf("auditoria full = %+v", audit.entries)
	}

	governed := NewBroker(BrokerConfig{Origin: OriginLocal, Audit: &recordingAudit{}, Workspace: dir})
	if _, err := governed.Execute(context.Background(), "exec_command", map[string]any{"command": "echo hive-full"}); err == nil {
		t.Fatal("modo governado aceitou shell livre")
	}
}

func TestNormalizeResidentPermissionDefaultsOff(t *testing.T) {
	if (Config{}).IsFullAccess() {
		t.Fatal("full não pode ser o padrão")
	}
	if !(Config{PermissionMode: "full"}).IsFullAccess() {
		t.Fatal("full")
	}
	if !(Config{PermissionMode: "full_access"}).IsFullAccess() {
		t.Fatal("full_access")
	}
	if NormalizeResidentPermission("qualquer") != "governed" {
		t.Fatal("valor desconhecido deve continuar governado")
	}
}
