package idecore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// All projects, repos and issues below are SYNTHETIC (AGENTS.md: acme/org-alpha
// style identities only — no real customer data).

func TestMatchJiraProjectFromNamesIsGeneric(t *testing.T) {
	projs := []jirax.Project{
		{Key: "ACME", Name: "Acme Principal"},
		{Key: "PLAT", Name: "OrgAlpha Web (PLAT)"},
		{Key: "SIG", Name: "Sigma"},
		{Key: "QDI", Name: "Quadro de Ideias"},
		{Key: "HLP", Name: "Helpdesk Central"},
		{Key: "AW", Name: "Alpha Web"},
		{Key: "AM", Name: "Alpha Mobile"},
	}

	cases := []struct {
		raw  string
		want string
	}{
		// Exact key match.
		{"acme", "ACME"},
		{"acme-mobile", "ACME"},
		// Containment of the normalized (unique) project title.
		{"orgAlpha", "PLAT"},
		{"orgalpha-web", "PLAT"},
		{"AppOrgAlphaWeb", "PLAT"},
		// camelCase humps + discriminative token match.
		{"sigmaCore", "SIG"},
		{"SigmaAppApiAtualizado", "SIG"},
		{"QuadroIdeias", "QDI"},
		{"QuadroIdeiasWeb", "QDI"},
		{"helpdesk", "HLP"},
		// No project matches: org-common token never matches alone.
		{"alpha-data", ""},
		{"trace-pharma", ""},
		{"service-ponto", ""},
	}
	for _, tc := range cases {
		if got := matchJiraProjectFromNames(projs, tc.raw); got != tc.want {
			t.Errorf("matchJiraProjectFromNames(%q) = %q, esperava %q", tc.raw, got, tc.want)
		}
	}
}

func TestMatchJiraProjectFromNamesAmbiguousTokenNeverMatches(t *testing.T) {
	projs := []jirax.Project{
		{Key: "AW", Name: "Alpha Web"},
		{Key: "AM", Name: "Alpha Mobile"},
	}
	if got := matchJiraProjectFromNames(projs, "alpha"); got != "" {
		t.Fatalf("token presente em 2 projetos é ambíguo e não pode casar, casou com %q", got)
	}
	if got := matchJiraProjectFromNames(projs, "alpha-web"); got != "AW" {
		t.Fatalf("combinação de tokens únicos deve casar, got %q", got)
	}
}

// TestMatchJiraProjectFromNamesBehaviorIsDataDriven pins the generic contract:
// matching depends ONLY on the project names passed in — the same repo name
// resolves differently against two different synthetic orgs, proving no
// customer heuristics are baked into the code.
func TestMatchJiraProjectFromNamesBehaviorIsDataDriven(t *testing.T) {
	repo := "sigma-mobile"
	orgA := []jirax.Project{{Key: "SIG", Name: "Sigma"}}
	orgB := []jirax.Project{{Key: "ZT", Name: "Zeta Tools"}, {Key: "BX", Name: "Box Storage"}}
	if got := matchJiraProjectFromNames(orgA, repo); got != "SIG" {
		t.Fatalf("org A deveria casar SIG, got %q", got)
	}
	if got := matchJiraProjectFromNames(orgB, repo); got != "" {
		t.Fatalf("org B não tem Sigma — nada deveria casar, got %q", got)
	}
}

// TestListProjectIssuesNeverMutatesTeamYAML pins the read-only contract of
// Spec 08: the listing call may infer the Jira project for THIS query, but it
// must never write .hivemind/team.yaml (the old EnableProjectJira side effect).
func TestListProjectIssuesNeverMutatesTeamYAML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/rest/api/3/project/search":
			_, _ = w.Write([]byte(`{"values":[{"key":"ACME","name":"Acme Principal","projectTypeKey":"software"},{"key":"PLAT","name":"OrgAlpha Web (PLAT)","projectTypeKey":"software"}]}`))
		case r.URL.Path == "/rest/api/3/search/jql" || r.URL.Path == "/rest/api/3/search":
			_, _ = w.Write([]byte(`{"issues":[{"key":"ACME-1","fields":{"summary":"Tarefa um","status":{"name":"A Fazer","statusCategory":{"key":"new"}},"issuetype":{"name":"Task"}}}]}`))
		default:
			http.Error(w, "rota inesperada: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("JIRA_BASE_URL", srv.URL)
	t.Setenv("JIRA_EMAIL", "qa@example.com")
	t.Setenv("JIRA_API_TOKEN", "tok")

	root := t.TempDir()
	ws := filepath.Join(root, "acme") // basename matches the ACME project key
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	f := team.File{
		Version:  1,
		Active:   "padrao",
		Settings: team.DefaultSettings(),
		Teams: []team.Team{{
			Name:   "padrao",
			Agents: []team.Agent{{Name: "dev", Role: team.RoleDeveloper, Provider: "gemini", Model: "gemini-flash-latest"}},
		}},
	}
	if err := team.Save(ws, f); err != nil {
		t.Fatal(err)
	}
	teamPath := filepath.Join(ws, ".hivemind", "team.yaml")
	before, err := os.ReadFile(teamPath)
	if err != nil {
		t.Fatal(err)
	}

	issues, err := ListProjectIssues(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	foundACME := false
	for _, iss := range issues {
		if iss.Source == "jira" && iss.Key == "ACME-1" {
			foundACME = true
		}
	}
	if !foundACME {
		t.Fatalf("esperava a issue inferida ACME-1 no resultado, got %+v", issues)
	}

	after, err := os.ReadFile(teamPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("ListProjectIssues MUTOU .hivemind/team.yaml — a listagem deve ser 100% read-only")
	}
}
