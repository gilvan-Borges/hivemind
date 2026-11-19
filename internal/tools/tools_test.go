package tools

import (
	"strings"
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
	for _, forbidden := range []string{"git_commit", "git_push", "git_pull", "github_create_pr", "github_merge_pr", "github_comment_pr", "github_reply_review_comment", "github_review_pr"} {
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
		for _, expected := range []string{
			"git_commit", "git_push",
			"github_create_pr", "github_get_pr", "github_issue_comments",
			"github_pr_diff", "github_list_prs", "github_get_issue",
			"github_comment_pr", "github_reply_review_comment",
			"github_request_review", "github_review_pr", "github_merge_pr",
		} {
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
	if !triageNames["github_merge_pr"] || !triageNames["github_review_pr"] || !triageNames["github_pr_diff"] || !triageNames["github_reply_review_comment"] {
		t.Fatal("triage chief should keep full PR inspection, review and merge governance tools")
	}
}

func TestFormatPRDetailShowsRoleAndAllComments(t *testing.T) {
	pr := githubx.PullRequestDetail{
		Number:  699,
		Title:   "feat: cabecalho execucao",
		Author:  "gilvan-Borges",
		State:   "open",
		HeadRef: "feat/sdd-front-ft082-cabecalho-execucao",
		BaseRef: "homolog",
		ReviewComments: []githubx.Comment{
			{ID: 777, Author: "marcusrdrigues", Path: "src/header.ts", Line: 42, Body: "Ajustar aria-label"},
		},
	}
	issueComments := []githubx.Comment{
		{ID: 888, Author: "marcusrdrigues", Body: "Rodar E2E antes do merge"},
	}

	own := formatPRDetail(pr, issueComments, "gilvan-Borges")
	if !strings.Contains(own, "PR NOSSO") || !strings.Contains(own, "VOCÊ NÃO É O REVISOR") {
		t.Fatalf("expected own-PR role banner, got:\n%s", own)
	}
	if !strings.Contains(own, "[comment_id=777]") || !strings.Contains(own, "src/header.ts:42") {
		t.Fatalf("expected inline comment with file:line and comment_id, got:\n%s", own)
	}
	if !strings.Contains(own, "Rodar E2E antes do merge") {
		t.Fatalf("expected general conversation comment, got:\n%s", own)
	}

	thirdParty := formatPRDetail(pr, issueComments, "outro-dev")
	if !strings.Contains(thirdParty, "PR DE TERCEIRO") {
		t.Fatalf("expected third-party PR role banner, got:\n%s", thirdParty)
	}
}

func TestAutoReviewToolsNeverExposeMerge(t *testing.T) {
	gh := &githubx.REST{Token: "tok", Owner: "acme", Repo: "app"}
	deps := Deps{
		Root:                   t.TempDir(),
		Settings:               team.DefaultSettings(),
		DisableGitWrite:        true,
		DisableMerge:           true,
		ReviewCommentOnly:      true,
		AllowReadOnlyTestShell: true,
		ReviewRequestedInObj:   true,
		GitHub:                 gh,
	}
	list, err := ForRole(team.RoleProjectManager, deps)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range list {
		names[tl.Name()] = true
	}
	for _, forbidden := range []string{"github_merge_pr", "git_commit", "git_push", "git_pull", "github_create_pr", "write_file"} {
		if names[forbidden] {
			t.Fatalf("auto-review mode must NEVER expose %s", forbidden)
		}
	}
	for _, expected := range []string{"github_get_pr", "github_pr_files", "github_pr_diff", "github_review_pr", "github_comment_pr", "search_text", "find_files", "read_file", "run_shell"} {
		if !names[expected] {
			t.Fatalf("auto-review mode missing expected tool %s", expected)
		}
	}
}
