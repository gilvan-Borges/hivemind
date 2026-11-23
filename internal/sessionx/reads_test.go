package sessionx

import (
	"context"
	"testing"
)

func TestConversationPreviewAndUnread(t *testing.T) {
	dir := t.TempDir()
	id, err := EnsureChatSession(dir, "", "fio do app", "hive")
	if err != nil {
		t.Fatal(err)
	}
	if err := AppendChatLine(dir, id, "u1", "user", "user", "primeira"); err != nil {
		t.Fatal(err)
	}
	if err := AppendChatLine(dir, id, "m1", "model", "Agent Hive", "resposta curta"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := ListWorkspaceSessions(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Sessions) != 1 || first.Sessions[0].Unread != 0 || first.Sessions[0].Preview != "resposta curta" {
		t.Fatalf("primeira lista = %+v", first.Sessions)
	}
	if err := AppendChatLine(dir, id, "u2", "user", "user", "ainda não lida"); err != nil {
		t.Fatal(err)
	}
	second, err := ListWorkspaceSessions(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if second.Sessions[0].Unread != 1 || second.Sessions[0].Preview != "ainda não lida" {
		t.Fatalf("não lidas = %+v", second.Sessions[0])
	}
	if err := MarkSessionRead(ctx, dir, id); err != nil {
		t.Fatal(err)
	}
	third, err := ListWorkspaceSessions(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if third.Sessions[0].Unread != 0 {
		t.Fatalf("depois de ler = %+v", third.Sessions[0])
	}
}
