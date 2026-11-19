package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeCommandRegistered(t *testing.T) {
	root := newRoot()
	cmd, _, err := root.Find([]string{"serve"})
	if err != nil {
		t.Fatalf("subcomando serve deveria existir: %v", err)
	}
	if cmd.Use != "serve" {
		t.Fatalf("Use errado: %s", cmd.Use)
	}
}

func TestLocateIDEBinaryFromLocalAppData(t *testing.T) {
	dir := t.TempDir()
	exeName := "hivemind-ide"
	if os.PathSeparator == '\\' {
		exeName += ".exe"
	}
	progDir := filepath.Join(dir, "Programs", "hivemind-ide")
	if err := os.MkdirAll(progDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exePath := filepath.Join(progDir, exeName)
	if err := os.WriteFile(exePath, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALAPPDATA", dir)

	got, err := locateIDEBinary()
	if err != nil {
		t.Fatalf("locateIDEBinary: %v", err)
	}
	if got != exePath {
		t.Fatalf("caminho errado: %s (esperava %s)", got, exePath)
	}
}

func TestLocateIDEBinaryNotFound(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	// Esvazia o PATH para o LookPath não achar nada por acaso.
	t.Setenv("PATH", t.TempDir())
	_, err := locateIDEBinary()
	if err == nil {
		t.Fatal("deveria falhar sem binário da IDE")
	}
	if !strings.Contains(err.Error(), "hivemind-ide") {
		t.Fatalf("mensagem de erro sem contexto: %v", err)
	}
}
