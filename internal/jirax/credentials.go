package jirax

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

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
// 1. Environment variables (JIRA_BASE_URL, JIRA_EMAIL, JIRA_API_TOKEN, JIRA_PROJECT)
// 2. Native HiveMind Jira connector (%AppData%\HiveMind\jira-connector.json)
func LoadCredentials() (baseURL, email, token, defaultProject, source string) {
	envBase := strings.TrimRight(strings.TrimSpace(os.Getenv("JIRA_BASE_URL")), "/")
	envEmail := strings.TrimSpace(os.Getenv("JIRA_EMAIL"))
	envToken := strings.TrimSpace(os.Getenv("JIRA_API_TOKEN"))
	envProj := strings.ToUpper(strings.TrimSpace(os.Getenv("JIRA_PROJECT")))

	stBase, stEmail, stToken, stProj := LoadStoredConfig()

	baseURL = envBase
	email = envEmail
	token = envToken
	defaultProject = envProj
	if baseURL != "" && email != "" && token != "" {
		source = "env"
	}
	if baseURL == "" {
		baseURL = stBase
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
	return baseURL, email, token, defaultProject, source
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
			BaseURL:        strings.TrimRight(strings.TrimSpace(baseURL), "/"),
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
	baseURL, email, token, proj, source := LoadCredentials()
	st := ConnectorStatus{
		Configured:     baseURL != "" && email != "" && token != "",
		Source:         source,
		BaseURL:        baseURL,
		Email:          email,
		TokenMasked:    maskJiraToken(token),
		DefaultProject: proj,
	}
	if !st.Configured {
		st.Detail = "Não configurado (informe URL, E-mail e API Token do Jira Cloud)"
		return st
	}
	client := &HTTP{BaseURL: baseURL, Email: email, Token: token}
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

func sourceLabel(src string) string {
	if src == "env" {
		return "ENV"
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
