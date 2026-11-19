package githubx

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/procx"
)

// DefaultAPIBaseURL is the public GitHub REST API base URL.
const DefaultAPIBaseURL = "https://api.github.com"

type storedGitHubConfig struct {
	Token   string `json:"token,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
}

// ConnectorInfo summarizes the active native GitHub connector transport without making network calls.
type ConnectorInfo struct {
	Configured  bool   `json:"configured"`
	Source      string `json:"source"`      // "env", "config", "gh-token", "gh-cli", ""
	Mode        string `json:"mode"`        // "rest-env", "rest-config", "rest-gh", "cli", "none"
	BaseURL     string `json:"baseUrl"`     // e.g. "https://api.github.com"
	TokenMasked string `json:"tokenMasked"` // masked token when present
}

var (
	ghTokenMu       sync.Mutex
	ghTokenCached   string
	ghTokenCachedAt time.Time
	ghTokenTTL      = 2 * time.Minute
	ghAuthTokenFn   = runGHAuthToken
)

func githubConfigPath() (string, error) {
	if base, err := os.UserConfigDir(); err == nil && strings.TrimSpace(base) != "" {
		return filepath.Join(base, "HiveMind", "github-connector.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hivemind", "github-connector.json"), nil
}

// LoadStoredConfig returns only the token and base URL saved inside HiveMind's native connector config.
func LoadStoredConfig() (token, baseURL string) {
	path, err := githubConfigPath()
	if err != nil {
		return "", ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	var cfg storedGitHubConfig
	if json.Unmarshal(data, &cfg) != nil {
		return "", ""
	}
	return strings.TrimSpace(cfg.Token), strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
}

// LoadCredentials resolves GitHub REST API credentials in priority order:
// 1. GITHUB_TOKEN / GH_TOKEN environment variables
// 2. Native HiveMind GitHub connector config (%AppData%\HiveMind\github-connector.json)
// 3. Authenticated `gh auth token` session (upgrades `gh` users to native HTTPS REST API)
func LoadCredentials() (token, baseURL, source string) {
	baseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("GITHUB_API_URL")), "/")

	if envTok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); envTok != "" {
		token = envTok
		source = "env"
	} else if envTok := strings.TrimSpace(os.Getenv("GH_TOKEN")); envTok != "" {
		token = envTok
		source = "env"
	}

	storedTok, storedBase := LoadStoredConfig()
	if token == "" && storedTok != "" {
		token = storedTok
		source = "config"
	}
	if baseURL == "" && storedBase != "" {
		baseURL = storedBase
	}

	if token == "" && (baseURL == "" || strings.EqualFold(baseURL, DefaultAPIBaseURL)) {
		if ghTok := cachedGHAuthToken(); ghTok != "" {
			token = ghTok
			source = "gh-token"
		}
	}

	if baseURL == "" {
		baseURL = DefaultAPIBaseURL
	}
	return token, strings.TrimRight(baseURL, "/"), source
}

// SaveCredentials stores or clears the native GitHub Personal Access Token and optional API base URL.
func SaveCredentials(token, baseURL string) error {
	path, err := githubConfigPath()
	if err != nil {
		return err
	}
	cleanTok := strings.TrimSpace(token)
	cleanBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if cleanBase == DefaultAPIBaseURL {
		cleanBase = ""
	}
	cfg := storedGitHubConfig{
		Token:   cleanTok,
		BaseURL: cleanBase,
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	InvalidateTokenCache()
	return nil
}

// InvalidateTokenCache clears the short-lived in-memory `gh auth token` cache.
func InvalidateTokenCache() {
	ghTokenMu.Lock()
	ghTokenCached = ""
	ghTokenCachedAt = time.Time{}
	ghTokenMu.Unlock()
}

// InspectConnector returns the current native GitHub connector transport metadata.
func InspectConnector() ConnectorInfo {
	token, baseURL, source := LoadCredentials()
	info := ConnectorInfo{
		BaseURL: baseURL,
	}
	if token != "" {
		info.Configured = true
		info.Source = source
		info.TokenMasked = MaskToken(token)
		switch source {
		case "env":
			info.Mode = "rest-env"
		case "config":
			info.Mode = "rest-config"
		case "gh-token":
			info.Mode = "rest-gh"
		default:
			info.Mode = "rest-config"
		}
		return info
	}
	if _, err := exec.LookPath("gh"); err == nil {
		info.Source = "gh-cli"
		info.Mode = "cli"
		return info
	}
	info.Mode = "none"
	return info
}

// MaskToken masks a GitHub token for safe display in the UI.
func MaskToken(token string) string {
	k := strings.TrimSpace(token)
	if k == "" {
		return ""
	}
	if len(k) <= 8 {
		return "****"
	}
	return k[:4] + "…" + k[len(k)-4:]
}

func cachedGHAuthToken() string {
	if strings.TrimSpace(os.Getenv("HIVEMIND_DISABLE_GH_TOKEN")) == "1" {
		return ""
	}
	ghTokenMu.Lock()
	if ghTokenCached != "" && time.Since(ghTokenCachedAt) < ghTokenTTL {
		tok := ghTokenCached
		ghTokenMu.Unlock()
		return tok
	}
	ghTokenMu.Unlock()

	tok := strings.TrimSpace(ghAuthTokenFn())
	ghTokenMu.Lock()
	ghTokenCached = tok
	ghTokenCachedAt = time.Now()
	ghTokenMu.Unlock()
	return tok
}

func runGHAuthToken() string {
	ghBin, err := exec.LookPath("gh")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ghBin, "auth", "token", "--hostname", "github.com")
	procx.ConfigureContext(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}
