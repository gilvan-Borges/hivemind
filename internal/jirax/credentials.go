package jirax

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gilvan-Borges/HiveMind/internal/orgx"
)

// Project represents a Jira Cloud project discovered dynamically via REST API v3.
type Project struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	ProjectType string `json:"projectType,omitempty"`
}

// JiraProfile defines one named Jira Cloud connection (e.g., Org A, Org B).
type JiraProfile struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	BaseURL        string   `json:"base_url,omitempty"`
	Email          string   `json:"email,omitempty"`
	Token          string   `json:"token,omitempty"`
	DefaultProject string   `json:"default_project,omitempty"`
	OrgPatterns    []string `json:"org_patterns,omitempty"`
	IsDefault      bool     `json:"is_default,omitempty"`
}

// JiraProfileSummary is the secret-safe UI view of a JiraProfile.
type JiraProfileSummary struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	BaseURL        string   `json:"baseUrl"`
	Email          string   `json:"email"`
	TokenMasked    string   `json:"tokenMasked"`
	DefaultProject string   `json:"defaultProject"`
	OrgPatterns    []string `json:"orgPatterns,omitempty"`
	IsDefault      bool     `json:"isDefault,omitempty"`
	HasToken       bool     `json:"hasToken"`
}

// NormalizeBaseURL turns user-pasted Jira links (e.g. "empresa.atlassian.net" or
// "https://empresa.atlassian.net/jira/software/projects/IPG/boards/1") into a clean
// origin URL ("https://empresa.atlassian.net").
func NormalizeBaseURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return strings.TrimRight(s, "/")
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, u.Host)
}

type storedJiraFile struct {
	// Legacy single-connector fields kept for backward compatibility.
	BaseURL        string `json:"base_url,omitempty"`
	Email          string `json:"email,omitempty"`
	Token          string `json:"token,omitempty"`
	DefaultProject string `json:"default_project,omitempty"`

	// Multi-profile / multi-org schema.
	ActiveProfile     string            `json:"active_profile,omitempty"`
	Profiles          []JiraProfile     `json:"profiles,omitempty"`
	WorkspaceBindings map[string]string `json:"workspace_bindings,omitempty"` // normalized workspace -> profileID or "none"
}

// ConnectorStatus describes the native Jira Cloud connector state for the IDE UI.
type ConnectorStatus struct {
	Configured        bool                 `json:"configured"`
	Connected         bool                 `json:"connected"`
	Source            string               `json:"source"` // "env", "config", "mcp:...", "none", ""
	BaseURL           string               `json:"baseUrl"`
	Email             string               `json:"email"`
	TokenMasked       string               `json:"tokenMasked"`
	DefaultProject    string               `json:"defaultProject"`
	User              string               `json:"user"`
	Detail            string               `json:"detail"`
	ActiveProfileID   string               `json:"activeProfileId,omitempty"`
	ActiveProfileName string               `json:"activeProfileName,omitempty"`
	MatchMode         string               `json:"matchMode,omitempty"` // "env", "manual", "auto_org", "default", "isolated_none", "mcp"
	MatchedRule       string               `json:"matchedRule,omitempty"`
	BindingMode       string               `json:"bindingMode,omitempty"` // "", "none", or pinned profileID
	DetectedOrg       string               `json:"detectedOrg,omitempty"`
	SuggestedPatterns []string             `json:"suggestedPatterns,omitempty"`
	Profiles          []JiraProfileSummary `json:"profiles,omitempty"`
}

// ResolvedCredentials describes the exact Jira resolution outcome for a workspace.
type ResolvedCredentials struct {
	BaseURL           string
	Email             string
	Token             string
	DefaultProject    string
	Source            string
	ProfileID         string
	ProfileName       string
	MatchMode         string
	MatchedRule       string
	BindingMode       string
	DetectedOrg       string
	SuggestedPatterns []string
	Profiles          []JiraProfileSummary
}

var (
	jiraMu   sync.Mutex
	jiraBlob struct {
		path string
		mod  time.Time
		size int64
		raw  []byte
		ok   bool
	}
)

