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
)

// Project represents a Jira Cloud project discovered dynamically via REST API v3.
type Project struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	ProjectType string `json:"projectType,omitempty"`
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

type storedJiraConfig struct {
	BaseURL        string `json:"base_url,omitempty"`
	Email          string `json:"email,omitempty"`
	Token          string `json:"token,omitempty"`
	DefaultProject string `json:"default_project,omitempty"`
}

// ConnectorStatus describes the native Jira Cloud connector state for the IDE UI.
type ConnectorStatus struct {
	Configured     bool   `json:"configured"`
	Connected      bool   `json:"connected"`
	Source         string `json:"source"` // "env", "config", ""
	BaseURL        string `json:"baseUrl"`
	Email          string `json:"email"`
	TokenMasked    string `json:"tokenMasked"`
	DefaultProject string `json:"defaultProject"`
	User           string `json:"user"`
	Detail         string `json:"detail"`
}

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

// LoadStoredConfig reads the native Jira connector settings saved in the user config dir.
func LoadStoredConfig() (baseURL, email, token, defaultProject string) {
	path, err := jiraConfigPath()
	if err != nil {
		return "", "", "", ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", ""
	}
	var cfg storedJiraConfig
	if json.Unmarshal(data, &cfg) != nil {
		return "", "", "", ""
	}
	return strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		strings.TrimSpace(cfg.Email),
		strings.TrimSpace(cfg.Token),
		strings.ToUpper(strings.TrimSpace(cfg.DefaultProject))
}

// LoadCredentials resolves Jira Cloud credentials in priority order:
// 1. Environment variables (JIRA_BASE_URL/JIRA_HOST/JIRA_URL, JIRA_EMAIL, JIRA_API_TOKEN/JIRA_TOKEN, JIRA_PROJECT)
// 2. Native HiveMind Jira connector (%AppData%\HiveMind\jira-connector.json)
func LoadCredentials() (baseURL, email, token, defaultProject, source string) {
	return LoadCredentialsForWorkspace("")
}

// LoadCredentialsForWorkspace resolves Jira Cloud credentials with MCP auto-discovery:
// 1. Environment variables
// 2. Native HiveMind Jira connector (%AppData%\HiveMind\jira-connector.json)
// 3. Existing MCP configs (.agents/mcp_config.json, .mcp.json, .cursor/mcp.json in workspace, parent dir, or user home)
func LoadCredentialsForWorkspace(workspace string) (baseURL, email, token, defaultProject, source string) {
	envBase := NormalizeBaseURL(firstNonEmptyEnv("JIRA_BASE_URL", "JIRA_HOST", "JIRA_URL", "ATLASSIAN_URL", "ATLASSIAN_DOMAIN"))
	envEmail := firstNonEmptyEnv("JIRA_EMAIL", "JIRA_USERNAME", "ATLASSIAN_EMAIL")
	envToken := firstNonEmptyEnv("JIRA_API_TOKEN", "JIRA_TOKEN", "ATLASSIAN_API_TOKEN")
	envProj := strings.ToUpper(firstNonEmptyEnv("JIRA_PROJECT"))

	stBase, stEmail, stToken, stProj := LoadStoredConfig()

	baseURL = envBase
	email = envEmail
	token = envToken
	defaultProject = envProj
	if baseURL != "" && email != "" && token != "" {
		source = "env"
	}
	if baseURL == "" {
		baseURL = NormalizeBaseURL(stBase)
	}
	if email == "" {
		email = stEmail
	}
	if token == "" {
		token = stToken
	}
	if defaultProject == "" {
		defaultProject = stProj
	}
	if source == "" && baseURL != "" && email != "" && token != "" {
		source = "config"
	}
	if baseURL == "" || email == "" || token == "" {
		mcpBase, mcpEmail, mcpToken, mcpProj, mcpPath := DetectMCPConfig(workspace)
		if baseURL == "" {
			baseURL = mcpBase
		}
		if email == "" {
			email = mcpEmail
		}
		if token == "" {
			token = mcpToken
		}
		if defaultProject == "" {
			defaultProject = mcpProj
		}
		if source == "" && baseURL != "" && email != "" && token != "" && mcpPath != "" {
			source = "mcp:" + filepath.Base(mcpPath)
		}
	}
	return baseURL, email, token, defaultProject, source
}

