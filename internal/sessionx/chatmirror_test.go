package sessionx

import (
	"context"
	"testing"
)

func TestAppendChatLineShowsInIDEListWithoutDuplicating(t *testing.T) {
	dir := t.TempDir()
	id, err := EnsureChatSession(dir, "", "continuar pelo Hive", "hive")
	if err != nil || id == "" {
		t.Fatalf("sessão: id=%q err=%v", id, err)
	}
	if err := AppendChatLine(dir, id, "u1", "user", "Você", "mensagem vinda do Orb"); err != nil {
		t.Fatal(err)
	}
	if err := AppendChatLine(dir, id, "m1", "model", "Agent Hive", "resposta do Hive"); err != nil {
		t.Fatal(err)
	}
	if err := AppendChatLine(dir, id, "u1", "user", "Você", "mensagem vinda do Orb"); err != nil {
		t.Fatal(err)
	}
	if err := AppendChatLine(dir, id, "m1", "model", "Agent Hive", "resposta do Hive"); err != nil {
		t.Fatal(err)
	}

	summary, err := ListWorkspaceSessions(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Sessions) != 1 {
		t.Fatalf("sessões = %d", len(summary.Sessions))
	}
	if summary.Sessions[0].Kind != "hive" {
		t.Fatalf("kind = %q", summary.Sessions[0].Kind)
	}
	if summary.Sessions[0].Title != "continuar pelo Hive" {
		t.Fatalf("título = %q", summary.Sessions[0].Title)
	}
	detail, err := LoadSessionDetail(context.Background(), dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Kind != "hive" || len(detail.Messages) != 2 {
		t.Fatalf("detalhe kind=%s msgs=%d", detail.Kind, len(detail.Messages))
	}
	if detail.Messages[0].Kind != "user" || detail.Messages[0].Text != "mensagem vinda do Orb" {
		t.Fatalf("user = %+v", detail.Messages[0])
	}
	if detail.Messages[1].Text != "resposta do Hive" {
		t.Fatalf("model = %+v", detail.Messages[1])
	}
}