func jiraConfigPath() (string, error) {
	if base, err := os.UserConfigDir(); err == nil && strings.TrimSpace(base) != "" {
		return filepath.Join(base, "HiveMind", "jira-connector.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hivemind", "jira-connector.json"), nil
}

func readJiraFile() storedJiraFile {
	jiraMu.Lock()
	defer jiraMu.Unlock()
	return parseJiraDoc(readJiraBytesLocked())
}

func readJiraBytesLocked() []byte {
	path, err := jiraConfigPath()
	if err != nil {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		jiraBlob.ok = false
		return nil
	}
	if jiraBlob.ok && jiraBlob.path == path && jiraBlob.size == info.Size() && jiraBlob.mod.Equal(info.ModTime()) && jiraBlob.raw != nil {
		return jiraBlob.raw
	}
	data, err := os.ReadFile(path)
	if err != nil {
		jiraBlob.ok = false
		return nil
	}
	jiraBlob.path = path
	jiraBlob.mod = info.ModTime()
	jiraBlob.size = info.Size()
	jiraBlob.raw = append([]byte(nil), data...)
	jiraBlob.ok = true
	return jiraBlob.raw
}

func parseJiraDoc(data []byte) storedJiraFile {
	if len(data) == 0 {
		return storedJiraFile{}
	}
	var doc storedJiraFile
	if json.Unmarshal(data, &doc) != nil {
		return storedJiraFile{}
	}
	// Migrate legacy flat config in-memory if profiles list is empty.
	if len(doc.Profiles) == 0 && (strings.TrimSpace(doc.BaseURL) != "" || strings.TrimSpace(doc.Email) != "" || strings.TrimSpace(doc.Token) != "") {
		doc.Profiles = []JiraProfile{{
			ID:             "default",
			Name:           deriveProfileName(doc.BaseURL, "Padrão"),
			BaseURL:        NormalizeBaseURL(doc.BaseURL),
			Email:          strings.TrimSpace(doc.Email),
			Token:          strings.TrimSpace(doc.Token),
			DefaultProject: strings.ToUpper(strings.TrimSpace(doc.DefaultProject)),
			IsDefault:      true,
		}}
		doc.ActiveProfile = "default"
	}
	for i := range doc.Profiles {
		p := &doc.Profiles[i]
		p.ID = sanitizeProfileID(p.ID)
		if p.ID == "" {
			p.ID = sanitizeProfileID(p.Name)
		}
		if p.ID == "" {
			p.ID = fmt.Sprintf("jira-%d", i+1)
		}
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			p.Name = deriveProfileName(p.BaseURL, p.ID)
		}
		p.BaseURL = NormalizeBaseURL(p.BaseURL)
		p.Email = strings.TrimSpace(p.Email)
		p.Token = strings.TrimSpace(p.Token)
		p.DefaultProject = strings.ToUpper(strings.TrimSpace(p.DefaultProject))
	}
	return doc
}

func writeJiraFile(doc storedJiraFile) error {
	jiraMu.Lock()
	defer jiraMu.Unlock()
	return writeJiraFileLocked(doc)
}

func writeJiraFileLocked(doc storedJiraFile) error {
	path, err := jiraConfigPath()
	if err != nil {
		return err
	}
	// Keep legacy flat fields synced to active/default profile for external readers when single/default exists.
	var primary *JiraProfile
	for i := range doc.Profiles {
		if doc.Profiles[i].ID == doc.ActiveProfile {
			primary = &doc.Profiles[i]
			break
		}
	}
	if primary == nil && len(doc.Profiles) > 0 {
		primary = &doc.Profiles[0]
	}
	if primary != nil && len(doc.Profiles) == 1 && len(primary.OrgPatterns) == 0 {
		doc.BaseURL = primary.BaseURL
		doc.Email = primary.Email
		doc.Token = primary.Token
		doc.DefaultProject = primary.DefaultProject
	} else {
		doc.BaseURL = ""
		doc.Email = ""
		doc.Token = ""
		doc.DefaultProject = ""
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := writeSecretFile(path, data); err != nil {
		jiraBlob.ok = false
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		jiraBlob.ok = false
		return nil
	}
	jiraBlob.path = path
	jiraBlob.mod = info.ModTime()
	jiraBlob.size = info.Size()
	jiraBlob.raw = append([]byte(nil), data...)
	jiraBlob.ok = true
	return nil
}

func writeSecretFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".jira-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(path)
		if err2 := os.Rename(tmpName, path); err2 != nil {
			return err2
		}
	}
	cleanup = false
	return os.Chmod(path, 0o600)
}

