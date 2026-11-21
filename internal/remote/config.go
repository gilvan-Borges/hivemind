// Package remote implements the LAN bridge: HTTP + SSE on port 47890.
//
// Security model:
//   - Disabled by default; one click enables it from the IDE status bar.
//   - The session token is not stored. remote-bridge.json keeps only the
//     SHA-256 hex digest (mode 0600), outside any Git repo.
//   - The long-term X25519 private key lives in the OS vault (lan-bridge-box).
//   - Authenticated bodies are naclbox frames. GET /api/v1/health stays public.
package remote

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/atomicx"
)

// DefaultPort is the TCP port the bridge listens on (also used in the pairing
// QR payload shown by the IDE).
const DefaultPort = 47890

// BridgeVersion is the version reported by GET /api/v1/health.
const BridgeVersion = "0.1.0"

// LanDevice is one paired client. AppPub is the X25519 public key (base64url).
// LastSeq is the last request sequence this process accepted for that device.
type LanDevice struct {
	DeviceID string `json:"deviceId"`
	AppPub   string `json:"appPub"`
	LastSeq  uint64 `json:"lastSeq"`
}

// Config is the persisted remote bridge configuration.
// Token is read only to migrate an older file; saveConfig never writes it back.
type Config struct {
	Token     string      `json:"token,omitempty"`
	TokenHash string      `json:"tokenHash,omitempty"`
	BoxPub    string      `json:"boxPub,omitempty"`
	Port      int         `json:"port"`
	Enabled   bool        `json:"enabled"`
	Devices   []LanDevice `json:"devices,omitempty"`
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

// LoadConfig loads the persisted config. A legacy raw token is hashed once
// and removed; clear Bearer authentication stops accepting it.
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
		if cfg.Token != "" {
			if cfg.TokenHash == "" {
				cfg.TokenHash = hashToken(cfg.Token)
			}
			cfg.Token = ""
			if err := saveConfig(path, cfg); err != nil {
				return Config{}, err
			}
		}
		cfg.Token = ""
		return cfg, nil
	}
	if !os.IsNotExist(err) {
		return Config{}, fmt.Errorf("lendo config do bridge (%s): %w", path, err)
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
	cfg.Token = ""
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return atomicx.WriteFile(path, append(data, '\n'), 0o600)
}

// RotateToken drops every paired device. The new digest has no known preimage,
// so previously issued session tokens stop matching.
func RotateToken() (Config, error) {
	return rotateAt(configPathFn())
}

func rotateAt(path string) (Config, error) {
	cfg, err := LoadConfigFrom(path)
	if err != nil {
		return Config{}, err
	}
	tok, err := newSessionToken()
	if err != nil {
		return Config{}, err
	}
	cfg.Token = ""
	cfg.TokenHash = hashToken(tok)
	cfg.Devices = nil
	if err := saveConfig(path, cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
