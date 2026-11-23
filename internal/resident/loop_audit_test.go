package resident

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoopOriginIsAuditedAndNotTrustedLikeLocal(t *testing.T) {
	if d := Decide(Call{Name: "read_file", Args: map[string]any{"path": "README.md"}, Origin: OriginLoop}); d.Allow || !d.Ask {
		t.Fatalf("leitura de loop = %+v, esperado pedir aprovação como rotina", d)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "nota.txt")
	if err := os.WriteFile(path, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	audit := &recordingAudit{}
	broker := NewBroker(BrokerConfig{
		Origin:     OriginLoop,
		LoopID:     "fix-auth",
		Iteration:  2,
		Audit:      audit,
		Workspace:  dir,
		FullAccess: true,
	})
	if _, err := broker.Execute(context.Background(), "read_file", map[string]any{"path": "nota.txt"}); err != nil {
		t.Fatal(err)
	}
	if len(audit.entries) == 0 {
		t.Fatal("sem auditoria")
	}
	got := audit.entries[0]
	if got.Origin != OriginLoop || got.LoopID != "fix-auth" || got.Iteration != 2 {
		t.Fatalf("auditoria: %+v", got)
	}
}
