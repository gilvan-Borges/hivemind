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
	var prBody, commentBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("auth = %s", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/app/pulls":
			prBody = read(r)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number":7,"html_url":"https://github.com/acme/app/pull/7","title":"PROJ-1: oi"}`))
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
	client := &REST{BaseURL: srv.URL, Token: "token", Owner: "acme", Repo: "app", HTTP: srv.Client()}
	pr, err := client.CreatePullRequest(context.Background(), CreatePR{Title: "PROJ-1: oi", Head: "hivemind/proj-1/dev_1", Base: "main", Body: "pronto"})
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 7 || !strings.Contains(pr.URL, "/pull/7") {
		t.Fatalf("pr = %+v", pr)
	}
	if !strings.Contains(prBody, "hivemind/proj-1/dev_1") {
		t.Fatalf("body = %s", prBody)
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
