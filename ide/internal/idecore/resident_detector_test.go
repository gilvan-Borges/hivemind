package idecore

import (
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/resident"
)

func TestDetectorRejectionIsReadTierAndOnce(t *testing.T) {
	m := NewResidentManager(ResidentManagerOptions{BaseDir: t.TempDir()})
	m.NoteDetectorRejected("bad.json", "json inválido", "fp-1")
	m.NoteDetectorRejected("bad.json", "json inválido", "fp-1")
	m.NoteDetectorRejected("bad.json", "detector recusado", "fp-2")
	entries, err := m.AuditLog(10)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.Tool != "detector_rejected" {
			continue
		}
		n++
		if e.Tier == resident.TierCredentials {
			t.Fatalf("recusa de detector classificada como cofre: %+v", e)
		}
		if e.Tier != resident.TierRead {
			t.Fatalf("tier: %s", e.Tier)
		}
	}
	if n != 2 {
		t.Fatalf("esperava 2 registros, veio %d (%+v)", n, entries)
	}
}
