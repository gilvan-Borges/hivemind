package modelx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gilvan-Borges/HiveMind/internal/orgx"
)

// ProviderProfile defines a named credential/account profile for any model provider
// (agy, cursor, openrouter, openai, anthropic, gemini, opencode) with optional
// organization/path auto-detection patterns.
type ProviderProfile struct {
	ID          string            `json:"id"`
	Provider    string            `json:"provider"`
	Name        string            `json:"name"`
	AuthMode    string            `json:"auth_mode,omitempty"` // p/ agy: "live" | "adc" | "env"; p/ outros: "key"
	Command     string            `json:"command,omitempty"`   // p/ agy: caminho opcional de binário/wrapper
	Env         map[string]string `json:"env,omitempty"`
	OrgPatterns []string          `json:"org_patterns,omitempty"`
	IsDefault   bool              `json:"is_default,omitempty"`
}

// ProviderProfileSummary is the secret-safe representation exposed to the IDE UI.
type ProviderProfileSummary struct {
	ID           string            `json:"id"`
	Provider     string            `json:"provider"`
	Name         string            `json:"name"`
	AuthMode     string            `json:"authMode,omitempty"`
	Command      string            `json:"command,omitempty"`
	EnvMasked    map[string]string `json:"envMasked,omitempty"`    // segredos mascarados
	ConfigValues map[string]string `json:"configValues,omitempty"` // valores não-secretos (URL, projeto GCP, CLOUDSDK_CONFIG, etc.)
	OrgPatterns  []string          `json:"orgPatterns,omitempty"`
	IsDefault    bool              `json:"isDefault,omitempty"`
	Ready        bool              `json:"ready"`
	ReadyDetail  string            `json:"readyDetail,omitempty"`
}

