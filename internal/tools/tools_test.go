package tools

import (
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestRoleCommitAndPRToolSeparation(t *testing.T) {
	gh := &githubx.REST{Token: "tok", Owner: "acme", Repo: "app"}
	deps := Deps{
		Root:     t.TempDir(),
		Settings: team.DefaultSettings(),
		GitHub:   gh,
	}

	devTools, err := ForRole(team.RoleDeveloper, deps)
	if err != nil {
		t.Fatal(err)
	}
	devNames := map[string]bool{}
	for _, tl := range devTools {
		devNames[tl.Name()] = true
	}
	for _, forbidden := range []string{"git_commit", "git_push", "git_pull", "github_create_pr", "github_merge_pr", "github_comment_pr", "github_review_pr"} {
		if devNames[forbidden] {
			t.Fatalf("developer must NOT have %s tool", forbidden)
		}
	}

	for _, leaderRole := range []string{team.RoleProjectManager, team.RoleArchitect} {
		leaderTools, err := ForRole(leaderRole, deps)
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, tl := range leaderTools {
			names[tl.Name()] = true
		}
		for _, expected := range []string{"git_commit", "git_push", "github_create_pr", "github_get_pr", "github_comment_pr", "github_request_review", "github_review_pr", "github_merge_pr"} {
			if !names[expected] {
				t.Fatalf("role %s should have %s tool", leaderRole, expected)
			}
		}
	}

	// Triage Chief on main workspace (DisableGitWrite = true) has PR governance tools but cannot commit on main checkout.
	triageDeps := deps
	triageDeps.DisableGitWrite = true
	triageTools, err := ForRole(team.RoleProjectManager, triageDeps)
	if err != nil {
		t.Fatal(err)
	}
	triageNames := map[string]bool{}
	for _, tl := range triageTools {
		triageNames[tl.Name()] = true
	}
	if triageNames["git_commit"] || triageNames["git_push"] {
		t.Fatal("triage chief on main workspace must not have direct git_commit/git_push")
	}
	if !triageNames["github_merge_pr"] || !triageNames["github_review_pr"] {
		t.Fatal("triage chief should keep PR review and merge governance tools")
	}
}
