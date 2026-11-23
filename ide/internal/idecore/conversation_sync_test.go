package idecore

import (
	"context"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
)

func TestSharedTranscriptDedupesAndSurvivesRestart(t *testing.T) {
	base := t.TempDir()
	ws := t.TempDir()
	m := NewResidentManager(ResidentManagerOptions{BaseDir: base})
	t.Cleanup(func() { m.Close() })

	m.mirrorIDELine(ws, "lane-1", "sess-ide", "user", "Você", "olá da IDE", "ide-line-1")
	m.mirrorIDELine(ws, "lane-1", "sess-ide", "user", "Você", "olá da IDE", "ide-line-1")

	listed, err := m.ListSharedConversations(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].MessageCount != 1 {
		t.Fatalf("lista = %+v", listed)
	}

	shared, err := m.prepareSharedTurn(ws, "geral", "continuar pelo Hive", "", "hive")
	if err != nil {
		t.Fatal(err)
	}
	if shared.Conv.SessionID != "sess-ide" {
		t.Fatalf("a continuação não usou a sessão da IDE: %s", shared.Conv.SessionID)
	}
	m.finishSharedTurn(shared, "resposta do Hive")
	m.finishSharedTurn(shared, "resposta do Hive")

	got, err := m.OpenSharedConversation(shared.Conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("mensagens = %d, quero 3 sem duplicar", len(got.Messages))
	}

	summary, err := sessionx.ListWorkspaceSessions(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Sessions) != 1 || summary.Sessions[0].ID != "sess-ide" {
		t.Fatalf("sessões da IDE = %+v", summary.Sessions)
	}
	detail, err := sessionx.LoadSessionDetail(context.Background(), ws, "sess-ide")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 2 {
		t.Fatalf("timeline = %d, quero user+resposta uma vez: %+v", len(detail.Messages), detail.Messages)
	}

	reopened := NewResidentManager(ResidentManagerOptions{BaseDir: base})
	t.Cleanup(func() { reopened.Close() })
	again, err := reopened.ListSharedConversations(ws)
	if err != nil || len(again) != 1 || again[0].MessageCount != 3 {
		t.Fatalf("reopen = %+v err=%v", again, err)
	}
}
