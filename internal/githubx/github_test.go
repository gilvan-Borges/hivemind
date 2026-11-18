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
	var prBody, commentBody, reqRevBody, mergeBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("auth = %s", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/user":
			_, _ = w.Write([]byte(`{"login":"gilvan"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/app/pulls":
			prBody = read(r)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":7,"html_url":"https://github.com/acme/app/pull/7","title":"PROJ-1: oi"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/app/pulls/7":
			_, _ = w.Write([]byte(`{"number":7,"title":"PROJ-1: oi","html_url":"https://github.com/acme/app/pull/7","state":"open","merged":false,"user":{"login":"gilvan"},"head":{"ref":"hivemind/proj-1/entrega"},"base":{"ref":"homolog"},"requested_reviewers":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/app/pulls/7/reviews":
			_, _ = w.Write([]byte(`[{"id":10,"state":"CHANGES_REQUESTED","body":"ajustar erro","user":{"login":"fulano"}},{"id":11,"state":"APPROVED","body":"agora sim","user":{"login":"fulano"}}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/app/pulls/7/comments":
			_, _ = w.Write([]byte(`[{"id":20,"path":"main.go","body":"linha 12","user":{"login":"fulano"}}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/app/pulls/7/requested_reviewers":
			reqRevBody = read(r)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPut && r.URL.Path == "/repos/acme/app/pulls/7/merge":
			mergeBody = read(r)
			_, _ = w.Write([]byte(`{"merged":true}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/comments"):
			commentBody = read(r)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/comments"):
			_, _ = w.Write([]byte(`[{"id":1,"body":"olá","user":{"login":"ana"}}]`))
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
	detail, err := client.GetPullRequest(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Author != "gilvan" || detail.BaseRef != "homolog" || len(detail.Reviews) != 2 {
		t.Fatalf("detail = %+v", detail)
	}
	if err := CheckPRMergeAllowed(detail); err != nil {
		t.Fatalf("expected fulano re-approval to allow merge: %v", err)
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
	if err != nil || len(comments) != 1 || comments[0].Author != "ana" {
		t.Fatalf("comments = %+v %v", comments, err)
	}
	state, err := client.CIStatus(context.Background(), "abc")
	if err != nil || state != "success" {
		t.Fatalf("ci = %s %v", state, err)
	}
	if _, err := client.CIStatus(context.Background(), "../../secret"); err == nil {
		t.Fatal("expected path traversal ref to fail")
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

func TestGHCLI(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "gh")
	if runtime.GOOS == "windows" {
		bin += ".exe"
		t.Skip("script fake do gh é exercitado no Linux/macOS")
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + filepath.Join(dir, "args.txt") + "\"\nif [ \"$1\" = \"pr\" ]; then echo https://github.com/acme/app/pull/3; exit 0; fi\nif [ \"$1\" = \"api\" ]; then echo success; exit 0; fi\necho '{\"comments\":[{\"id\":1,\"body\":\"oi\",\"author\":{\"login\":\"ana\"}}]}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cli := &CLI{Bin: bin, Owner: "acme", Repo: "app"}
	pr, err := cli.CreatePullRequest(context.Background(), CreatePR{Title: "t", Head: "h", Base: "main", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if pr.URL != "https://github.com/acme/app/pull/3" {
		t.Fatalf("url = %s", pr.URL)
	}
	comments, err := cli.ListIssueComments(context.Background(), 3)
	if err != nil || comments[0].Body != "oi" {
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