// FromWorkspace returns a configured Jira client using env, native connector, or MCP config discovery.
func FromWorkspace(workspace string) Client {
	baseURL, email, token, _, _ := LoadCredentialsForWorkspace(workspace)
	if baseURL == "" || email == "" || token == "" {
		return Disabled{Reason: "Jira não configurado (conecte via Conector Jira Nativo, mcp_config.json ou variáveis JIRA_*)"}
	}
	return &HTTP{BaseURL: NormalizeBaseURL(baseURL), Email: email, Token: token}
}

func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
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

	// Prefer servers explicitly named jira/atlassian first, then fallback to any server with Atlassian creds.
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
	if err := SaveCredentials(b, e, t, p, false); err != nil {
		return "", err
	}
	return foundPath, nil
}

// SaveCredentials stores or updates the native Jira Cloud credentials outside project repos.
// If clear is true, all stored credentials are removed. If clear is false and token is empty,
// any previously stored token is preserved so the user can update URL/email/project safely.
func SaveCredentials(baseURL, email, token, defaultProject string, clear bool) error {
	path, err := jiraConfigPath()
	if err != nil {
		return err
	}
	var cfg storedJiraConfig
	if !clear {
		_, _, existingTok, _ := LoadStoredConfig()
		tok := strings.TrimSpace(token)
		if tok == "" {
			tok = existingTok
		}
		cfg = storedJiraConfig{
			BaseURL:        NormalizeBaseURL(baseURL),
			Email:          strings.TrimSpace(email),
			Token:          tok,
			DefaultProject: strings.ToUpper(strings.TrimSpace(defaultProject)),
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// CheckConnection validates the active Jira credentials live against GET /rest/api/3/myself.
func CheckConnection(ctx context.Context) ConnectorStatus {
	return CheckConnectionForWorkspace(ctx, "")
}

// CheckConnectionForWorkspace validates active Jira credentials (including MCP auto-discovery for workspace).
func CheckConnectionForWorkspace(ctx context.Context, workspace string) ConnectorStatus {
	baseURL, email, token, proj, source := LoadCredentialsForWorkspace(workspace)
	st := ConnectorStatus{
		Configured:     baseURL != "" && email != "" && token != "",
		Source:         source,
		BaseURL:        baseURL,
		Email:          email,
		TokenMasked:    maskJiraToken(token),
		DefaultProject: proj,
	}
	if !st.Configured {
		st.Detail = "Não configurado (informe URL, E-mail e API Token ou use mcp_config.json)"
		return st
	}
	client := &HTTP{BaseURL: NormalizeBaseURL(baseURL), Email: email, Token: token}
	var raw struct {
		DisplayName  string `json:"displayName"`
		EmailAddress string `json:"emailAddress"`
	}
	if err := client.do(ctx, http.MethodGet, "/rest/api/3/myself", nil, &raw); err != nil {
		st.Detail = fmt.Sprintf("Falha ao autenticar no Jira: %v", err)
		return st
	}
	st.Connected = true
	st.User = strings.TrimSpace(raw.DisplayName)
	if st.User == "" {
		st.User = email
	}
	st.Detail = fmt.Sprintf("Conectado como %s (%s)", st.User, sourceLabel(source))
	return st
}

// ListProjects fetches Jira Cloud projects accessible to the active credentials.
func ListProjects(ctx context.Context) ([]Project, error) {
	return ListProjectsForWorkspace(ctx, "")
}

// ListProjectsForWorkspace fetches Jira Cloud projects using workspace-aware credentials (env, native, or MCP).
func ListProjectsForWorkspace(ctx context.Context, workspace string) ([]Project, error) {
	baseURL, email, token, _, _ := LoadCredentialsForWorkspace(workspace)
	if baseURL == "" || email == "" || token == "" {
		return nil, fmt.Errorf("Jira não configurado")
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
	return k[:4] + "…" + k[len(k)-4:]
}
