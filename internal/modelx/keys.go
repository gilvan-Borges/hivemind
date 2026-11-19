package modelx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SupportedStoredKeyNames lists the API key/setting names the IDE can persist
// outside environment variables (saved in the user config dir, never in the repo).
var SupportedStoredKeyNames = []string{
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
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return out, err
	}
	for k, v := range raw {
		if v = strings.TrimSpace(v); v != "" {
			out[strings.ToUpper(strings.TrimSpace(k))] = v
		}
	}
	return out, nil
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
			out[name] = MaskKey(v)
		}
		if strings.TrimSpace(os.Getenv(name)) != "" {
			out["env_"+name] = "1"
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
		} else {
			stored[name] = v
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, os.WriteFile(path, data, 0o600)
	}
	out := map[string]string{}
	for k, v := range stored {
		out[k] = MaskKey(v)
	}
	return out, nil
}

// MaskKey hides most of a secret for safe display.
func MaskKey(v string) string {
	v = strings.TrimSpace(v)
	if len(v) <= 10 {
		return "••••"
	}
	return v[:5] + "…" + v[len(v)-3:]
}
