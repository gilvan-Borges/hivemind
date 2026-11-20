package jirax

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHTTPIssueSearchCommentTransition(t *testing.T) {
	var sawSearchFallback bool
	var comment string
	var transition string
	var subtask string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "ana@example.com" || pass != "token" {
			t.Errorf("basic auth = %s %s %v", user, pass, ok)
		}
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/PROJ-123":
			_, _ = w.Write([]byte(`{"key":"PROJ-123","fields":{"summary":"Healthcheck","status":{"name":"A fazer"},"description":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"expor /health"}]}]}}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql":
			http.NotFound(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search":
			sawSearchFallback = true
			if !strings.Contains(string(body), "project = PROJ") {
				t.Errorf("jql body = %s", body)
			}
			_, _ = w.Write([]byte(`{"issues":[{"key":"PROJ-123","fields":{"summary":"Healthcheck","status":{"name":"A fazer"},"description":"texto"}}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue":
			subtask = string(body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"key":"PROJ-124"}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comment"):
			comment = string(body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"1"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/transitions"):
			_, _ = w.Write([]byte(`{"transitions":[{"id":"31","name":"In Progress"}]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/transitions"):
			transition = string(body)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client := &HTTP{BaseURL: srv.URL, Email: "ana@example.com", Token: "token", HTTP: srv.Client()}
	issue, err := client.GetIssue(context.Background(), "PROJ-123")
	if err != nil {
		t.Fatal(err)
	}
	if issue.Summary != "Healthcheck" || !strings.Contains(issue.Description, "/health") || issue.Status != "A fazer" {
		t.Fatalf("issue = %+v", issue)
	}
	found, err := client.Search(context.Background(), "project = PROJ", 5)
	if err != nil || !sawSearchFallback || len(found) != 1 {
		t.Fatalf("search %+v %v fallback %v", found, err, sawSearchFallback)
	}
	sub, err := client.CreateSubtask(context.Background(), "PROJ-123", "Implementar", "detalhe")
	if err != nil || sub.Key != "PROJ-124" {
		t.Fatalf("sub = %+v %v", sub, err)
	}
	if !strings.Contains(subtask, `"parent"`) || !strings.Contains(subtask, "PROJ-123") || !strings.Contains(subtask, "Sub-task") {
		t.Fatalf("subtask body = %s", subtask)
	}
	if err := client.Comment(context.Background(), "PROJ-123", "PR aberto"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(comment, "PR aberto") || !strings.Contains(comment, `"doc"`) {
		t.Fatalf("comment = %s", comment)
	}
	if err := client.Transition(context.Background(), "PROJ-123", "in progress"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(transition, `"31"`) {
		t.Fatalf("transition = %s", transition)
	}
	if _, err := client.GetIssue(context.Background(), "../../myself"); err == nil {
		t.Fatal("expected traversal issue key to be rejected")
	}
	epic, err := client.CreateIssue(context.Background(), "PROJ", "Epic", "Fase 1: Fundação", "Linha 1\nLinha 2", "")
	if err != nil || epic.Key != "PROJ-124" {
		t.Fatalf("epic = %+v %v", epic, err)
	}
	if !strings.Contains(subtask, `"Epic"`) || !strings.Contains(subtask, "Linha 2") {
		t.Fatalf("create issue body = %s", subtask)
	}
}

func TestJiraNativeCredentials(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("APPDATA", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("JIRA_BASE_URL", "")
	t.Setenv("JIRA_EMAIL", "")
	t.Setenv("JIRA_API_TOKEN", "")
	t.Setenv("JIRA_PROJECT", "")

	if err := SaveCredentials("https://acme.atlassian.net/", "dev@acme.com", "secret-tok-1234", "ipg", false); err != nil {
		t.Fatal(err)
	}
	base, email, tok, proj, src := LoadCredentials()
	if base != "https://acme.atlassian.net" || email != "dev@acme.com" || tok != "secret-tok-1234" || proj != "IPG" || src != "config" {
		t.Fatalf("unexpected creds: %q %q %q %q %q", base, email, tok, proj, src)
	}
	// Updating project without re-typing token preserves token
	if err := SaveCredentials("https://acme.atlassian.net", "dev@acme.com", "", "HM", false); err != nil {
		t.Fatal(err)
	}
	_, _, tok2, proj2, _ := LoadCredentials()
	if tok2 != "secret-tok-1234" || proj2 != "HM" {
		t.Fatalf("expected token preserved, got tok=%q proj=%q", tok2, proj2)
	}
	if !FromEnv().Enabled() {
		t.Fatal("FromEnv must enable client from saved native connector config")
	}
	// Clearing removes stored credentials
	if err := SaveCredentials("", "", "", "", true); err != nil {
		t.Fatal(err)
	}
	if FromEnv().Enabled() {
		t.Fatal("FromEnv must be disabled after clear")
	}
}

func TestNormalizeBaseURLAndListProjects(t *testing.T) {
	if got := NormalizeBaseURL("vibetex.atlassian.net"); got != "https://vibetex.atlassian.net" {
		t.Fatalf("expected https scheme added, got %q", got)
	}
	if got := NormalizeBaseURL("https://vibetex.atlassian.net/jira/software/projects/IPG/boards/1"); got != "https://vibetex.atlassian.net" {
		t.Fatalf("expected path stripped to origin, got %q", got)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/project/search" {
			_, _ = w.Write([]byte(`{"values":[{"key":"WEB","name":"Portal Web","projectTypeKey":"software"},{"key":"IPG","name":"IPG Core","projectTypeKey":"software"}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := &HTTP{BaseURL: srv.URL, Email: "a@b.com", Token: "tok", HTTP: srv.Client()}
	projs, err := client.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects failed: %v", err)
	}
	if len(projs) != 2 || projs[0].Key != "IPG" || projs[1].Key != "WEB" {
		t.Fatalf("expected sorted [IPG, WEB], got %+v", projs)
	}
}

func TestDetectMCPConfigParentDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("APPDATA", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	for _, k := range []string{"JIRA_BASE_URL", "JIRA_HOST", "JIRA_URL", "JIRA_EMAIL", "JIRA_API_TOKEN", "JIRA_TOKEN", "ATLASSIAN_DOMAIN", "ATLASSIAN_EMAIL", "ATLASSIAN_API_TOKEN"} {
		t.Setenv(k, "")
	}

	parent := filepath.Join(tmp, "Projetos")
	ws := filepath.Join(parent, "IPG")
	if err := os.MkdirAll(filepath.Join(parent, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	mcpJSON := `{"mcpServers":{"jira":{"command":"npx","args":["-y","@tocharianou/jira-mcp"],"env":{"JIRA_HOST":"https://acme.atlassian.net","JIRA_EMAIL":"dev@acme.com","JIRA_TOKEN":"tok-mcp-12345"}}}}`
	if err := os.WriteFile(filepath.Join(parent, ".agents", "mcp_config.json"), []byte(mcpJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	base, email, tok, _, src := LoadCredentialsForWorkspace(ws)
	if base != "https://acme.atlassian.net" || email != "dev@acme.com" || tok != "tok-mcp-12345" || !strings.HasPrefix(src, "mcp:") {
		t.Fatalf("expected MCP auto-discovery from parent .agents/mcp_config.json, got %q %q %q %q", base, email, tok, src)
	}
}

func TestMultiOrgJiraIsolation(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("APPDATA", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	for _, k := range []string{"JIRA_BASE_URL", "JIRA_HOST", "JIRA_URL", "JIRA_EMAIL", "JIRA_API_TOKEN", "JIRA_TOKEN", "ATLASSIAN_DOMAIN", "ATLASSIAN_EMAIL", "ATLASSIAN_API_TOKEN", "JIRA_PROJECT"} {
		t.Setenv(k, "")
	}

	mkRepo := func(folder, remote string) string {
		ws := filepath.Join(tmp, folder)
		gd := filepath.Join(ws, ".git")
		if err := os.MkdirAll(gd, 0o755); err != nil {
			t.Fatal(err)
		}
		if remote != "" {
			cfg := "[remote \"origin\"]\n\turl = " + remote + "\n"
			if err := os.WriteFile(filepath.Join(gd, "config"), []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return ws
	}

	wsVibetex := mkRepo("vibetex-ipg", "git@github.com:vibetex/ipg.git")
	wsEmpresaB := mkRepo("empresa-b-api", "https://github.com/empresab/api.git")
	wsOrg3 := mkRepo("terceira-org-app", "https://github.com/org3/app.git")

	if err := SaveProfile(JiraProfile{
		ID:             "vibetex",
		Name:           "Jira Vibetex",
		BaseURL:        "https://vibetex.atlassian.net",
		Email:          "gilvan@vibetex.com",
		Token:          "tok-vibetex-1234",
		DefaultProject: "IPG",
		OrgPatterns:    []string{"vibetex"},
	}, ""); err != nil {
		t.Fatal(err)
	}
	if err := SaveProfile(JiraProfile{
		ID:             "empresab",
		Name:           "Jira Empresa B",
		BaseURL:        "https://empresab.atlassian.net",
		Email:          "gilvan@empresab.com",
		Token:          "tok-empb-5678",
		DefaultProject: "EB",
		OrgPatterns:    []string{"empresab"},
	}, ""); err != nil {
		t.Fatal(err)
	}

	// Org 1 -> Vibetex Jira
	rV := ResolveForWorkspace(wsVibetex)
	if rV.BaseURL != "https://vibetex.atlassian.net" || rV.DefaultProject != "IPG" || rV.MatchMode != "auto_org" {
		t.Fatalf("expected vibetex auto_org, got %+v", rV)
	}

	// Org 2 -> Empresa B Jira
	rB := ResolveForWorkspace(wsEmpresaB)
	if rB.BaseURL != "https://empresab.atlassian.net" || rB.DefaultProject != "EB" || rB.MatchMode != "auto_org" {
		t.Fatalf("expected empresab auto_org, got %+v", rB)
	}

	// Org 3 -> strictly isolated (no Jira configured, NEVER leaks Vibetex or Empresa B)
	r3 := ResolveForWorkspace(wsOrg3)
	if r3.MatchMode != "isolated_none" || r3.BaseURL != "" || FromWorkspace(wsOrg3).Enabled() {
		t.Fatalf("expected org3 isolated without Jira, got %+v", r3)
	}

	// Manual pin overrides isolation or switches Jira
	if err := BindWorkspace(wsOrg3, "vibetex"); err != nil {
		t.Fatal(err)
	}
	r3Pinned := ResolveForWorkspace(wsOrg3)
	if r3Pinned.BaseURL != "https://vibetex.atlassian.net" || r3Pinned.MatchMode != "manual" {
		t.Fatalf("expected manual pin to vibetex, got %+v", r3Pinned)
	}

	// Explicit "none" disables Jira even on a matched org
	if err := BindWorkspace(wsVibetex, "none"); err != nil {
		t.Fatal(err)
	}
	rVOff := ResolveForWorkspace(wsVibetex)
	if rVOff.MatchMode != "isolated_none" || FromWorkspace(wsVibetex).Enabled() {
		t.Fatalf("expected manual none to disable vibetex Jira, got %+v", rVOff)
	}
	for _, s := range rV.Profiles {
		if strings.Contains(s.TokenMasked, "tok-vibetex") || strings.Contains(s.TokenMasked, "tok-empb") {
			t.Fatalf("profile summary leaked a token: %+v", s)
		}
	}
}

func TestJiraIsolationIgnoresEnvLegacyAndDefault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("APPDATA", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	for _, k := range []string{"JIRA_BASE_URL", "JIRA_HOST", "JIRA_URL", "JIRA_EMAIL", "JIRA_USERNAME", "JIRA_API_TOKEN", "JIRA_TOKEN", "ATLASSIAN_URL", "ATLASSIAN_DOMAIN", "ATLASSIAN_EMAIL", "ATLASSIAN_API_TOKEN", "JIRA_PROJECT"} {
		t.Setenv(k, "")
	}

	wsOrg := filepath.Join(tmp, "org3")
	gitDir := filepath.Join(wsOrg, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[remote \"origin\"]\n\turl = https://github.com/org3/app.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wsV := filepath.Join(tmp, "vibetex-app")
	if err := os.MkdirAll(filepath.Join(wsV, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsV, ".git", "config"), []byte("[remote \"origin\"]\n\turl = git@github.com:vibetex/app.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SaveProfile(JiraProfile{
		ID: "legacy", Name: "Legado", BaseURL: "https://legacy.atlassian.net",
		Email: "legacy@example.com", Token: "legacy-token-abcdef", IsDefault: true,
	}, ""); err != nil {
		t.Fatal(err)
	}
	if err := SaveProfile(JiraProfile{
		ID: "vibetex", Name: "Vibetex", BaseURL: "https://vibetex.atlassian.net",
		Email: "a@vibetex.com", Token: "vibetex-token-zzzz", OrgPatterns: []string{"vibetex"},
	}, ""); err != nil {
		t.Fatal(err)
	}

	t.Setenv("JIRA_BASE_URL", "https://env.atlassian.net")
	t.Setenv("JIRA_EMAIL", "env@example.com")
	t.Setenv("JIRA_API_TOKEN", "env-token-should-not-leak")

	got := ResolveForWorkspace(wsOrg)
	if got.MatchMode != "isolated_none" || got.Token != "" || got.Email != "" || FromWorkspace(wsOrg).Enabled() {
		t.Fatalf("org3 got another org's Jira: %+v", got)
	}
	matched := ResolveForWorkspace(wsV)
	if matched.Token != "vibetex-token-zzzz" || matched.Email != "a@vibetex.com" || matched.MatchMode != "auto_org" {
		t.Fatalf("env mixed into the matched profile: %+v", matched)
	}
	if err := BindWorkspace(wsV, "none"); err != nil {
		t.Fatal(err)
	}
	off := ResolveForWorkspace(wsV)
	if off.MatchMode != "isolated_none" || off.Token != "" || FromWorkspace(wsV).Enabled() {
		t.Fatalf("none binding lost to env: %+v", off)
	}

	path, err := jiraConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("jira file mode = %v", info.Mode().Perm())
	}
}

func TestLegacyJiraFileMigration(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("APPDATA", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	for _, k := range []string{"JIRA_BASE_URL", "JIRA_HOST", "JIRA_URL", "JIRA_EMAIL", "JIRA_API_TOKEN", "JIRA_TOKEN", "ATLASSIAN_DOMAIN", "ATLASSIAN_EMAIL", "ATLASSIAN_API_TOKEN", "JIRA_PROJECT"} {
		t.Setenv(k, "")
	}
	dir := filepath.Join(tmp, "HiveMind")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"base_url":"https://old.atlassian.net/jira/software/projects/OLD/boards/1","email":"a@old.com","token":"legacy-token-abcdef","default_project":"old"}`)
	path := filepath.Join(dir, "jira-connector.json")
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	got := ResolveForWorkspace("")
	if got.BaseURL != "https://old.atlassian.net" || got.Email != "a@old.com" || got.Token != "legacy-token-abcdef" || got.DefaultProject != "OLD" {
		t.Fatalf("legacy file not migrated: %+v", got)
	}
	if len(got.Profiles) != 1 || strings.Contains(got.Profiles[0].TokenMasked, "legacy-token") {
		t.Fatalf("legacy token leaked in summary: %+v", got.Profiles)
	}
}

func TestMatchJiraTransitionIDSemanticAndPlural(t *testing.T) {
	var transitions []jiraTransitionItem
	mk := func(id, name, toName, catKey string) jiraTransitionItem {
		var tr jiraTransitionItem
		tr.ID = id
		tr.Name = name
		tr.To.Name = toName
		tr.To.StatusCategory.Key = catKey
		return tr
	}
	transitions = []jiraTransitionItem{
		mk("11", "To Do", "A fazer", "new"),
		mk("21", "In Progress", "EM ANDAMENTO", "indeterminate"),
		mk("31", "Review", "REVISÃO", "indeterminate"),
		mk("41", "Done", "CONCLUÍDOS", "done"),
	}

	cases := []struct {
		query  string
		wantID string
	}{
		{"Concluído", "41"},
		{"concluido", "41"},
		{"CONCLUÍDOS", "41"},
		{"Done", "41"},
		{"Finalizado", "41"},
		{"Em andamento", "21"},
		{"In Progress", "21"},
		{"Revisão", "31"},
		{"revisar", "31"},
		{"A fazer", "11"},
		{"To Do", "11"},
	}
	for _, tc := range cases {
		if got := matchJiraTransitionID(transitions, tc.query); got != tc.wantID {
			t.Errorf("matchJiraTransitionID(%q) = %q, want %q", tc.query, got, tc.wantID)
		}
	}
}
