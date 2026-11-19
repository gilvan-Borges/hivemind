package remote

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigCreatesTokenOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "remote-bridge.json")

	cfg1, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	if len(cfg1.Token) != 64 {
		t.Fatalf("token deveria ter 64 hex chars (256 bits), veio %d", len(cfg1.Token))
	}
	if _, err := hex.DecodeString(cfg1.Token); err != nil {
		t.Fatalf("token não é hex: %v", err)
	}
	if cfg1.Port != DefaultPort {
		t.Fatalf("porta padrão errada: %d", cfg1.Port)
	}
	if cfg1.Enabled {
		t.Fatalf("bridge deve começar desligada por padrão")
	}

	// Same token on reload — no regeneration.
	cfg2, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg2.Token != cfg1.Token {
		t.Fatalf("token não deveria mudar no reload")
	}

	// Rotation must produce a different token.
	oldPathFn := configPathFn
	configPathFn = func() string { return path }
	defer func() { configPathFn = oldPathFn }()

	cfg1.Enabled = true
	if err := SaveConfig(cfg1); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg3, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatalf("reload pós-save: %v", err)
	}
	if !cfg3.Enabled {
		t.Fatalf("enabled não persistiu: %+v", cfg3)
	}
}

func TestRotateToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote-bridge.json")
	oldCfg, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	oldPathFn := configPathFn
	configPathFn = func() string { return path }
	defer func() { configPathFn = oldPathFn }()

	rotated, err := RotateToken()
	if err != nil {
		t.Fatalf("RotateToken: %v", err)
	}
	if rotated.Token == oldCfg.Token {
		t.Fatalf("token deveria mudar após rotação")
	}
	if len(rotated.Token) != 64 {
		t.Fatalf("token rotacionado inválido: %d chars", len(rotated.Token))
	}
	reloaded, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Token != rotated.Token {
		t.Fatalf("rotação não persistiu")
	}
}

func TestLoadConfigBadJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote-bridge.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfigFrom(path); err == nil {
		t.Fatalf("JSON inválido deveria falhar")
	}
}

func TestDefaultConfigPath(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Local`)
	p := DefaultConfigPath()
	if !strings.HasSuffix(p, filepath.Join("HiveMind", "remote-bridge.json")) {
		t.Fatalf("caminho inesperado: %s", p)
	}
	if !strings.HasPrefix(p, `C:\Local`) {
		t.Fatalf("deveria usar LOCALAPPDATA: %s", p)
	}
}
