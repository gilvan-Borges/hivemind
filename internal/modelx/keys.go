package modelx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/atomicx"
)

// SupportedStoredKeyNames lists the API key/setting names the IDE can persist
// outside environment variables (saved in the user config dir, never in the repo).
var SupportedStoredKeyNames = []string{
	"TYPESAFE_API_KEY",
	"TYPESAFE_BASE_URL",
	"OPENCODE_API_KEY",
	"OPENCODE_BASE_URL",
	"CURSOR_API_KEY",
	"OPENROUTER_API_KEY",
	"OPENROUTER_BASE_URL",
	"OPENAI_API_KEY",
	"OPENAI_BASE_URL",
	"ANTHROPIC_API_KEY",
	"GOOGLE_API_KEY",
	"GOOGLE_CLOUD_PROJECT",
	"GOOGLE_CLOUD_LOCATION",
	"GOOGLE_GENAI_USE_VERTEXAI",
}

var (
	keysMu         sync.Mutex
	keysFilePathFn = defaultAPIKeysPath
	keysBlob       struct {
		path string
		mod  time.Time
		size int64
		raw  []byte
		ok   bool
	}
)

func defaultAPIKeysPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(base) == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", herr
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "HiveMind", "api-keys.json"), nil
}

func loadStoredKeysLocked() (map[string]string, error) {
	out := map[string]string{}
	path, err := keysFilePathFn()
	if err != nil {
		return out, err
	}
	info, statErr := os.Stat(path)
	var data []byte
	if statErr != nil {
		if os.IsNotExist(statErr) {
			keysBlob.ok = false
			return out, nil
		}
		return out, statErr
	}
	if keysBlob.ok && keysBlob.path == path && keysBlob.size == info.Size() && keysBlob.mod.Equal(info.ModTime()) && keysBlob.raw != nil {
		data = keysBlob.raw
	} else {
		data, err = os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				keysBlob.ok = false
				return out, nil
			}
			return out, err
		}
		keysBlob.path = path
		keysBlob.mod = info.ModTime()
		keysBlob.size = info.Size()
		keysBlob.raw = append([]byte(nil), data...)
		keysBlob.ok = true
		data = keysBlob.raw
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return out, err
	}
	for k, v := range raw {
		if v = strings.TrimSpace(v); v != "" {
			uk := strings.ToUpper(strings.TrimSpace(k))
			out[uk] = v
		}
	}
	migrated := false
	for k, v := range out {
		if isSecretStoredKey(k) {
			resolved, mig := vaultLoadSecret(path, "modelx:apikey:"+k, v)
			if mig {
				migrated = true
			}
			if resolved != "" {
				out[k] = resolved
			} else {
				delete(out, k)
			}
		}
	}
	if migrated {
		disk := encodeStoredKeysForDisk(path, out)
		if migData, merr := json.MarshalIndent(disk, "", "  "); merr == nil {
			migData = append(migData, '\n')
			if writeSecretFile(path, migData) == nil {
				if st, serr := os.Stat(path); serr == nil {
					keysBlob.path = path
					keysBlob.mod = st.ModTime()
					keysBlob.size = st.Size()
					keysBlob.raw = append([]byte(nil), migData...)
					keysBlob.ok = true
				}
			}
		}
	}
	return out, nil
}

func isSecretStoredKey(name string) bool {
	u := strings.ToUpper(strings.TrimSpace(name))
	if u == "" || strings.HasSuffix(u, "_URL") || u == "GOOGLE_CLOUD_PROJECT" || u == "GOOGLE_CLOUD_LOCATION" || u == "GOOGLE_GENAI_USE_VERTEXAI" {
		return false
	}
	return true
}

func encodeStoredKeysForDisk(path string, stored map[string]string) map[string]string {
	disk := make(map[string]string, len(stored))
	for k, v := range stored {
		if isSecretStoredKey(k) {
			if ref, err := vaultSaveSecret(path, "modelx:apikey:"+k, v); err == nil && ref != "" {
				disk[k] = ref
				continue
			}
		}
		disk[k] = v
	}
	return disk
}

