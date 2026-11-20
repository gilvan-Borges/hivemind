package jev

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// Weights controls the normalized Score components (re-normalized to sum to 1).
type Weights struct {
	Quality float64 `json:"quality"`
	Cost    float64 `json:"cost"`
	Latency float64 `json:"latency"`
}

// CooldownConfig defines default circuit breaker durations.
type CooldownConfig struct {
	Quota     string `json:"quota"`
	RateLimit string `json:"rate_limit"`
	Outage    string `json:"outage"`
}

// RepoOverride restricts providers for a given repository key.
type RepoOverride struct {
	Deny []string `json:"deny"`
}

// RolePin fixes a default provider+model for a normalized role in policy.json.
type RolePin struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Strict   bool   `json:"strict"`
}

// Policy holds user-level Jev routing configuration stored outside any repository.
type Policy struct {
	Version                 int                     `json:"version"`
	Enabled                 bool                    `json:"enabled"`
	Weights                 Weights                 `json:"weights"`
	AutoEscalateUSD         float64                 `json:"autoEscalateUsd"`
	MaxEscalateRatio        float64                 `json:"maxEscalateRatio"`
	OpenRouterMinReserveUSD float64                 `json:"openRouterMinReserveUsd,omitempty"`
	DailyUSD                map[string]float64      `json:"dailyUsd"`
	Cooldown                CooldownConfig          `json:"cooldown"`
	AllowDataClass          []DataClass             `json:"allowDataClass"`
	Repos                   map[string]RepoOverride `json:"repos,omitempty"`
	Pins                    map[string]RolePin      `json:"pins,omitempty"`
}

// RepoPolicy represents the optional .hivemind/jev.yaml file inside a project.
// By design, a repository can ONLY deny providers; it cannot widen budgets,
// weights, allowlists, or pins.
type RepoPolicy struct {
	DenyProviders []string `yaml:"deny_providers"`
}

// DefaultPolicy returns the safe default Jev configuration (opt-in Enabled: false).
func DefaultPolicy() Policy {
	return Policy{
		Version: 1,
		Enabled: false,
		Weights: Weights{
			Quality: 0.35,
			Cost:    0.50,
			Latency: 0.15,
		},
		AutoEscalateUSD:         0.02,
		MaxEscalateRatio:        3.0,
		OpenRouterMinReserveUSD: 1.00,
		DailyUSD: map[string]float64{
			"openrouter":       1.0,
			"opencode":         1.0,
			"gemini":           1.0,
			"vertex":           1.0,
			"openai":           1.0,
			"openai-responses": 1.0,
			"anthropic":        1.0,
		},
		Cooldown: CooldownConfig{
			Quota:     "15m",
			RateLimit: "2m",
			Outage:    "1m",
		},
		AllowDataClass: []DataClass{
			DataClassCLIAccount,
			DataClassHTTPAPI,
			DataClassWorkspaceAgent,
		},
	}
}

// ConfigDir returns the user-level Jev config directory (os.UserConfigDir()/HiveMind/jev).
func ConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(base) == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", fmt.Errorf("diretório de configuração indisponível: %w", herr)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "HiveMind", "jev"), nil
}

// PolicyPath returns the default user-level policy.json path.
func PolicyPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "policy.json"), nil
}

// LoadPolicy reads policy.json from the user config directory, returning DefaultPolicy
// when the file does not exist.
func LoadPolicy() (Policy, error) {
	p, err := PolicyPath()
	if err != nil {
		return DefaultPolicy(), err
	}
	return LoadPolicyFrom(p)
}

// LoadPolicyFrom loads a Policy JSON file from path, applying defaults for missing fields.
func LoadPolicyFrom(path string) (Policy, error) {
	pol := DefaultPolicy()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return pol, nil
		}
		return pol, err
	}
	var parsed Policy
	if err := json.Unmarshal(data, &parsed); err != nil {
		return pol, err
	}
	if parsed.Version == 0 {
		parsed.Version = 1
	}
	if parsed.Weights.Quality == 0 && parsed.Weights.Cost == 0 && parsed.Weights.Latency == 0 {
		parsed.Weights = pol.Weights
	}
	if parsed.AutoEscalateUSD <= 0 {
		parsed.AutoEscalateUSD = pol.AutoEscalateUSD
	}
	if parsed.MaxEscalateRatio <= 0 {
		parsed.MaxEscalateRatio = pol.MaxEscalateRatio
	}
	if parsed.OpenRouterMinReserveUSD <= 0 {
		parsed.OpenRouterMinReserveUSD = pol.OpenRouterMinReserveUSD
	}
	if parsed.DailyUSD == nil {
		parsed.DailyUSD = pol.DailyUSD
	}
	if parsed.Cooldown == (CooldownConfig{}) {
		parsed.Cooldown = pol.Cooldown
	}
	if parsed.AllowDataClass == nil {
		parsed.AllowDataClass = pol.AllowDataClass
	}
	return parsed, nil
}

