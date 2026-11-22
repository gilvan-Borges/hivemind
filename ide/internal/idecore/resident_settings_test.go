package idecore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/resident"
)

func TestResidentEnableOrbAndJevFailoverSettings(t *testing.T) {
	baseDir := t.TempDir()
	m := NewResidentManager(ResidentManagerOptions{BaseDir: baseDir})
	t.Cleanup(m.Close)

	initial := m.Status()
	if !initial.Enabled || !initial.OrbEnabled || initial.JevFailover {
		t.Fatalf("unexpected defaults: %+v", initial)
	}

	st, err := m.SetEnabled(false)
	if err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	if st.Enabled || st.State != "desativado" {
		t.Fatalf("expected disabled status, got %+v", st)
	}
	if m.IsEnabled() || m.IsOrbEnabled() {
		t.Fatalf("expected IsEnabled and IsOrbEnabled to be false when disabled")
	}

	// Turn should be rejected while disabled.
	if _, err := m.RunTurn(context.Background(), "", "geral", "olá", resident.OriginLocal); err == nil || !strings.Contains(err.Error(), "desativado") {
		t.Fatalf("expected disabled RunTurn error, got %v", err)
	}

	// Updating provider/model must preserve Enabled == false.
	stAfterCfg, err := m.SaveConfig("openrouter", "openrouter/auto", "geral")
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if stAfterCfg.Enabled || stAfterCfg.State != "desativado" {
		t.Fatalf("SaveConfig re-enabled resident unexpectedly: %+v", stAfterCfg)
	}

	// Re-enable resident, disable floating Orb, enable Jev failover.
	if st, err = m.SetEnabled(true); err != nil || !st.Enabled {
		t.Fatalf("SetEnabled(true): %+v err=%v", st, err)
	}
	if st, err = m.SetOrbEnabled(false); err != nil || st.OrbEnabled {
		t.Fatalf("SetOrbEnabled(false): %+v err=%v", st, err)
	}
	if m.IsOrbEnabled() {
		t.Fatalf("expected IsOrbEnabled() false when OrbDisabled persisted")
	}
	if st, err = m.SetJevFailover(true); err != nil || !st.JevFailover {
		t.Fatalf("SetJevFailover(true): %+v err=%v", st, err)
	}

	raw, err := os.ReadFile(filepath.Join(baseDir, "resident.json"))
	if err != nil {
		t.Fatalf("read resident.json: %v", err)
	}
	var disk resident.Config
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatalf("unmarshal resident.json: %v", err)
	}
	if !disk.Enabled || !disk.OrbDisabled || !disk.JevFailover {
		t.Fatalf("unexpected persisted resident.json: %+v", disk)
	}
}