func summarizeProfiles(list []JiraProfile) []JiraProfileSummary {
	out := make([]JiraProfileSummary, 0, len(list))
	for _, p := range list {
		out = append(out, JiraProfileSummary{
			ID:             p.ID,
			Name:           p.Name,
			BaseURL:        p.BaseURL,
			Email:          p.Email,
			TokenMasked:    maskJiraToken(p.Token),
			DefaultProject: p.DefaultProject,
			OrgPatterns:    append([]string(nil), p.OrgPatterns...),
			IsDefault:      p.IsDefault,
			HasToken:       strings.TrimSpace(p.Token) != "",
		})
	}
	return out
}

func findProfileByID(list []JiraProfile, id string) *JiraProfile {
	target := strings.ToLower(strings.TrimSpace(id))
	if target == "" {
		return nil
	}
	for i := range list {
		if strings.ToLower(strings.TrimSpace(list[i].ID)) == target {
			return &list[i]
		}
	}
	return nil
}

// LoadStoredConfig reads the active native Jira connector settings saved in the user config dir.
func LoadStoredConfig() (baseURL, email, token, defaultProject string) {
	doc := readJiraFile()
	if p := findProfileByID(doc.Profiles, doc.ActiveProfile); p != nil {
		return p.BaseURL, p.Email, p.Token, p.DefaultProject
	}
	if len(doc.Profiles) > 0 {
		p := doc.Profiles[0]
		return p.BaseURL, p.Email, p.Token, p.DefaultProject
	}
	return "", "", "", ""
}

// LoadCredentials resolves Jira Cloud credentials without workspace context.
func LoadCredentials() (baseURL, email, token, defaultProject, source string) {
	return LoadCredentialsForWorkspace("")
}

