package resident

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/jev"
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

// BuildLLM asks Jev for the route and only then builds the model.
// An explicit provider+model is a pin. auto goes through Score.
func BuildLLM(ctx context.Context, baseDir string, cfg Config) (model.LLM, string, string, error) {
	return buildThroughJev(ctx, cfg, jevRouteDeps{tokens: LoadRouteTokens(baseDir)})
}

type jevRouteDeps struct {
	build   func(ctx context.Context, provider, modelName string) (model.LLM, error)
	catalog *jev.Catalog
	health  func() jev.HealthSnapshot
	tokens  int
	policy  *jev.Policy
}

func buildThroughJev(ctx context.Context, cfg Config, deps jevRouteDeps) (model.LLM, string, string, error) {
	build := deps.build
	if build == nil {
		build = modelx.New
	}
	pol := jev.DefaultPolicy()
	if deps.policy != nil {
		pol = *deps.policy
	} else if loaded, err := jev.LoadPolicy(); err == nil {
		pol = loaded
	}
	tokens := deps.tokens
	class, comp := adjustResidentRoute(tokens)
	pin := jev.AgentPin{Provider: "jev", Model: "auto"}
	if explicitResidentPin(cfg) {
		prov, mod := ResolveModelRoute(cfg)
		pin = jev.AgentPin{Provider: prov, Model: mod}
	}
	cat := jev.DefaultStaticCatalog()
	if deps.catalog != nil {
		cat = *deps.catalog
	}
	health := jev.HealthSnapshot{AgyRemainingPct: -1}
	if deps.health != nil {
		health = deps.health()
	} else if pres := modelx.JevCredentialPresence(); pres != nil {
		health.Credentials = pres
	}
	// Failover Jev opcional (SPEC_JEV_ORQUESTRADOR / W2-10): com cfg.JevFailover
	// ligado, uma falha de build não aborta o resident — o Score re-avalia
	// excluindo o par que falhou. Sem o flag, comportamento original (1 tentativa).
	excludePairs := map[string]bool{}
	attempts := 1
	if cfg.JevFailover {
		attempts = 3
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		snapHealth := health
		snapHealth.ExcludePairs = excludePairs
		choice, ref := jev.Score(jev.Task{
			Role:        "resident",
			Class:       class,
			Complexity: comp,
			InputTokens: tokens,
			NeedsTools:  true,
		}, cat, pol, snapHealth, pin, jev.RepoPolicy{})
		if ref.Code != "" {
			lastErr = fmt.Errorf("jev recusou a rota do residente (%s)", ref.Code)
			break
		}
		llm, err := build(ctx, choice.Provider, choice.Model)
		if err == nil {
			return llm, choice.Provider, choice.Model, nil
		}
		lastErr = fmt.Errorf("inicializar modelo do residente (%s / %s): %w", choice.Provider, choice.Model, err)
		if !cfg.JevFailover {
			break
		}
		excludePairs[strings.ToLower(choice.Provider)+"\x00"+choice.Model] = true
	}
	return nil, "", "", lastErr
}

func explicitResidentPin(cfg Config) bool {
	p := strings.ToLower(strings.TrimSpace(cfg.Provider))
	m := strings.TrimSpace(cfg.Model)
	if p == "" || p == "auto" || p == "jev" {
		return false
	}
	return m != "" && !strings.EqualFold(m, "auto")
}

// adjustResidentRoute shifts the next auto route from the last measured input.
// Under 20k tokens stays trivial. Above 100k is a hard coding step.
func adjustResidentRoute(inputTokens int) (jev.Class, jev.Complexity) {
	switch {
	case inputTokens > 100_000:
		return jev.ClassCoding, jev.ComplexityHigh
	case inputTokens > 20_000:
		return jev.ClassCoding, jev.ComplexityMedium
	default:
		return jev.ClassTrivial, jev.ComplexityLow
	}
}

func routeStatsPath(baseDir string) string {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = DefaultBaseDir()
	}
	return filepath.Join(baseDir, "route-stats.json")
}

// SaveRouteTokens records the last turn's input size for the next auto route.
func SaveRouteTokens(baseDir string, inputTokens int64) error {
	if inputTokens < 0 {
		inputTokens = 0
	}
	raw, err := json.Marshal(map[string]int64{"lastInputTokens": inputTokens})
	if err != nil {
		return err
	}
	path := routeStatsPath(baseDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadRouteTokens returns the last saved input size. Missing file is zero.
func LoadRouteTokens(baseDir string) int {
	data, err := os.ReadFile(routeStatsPath(baseDir))
	if err != nil {
		return 0
	}
	var body struct {
		LastInputTokens int `json:"lastInputTokens"`
	}
	if json.Unmarshal(data, &body) != nil || body.LastInputTokens < 0 {
		return 0
	}
	return body.LastInputTokens
}
