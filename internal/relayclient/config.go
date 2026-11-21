package relayclient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gilvan-Borges/HiveMind/internal/atomicx"
	"github.com/gilvan-Borges/HiveMind/internal/remote"
	"github.com/gilvan-Borges/HiveMind/internal/secretx"
)

const vaultRefPrefix = "vault:"

var (
	rcVaultMu  sync.Mutex
	rcMemVault = map[string]*secretx.MemoryVault{}
	rcOSVault  = secretx.NewOSVault()
)

func vaultForPath(path string) secretx.Vault {
	dir := filepath.Clean(filepath.Dir(path))
	defDir := filepath.Clean(filepath.Dir(DefaultPath()))
	if strings.EqualFold(dir, defDir) {
		if ok, _ := rcOSVault.Available(); ok {
			return rcOSVault
		}
	}
	rcVaultMu.Lock()
	defer rcVaultMu.Unlock()
	v := rcMemVault[dir]
	if v == nil {
		v = secretx.NewMemoryVault()
		rcMemVault[dir] = v
	}
	return v
}

// Config is the IDE's relay session. The refresh token, device credential and
// private key live in secretx.Vault while relay.json stores only metadata and vault refs.
type Config struct {
	BaseURL       string `json:"baseUrl,omitempty"`
	Email         string `json:"email,omitempty"`
	Role          string `json:"role,omitempty"`
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

func resolveConfigSecret(v secretx.Vault, key, rawOrRef string) (string, bool) {
	val := strings.TrimSpace(rawOrRef)
	if val == "" {
		return "", false
	}
	if strings.HasPrefix(val, vaultRefPrefix) {
		name := strings.TrimSpace(strings.TrimPrefix(val, vaultRefPrefix))
		if name == "" {
			name = key
		}
		if secret, err := v.Get(name); err == nil {
			return secret, false
		}
		return "", false
	}
	// Legacy plaintext value in relay.json: migrate into vault immediately.
	if err := v.Set(key, val); err == nil {
		return val, true
	}
	return val, false
}

func storeConfigSecret(v secretx.Vault, key, val string) (string, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		_ = v.Delete(key)
		return "", nil
	}
	if err := v.Set(key, val); err != nil {
		return "", err
	}
	return vaultRefPrefix + key, nil
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

	v := vaultForPath(path)
	var mig1, mig2, mig3 bool
	cfg.RefreshToken, mig1 = resolveConfigSecret(v, "relayclient:refresh_token", cfg.RefreshToken)
	cfg.DeviceToken, mig2 = resolveConfigSecret(v, "relayclient:device_token", cfg.DeviceToken)
	cfg.PrivateKey, mig3 = resolveConfigSecret(v, "relayclient:private_key", cfg.PrivateKey)
	if mig1 || mig2 || mig3 {
		_ = save(path, cfg)
	}
	return cfg, nil
}

func save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	v := vaultForPath(path)
	diskCfg := cfg
	var err error
	if diskCfg.RefreshToken, err = storeConfigSecret(v, "relayclient:refresh_token", cfg.RefreshToken); err != nil {
		return err
	}
	if diskCfg.DeviceToken, err = storeConfigSecret(v, "relayclient:device_token", cfg.DeviceToken); err != nil {
		return err
	}
	if diskCfg.PrivateKey, err = storeConfigSecret(v, "relayclient:private_key", cfg.PrivateKey); err != nil {
		return err
	}
	data, err := json.MarshalIndent(diskCfg, "", "  ")
	if err != nil {
		return err
	}
	return atomicx.WriteFile(path, append(data, '\n'), 0o600)
}
