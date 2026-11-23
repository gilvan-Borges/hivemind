package resident

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConversationAppendIsIdempotentAndSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	store := NewConversationStore(dir)
	conv, err := store.EnsureActive(filepath.Join(dir, "proj"), "geral", "hive", []LocalTurnMessage{
		{Role: "user", Text: "olá da sessão antiga", At: time.Now().UTC()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if conv.Title != "olá da sessão antiga" {
		t.Fatalf("título = %q", conv.Title)
	}
	inserted, err := store.AppendMessage(conv.ID, ConvMessage{ID: "m1", Role: "user", Text: "continuar da IDE"})
	if err != nil || !inserted {
		t.Fatalf("primeira inclusão: inserted=%v err=%v", inserted, err)
	}
	inserted, err = store.AppendMessage(conv.ID, ConvMessage{ID: "m1", Role: "user", Text: "continuar da IDE"})
	if err != nil || inserted {
		t.Fatalf("repetir o mesmo id deve ser no-op: inserted=%v err=%v", inserted, err)
	}
	inserted, err = store.AppendMessage(conv.ID, ConvMessage{ID: "m2", Role: "model", Author: "Agent Hive", Text: "segue aqui"})
	if err != nil || !inserted {
		t.Fatalf("resposta: inserted=%v err=%v", inserted, err)
	}

	reopened := NewConversationStore(dir)
	got, err := reopened.Get(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("mensagens = %d, quero 3 (legado + 2, sem duplicar m1)", len(got.Messages))
	}
	if reopened.ActiveID(got.Workspace) != conv.ID {
		t.Fatal("a conversa ativa não sobreviveu ao reopen")
	}
	list, err := reopened.List(got.Workspace)
	if err != nil || len(list) != 1 || list[0].MessageCount != 3 {
		t.Fatalf("lista = %+v err=%v", list, err)
	}
}

func TestBindSessionReusesTheSameConversation(t *testing.T) {
	store := NewConversationStore(t.TempDir())
	first, err := store.BindSession("/tmp/proj", "sess-1", "lane-1", "team", "ide", "Refatorar handler")
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.BindSession("/tmp/proj", "sess-1", "lane-2", "team", "ide", "outro título")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID {
		t.Fatalf("bind criou outra conversa: %s vs %s", again.ID, first.ID)
	}
	if again.LaneID != "lane-2" {
		t.Fatalf("lane = %s", again.LaneID)
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), first.ID+".json")); err != nil {
		t.Fatal(err)
	}
}
