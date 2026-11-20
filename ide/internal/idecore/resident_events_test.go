package idecore

import (
	"strings"
	"testing"
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