// SaveProfileRequest captures user input from the IDE UI when creating/updating a profile.
type SaveProfileRequest struct {
	ID            string            `json:"id"`
	Provider      string            `json:"provider"`
	Name          string            `json:"name"`
	AuthMode      string            `json:"authMode,omitempty"`
	Command       string            `json:"command,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	OrgPatterns   []string          `json:"orgPatterns,omitempty"`
	IsDefault     bool              `json:"isDefault,omitempty"`
	BindWorkspace string            `json:"bindWorkspace,omitempty"`
}

// ResolvedProviderInfo explains which profile was selected for a workspace and why.
type ResolvedProviderInfo struct {
	Provider    string `json:"provider"`
	ProfileID   string `json:"profileId,omitempty"`
	ProfileName string `json:"profileName,omitempty"`
	MatchMode   string `json:"matchMode"` // "override", "manual", "auto_org", "default", "global"
	MatchedRule string `json:"matchedRule,omitempty"`
	BindingMode string `json:"bindingMode,omitempty"` // "", "default", or pinned profileID
}

// ProfilesOverview bundles org detection + all profiles + active resolution per provider.
type ProfilesOverview struct {
	Identity orgx.Identity                   `json:"identity"`
	Profiles []ProviderProfileSummary        `json:"profiles"`
	Resolved map[string]ResolvedProviderInfo `json:"resolved"`
}

type storedProfilesFile struct {
	Profiles          []ProviderProfile            `json:"profiles,omitempty"`
	WorkspaceBindings map[string]map[string]string `json:"workspace_bindings,omitempty"` // normWorkspace -> provider -> profileID
}

type workspaceCtxKey struct{}
type profileOverrideCtxKey struct{}

var (
	profilesMu         sync.Mutex
	profilesFilePathFn = defaultProfilesFilePath
	profilesBlob       struct {
		path string
		mod  time.Time
		size int64
		raw  []byte
		ok   bool
	}
)

func defaultProfilesFilePath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(base) == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", herr
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "HiveMind", "provider-profiles.json"), nil
}

// WithWorkspace attaches the target workspace path to ctx so modelx.New resolves
// organization-scoped accounts automatically.
func WithWorkspace(ctx context.Context, workspace string) context.Context {
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		return ctx
	}
	return context.WithValue(ctx, workspaceCtxKey{}, ws)
}

// WorkspaceFromContext extracts the workspace attached via WithWorkspace.
func WorkspaceFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	ws, _ := ctx.Value(workspaceCtxKey{}).(string)
	return strings.TrimSpace(ws)
}

// WithProfileOverride pins a specific profile ID for provider in ctx.
func WithProfileOverride(ctx context.Context, provider, profileID string) context.Context {
	prov := NormalizeProfileProvider(provider)
	pid := sanitizeSlug(profileID)
	if prov == "" || pid == "" {
		return ctx
	}
	next := map[string]string{}
	if existing, ok := ctx.Value(profileOverrideCtxKey{}).(map[string]string); ok {
		for k, v := range existing {
			next[k] = v
		}
	}
	next[prov] = pid
	return context.WithValue(ctx, profileOverrideCtxKey{}, next)
}

func profileOverrideFromContext(ctx context.Context, provider string) string {
	if ctx == nil {
		return ""
	}
	m, _ := ctx.Value(profileOverrideCtxKey{}).(map[string]string)
	return m[NormalizeProfileProvider(provider)]
}

// NormalizeProfileProvider maps provider aliases to canonical profile categories.
func NormalizeProfileProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "agy", "antigravity":
		return "agy"
	case "opencode", "opencode-go", "opencode_go", "opencode-zen", "zen":
		return "opencode"
	case "gemini", "vertex":
		return "gemini"
	case "openai", "openai-responses":
		return "openai"
	case "openrouter":
		return "openrouter"
	case "anthropic":
		return "anthropic"
	case "cursor":
		return "cursor"
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

func loadProfilesLocked() (storedProfilesFile, error) {
	var out storedProfilesFile
	path, err := profilesFilePathFn()
	if err != nil {
		return out, err
	}
	info, statErr := os.Stat(path)
	var data []byte
	if statErr != nil {
		if os.IsNotExist(statErr) {
			profilesBlob.ok = false
			return out, nil
		}
		return out, statErr
	}
	if profilesBlob.ok && profilesBlob.path == path && profilesBlob.size == info.Size() && profilesBlob.mod.Equal(info.ModTime()) && profilesBlob.raw != nil {
		data = profilesBlob.raw
	} else {
		data, err = os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				profilesBlob.ok = false
				return out, nil
			}
			return out, err
		}
		profilesBlob.path = path
		profilesBlob.mod = info.ModTime()
		profilesBlob.size = info.Size()
		profilesBlob.raw = append([]byte(nil), data...)
		profilesBlob.ok = true
		data = profilesBlob.raw
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	for i := range out.Profiles {
		out.Profiles[i].Provider = NormalizeProfileProvider(out.Profiles[i].Provider)
		out.Profiles[i].ID = sanitizeSlug(out.Profiles[i].ID)
	}
	return out, nil
}

func saveProfilesLocked(doc storedProfilesFile) error {
	path, err := profilesFilePathFn()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := writeSecretFile(path, data); err != nil {
		profilesBlob.ok = false
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		profilesBlob.ok = false
		return nil
	}
	profilesBlob.path = path
	profilesBlob.mod = info.ModTime()
	profilesBlob.size = info.Size()
	profilesBlob.raw = append([]byte(nil), data...)
	profilesBlob.ok = true
	return nil
}

// AgyADCConfigDir returns an isolated CLOUDSDK_CONFIG directory inside the user's
// HiveMind config root for a named AGY ADC profile.
func AgyADCConfigDir(profileID string) (string, error) {
	base, err := profilesFilePathFn()
	if err != nil {
		return "", err
	}
	slug := sanitizeSlug(profileID)
	if slug == "" {
		slug = "default"
	}
	return filepath.Join(filepath.Dir(base), "agy-profiles", slug, "gcloud"), nil
}

// HasAgyADCCredentials reports whether an isolated gcloud config dir already holds
// Application Default Credentials.
func HasAgyADCCredentials(configDir string) bool {
	if strings.TrimSpace(configDir) == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(strings.TrimSpace(configDir), "application_default_credentials.json"))
	return err == nil && !info.IsDir()
}

// ResolveProfile selects the active profile for provider given ctx's workspace/override.
// mode "isolated" means org-scoped profiles exist and this workspace must not receive
// another org's account, the default profile, or the global env/api-keys store.
func ResolveProfile(ctx context.Context, provider string) (*ProviderProfile, string, string) {
	prov := NormalizeProfileProvider(provider)
	if prov == "" {
		return nil, "global", ""
	}
	profilesMu.Lock()
	doc, err := loadProfilesLocked()
	profilesMu.Unlock()
	if err != nil || len(doc.Profiles) == 0 {
		return nil, "global", ""
	}

	byID := func(id string) *ProviderProfile {
		for i := range doc.Profiles {
			if doc.Profiles[i].Provider == prov && strings.EqualFold(doc.Profiles[i].ID, id) {
				return prepareProfile(doc.Profiles[i])
			}
		}
		return nil
	}

	// 1. Explicit ctx override. "__global__" forces the default/system credential without profile isolation.
	if oid := profileOverrideFromContext(ctx, prov); oid != "" {
		if strings.EqualFold(oid, "__global__") || strings.EqualFold(oid, "global") {
			return nil, "global", ""
		}
		if p := byID(oid); p != nil {
			return p, "override", ""
		}
	}

	ws := WorkspaceFromContext(ctx)
	wsKey := orgx.NormalizeWorkspaceKey(ws)

	// 2. Explicit workspace binding. "global" is an opt-in to the legacy env store.
	if wsKey != "" && doc.WorkspaceBindings != nil {
		if provMap := doc.WorkspaceBindings[wsKey]; provMap != nil {
			pinned := strings.TrimSpace(provMap[prov])
			if strings.EqualFold(pinned, "global") {
				return nil, "global", ""
			}
			if pinned != "" {
				if p := byID(pinned); p != nil {
					return p, "manual", ""
				}
			}
		}
	}

	hasOrgScoped := false
	if ws != "" {
		orgID := orgx.Detect(ws)
		for i := range doc.Profiles {
			p := doc.Profiles[i]
			if p.Provider != prov || len(p.OrgPatterns) == 0 {
				continue
			}
			hasOrgScoped = true
			if matched, pat := orgx.MatchPatterns(orgID, p.OrgPatterns); matched {
				return prepareProfile(p), "auto_org", pat
			}
		}
	} else {
		for i := range doc.Profiles {
			if doc.Profiles[i].Provider == prov && len(doc.Profiles[i].OrgPatterns) > 0 {
				hasOrgScoped = true
				break
			}
		}
	}
	if hasOrgScoped {
		return nil, "isolated", ""
	}

	for i := range doc.Profiles {
		if doc.Profiles[i].Provider == prov && doc.Profiles[i].IsDefault {
			return prepareProfile(doc.Profiles[i]), "default", ""
		}
	}
	return nil, "global", ""
}

func prepareProfile(p ProviderProfile) *ProviderProfile {
	if len(p.Env) > 0 {
		env := make(map[string]string, len(p.Env))
		for k, v := range p.Env {
			uk := strings.ToUpper(strings.TrimSpace(k))
			if uk == "" {
				continue
			}
			env[uk] = v
		}
		p.Env = env
	}
	if p.Provider == "agy" && strings.EqualFold(strings.TrimSpace(p.AuthMode), "adc") {
		if p.Env == nil {
			p.Env = map[string]string{}
		}
		if dir, err := AgyADCConfigDir(p.ID); err == nil && dir != "" {
			p.Env["CLOUDSDK_CONFIG"] = dir
			p.Env["AGY_ADC_AUTH"] = "true"
		}
	}
	return &p
}

// CredentialIsolated reports that provider must not use another org's account or the global key store.
func CredentialIsolated(ctx context.Context, provider string) bool {
	_, mode, _ := ResolveProfile(ctx, provider)
	return mode == "isolated"
}

// ResolveEnv returns the workspace-aware setting. An isolated org gets an empty string
// instead of another profile, the process environment, or api-keys.json.
func ResolveEnv(ctx context.Context, provider string, names ...string) string {
	return resolveEnvForContext(ctx, provider, names...)
}

// resolveEnvForContext returns the profile-scoped setting when a profile resolves for
// (ctx, provider) and defines one of names, otherwise falling back to env/global store.
func resolveEnvForContext(ctx context.Context, provider string, names ...string) string {
	prof, mode, _ := ResolveProfile(ctx, provider)
	if mode == "isolated" {
		return ""
	}
	if prof != nil {
		for _, n := range names {
			if val := profileEnvValue(prof, n); val != "" {
				return val
			}
		}
		// A selected org/manual profile must not borrow a global key it does not define.
		if len(prof.OrgPatterns) > 0 || mode == "auto_org" || mode == "manual" || mode == "override" {
			return ""
		}
	}
	return FirstEnvOrStored(names...)
}

func profileEnvValue(p *ProviderProfile, name string) string {
	if p == nil || len(p.Env) == 0 {
		return ""
	}
	want := strings.ToUpper(strings.TrimSpace(name))
	for k, v := range p.Env {
		if strings.EqualFold(strings.TrimSpace(k), want) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// ListProfilesOverview returns a secret-safe overview of all saved provider profiles
// plus how each supported provider resolves for workspace.
func ListProfilesOverview(workspace string) ProfilesOverview {
	// Ensure live credential slot is restored from vault if empty, and sync active account.
	EnsureLiveAgyCredentialRestored()
	_, _ = SyncCurrentLiveAgyAccountToProfiles()

	ws := strings.TrimSpace(workspace)
	orgID := orgx.Detect(ws)

	profilesMu.Lock()
	doc, _ := loadProfilesLocked()
	profilesMu.Unlock()

	summaries := make([]ProviderProfileSummary, 0, len(doc.Profiles))
	for _, p := range doc.Profiles {
		summaries = append(summaries, summarizeProviderProfile(p))
	}

	wsKey := orgx.NormalizeWorkspaceKey(ws)
	ctx := WithWorkspace(context.Background(), ws)
	providers := []string{"agy", "cursor", "openrouter", "openai", "anthropic", "gemini", "opencode"}
	resolved := make(map[string]ResolvedProviderInfo, len(providers))
	for _, prov := range providers {
		prof, mode, rule := ResolveProfile(ctx, prov)
		binding := ""
		if wsKey != "" && doc.WorkspaceBindings != nil && doc.WorkspaceBindings[wsKey] != nil {
			binding = doc.WorkspaceBindings[wsKey][prov]
		}
		info := ResolvedProviderInfo{
			Provider:    prov,
			MatchMode:   mode,
			MatchedRule: rule,
			BindingMode: binding,
		}
		if prof != nil {
			info.ProfileID = prof.ID
			info.ProfileName = prof.Name
		}
		resolved[prov] = info
	}

	return ProfilesOverview{
		Identity: orgID,
		Profiles: summaries,
		Resolved: resolved,
	}
}

func summarizeProviderProfile(p ProviderProfile) ProviderProfileSummary {
	envMasked := map[string]string{}
	configVals := map[string]string{}
	for k, v := range p.Env {
		uk := strings.ToUpper(strings.TrimSpace(k))
		if uk == "" || strings.TrimSpace(v) == "" {
			continue
		}
		if isSecretKeyName(uk) {
			envMasked[uk] = MaskKey(v)
		} else {
			configVals[uk] = v
		}
	}
	ready, detail := evaluateProfileReadiness(p)
	return ProviderProfileSummary{
		ID:           p.ID,
		Provider:     p.Provider,
		Name:         p.Name,
		AuthMode:     p.AuthMode,
		Command:      p.Command,
		EnvMasked:    envMasked,
		ConfigValues: configVals,
		OrgPatterns:  append([]string(nil), p.OrgPatterns...),
		IsDefault:    p.IsDefault,
		Ready:        ready,
		ReadyDetail:  detail,
	}
}

func evaluateProfileReadiness(p ProviderProfile) (bool, string) {
	switch p.Provider {
	case "agy":
		switch strings.ToLower(strings.TrimSpace(p.AuthMode)) {
		case "vault":
			if ok, email := HasAgyVaultSession(p.ID); ok {
				if email != "" {
					return true, "Conta Google conectada e salva em pasta isolada (" + email + ")"
				}
				return true, "Sessão OAuth Google salva em pasta isolada"
			}
			return false, "Clique em Conectar com Google para autenticar esta conta"
		case "adc":
			cfgDir, _ := AgyADCConfigDir(p.ID)
			if cfgDir == "" {
				cfgDir = strings.TrimSpace(p.Env["CLOUDSDK_CONFIG"])
			}
			if HasAgyADCCredentials(cfgDir) {
				return true, "Google Cloud ADC pronto (" + cfgDir + ")"
			}
			if ok, email := HasAgyVaultSession(p.ID); ok {
				return true, "Conta Google conectada (" + email + ")"
			}
			return false, "Clique em Conectar com Google para autenticar esta conta"
		case "env":
			if len(p.Env) > 0 || strings.TrimSpace(p.Command) != "" {
				return true, "Ambiente/binário customizado"
			}
			return false, "Defina variáveis de ambiente ou caminho do binário"
		default:
			if ok, email := HasAgyVaultSession(p.ID); ok && email != "" {
				return true, "Conta Google conectada (" + email + ")"
			}
			return AgyAvailable(), "Usa a sessão `agy` ativa no sistema"
		}
	case "cursor":
		if strings.TrimSpace(p.Env["CURSOR_API_KEY"]) != "" {
			return true, "CURSOR_API_KEY dedicada configurada"
		}
		return false, "Informe CURSOR_API_KEY para esta conta"
	case "openrouter":
		if strings.TrimSpace(p.Env["OPENROUTER_API_KEY"]) != "" {
			return true, "OPENROUTER_API_KEY dedicada configurada"
		}
		return false, "Informe OPENROUTER_API_KEY"
	case "openai":
		if strings.TrimSpace(p.Env["OPENAI_API_KEY"]) != "" || strings.TrimSpace(p.Env["OPENAI_BASE_URL"]) != "" {
			return true, "Credencial OpenAI configurada"
		}
		return false, "Informe OPENAI_API_KEY ou OPENAI_BASE_URL"
	case "anthropic":
		if strings.TrimSpace(p.Env["ANTHROPIC_API_KEY"]) != "" {
			return true, "ANTHROPIC_API_KEY dedicada configurada"
		}
		return false, "Informe ANTHROPIC_API_KEY"
	case "gemini":
		if strings.TrimSpace(p.Env["GOOGLE_API_KEY"]) != "" || strings.TrimSpace(p.Env["GEMINI_API_KEY"]) != "" || strings.TrimSpace(p.Env["GOOGLE_CLOUD_PROJECT"]) != "" {
			return true, "Credencial Google/Vertex configurada"
		}
		return false, "Informe GOOGLE_API_KEY ou GOOGLE_CLOUD_PROJECT"
	case "opencode":
		if strings.TrimSpace(p.Env["OPENCODE_API_KEY"]) != "" || strings.TrimSpace(p.Env["OPENCODE_ZEN_API_KEY"]) != "" {
			return true, "Token OpenCode dedicado configurado"
		}
		return false, "Informe OPENCODE_API_KEY"
	default:
		return len(p.Env) > 0, ""
	}
}

// SaveProviderProfile creates or updates a provider profile while preserving any secret
// env keys left blank on edit, and optionally pins bindWorkspace to this profile.
func SaveProviderProfile(req SaveProfileRequest) (ProfilesOverview, error) {
	prov := NormalizeProfileProvider(req.Provider)
	if prov == "" {
		return ProfilesOverview{}, fmt.Errorf("provedor inválido")
	}
	name := strings.TrimSpace(req.Name)
	id := sanitizeSlug(req.ID)
	if id == "" {
		id = sanitizeSlug(prov + "-" + name)
	}
	if id == "" {
		return ProfilesOverview{}, fmt.Errorf("informe um nome para o perfil")
	}
	if name == "" {
		name = id
	}

	authMode := strings.ToLower(strings.TrimSpace(req.AuthMode))
	if prov == "agy" && authMode == "" {
		authMode = "live"
	} else if prov != "agy" && authMode == "" {
		authMode = "key"
	}

	profilesMu.Lock()
	doc, err := loadProfilesLocked()
	if err != nil {
		profilesMu.Unlock()
		return ProfilesOverview{}, err
	}

	var existing *ProviderProfile
	for i := range doc.Profiles {
		if strings.EqualFold(doc.Profiles[i].ID, id) {
			cp := doc.Profiles[i]
			existing = &cp
			break
		}
	}

	nextEnv := map[string]string{}
	if existing != nil {
		for k, v := range existing.Env {
			if isSecretKeyName(k) && strings.TrimSpace(v) != "" {
				nextEnv[strings.ToUpper(strings.TrimSpace(k))] = strings.TrimSpace(v)
			}
		}
	}
	for k, v := range req.Env {
		uk := strings.ToUpper(strings.TrimSpace(k))
		if uk == "" {
			continue
		}
		tv := strings.TrimSpace(v)
		if tv == "" {
			if !isSecretKeyName(uk) {
				delete(nextEnv, uk)
			}
			continue
		}
		nextEnv[uk] = tv
	}

	if prov == "agy" && authMode == "adc" {
		nextEnv["AGY_ADC_AUTH"] = "true"
		// Always the per-profile directory. A caller-supplied CLOUDSDK_CONFIG
		// could point every account at the shared gcloud ADC dir.
		if dir, derr := AgyADCConfigDir(id); derr == nil && dir != "" {
			_ = os.MkdirAll(dir, 0o700)
			nextEnv["CLOUDSDK_CONFIG"] = dir
		} else {
			delete(nextEnv, "CLOUDSDK_CONFIG")
		}
	}

	var patterns []string
	seen := map[string]bool{}
	for _, pat := range req.OrgPatterns {
		p := strings.TrimSpace(pat)
		if p == "" {
			continue
		}
		lp := strings.ToLower(p)
		if !seen[lp] {
			seen[lp] = true
			patterns = append(patterns, p)
		}
	}

	next := ProviderProfile{
		ID:          id,
		Provider:    prov,
		Name:        name,
		AuthMode:    authMode,
		Command:     strings.TrimSpace(req.Command),
		Env:         nextEnv,
		OrgPatterns: patterns,
		IsDefault:   req.IsDefault,
	}
	if next.IsDefault {
		for i := range doc.Profiles {
			if doc.Profiles[i].Provider == prov {
				doc.Profiles[i].IsDefault = false
			}
		}
	}

	replaced := false
	for i := range doc.Profiles {
		if strings.EqualFold(doc.Profiles[i].ID, id) {
			doc.Profiles[i] = next
			replaced = true
			break
		}
	}
	if !replaced {
		doc.Profiles = append(doc.Profiles, next)
	}

	if wsKey := orgx.NormalizeWorkspaceKey(req.BindWorkspace); wsKey != "" {
		if doc.WorkspaceBindings == nil {
			doc.WorkspaceBindings = map[string]map[string]string{}
		}
		if doc.WorkspaceBindings[wsKey] == nil {
			doc.WorkspaceBindings[wsKey] = map[string]string{}
		}
		doc.WorkspaceBindings[wsKey][prov] = id
	}

	err = saveProfilesLocked(doc)
	profilesMu.Unlock()
	if err != nil {
		return ProfilesOverview{}, err
	}
	return ListProfilesOverview(req.BindWorkspace), nil
}

// DeleteProviderProfile removes a profile and any workspace bindings pointing to it.
func DeleteProviderProfile(profileID, workspace string) (ProfilesOverview, error) {
	target := strings.ToLower(strings.TrimSpace(profileID))
	if target == "" {
		return ProfilesOverview{}, fmt.Errorf("id do perfil inválido")
	}
	profilesMu.Lock()
	doc, err := loadProfilesLocked()
	if err != nil {
		profilesMu.Unlock()
		return ProfilesOverview{}, err
	}
	kept := make([]ProviderProfile, 0, len(doc.Profiles))
	for _, p := range doc.Profiles {
		if !strings.EqualFold(p.ID, target) {
			kept = append(kept, p)
		}
	}
	doc.Profiles = kept
	for wsKey, provMap := range doc.WorkspaceBindings {
		for prov, pid := range provMap {
			if strings.EqualFold(pid, target) {
				delete(provMap, prov)
			}
		}
		if len(provMap) == 0 {
			delete(doc.WorkspaceBindings, wsKey)
		}
	}
	err = saveProfilesLocked(doc)
	profilesMu.Unlock()
	if err != nil {
		return ProfilesOverview{}, err
	}
	return ListProfilesOverview(workspace), nil
}

// BindProviderWorkspace pins a workspace+provider to profileID ("", "auto", "global", or profile ID).
func BindProviderWorkspace(workspace, provider, profileID string) (ProfilesOverview, error) {
	wsKey := orgx.NormalizeWorkspaceKey(workspace)
	prov := NormalizeProfileProvider(provider)
	if wsKey == "" || prov == "" {
		return ProfilesOverview{}, fmt.Errorf("workspace ou provedor inválido")
	}
	val := strings.TrimSpace(profileID)

	profilesMu.Lock()
	doc, err := loadProfilesLocked()
	if err != nil {
		profilesMu.Unlock()
		return ProfilesOverview{}, err
	}
	if doc.WorkspaceBindings == nil {
		doc.WorkspaceBindings = map[string]map[string]string{}
	}
	if doc.WorkspaceBindings[wsKey] == nil {
		doc.WorkspaceBindings[wsKey] = map[string]string{}
	}
	switch {
	case val == "" || strings.EqualFold(val, "auto"):
		delete(doc.WorkspaceBindings[wsKey], prov)
		if len(doc.WorkspaceBindings[wsKey]) == 0 {
			delete(doc.WorkspaceBindings, wsKey)
		}
	case strings.EqualFold(val, "global"):
		doc.WorkspaceBindings[wsKey][prov] = "global"
	default:
		found := ""
		for _, p := range doc.Profiles {
			if p.Provider == prov && strings.EqualFold(p.ID, val) {
				found = p.ID
				break
			}
		}
		if found == "" {
			profilesMu.Unlock()
			return ProfilesOverview{}, fmt.Errorf("perfil %q não encontrado para %s", val, prov)
		}
		doc.WorkspaceBindings[wsKey][prov] = found
	}
	err = saveProfilesLocked(doc)
	profilesMu.Unlock()
	if err != nil {
		return ProfilesOverview{}, err
	}
	return ListProfilesOverview(workspace), nil
}

func isSecretKeyName(name string) bool {
	u := strings.ToUpper(strings.TrimSpace(name))
	return strings.Contains(u, "KEY") ||
		strings.Contains(u, "TOKEN") ||
		strings.Contains(u, "SECRET") ||
		strings.Contains(u, "PASSWORD") ||
		strings.Contains(u, "PASSWD") ||
		strings.Contains(u, "CREDENTIAL") ||
		strings.Contains(u, "PRIVATE")
}

// stripInheritedSecrets drops credential-bearing variables from a child process
// environment so an org-scoped account does not inherit another org's keys.
func stripInheritedSecrets(base []string) []string {
	out := make([]string, 0, len(base))
	for _, entry := range base {
		k, _, ok := strings.Cut(entry, "=")
		if !ok {
			out = append(out, entry)
			continue
		}
		name := strings.ToUpper(strings.TrimSpace(k))
		if isSecretKeyName(name) || strings.HasPrefix(name, "JIRA_") || strings.HasPrefix(name, "ATLASSIAN_") || name == "CLOUDSDK_CONFIG" {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func sanitizeSlug(raw string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// mergeEnv returns base with overrides applied (replacing existing keys case-insensitively on Windows).
func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(overrides))
	upperOverrides := make(map[string]string, len(overrides))
	for k, v := range overrides {
		if strings.TrimSpace(k) != "" {
			upperOverrides[strings.ToUpper(strings.TrimSpace(k))] = v
		}
	}
	for _, entry := range base {
		k, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, skip := upperOverrides[strings.ToUpper(strings.TrimSpace(k))]; skip {
				continue
			}
		}
		out = append(out, entry)
	}
	for k, v := range overrides {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}