// ResolveForWorkspace resolves Jira Cloud credentials with strict multi-org isolation:
//  1. Explicit workspace opt-out ("none") — beats environment variables
//  2. Explicit pin of a profile — that profile only, no env mixing
//  3. Auto-detection of Git organization against profile org_patterns
//  4. Fail closed when any profile is org-scoped and nothing matched: no env,
//     no legacy flat file, no default profile, no parent/home MCP
//  5. Legacy single-tenant (no org patterns): env, then the stored profile, then MCP
func ResolveForWorkspace(workspace string) ResolvedCredentials {
	doc := readJiraFile()
	ws := strings.TrimSpace(workspace)
	orgID := orgx.Detect(ws)
	res := ResolvedCredentials{
		DetectedOrg:       orgID.GitOrg,
		SuggestedPatterns: orgID.SuggestedPatterns,
		Profiles:          summarizeProfiles(doc.Profiles),
	}
	wsKey := orgx.NormalizeWorkspaceKey(ws)
	if wsKey != "" && doc.WorkspaceBindings != nil {
		res.BindingMode = strings.TrimSpace(doc.WorkspaceBindings[wsKey])
	}

	envBase := NormalizeBaseURL(firstNonEmptyEnv("JIRA_BASE_URL", "JIRA_HOST", "JIRA_URL", "ATLASSIAN_URL", "ATLASSIAN_DOMAIN"))
	envEmail := firstNonEmptyEnv("JIRA_EMAIL", "JIRA_USERNAME", "ATLASSIAN_EMAIL")
	envToken := firstNonEmptyEnv("JIRA_API_TOKEN", "JIRA_TOKEN", "ATLASSIAN_API_TOKEN")
	envProj := strings.ToUpper(firstNonEmptyEnv("JIRA_PROJECT"))

	applyProfileOnly := func(p *JiraProfile, mode, rule string) ResolvedCredentials {
		res.BaseURL = p.BaseURL
		res.Email = p.Email
		res.Token = p.Token
		res.DefaultProject = p.DefaultProject
		res.ProfileID = p.ID
		res.ProfileName = p.Name
		res.MatchMode = mode
		res.MatchedRule = rule
		if res.BaseURL != "" && res.Email != "" && res.Token != "" {
			res.Source = "config"
		}
		return res
	}

	// Opt-out is absolute: a full JIRA_* environment must not re-enable this workspace.
	if strings.EqualFold(res.BindingMode, "none") {
		res.Source = "none"
		res.MatchMode = "isolated_none"
		return res
	}
	if res.BindingMode != "" {
		if p := findProfileByID(doc.Profiles, res.BindingMode); p != nil {
			return applyProfileOnly(p, "manual", "")
		}
	}

	hasOrgScopedProfiles := false
	for i := range doc.Profiles {
		if len(doc.Profiles[i].OrgPatterns) == 0 {
			continue
		}
		hasOrgScopedProfiles = true
		if ws == "" {
			continue
		}
		if matched, pat := orgx.MatchPatterns(orgID, doc.Profiles[i].OrgPatterns); matched {
			return applyProfileOnly(&doc.Profiles[i], "auto_org", pat)
		}
	}

	// Org-scoped profiles exist and this workspace did not match one.
	// Do not fall through to env, the legacy flat file, IsDefault, or another org's MCP.
	if hasOrgScopedProfiles {
		if ws != "" {
			if mcpBase, mcpEmail, mcpToken, mcpProj, mcpPath := detectWorkspaceLocalMCPConfig(ws); mcpBase != "" && mcpEmail != "" && mcpToken != "" {
				res.BaseURL = mcpBase
				res.Email = mcpEmail
				res.Token = mcpToken
				res.DefaultProject = mcpProj
				res.Source = "mcp:" + filepath.Base(mcpPath)
				res.MatchMode = "mcp"
				return res
			}
		}
		res.Source = "none"
		res.MatchMode = "isolated_none"
		return res
	}

	// Legacy single-tenant: complete env wins, then the stored profile with partial env fill, then MCP.
	if envBase != "" && envEmail != "" && envToken != "" {
		res.BaseURL = envBase
		res.Email = envEmail
		res.Token = envToken
		res.DefaultProject = envProj
		res.Source = "env"
		res.MatchMode = "env"
		return res
	}
	applyLegacy := func(p *JiraProfile) ResolvedCredentials {
		out := applyProfileOnly(p, "default", "")
		if envBase != "" {
			out.BaseURL = envBase
		}
		if envEmail != "" {
			out.Email = envEmail
		}
		if envToken != "" {
			out.Token = envToken
		}
		if envProj != "" {
			out.DefaultProject = envProj
		}
		if out.BaseURL != "" && out.Email != "" && out.Token != "" {
			out.Source = "config"
			if envBase != "" && envEmail != "" && envToken != "" {
				out.Source = "env"
			}
		}
		return out
	}
	if p := findProfileByID(doc.Profiles, doc.ActiveProfile); p != nil {
		return applyLegacy(p)
	}
	for i := range doc.Profiles {
		if doc.Profiles[i].IsDefault {
			return applyLegacy(&doc.Profiles[i])
		}
	}
	if len(doc.Profiles) > 0 {
		return applyLegacy(&doc.Profiles[0])
	}

	mcpBase, mcpEmail, mcpToken, mcpProj, mcpPath := DetectMCPConfig(ws)
	res.BaseURL = firstNonEmpty(envBase, mcpBase)
	res.Email = firstNonEmpty(envEmail, mcpEmail)
	res.Token = firstNonEmpty(envToken, mcpToken)
	res.DefaultProject = firstNonEmpty(envProj, mcpProj)
	if res.BaseURL != "" && res.Email != "" && res.Token != "" && mcpPath != "" {
		res.Source = "mcp:" + filepath.Base(mcpPath)
		res.MatchMode = "mcp"
	}
	return res
}

// LoadCredentialsForWorkspace resolves Jira Cloud credentials with org/workspace awareness and MCP discovery.
func LoadCredentialsForWorkspace(workspace string) (baseURL, email, token, defaultProject, source string) {
	res := ResolveForWorkspace(workspace)
	return res.BaseURL, res.Email, res.Token, res.DefaultProject, res.Source
}