// EnvOrStored returns the value of the environment variable name, falling back
// to the persisted IDE API-keys store. Environment always wins.
func EnvOrStored(name string) string {
	name = strings.ToUpper(strings.TrimSpace(name))
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	keysMu.Lock()
	defer keysMu.Unlock()
	stored, err := loadStoredKeysLocked()
	if err != nil {
		return ""
	}
	return stored[name]
}

// FirstEnvOrStored checks several names in order (env first, then stored).
func FirstEnvOrStored(names ...string) string {
	for _, n := range names {
		if v := EnvOrStored(n); v != "" {
			return v
		}
	}
	return ""
}

// StoredAPIKeysStatus returns the masked persisted values for every supported
// name so the IDE can show which keys are configured without leaking them.
func StoredAPIKeysStatus() map[string]string {
	keysMu.Lock()
	stored, err := loadStoredKeysLocked()
	keysMu.Unlock()
	out := map[string]string{}
	if err != nil {
		return out
	}
	for _, name := range SupportedStoredKeyNames {
		if v := stored[name]; v != "" {
			if strings.HasSuffix(name, "_URL") || name == "GOOGLE_CLOUD_PROJECT" || name == "GOOGLE_CLOUD_LOCATION" {
				out[name] = v
			} else {
				out[name] = MaskKey(v)
			}
		}
		if strings.TrimSpace(os.Getenv(name)) != "" {
			out["env_"+name] = "1"
		}
	}
	// Mirror legacy credentials.json OpenCode token into the unified global status view.
	if ocKey, ocBase, ocSrc := LoadOpenCodeCredentials(); ocKey != "" {
		if ocSrc == "env" {
			out["env_OPENCODE_API_KEY"] = "1"
		} else if out["OPENCODE_API_KEY"] == "" {
			out["OPENCODE_API_KEY"] = MaskKey(ocKey)
		}
		if ocBase != "" && out["OPENCODE_BASE_URL"] == "" {
			out["OPENCODE_BASE_URL"] = ocBase
		}
	}
	return out
}

// SaveStoredAPIKeys persists the given entries (only supported names are kept;
// empty values remove the stored entry). Returns the new masked status.
func SaveStoredAPIKeys(entries map[string]string) (map[string]string, error) {
	path, err := keysFilePathFn()
	if err != nil {
		return nil, err
	}
	keysMu.Lock()
	defer keysMu.Unlock()
	stored, err := loadStoredKeysLocked()
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, n := range SupportedStoredKeyNames {
		allowed[n] = true
	}
	for k, v := range entries {
		name := strings.ToUpper(strings.TrimSpace(k))
		if !allowed[name] {
			continue
		}
		if v = strings.TrimSpace(v); v == "" {
			delete(stored, name)
			if isSecretStoredKey(name) {
				vaultDeleteSecret(path, "modelx:apikey:"+name)
			}
		} else {
			stored[name] = v
		}
	}
	disk := encodeStoredKeysForDisk(path, stored)
	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if err := writeSecretFile(path, data); err != nil {
		keysBlob.ok = false
		return nil, err
	}
	if info, statErr := os.Stat(path); statErr == nil {
		keysBlob.path = path
		keysBlob.mod = info.ModTime()
		keysBlob.size = info.Size()
		keysBlob.raw = append([]byte(nil), data...)
		keysBlob.ok = true
	} else {
		keysBlob.ok = false
	}
	out := map[string]string{}
	for k, v := range stored {
		out[k] = MaskKey(v)
	}
	return out, nil
}

// MaskKey hides a secret for safe display. Only a short suffix is kept.
func MaskKey(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if len(v) <= 8 {
		return "••••"
	}
	return "••••" + v[len(v)-4:]
}

// writeSecretFile replaces path atomically and leaves it readable only by the user.
func writeSecretFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicx.WriteFile(path, data, 0o600)
}