// SavePolicyTo writes policy.json with strict permissions (0700 dir, 0600 file).
func SavePolicyTo(path string, pol Policy) error {
	if pol.Version == 0 {
		pol.Version = 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(pol, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return os.WriteFile(path, data, 0o600)
	}
	return nil
}

// LoadRepoPolicy loads the optional .hivemind/jev.yaml in workspace.
// Any security-sensitive fields (dailyUsd, weights, allow, pins) are ignored and reported as warnings.
func LoadRepoPolicy(workspace string) (RepoPolicy, []string, error) {
	if strings.TrimSpace(workspace) == "" {
		return RepoPolicy{}, nil, nil
	}
	path := filepath.Join(workspace, ".hivemind", "jev.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return RepoPolicy{}, nil, nil
		}
		return RepoPolicy{}, nil, err
	}
	var rawMap map[string]any
	if err := yaml.Unmarshal(data, &rawMap); err != nil {
		return RepoPolicy{}, nil, err
	}
	var warnings []string
	for k := range rawMap {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "deny_providers":
		case "dailyusd", "daily_usd", "weights", "allow", "allowdataclass", "allow_data_class", "pins", "enabled", "autoescalateusd", "auto_escalate_usd":
			warnings = append(warnings, fmt.Sprintf("campo %q em .hivemind/jev.yaml ignorado por segurança (use policy.json do usuário)", k))
		}
	}
	var rp RepoPolicy
	if err := yaml.Unmarshal(data, &rp); err != nil {
		return RepoPolicy{}, warnings, err
	}
	cleaned := make([]string, 0, len(rp.DenyProviders))
	for _, p := range rp.DenyProviders {
		norm := team.NormalizeProvider(p, "")
		if norm != "" {
			cleaned = append(cleaned, norm)
		}
	}
	rp.DenyProviders = cleaned
	return rp, warnings, nil
}

// RepoKey returns a stable repository key ("host/owner/repo" when remoteURL parses,
// or a SHA-256 prefix of the absolute workspace path when no remote exists).
func RepoKey(workspace, remoteURL string) string {
	if key := parseRemoteRepoKey(remoteURL); key != "" {
		return key
	}
	abs, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil || abs == "" {
		abs = strings.TrimSpace(workspace)
	}
	sum := sha256.Sum256([]byte(filepath.ToSlash(abs)))
	return "local:" + hex.EncodeToString(sum[:8])
}

func parseRemoteRepoKey(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// SCP-like syntax: git@github.com:owner/repo.git
	if !strings.Contains(s, "://") && strings.Contains(s, ":") {
		if at := strings.IndexByte(s, '@'); at >= 0 {
			s = s[at+1:]
		}
		host, rest, ok := strings.Cut(s, ":")
		if ok {
			rest = strings.TrimSuffix(strings.Trim(rest, "/"), ".git")
			if host != "" && strings.Contains(rest, "/") {
				return strings.ToLower(host + "/" + rest)
			}
		}
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return ""
	}
	path := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	if !strings.Contains(path, "/") {
		return ""
	}
	return strings.ToLower(u.Host + "/" + path)
}

func (p Policy) normalizedWeights() Weights {
	w := p.Weights
	if w.Quality < 0 {
		w.Quality = 0
	}
	if w.Cost < 0 {
		w.Cost = 0
	}
	if w.Latency < 0 {
		w.Latency = 0
	}
	sum := w.Quality + w.Cost + w.Latency
	if sum <= 0 {
		return Weights{Quality: 0.35, Cost: 0.50, Latency: 0.15}
	}
	return Weights{
		Quality: w.Quality / sum,
		Cost:    w.Cost / sum,
		Latency: w.Latency / sum,
	}
}

func (p Policy) isDataClassAllowed(dc DataClass) bool {
	if len(p.AllowDataClass) == 0 {
		return false
	}
	for _, allowed := range p.AllowDataClass {
		if allowed == dc {
			return true
		}
	}
	return false
}

// ValidatePolicy rejects weights, caps and data classes the local editor must not store.
func ValidatePolicy(pol Policy) error {
	w := pol.Weights
	if math.IsNaN(w.Quality) || math.IsNaN(w.Cost) || math.IsNaN(w.Latency) {
		return fmt.Errorf("pesos inválidos")
	}
	if w.Quality < 0 || w.Cost < 0 || w.Latency < 0 || w.Quality+w.Cost+w.Latency <= 0 {
		return fmt.Errorf("pesos inválidos")
	}
	if pol.AutoEscalateUSD < 0 || pol.MaxEscalateRatio < 0 {
		return fmt.Errorf("teto de escalada inválido")
	}
	for _, dc := range pol.AllowDataClass {
		switch dc {
		case DataClassCLIAccount, DataClassHTTPAPI, DataClassWorkspaceAgent:
		default:
			return fmt.Errorf("classe de dado desconhecida: %s", dc)
		}
	}
	for name, v := range pol.DailyUSD {
		if v < 0 || math.IsNaN(v) || strings.Contains(name, "/") || strings.Contains(name, "..") {
			return fmt.Errorf("teto diário inválido para %s", name)
		}
	}
	return nil
}

// SavePolicy writes the user-level policy.json. A repository file cannot call this.
func SavePolicy(pol Policy) error {
	if err := ValidatePolicy(pol); err != nil {
		return err
	}
	path, err := PolicyPath()
	if err != nil {
		return err
	}
	return SavePolicyTo(path, pol)
}

func (p Policy) isProviderDenied(provider, repoKey string, repoPolicy RepoPolicy) bool {
	prov := strings.ToLower(strings.TrimSpace(provider))
	for _, denied := range repoPolicy.DenyProviders {
		if strings.EqualFold(denied, prov) {
			return true
		}
	}
	if repoKey != "" {
		if ov, ok := p.Repos[repoKey]; ok {
			for _, denied := range ov.Deny {
				if strings.EqualFold(strings.TrimSpace(denied), prov) {
					return true
				}
			}
		}
	}
	return false
}
