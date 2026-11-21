package modelx

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gilvan-Borges/HiveMind/internal/secretx"
)

const vaultRefPrefix = "vault:"

var (
	modelxVaultMu  sync.Mutex
	modelxMemVault = map[string]*secretx.MemoryVault{}
	modelxOSVault  = secretx.NewOSVault()
)

func defaultModelxVaultDir() string {
	if p, err := defaultAPIKeysPath(); err == nil && strings.TrimSpace(p) != "" {
		return filepath.Clean(filepath.Dir(p))
	}
	if p, err := defaultProfilesFilePath(); err == nil && strings.TrimSpace(p) != "" {
		return filepath.Clean(filepath.Dir(p))
	}
	return ""
}

func vaultForConfigPath(configPath string) secretx.Vault {
	dir := filepath.Clean(filepath.Dir(configPath))
	if def := defaultModelxVaultDir(); def != "" && strings.EqualFold(dir, def) {
		if ok, _ := modelxOSVault.Available(); ok {
			return modelxOSVault
		}
	}
	modelxVaultMu.Lock()
	defer modelxVaultMu.Unlock()
	v := modelxMemVault[dir]
	if v == nil {
		v = secretx.NewMemoryVault()
		modelxMemVault[dir] = v
	}
	return v
}

func safeVaultKey(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-', r == ':':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	safe := b.String()
	if safe == "" {
		safe = "secret"
	}
	if len(safe) > 80 {
		sum := sha256.Sum256([]byte(raw))
		safe = safe[:60] + ":" + hex.EncodeToString(sum[:8])
	}
	return safe
}

func vaultSaveSecret(configPath, key, secret string) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", nil
	}
	safeKey := safeVaultKey(key)
	v := vaultForConfigPath(configPath)
	if err := v.Set(safeKey, secret); err != nil {
		return "", err
	}
	return vaultRefPrefix + safeKey, nil
}

func vaultLoadSecret(configPath, key, rawOrRef string) (string, bool) {
	val := strings.TrimSpace(rawOrRef)
	if val == "" {
		return "", false
	}
	v := vaultForConfigPath(configPath)
	if strings.HasPrefix(val, vaultRefPrefix) {
		refKey := strings.TrimSpace(strings.TrimPrefix(val, vaultRefPrefix))
		if refKey == "" {
			refKey = safeVaultKey(key)
		}
		if secret, err := v.Get(refKey); err == nil {
			return secret, false
		}
		return "", false
	}
	// Legacy plaintext secret read from disk: migrate immediately to vault.
	safeKey := safeVaultKey(key)
	if err := v.Set(safeKey, val); err == nil {
		return val, true
	}
	return val, false
}

func vaultDeleteSecret(configPath, key string) {
	v := vaultForConfigPath(configPath)
	_ = v.Delete(safeVaultKey(key))
}

func defaultCredentialsFilePath() (string, error) {
	if base, err := os.UserConfigDir(); err == nil && strings.TrimSpace(base) != "" {
		return filepath.Join(base, "HiveMind", "credentials.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hivemind", "credentials.json"), nil
}
