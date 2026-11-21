package remote

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigStartsWithoutRawToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "remote-bridge.json")

	cfg1, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	if cfg1.Token != "" || cfg1.TokenHash != "" {
		t.Fatalf("arquivo novo não guarda token cru nem hash: %+v", cfg1)
	}
	if cfg1.Port != DefaultPort {
		t.Fatalf("porta padrão errada: %d", cfg1.Port)
	}
	if cfg1.Enabled {
		t.Fatalf("bridge deve começar desligada por padrão")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"token"`) {
		t.Fatalf("disco contém token: %s", raw)
	}

	cfg1.Enabled = true
	if err := saveConfig(path, cfg1); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg3, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatalf("reload pós-save: %v", err)
	}
	if !cfg3.Enabled || cfg3.Token != "" {
		t.Fatalf("enabled não persistiu sem token cru: %+v", cfg3)
	}
}

func TestMigrateLegacyTokenDropsRawValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote-bridge.json")
	legacy := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(path, []byte(`{"token":"`+legacy+`","port":47890,"enabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "" {
		t.Fatalf("token cru sobreviveu à migração: %+v", cfg)
	}
	if cfg.TokenHash != hashToken(legacy) || len(cfg.TokenHash) != 64 {
		t.Fatalf("tokenHash: %q", cfg.TokenHash)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), legacy) || strings.Contains(string(raw), `"token"`) {
		t.Fatalf("arquivo migrado ainda tem o token cru: %s", raw)
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
	if rotated.Token != "" {
		t.Fatal("rotação não pode devolver token cru")
	}
	if rotated.TokenHash == "" || rotated.TokenHash == oldCfg.TokenHash || len(rotated.TokenHash) != 64 {
		t.Fatalf("tokenHash inválido após rotação: %+v", rotated)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"token"`) {
		t.Fatalf("rotação gravou token cru: %s", raw)
	}
	reloaded, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.TokenHash != rotated.TokenHash || reloaded.Token != "" {
		t.Fatalf("rotação não persistiu: %+v", reloaded)
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
