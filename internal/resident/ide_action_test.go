package resident

import (
	"context"
	"strings"
	"testing"
)

func TestIDEActionRequiresApprovalThenRunsAndAudits(t *testing.T) {
	audit := &recordingAudit{}
	called := false
	broker := NewBroker(BrokerConfig{
		Origin: OriginLocal,
		Audit:  audit,
		Approve: func(context.Context, string, string) bool {
			return true
		},
		IDE: func(_ context.Context, name string, args map[string]any) (string, error) {
			called = true
			if name != "ide_action" || args["action"] != "list_sessions" {
				t.Fatalf("chamada = %s %+v", name, args)
			}
			if args["workspace"] != "/tmp/proj" {
				t.Fatalf("workspace = %v", args["workspace"])
			}
			return "sess-1\thive\tolá", nil
		},
		Workspace: "/tmp/proj",
	})
	out, err := broker.Execute(context.Background(), "ide_action", map[string]any{"action": "list_sessions"})
	if err != nil {
		t.Fatal(err)
	}
	if !called || !strings.Contains(out, "sess-1") {
		t.Fatalf("out=%q called=%v", out, called)
	}
	if len(audit.entries) == 0 || audit.entries[0].Decision != "allow" || audit.entries[0].Tool != "ide_action" {
		t.Fatalf("auditoria = %+v", audit.entries)
	}
}

func TestIDEActionDeniedWithoutApproval(t *testing.T) {
	broker := NewBroker(BrokerConfig{
		Origin: OriginLocal,
		Audit:  &recordingAudit{},
		IDE: func(context.Context, string, map[string]any) (string, error) {
			t.Fatal("a IDE não deve rodar sem aprovação")
			return "", nil
		},
	})
	_, err := broker.Execute(context.Background(), "ide_action", map[string]any{"action": "save_file", "path": "a.txt"})
	if err == nil || !strings.Contains(err.Error(), "recusado") {
		t.Fatalf("err = %v", err)
	}
}

func TestIDEActionEmptyIsClosed(t *testing.T) {
	dec := Decide(Call{Name: "ide_action", Origin: OriginLocal, Args: map[string]any{}})
	if dec.Allow || dec.Ask {
		t.Fatalf("decisão = %+v", dec)
	}
}
