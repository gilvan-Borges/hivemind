package relayclient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/gilvan-Borges/HiveMind/ide/internal/remote"
)

// Config is the IDE's relay session. The refresh token, device credential and
// private key stay in this file (mode 0600) and are never sent to the UI.
type Config struct {
	BaseURL       string `json:"baseUrl,omitempty"`
	Email         string `json:"email,omitempty"`
	RefreshToken  string `json:"refreshToken,omitempty"`
	DeviceID      string `json:"deviceId,omitempty"`
	DeviceToken   string `json:"deviceToken,omitempty"`
	DeviceName    string `json:"deviceName,omitempty"`
	PublicKey     string `json:"publicKey,omitempty"`
	PrivateKey    string `json:"privateKey,omitempty"`
	PeerPublicKey string `json:"peerPublicKey,omitempty"`
	Enabled       bool   `json:"enabled"`
}

// DefaultPath is the relay config next to the LAN bridge config.
func DefaultPath() string {
	return filepath.Join(filepath.Dir(remote.DefaultConfigPath()), "relay.json")
}

func load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	return cfg, nil
}

func save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