// FromWorkspace returns a configured Jira client using env, org/workspace profile, or MCP discovery.
func FromWorkspace(workspace string) Client {
	res := ResolveForWorkspace(workspace)
	if res.BaseURL == "" || res.Email == "" || res.Token == "" {
		if res.MatchMode == "isolated_none" {
			if res.DetectedOrg != "" {
				return Disabled{Reason: fmt.Sprintf("Sem Jira vinculado para a organização %q", res.DetectedOrg)}
			}
			return Disabled{Reason: "Jira desativado para este projeto"}
		}
		return Disabled{Reason: "Jira não configurado (conecte via Conector Jira Nativo, mcp_config.json ou variáveis JIRA_*)"}
	}
	return &HTTP{BaseURL: NormalizeBaseURL(res.BaseURL), Email: res.Email, Token: res.Token}
}

func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func detectWorkspaceLocalMCPConfig(workspace string) (baseURL, email, token, defaultProject, foundPath string) {
	if strings.TrimSpace(workspace) == "" {
		return "", "", "", "", ""
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		abs = workspace
	}
	for _, path := range []string{
		filepath.Join(abs, ".agents", "mcp_config.json"),
		filepath.Join(abs, ".mcp.json"),
		filepath.Join(abs, ".cursor", "mcp.json"),
	} {
		b, e, t, p, ok := parseJiraFromMCPFile(path)
		if ok {
			return b, e, t, p, path
		}
	}
	return "", "", "", "", ""
}

// DetectMCPConfig scans standard MCP config files (workspace, parent directory, and user config)
// for a Jira/Atlassian server entry containing host/domain, email, and API token.
func DetectMCPConfig(workspace string) (baseURL, email, token, defaultProject, foundPath string) {
	var candidates []string
	addDir := func(dir string) {
		if strings.TrimSpace(dir) == "" {
			return
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			abs = dir
		}
		candidates = append(candidates,
			filepath.Join(abs, ".agents", "mcp_config.json"),
			filepath.Join(abs, ".mcp.json"),
			filepath.Join(abs, ".cursor", "mcp.json"),
		)
	}
	if strings.TrimSpace(workspace) != "" {
		addDir(workspace)
		parent := filepath.Dir(filepath.Clean(workspace))
		if parent != "" && parent != workspace {
			addDir(parent)
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"),
			filepath.Join(home, ".cursor", "mcp.json"),
			filepath.Join(home, ".agents", "mcp_config.json"),
		)
	}

	seen := map[string]bool{}
	for _, path := range candidates {
		norm := strings.ToLower(filepath.Clean(path))
		if seen[norm] {
			continue
		}
		seen[norm] = true
		b, e, t, p, ok := parseJiraFromMCPFile(path)
		if ok {
			return b, e, t, p, path
		}
	}
	return "", "", "", "", ""
}

func parseJiraFromMCPFile(path string) (baseURL, email, token, defaultProject string, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", "", false
	}
	var doc struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || len(doc.MCPServers) == 0 {
		return "", "", "", "", false
	}

	pick := func(env map[string]string, keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimSpace(env[k]); v != "" {
				return v
			}
		}
		return ""
	}

	var ordered []string
	for name := range doc.MCPServers {
		ordered = append(ordered, name)
	}
	sort.Slice(ordered, func(i, j int) bool {
		li, lj := strings.ToLower(ordered[i]), strings.ToLower(ordered[j])
		pi := strings.Contains(li, "jira")
		pj := strings.Contains(lj, "jira")
		if pi != pj {
			return pi
		}
		ai := strings.Contains(li, "atlassian")
		aj := strings.Contains(lj, "atlassian")
		if ai != aj {
			return ai
		}
		return li < lj
	})

	for _, name := range ordered {
		env := doc.MCPServers[name].Env
		if len(env) == 0 {
			continue
		}
		b := NormalizeBaseURL(pick(env, "JIRA_BASE_URL", "JIRA_HOST", "JIRA_URL", "ATLASSIAN_URL", "ATLASSIAN_DOMAIN"))
		e := pick(env, "JIRA_EMAIL", "JIRA_USERNAME", "ATLASSIAN_EMAIL")
		t := pick(env, "JIRA_API_TOKEN", "JIRA_TOKEN", "ATLASSIAN_API_TOKEN")
		p := strings.ToUpper(pick(env, "JIRA_PROJECT", "JIRA_DEFAULT_PROJECT"))
		if b != "" && e != "" && t != "" {
			return b, e, t, p, true
		}
	}
	return "", "", "", "", false
}

