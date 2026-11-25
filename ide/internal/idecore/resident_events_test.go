package idecore

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/modelx"
)

func TestResidentEventTriggerIsOptIn(t *testing.T) {
	var notes []string
	sessions := NewSessionManager(func(ev LaneEvent) {
		notes = append(notes, ev.Event.Kind+":"+ev.Event.Text)
	})
	m := NewResidentManager(ResidentManagerOptions{BaseDir: t.TempDir(), Sessions: sessions})
	t.Cleanup(m.Close)
	m.NoteHostEvent("arquivo")
	if len(notes) != 0 {
		t.Fatalf("gatilho desligado acordou o residente: %#v", notes)
	}
	if _, err := m.SetEventTriggers(true); err != nil {
		t.Fatal(err)
	}
	m.NoteHostEvent("arquivo")
	m.NoteHostEvent("comando")
	if len(notes) != 1 || !strings.Contains(notes[0], "evento arquivo") {
		t.Fatalf("acordar: %#v", notes)
	}
	if strings.Contains(notes[0], "tool") {
		t.Fatalf("evento não pode executar ferramenta: %s", notes[0])
	}
}

func TestExperimentalUnisolatedDefaultsOff(t *testing.T) {
	m := NewResidentManager(ResidentManagerOptions{BaseDir: t.TempDir()})
	t.Cleanup(m.Close)
	st := m.Status()
	if st.ExperimentalUnisolated || st.ExperimentalWarning != "" {
		t.Fatalf("padrão deveria recusar: %+v", st)
	}
	st, err := m.SetExperimentalUnisolated(true)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ExperimentalUnisolated || !strings.Contains(st.ExperimentalWarning, "SEM isolamento") {
		t.Fatalf("aviso ausente: %+v", st)
	}
	st, err = m.SetExperimentalUnisolated(false)
	if err != nil || st.ExperimentalUnisolated {
		t.Fatalf("desligar: %+v %v", st, err)
	}
}

func TestBrowserExtensionTurnNeverTouchesIDEConversationsOrLanes(t *testing.T) {
	base := t.TempDir()
	ws := t.TempDir()
	var emitted []LaneEvent
	sessions := NewSessionManager(func(ev LaneEvent) {
		emitted = append(emitted, ev)
	})
	fakeLLM := &modelx.Scripted{
		Fn: func(_ *model.LLMRequest, _ int) *model.LLMResponse {
			return modelx.Text("Resultados da busca no navegador prontos.")
		},
	}
	m := NewResidentManager(ResidentManagerOptions{
		BaseDir:   base,
		Sessions:  sessions,
		TestModel: fakeLLM,
	})
	t.Cleanup(m.Close)

	// Simulate an active IDE conversation bound to "principal".
	m.mirrorIDELine(ws, "lane-1", "sess-principal", "user", "Você", "tarefa da IDE", "ide-msg-1")
	_, _ = m.BindSharedSession(ws, "sess-principal", "lane-1")
	emitted = nil

	res, err := m.RunBrowserExtensionTurn(context.Background(), "pesquise acme no google")
	if err != nil {
		t.Fatalf("RunBrowserExtensionTurn failed: %v", err)
	}
	if !strings.Contains(res.FinalText, "Resultados da busca") {
		t.Fatalf("unexpected FinalText: %q", res.FinalText)
	}

	// 1. ZERO events emitted to IDE lanes.
	if len(emitted) != 0 {
		t.Fatalf("Chrome extension turn leaked %d event(s) into IDE lanes: %+v", len(emitted), emitted)
	}

	// 2. Active IDE shared conversation ("sess-principal") has ONLY its original IDE message.
	listed, err := m.ListSharedConversations(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].MessageCount != 1 {
		t.Fatalf("Chrome extension turn polluted IDE shared conversations: %+v", listed)
	}

	// 3. Default IDE workstream ("geral") remains empty, while "chrome-extension" has its own history.
	ideMsgs, _ := m.SessionMessages("geral")
	for _, msg := range ideMsgs {
		if strings.Contains(msg.Text, "pesquise acme") {
			t.Fatalf("Chrome extension prompt leaked into IDE resident transcript: %+v", ideMsgs)
		}
	}
}
