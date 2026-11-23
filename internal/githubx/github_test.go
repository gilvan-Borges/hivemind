package githubx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseRemote(t *testing.T) {
	cases := []struct{ in, owner, repo string }{
		{"git@github.com:acme/app.git", "acme", "app"},
		{"https://github.com/acme/app.git", "acme", "app"},
		{"https://x-access-token:abc@github.com/acme/app", "acme", "app"},
	}
	for _, tc := range cases {
		o, r, ok := ParseRemote(tc.in)
		if !ok || o != tc.owner || r != tc.repo {
			t.Fatalf("%s -> %s %s %v", tc.in, o, r, ok)
		}
	}
	if _, _, ok := ParseRemote("https://gitlab.com/a/b"); ok {
		t.Fatal("gitlab should not parse as github")
	}
}

func TestREST(t *testing.T) {
	var prBody, commentBody, replyBody, reqRevBody, mergeBody string
	var closedPR, closedIssue bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("auth = %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
			t.Errorf("api version = %s", r.Header.Get("X-GitHub-Api-Version"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/user":
			_, _ = w.Write([]byte(`{"login":"gilvan"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/app/pulls":
			prBody = read(r)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":7,"html_url":"https://github.com/acme/app/pull/7","title":"PROJ-1: oi"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/app/pulls":
			_, _ = w.Write([]byte(`[{"number":7,"title":"PROJ-1: oi","html_url":"https://github.com/acme/app/pull/7","state":"open","draft":false,"user":{"login":"gilvan"},"head":{"ref":"hivemind/proj-1/entrega"},"base":{"ref":"homolog"},"requested_reviewers":[{"login":"marcus"}]}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/app/pulls/7":
			_, _ = w.Write([]byte(`{"number":7,"title":"PROJ-1: oi","html_url":"https://github.com/acme/app/pull/7","state":"open","merged":false,"changed_files":2,"additions":15,"deletions":3,"user":{"login":"gilvan"},"head":{"ref":"hivemind/proj-1/entrega"},"base":{"ref":"homolog"},"requested_reviewers":[]}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/acme/app/pulls/7":
			closedPR = strings.Contains(read(r), `"closed"`)
			_, _ = w.Write([]byte(`{"number":7,"state":"closed"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/app/pulls/7/reviews":
			_, _ = w.Write([]byte(`[{"id":10,"state":"CHANGES_REQUESTED","body":"ajustar erro","submitted_at":"2026-11-18T12:00:00Z","user":{"login":"fulano"}},{"id":11,"state":"APPROVED","body":"agora sim","submitted_at":"2026-11-18T13:00:00Z","user":{"login":"fulano"}}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/app/pulls/7/comments":
			_, _ = w.Write([]byte(`[{"id":20,"path":"main.go","line":12,"diff_hunk":"@@ -10,3 +10,5 @@","body":"ajustar linha 12","created_at":"2026-11-18T12:05:00Z","user":{"login":"marcus"}}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/app/pulls/7/comments/20/replies":
			replyBody = read(r)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":21}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/app/pulls/7/files":
			_, _ = w.Write([]byte(`[{"filename":"main.go","status":"modified","additions":10,"deletions":2,"patch":"@@ -1,2 +1,10 @@"}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/app/pulls/7/requested_reviewers":
			reqRevBody = read(r)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPut && r.URL.Path == "/repos/acme/app/pulls/7/merge":
			mergeBody = read(r)
			_, _ = w.Write([]byte(`{"merged":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/app/issues/7":
			_, _ = w.Write([]byte(`{"number":7,"title":"Issue 7","body":"detalhes","html_url":"https://github.com/acme/app/issues/7","state":"open","user":{"login":"gilvan"},"labels":[{"name":"bug"}]}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/acme/app/issues/7":
			closedIssue = strings.Contains(read(r), `"closed"`)
			_, _ = w.Write([]byte(`{"number":7,"state":"closed"}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/issues/7/comments"):
			commentBody = read(r)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/issues/7/comments"):
			_, _ = w.Write([]byte(`[{"id":1,"body":"precisa corrigir E2E","created_at":"2026-11-18T12:10:00Z","user":{"login":"marcus"}}]`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/status"):
			_, _ = w.Write([]byte(`{"state":"success"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("GITHUB_USER", "")
	client := &REST{BaseURL: srv.URL, Token: "token", Owner: "acme", Repo: "app", HTTP: srv.Client()}

	me, err := client.CurrentUser(context.Background())
	if err != nil || me != "gilvan" {
		t.Fatalf("user = %q %v", me, err)
	}
	pr, err := client.CreatePullRequest(context.Background(), CreatePR{Title: "PROJ-1: oi", Head: "hivemind/proj-1/entrega", Base: "homolog", Body: "pronto"})
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 7 || !strings.Contains(pr.URL, "/pull/7") {
		t.Fatalf("pr = %+v", pr)
	}
	if !strings.Contains(prBody, "hivemind/proj-1/entrega") {
		t.Fatalf("body = %s", prBody)
	}

	prs, err := client.ListPullRequests(context.Background(), "open", 10)
	if err != nil || len(prs) != 1 || prs[0].Number != 7 || len(prs[0].RequestedReviewers) != 1 {
		t.Fatalf("prs = %+v %v", prs, err)
	}

	detail, err := client.GetPullRequest(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Author != "gilvan" || detail.BaseRef != "homolog" || len(detail.Reviews) != 2 {
		t.Fatalf("detail = %+v", detail)
	}
	if len(detail.ReviewComments) != 1 || detail.ReviewComments[0].Line != 12 || detail.ReviewComments[0].Author != "marcus" {
		t.Fatalf("reviewComments = %+v", detail.ReviewComments)
	}
	if len(detail.IssueComments) != 1 || detail.IssueComments[0].Author != "marcus" || !strings.Contains(detail.IssueComments[0].Body, "E2E") {
		t.Fatalf("issueComments = %+v", detail.IssueComments)
	}
	if len(detail.CommentFetchErrors) != 0 {
		t.Fatalf("unexpected commentFetchErrors: %+v", detail.CommentFetchErrors)
	}
	if err := CheckPRMergeAllowed(detail); err != nil {
		t.Fatalf("expected fulano re-approval to allow merge: %v", err)
	}

	files, err := client.ListPullRequestFiles(context.Background(), 7)
	if err != nil || len(files) != 1 || files[0].Filename != "main.go" {
		t.Fatalf("files = %+v %v", files, err)
	}
	if err := client.ReplyToReviewComment(context.Background(), 7, 20, "Corrigido no commit abc123"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replyBody, "Corrigido no commit abc123") {
		t.Fatalf("replyBody = %s", replyBody)
	}

	if err := client.RequestReviewers(context.Background(), 7, []string{"@fulano"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reqRevBody, `"fulano"`) {
		t.Fatalf("requested reviewers = %s", reqRevBody)
	}
	if err := client.MergePullRequest(context.Background(), 7, "squash", "feat: merge"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mergeBody, `"squash"`) {
		t.Fatalf("merge body = %s", mergeBody)
	}
	if err := client.CommentOnIssue(context.Background(), 7, "link do PR"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(commentBody, "link do PR") {
		t.Fatalf("comment = %s", commentBody)
	}
	comments, err := client.ListIssueComments(context.Background(), 7)
	if err != nil || len(comments) != 1 || comments[0].Author != "marcus" {
		t.Fatalf("comments = %+v %v", comments, err)
	}
	iss, err := client.GetIssue(context.Background(), 7)
	if err != nil || iss.Number != 7 || len(iss.Labels) != 1 || len(iss.Comments) != 1 {
		t.Fatalf("issue = %+v %v", iss, err)
	}
	if err := client.ClosePullRequest(context.Background(), 7); err != nil || !closedPR {
		t.Fatalf("close pr failed: %v closed=%v", err, closedPR)
	}
	if err := client.CloseIssue(context.Background(), 7); err != nil || !closedIssue {
		t.Fatalf("close issue failed: %v closed=%v", err, closedIssue)
	}
	state, err := client.CIStatus(context.Background(), "abc")
	if err != nil || state != "success" {
		t.Fatalf("ci = %s %v", state, err)
	}
	if _, err := client.CIStatus(context.Background(), "../../secret"); err == nil {
		t.Fatal("expected path traversal ref to fail")
	}
}

func TestDecodeIssueCommentsPayload(t *testing.T) {
	// Standard GitHub REST array response
	restJSON := `[{"id":991,"body":"favor ajustar testes","created_at":"2026-11-18T10:00:00Z","user":{"login":"marcus"}}]`
	list, err := decodeIssueCommentsPayload([]byte(restJSON))
	if err != nil || len(list) != 1 || list[0].ID != 991 || list[0].Author != "marcus" || list[0].Body != "favor ajustar testes" {
		t.Fatalf("rest decode failed: %+v %v", list, err)
	}

	// GraphQL wrapper response with string ID (must not fail unmarshaling!)
	gqlJSON := `{"comments":[{"id":"IC_kwDO12345","body":"olá do graphql","createdAt":"2026-11-18T11:00:00Z","author":{"login":"marcus"}}]}`
	list2, err := decodeIssueCommentsPayload([]byte(gqlJSON))
	if err != nil || len(list2) != 1 || list2[0].Author != "marcus" || list2[0].Body != "olá do graphql" {
		t.Fatalf("graphql decode failed: %+v %v", list2, err)
	}
}

func TestNativeConnectorCredentials(t *testing.T) {
	tmpCfg := t.TempDir()
	t.Setenv("APPDATA", tmpCfg)
	t.Setenv("XDG_CONFIG_HOME", tmpCfg)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_API_URL", "")
	t.Setenv("HIVEMIND_DISABLE_GH_TOKEN", "0")

	origFn := ghAuthTokenFn
	defer func() {
		ghAuthTokenFn = origFn
		InvalidateTokenCache()
	}()
	ghAuthTokenFn = func() string { return "gho_from_gh_cli_123456" }
	InvalidateTokenCache()

	// 1. Falls back automatically to `gh auth token` -> upgrades to native REST connector!
	tok, base, src := LoadCredentials()
	if tok != "gho_from_gh_cli_123456" || src != "gh-token" || base != DefaultAPIBaseURL {
		t.Fatalf("expected gh-token fallback, got tok=%q base=%q src=%q", tok, base, src)
	}
	if _, ok := Detect("acme", "app").(*REST); !ok {
		t.Fatal("Detect must return native *REST when gh auth token is available")
	}

	// 2. Saving native token in config overrides gh-token.
	if err := SaveCredentials("ghp_saved_native_9876", "https://ghe.example.com/api/v3/"); err != nil {
		t.Fatal(err)
	}
	tok, base, src = LoadCredentials()
	if tok != "ghp_saved_native_9876" || src != "config" || base != "https://ghe.example.com/api/v3" {
		t.Fatalf("expected config override, got tok=%q base=%q src=%q", tok, base, src)
	}
	info := InspectConnector()
	if !info.Configured || info.Mode != "rest-config" || info.TokenMasked != "ghp_…9876" {
		t.Fatalf("unexpected info: %+v", info)
	}

	// 3. Env var has top priority.
	t.Setenv("GITHUB_TOKEN", "ghp_env_priority_0001")
	tok, _, src = LoadCredentials()
	if tok != "ghp_env_priority_0001" || src != "env" {
		t.Fatalf("expected env priority, got tok=%q src=%q", tok, src)
	}

	// 4. Clearing saved credentials works cleanly.
	t.Setenv("GITHUB_TOKEN", "")
	if err := SaveCredentials("", ""); err != nil {
		t.Fatal(err)
	}
	storedTok, storedBase := LoadStoredConfig()
	if storedTok != "" || storedBase != "" {
		t.Fatalf("expected cleared config, got tok=%q base=%q", storedTok, storedBase)
	}
}

func TestExplicitReviewObjectiveIncludesMentionAndApprove(t *testing.T) {
	if !IsExplicitReviewObjective("Responder menção no PR #768 de @autor — título. Fui marcado por @colega.") {
		t.Fatal("menção no PR deveria abrir a revisão")
	}
	if !IsExplicitReviewObjective("coemente no PR e aprove entao") {
		t.Fatal("pedido de aprovar no PR deveria abrir a revisão")
	}
	if !IsExplicitReviewObjective("comenta essa porra pelo MCP do github") {
		t.Fatal("pedido explícito de MCP do GitHub deveria abrir a revisão")
	}
	jira := "Resolver issue PROJ-1\nDescrição:\naprovação pendente neste PR #1"
	if IsExplicitReviewObjective(jira) {
		t.Fatal("descrição de issue não deveria abrir revisão")
	}
}

func TestPRGovernancePolicies(t *testing.T) {
	thirdPartyPR := PullRequestDetail{
		Number:  42,
		State:   "open",
		Author:  "outro-dev",
		HeadRef: "feat/x",
		BaseRef: "homolog",
	}
	// Cannot comment or approve third-party PR without review request.
	if err := CheckPRCommentAllowed(thirdPartyPR, "gilvan", false); err == nil {
		t.Fatal("expected comment on third-party PR without review request to fail")
	}
	if err := CheckPRReviewAllowed(thirdPartyPR, "gilvan", "APPROVE", false); err == nil {
		t.Fatal("expected review on third-party PR without review request to fail")
	}
	// Allowed when review was requested on GitHub or explicitly asked by user.
	if err := CheckPRReviewAllowed(thirdPartyPR, "gilvan", "APPROVE", true); err != nil {
		t.Fatalf("expected explicit review request to allow review: %v", err)
	}
	thirdPartyPR.RequestedReviewers = []string{"gilvan"}
	if err := CheckPRCommentAllowed(thirdPartyPR, "gilvan", false); err != nil {
		t.Fatalf("expected requested reviewer to be allowed to comment: %v", err)
	}

	// Own PR lifecycle: fulano requested changes -> cannot merge -> re-request review -> still cannot merge -> fulano approves -> can merge!
	ownPR := PullRequestDetail{
		Number:  1,
		State:   "open",
		Author:  "gilvan",
		HeadRef: "hivemind/pr-1/entrega",
		BaseRef: "homolog",
		Reviews: []PRReview{
			{Author: "fulano", State: "CHANGES_REQUESTED", Body: "ajustar validação"},
		},
	}
	if err := CheckPRCommentAllowed(ownPR, "gilvan", false); err != nil {
		t.Fatalf("author must always be allowed to reply on own PR: %v", err)
	}
	if err := CheckPRReviewAllowed(ownPR, "gilvan", "APPROVE", true); err == nil {
		t.Fatal("author must not self-approve own PR")
	}
	if err := CheckPRReviewAllowed(ownPR, "gilvan", "COMMENT", true); err == nil {
		t.Fatal("author must not submit reviewer-style review on own PR")
	}
	if err := CheckPRMergeAllowed(ownPR); err == nil {
		t.Fatal("must not merge while fulano requested changes")
	}

	// Re-requested review from fulano (pending):
	ownPR.RequestedReviewers = []string{"fulano"}
	if err := CheckPRMergeAllowed(ownPR); err == nil {
		t.Fatal("must not merge while fulano re-review is still pending")
	}

	// Fulano approves:
	ownPR.RequestedReviewers = nil
	ownPR.Reviews = append(ownPR.Reviews, PRReview{Author: "fulano", State: "APPROVED", Body: "LGTM"})
	if err := CheckPRMergeAllowed(ownPR); err != nil {
		t.Fatalf("expected merge allowed once fulano approved: %v", err)
	}
}

func TestCheckOwnerMergeAllowed(t *testing.T) {
	pr := PullRequestDetail{
		Number:             42,
		Author:             "author",
		State:              "OPEN",
		RequestedReviewers: []string{"bot-reviewer"},
	}
	if err := CheckOwnerMergeAllowed(pr, "author"); err != nil {
		t.Fatalf("expected owner merge allowed with 0 approvals: %v", err)
	}
	if err := CheckOwnerMergeAllowed(pr, "other-user"); err == nil {
		t.Fatal("must block owner merge when currentUser != pr.Author")
	}

	prWithChanges := pr
	prWithChanges.Reviews = []PRReview{
		{Author: "reviewer", State: "CHANGES_REQUESTED", Body: "fix this"},
	}
	if err := CheckOwnerMergeAllowed(prWithChanges, "author"); err == nil {
		t.Fatal("must block owner merge while CHANGES_REQUESTED is unresolved")
	}
}

func TestGHCLI(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "gh")
	if runtime.GOOS == "windows" {
		bin += ".exe"
		t.Skip("script fake do gh é exercitado no Linux/macOS")
	}
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"" + filepath.Join(dir, "args.txt") + "\"\n" +
		"if [ \"$1\" = \"pr\" ]; then echo https://github.com/acme/app/pull/3; exit 0; fi\n" +
		"if [ \"$1\" = \"api\" ]; then\n" +
		"  case \"$2\" in\n" +
		"    */issues/*/comments*) echo '[{\"id\":1,\"body\":\"oi\",\"user\":{\"login\":\"ana\"}}]'; exit 0 ;;\n" +
		"    */status) echo success; exit 0 ;;\n" +
		"  esac\n" +
		"fi\n" +
		"echo '[]'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cli := &CLI{Bin: bin, Owner: "acme", Repo: "app"}
	pr, err := cli.CreatePullRequest(context.Background(), CreatePR{Title: "t", Head: "h", Base: "main", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if pr.URL != "https://github.com/acme/app/pull/3" || pr.Number != 3 {
		t.Fatalf("pr = %+v", pr)
	}
	comments, err := cli.ListIssueComments(context.Background(), 3)
	if err != nil || len(comments) != 1 || comments[0].Body != "oi" || comments[0].Author != "ana" {
		t.Fatalf("%+v %v", comments, err)
	}
	state, err := cli.CIStatus(context.Background(), "abc")
	if err != nil || state != "success" {
		t.Fatalf("state %s %v", state, err)
	}
	args, err := os.ReadFile(filepath.Join(dir, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "--comments") {
		t.Fatalf("must never invoke gh with --comments alongside --json: %s", args)
	}
	if !strings.Contains(string(args), "pr create") || !strings.Contains(string(args), "acme/app") {
		t.Fatalf("args = %s", args)
	}
}

func read(r *http.Request) string {
	b, _ := ioReadAll(r)
	return string(b)
}

func ioReadAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 512)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}
