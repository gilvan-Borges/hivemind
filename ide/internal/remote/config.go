// Package remote implements the Remote Bridge that lets the HiveMind Mobile
// App (Android) command and follow the IDE's agent teams over HTTP + SSE.
//
// Security model (see docs/SPEC_APP_MOBILE_NATIVO.md §3):
//   - Disabled by default; one click enables it from the IDE status bar.
//   - 256-bit bearer token generated with crypto/rand, persisted outside any
//     Git repo in %LOCALAPPDATA%\HiveMind\remote-bridge.json (mode 0600).
//   - Every /api/v1/* route (except GET /api/v1/health) requires the token,
//     compared in constant time.
package remote

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultPort is the TCP port the bridge listens on (also used in the pairing
// QR payload shown by the IDE).
const DefaultPort = 47890

// BridgeVersion is the version reported by GET /api/v1/health.
const BridgeVersion = "0.1.0"

// Config is the persisted remote bridge configuration.
type Config struct {
	Token   string `json:"token"`   // 64 hex chars (256 bits)
	Port    int    `json:"port"`    // TCP port (0 => DefaultPort on load)
	Enabled bool   `json:"enabled"` // false by default (opt-in)
}

// configPathFn is overridable in tests.
var configPathFn = DefaultConfigPath

// DefaultConfigPath returns %LOCALAPPDATA%\HiveMind\remote-bridge.json
// (or the OS user config dir / temp dir as fallbacks, mirroring
// idecore.RecentFilePath).
func DefaultConfigPath() string {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		if cfg, err := os.UserConfigDir(); err == nil && cfg != "" {
			base = cfg
		} else {
			base = os.TempDir()
		}
	}
	return filepath.Join(base, "HiveMind", "remote-bridge.json")
}

// LoadConfig loads the persisted config, generating a fresh 256-bit token on
// first use. Returns (Config, error).
func LoadConfig() (Config, error) {
	return LoadConfigFrom(configPathFn())
}

// LoadConfigFrom is LoadConfig with an explicit path (tests).
func LoadConfigFrom(path string) (Config, error) {
	cfg := Config{Port: DefaultPort}
	data, err := os.ReadFile(path)
	if err == nil {
		if uerr := json.Unmarshal(data, &cfg); uerr != nil {
			return Config{}, fmt.Errorf("config do bridge inválida (%s): %w", path, uerr)
		}
		if cfg.Port <= 0 || cfg.Port > 65535 {
			cfg.Port = DefaultPort
		}
		if cfg.Token == "" {
			if cfg.Token, err = newToken(); err != nil {
				return Config{}, err
			}
			if err := saveConfig(path, cfg); err != nil {
				return Config{}, err
			}
		}
		return cfg, nil
	}
	if !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("lendo config do bridge (%s): %w", path, err)
	}
	cfg.Token, err = newToken()
	if err != nil {
		return Config{}, err
	}
	if err := saveConfig(path, cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// SaveConfig persists the config with owner-only permissions (0600).
func SaveConfig(cfg Config) error {
	return saveConfig(configPathFn(), cfg)
}

func saveConfig(path string, cfg Config) error {
	if cfg.Port <= 0 || cfg.Port > 65535 {
		cfg.Port = DefaultPort
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// RotateToken replaces the token with a fresh one and persists it. This
// instantly invalidates every active mobile connection.
func RotateToken() (Config, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return Config{}, err
	}
	tok, err := newToken()
	if err != nil {
		return Config{}, err
	}
	cfg.Token = tok
	if err := SaveConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("gerando token do bridge: %w", err)
	}
	return hex.EncodeToString(b), nil
}