// ImportFromMCP copies Jira credentials detected in mcp_config.json into the user's
// native jira-connector.json (outside any Git repository).
func ImportFromMCP(workspace string) (string, error) {
	b, e, t, p, foundPath := DetectMCPConfig(workspace)
	if foundPath == "" {
		return "", fmt.Errorf("nenhuma configuração Jira encontrada em mcp_config.json (.agents/, .mcp.json ou pasta do usuário)")
	}
	orgID := orgx.Detect(workspace)
	name := deriveProfileName(b, "MCP")
	var patterns []string
	if orgID.GitOrg != "" {
		patterns = []string{orgID.GitOrg}
	}
	if err := SaveProfile(JiraProfile{
		ID:             sanitizeProfileID(name),
		Name:           name,
		BaseURL:        b,
		Email:          e,
		Token:          t,
		DefaultProject: p,
		OrgPatterns:    patterns,
	}, workspace); err != nil {
		return "", err
	}
	return foundPath, nil
}

// SaveCredentials stores or updates the default/active native Jira Cloud profile.
// When clear is true, all profiles are cleared (backward-compatible single-connector semantics).
func SaveCredentials(baseURL, email, token, defaultProject string, clear bool) error {
	if clear {
		return writeJiraFile(storedJiraFile{})
	}
	doc := readJiraFile()
	targetID := doc.ActiveProfile
	if targetID == "" && len(doc.Profiles) > 0 {
		targetID = doc.Profiles[0].ID
	}
	if targetID == "" {
		targetID = "default"
	}
	var existing JiraProfile
	if cur := findProfileByID(doc.Profiles, targetID); cur != nil {
		existing = *cur
	}
	existing.ID = targetID
	if strings.TrimSpace(existing.Name) == "" {
		existing.Name = deriveProfileName(baseURL, "Padrão")
	}
	existing.BaseURL = NormalizeBaseURL(baseURL)
	existing.Email = strings.TrimSpace(email)
	if strings.TrimSpace(token) != "" {
		existing.Token = strings.TrimSpace(token)
	}
	existing.DefaultProject = strings.ToUpper(strings.TrimSpace(defaultProject))
	return SaveProfile(existing, "")
}

