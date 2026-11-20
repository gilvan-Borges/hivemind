package resident

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// DefaultConfigPath returns %LOCALAPPDATA%\HiveMind\resident\resident.json
// (outside any project repository, §11).
func DefaultConfigPath(baseDir string) string {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = DefaultBaseDir()
	}
	return filepath.Join(baseDir, "resident.json")
}

// LoadConfig reads resident.json from baseDir (returns defaults when absent).
// Never reads configuration from .hivemind/ inside a repository (§11).
func LoadConfig(baseDir string) (Config, error) {
	p := DefaultConfigPath(baseDir)
	cfg := Config{
		Enabled:    true,
		Workstream: DefaultWorkstream,
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("ler resident.json: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("resident.json inválido: %w", err)
	}
	cfg.Workstream = SanitizeWorkstream(cfg.Workstream)
	return cfg, nil
}

// SaveConfig persists resident.json with owner-only permissions (0600, §11).
func SaveConfig(baseDir string, cfg Config) error {
	p := DefaultConfigPath(baseDir)
	cfg.Workstream = SanitizeWorkstream(cfg.Workstream)
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	cfg.Model = strings.TrimSpace(cfg.Model)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// DefaultModelRoute selects a cheap/auto HTTP provider already configured in
// internal/modelx (§5, §13). agy is NOT the default because it does not stream
// token-by-token and runs as a subprocess; Cursor CLI is supported if selected
// or configured, though HTTP providers are preferred first.
func DefaultModelRoute() (provider, modelName string) {
	if modelx.EnvOrStored("OPENROUTER_API_KEY") != "" {
		return "openrouter", "openrouter/auto"
	}
	if modelx.FirstEnvOrStored("OPENCODE_API_KEY", "OPENCODE_ZEN_API_KEY") != "" {
		return "opencode", "opencode:minimax-m2.5-free"
	}
	if modelx.FirstEnvOrStored("GOOGLE_API_KEY", "GEMINI_API_KEY") != "" {
		return "gemini", "gemini-2.5-flash"
	}
	if modelx.EnvOrStored("OPENAI_API_KEY") != "" {
		return "openai", "gpt-4o-mini"
	}
	if modelx.FirstEnvOrStored("ANTHROPIC_API_KEY") != "" {
		return "anthropic", "claude-3-5-haiku-latest"
	}
	if modelx.EnvOrStored("CURSOR_API_KEY") != "" {
		return "cursor", "cursor:auto"
	}
	return "openrouter", "openrouter/auto"
}

// ResolveModelRoute normalizes an explicit config choice or falls back to
// DefaultModelRoute().
func ResolveModelRoute(cfg Config) (provider, modelName string) {
	p := strings.ToLower(strings.TrimSpace(cfg.Provider))
	m := strings.TrimSpace(cfg.Model)
	if m == "" || strings.EqualFold(m, "auto") {
		if p == "" || p == "auto" {
			return DefaultModelRoute()
		}
		switch p {
		case "openrouter":
			return "openrouter", "openrouter/auto"
		case "opencode", "opencode-go", "opencode-zen":
			return "opencode", "opencode:minimax-m2.5-free"
		case "gemini", "vertex":
			return p, "gemini-2.5-flash"
		case "openai", "openai-responses":
			return p, "gpt-4o-mini"
		case "anthropic":
			return "anthropic", "claude-3-5-haiku-latest"
		case "cursor":
			return "cursor", "cursor:auto"
		default:
			return DefaultModelRoute()
		}
	}
	normProv := team.NormalizeProvider(p, m)
	return normProv, m
}

// BuildLLM constructs the ADK model via internal/modelx for the resident turn.
func BuildLLM(ctx context.Context, cfg Config) (model.LLM, string, string, error) {
	prov, mod := ResolveModelRoute(cfg)
	llm, err := modelx.New(ctx, prov, mod)
	if err != nil {
		return nil, prov, mod, fmt.Errorf("inicializar modelo do residente (%s / %s): %w", prov, mod, err)
	}
	return llm, prov, mod, nil
}
