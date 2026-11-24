package browserx

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/atomicx"
)

// Config é o pareamento local. O token nunca entra no Git nem em log.
type Config struct {
	Token        string   `json:"token"`
	Port         int      `json:"port"`
	AllowedHosts []string `json:"allowedHosts,omitempty"`
}

func configPath() (string, error) {
	if base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); base != "" {
		return filepath.Join(base, "HiveMind", "browser-bridge.json"), nil
	}
	if base, err := os.UserConfigDir(); err == nil && strings.TrimSpace(base) != "" {
		return filepath.Join(base, "HiveMind", "browser-bridge.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hivemind", "browser-bridge.json"), nil
}

// LoadConfig lê o pareamento. Se o arquivo não existe, gera um token e grava
// com permissão 0600. Um arquivo corrompido não é reescrito.
func LoadConfig() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return Config{}, err
		}
		cfg := Config{Port: DefaultPort, Token: newToken()}
		if err := SaveConfig(cfg); err != nil {
			return Config{}, err
		}
		return cfg, nil
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("ler pareamento do Chrome: %w", err)
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return Config{}, fmt.Errorf("pareamento do Chrome sem token; apague o arquivo de config local e rode hivemind browser pair")
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	cfg.AllowedHosts = normalizeHosts(cfg.AllowedHosts)
	return cfg, nil
}

// SaveConfig grava o pareamento sem expandir segredo em outro arquivo.
func SaveConfig(cfg Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return fmt.Errorf("token vazio")
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	cfg.AllowedHosts = normalizeHosts(cfg.AllowedHosts)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicx.WriteFile(path, data, 0o600)
}

func newToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

func normalizeHosts(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range in {
		h = strings.ToLower(strings.TrimSpace(h))
		h = strings.TrimSuffix(h, ".")
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}