// SaveProfile creates or updates a named Jira profile. Blank Token preserves any previously
// stored token for that profile ID. When bindWorkspace is non-empty, pins that workspace to this profile.
func SaveProfile(input JiraProfile, bindWorkspace string) error {
	jiraMu.Lock()
	defer jiraMu.Unlock()
	doc := parseJiraDoc(readJiraBytesLocked())
	id := sanitizeProfileID(input.ID)
	if id == "" {
		id = sanitizeProfileID(input.Name)
	}
	if id == "" {
		id = sanitizeProfileID(deriveProfileName(input.BaseURL, ""))
	}
	if id == "" {
		id = fmt.Sprintf("jira-%d", len(doc.Profiles)+1)
	}

	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = deriveProfileName(input.BaseURL, id)
	}

	var patterns []string
	seenPat := map[string]bool{}
	for _, pat := range input.OrgPatterns {
		p := strings.TrimSpace(pat)
		if p == "" {
			continue
		}
		lp := strings.ToLower(p)
		if !seenPat[lp] {
			seenPat[lp] = true
			patterns = append(patterns, p)
		}
	}

	existing := findProfileByID(doc.Profiles, id)
	tok := strings.TrimSpace(input.Token)
	if tok == "" && existing != nil {
		tok = existing.Token
	}

	next := JiraProfile{
		ID:             id,
		Name:           name,
		BaseURL:        NormalizeBaseURL(input.BaseURL),
		Email:          strings.TrimSpace(input.Email),
		Token:          tok,
		DefaultProject: strings.ToUpper(strings.TrimSpace(input.DefaultProject)),
		OrgPatterns:    patterns,
		IsDefault:      input.IsDefault,
	}
	if next.BaseURL == "" || next.Email == "" || next.Token == "" {
		return fmt.Errorf("informe URL do Jira, e-mail e API token para salvar o perfil %q", next.Name)
	}

	if next.IsDefault {
		for i := range doc.Profiles {
			doc.Profiles[i].IsDefault = false
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
	doc.ActiveProfile = id

	if wsKey := orgx.NormalizeWorkspaceKey(bindWorkspace); wsKey != "" {
		if doc.WorkspaceBindings == nil {
			doc.WorkspaceBindings = map[string]string{}
		}
		doc.WorkspaceBindings[wsKey] = id
	}
	return writeJiraFileLocked(doc)
}

// DeleteProfile removes a Jira profile by ID and clears workspace bindings referencing it.
func DeleteProfile(profileID string) error {
	target := strings.ToLower(strings.TrimSpace(profileID))
	if target == "" {
		return fmt.Errorf("id do perfil inválido")
	}
	jiraMu.Lock()
	defer jiraMu.Unlock()
	doc := parseJiraDoc(readJiraBytesLocked())
	filtered := make([]JiraProfile, 0, len(doc.Profiles))
	for _, p := range doc.Profiles {
		if strings.ToLower(strings.TrimSpace(p.ID)) != target {
			filtered = append(filtered, p)
		}
	}
	doc.Profiles = filtered
	if strings.EqualFold(doc.ActiveProfile, target) {
		doc.ActiveProfile = ""
		if len(doc.Profiles) > 0 {
			doc.ActiveProfile = doc.Profiles[0].ID
		}
	}
	for k, v := range doc.WorkspaceBindings {
		if strings.EqualFold(v, target) {
			delete(doc.WorkspaceBindings, k)
		}
	}
	return writeJiraFileLocked(doc)
}

// BindWorkspace sets how a workspace resolves its Jira profile:
// - "" resets to automatic org detection
// - "none" isolates/disables Jira for this workspace
// - "<profileID>" pins the named Jira profile for this workspace
func BindWorkspace(workspace, target string) error {
	wsKey := orgx.NormalizeWorkspaceKey(workspace)
	if wsKey == "" {
		return fmt.Errorf("workspace vazio")
	}
	jiraMu.Lock()
	defer jiraMu.Unlock()
	doc := parseJiraDoc(readJiraBytesLocked())
	if doc.WorkspaceBindings == nil {
		doc.WorkspaceBindings = map[string]string{}
	}
	val := strings.TrimSpace(target)
	switch {
	case val == "" || strings.EqualFold(val, "auto"):
		delete(doc.WorkspaceBindings, wsKey)
	case strings.EqualFold(val, "none"):
		doc.WorkspaceBindings[wsKey] = "none"
	default:
		p := findProfileByID(doc.Profiles, val)
		if p == nil {
			return fmt.Errorf("perfil Jira %q não encontrado", val)
		}
		doc.WorkspaceBindings[wsKey] = p.ID
	}
	return writeJiraFileLocked(doc)
}

// CheckConnection validates the active Jira credentials live against GET /rest/api/3/myself.
func CheckConnection(ctx context.Context) ConnectorStatus {
	return CheckConnectionForWorkspace(ctx, "")
}

// CheckConnectionForWorkspace validates active Jira credentials (including org/workspace profile or MCP discovery).
func CheckConnectionForWorkspace(ctx context.Context, workspace string) ConnectorStatus {
	res := ResolveForWorkspace(workspace)
	st := ConnectorStatus{
		Configured:        res.BaseURL != "" && res.Email != "" && res.Token != "",
		Source:            res.Source,
		BaseURL:           res.BaseURL,
		Email:             res.Email,
		TokenMasked:       maskJiraToken(res.Token),
		DefaultProject:    res.DefaultProject,
		ActiveProfileID:   res.ProfileID,
		ActiveProfileName: res.ProfileName,
		MatchMode:         res.MatchMode,
		MatchedRule:       res.MatchedRule,
		BindingMode:       res.BindingMode,
		DetectedOrg:       res.DetectedOrg,
		SuggestedPatterns: res.SuggestedPatterns,
		Profiles:          res.Profiles,
	}
	if res.MatchMode == "isolated_none" {
		if res.BindingMode == "none" {
			st.Detail = "Jira desativado manualmente para este projeto"
		} else if res.DetectedOrg != "" {
			st.Detail = fmt.Sprintf("Sem Jira vinculado à organização %q (perfis de outras orgs isolados)", res.DetectedOrg)
		} else {
			st.Detail = "Sem perfil Jira correspondente a este projeto"
		}
		return st
	}
	if !st.Configured {
		st.Detail = "Não configurado (informe URL, E-mail e API Token ou use mcp_config.json)"
		return st
	}
	client := &HTTP{BaseURL: NormalizeBaseURL(res.BaseURL), Email: res.Email, Token: res.Token}
	var raw struct {
		DisplayName  string `json:"displayName"`
		EmailAddress string `json:"emailAddress"`
	}
	if err := client.do(ctx, http.MethodGet, "/rest/api/3/myself", nil, &raw); err != nil {
		st.Detail = fmt.Sprintf("Falha ao autenticar no Jira (%s): %v", firstNonEmpty(res.ProfileName, sourceLabel(res.Source)), err)
		return st
	}
	st.Connected = true
	st.User = strings.TrimSpace(raw.DisplayName)
	if st.User == "" {
		st.User = res.Email
	}
	origin := sourceLabel(res.Source)
	if res.ProfileName != "" {
		origin = res.ProfileName
		switch res.MatchMode {
		case "auto_org":
			origin += " · auto (" + firstNonEmpty(res.DetectedOrg, res.MatchedRule) + ")"
		case "manual":
			origin += " · fixado"
		}
	}
	st.Detail = fmt.Sprintf("Conectado como %s (%s)", st.User, origin)
	return st
}

// ListProjects fetches Jira Cloud projects accessible to the active credentials.
func ListProjects(ctx context.Context) ([]Project, error) {
	return ListProjectsForWorkspace(ctx, "")
}

// ListProjectsForWorkspace fetches Jira Cloud projects using workspace/org-aware credentials.
func ListProjectsForWorkspace(ctx context.Context, workspace string) ([]Project, error) {
	baseURL, email, token, _, _ := LoadCredentialsForWorkspace(workspace)
	if baseURL == "" || email == "" || token == "" {
		return nil, fmt.Errorf("Jira não configurado para este workspace")
	}
	client := &HTTP{BaseURL: NormalizeBaseURL(baseURL), Email: email, Token: token}
	return client.ListProjects(ctx)
}

// ListProjects lists Jira projects via GET /rest/api/3/project/search (falling back to /rest/api/3/project).
func (h *HTTP) ListProjects(ctx context.Context) ([]Project, error) {
	type rawProject struct {
		Key            string `json:"key"`
		Name           string `json:"name"`
		ProjectTypeKey string `json:"projectTypeKey"`
	}
	var searchResp struct {
		Values []rawProject `json:"values"`
	}
	var list []rawProject
	err := h.do(ctx, http.MethodGet, "/rest/api/3/project/search?maxResults=100&orderBy=name", nil, &searchResp)
	if err == nil {
		list = searchResp.Values
	} else {
		var legacy []rawProject
		if err2 := h.do(ctx, http.MethodGet, "/rest/api/3/project", nil, &legacy); err2 != nil {
			return nil, err
		}
		list = legacy
	}
	out := make([]Project, 0, len(list))
	seen := make(map[string]bool, len(list))
	for _, p := range list {
		key := strings.ToUpper(strings.TrimSpace(p.Key))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Project{
			Key:         key,
			Name:        strings.TrimSpace(p.Name),
			ProjectType: strings.TrimSpace(p.ProjectTypeKey),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out, nil
}

func sourceLabel(src string) string {
	if src == "env" {
		return "ENV"
	}
	if src == "none" {
		return "Desativado"
	}
	if strings.HasPrefix(src, "mcp:") {
		return "MCP (" + strings.TrimPrefix(src, "mcp:") + ")"
	}
	return "Conector Nativo"
}

func maskJiraToken(tok string) string {
	k := strings.TrimSpace(tok)
	if k == "" {
		return ""
	}
	if len(k) <= 8 {
		return "****"
	}
	return "****" + k[len(k)-4:]
}

func deriveProfileName(baseURL, fallback string) string {
	norm := NormalizeBaseURL(baseURL)
	if norm != "" {
		if u, err := url.Parse(norm); err == nil && u.Host != "" {
			host := strings.TrimSuffix(u.Host, ".atlassian.net")
			host = strings.TrimPrefix(host, "jira.")
			if host != "" {
				return host
			}
		}
	}
	return fallback
}

func sanitizeProfileID(raw string) string {
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
