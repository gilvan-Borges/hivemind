package githubx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestParseRemoteForHostGHES(t *testing.T) {
	cases := []struct{ in, owner, repo string }{
		{"git@git.empresa.local:org/app.git", "org", "app"},
		{"https://ghe.corp/org/app.git", "org", "app"},
		{"https://ghe.corp:8443/org/app", "org", "app"},
		{"ssh://git@ghe.corp:2222/org/app", "org", "app"},
		{"https://x-access-token:abc@ghe.corp/org/app", "org", "app"},
		{"git@github.com:acme/app.git", "acme", "app"},
		{"https://github.com/acme/app", "acme", "app"},
	}
	for _, tc := range cases {
		o, r, ok := ParseRemoteForHost(tc.in, "github.com", "ghe.corp", "git.empresa.local")
		if !ok || o != tc.owner || r != tc.repo {
			t.Fatalf("%s -> (%s, %s, %v), esperava (%s, %s, true)", tc.in, o, r, ok, tc.owner, tc.repo)
		}
	}
}

func TestParseRemoteForHostRejectsUnknownHost(t *testing.T) {
	if _, _, ok := ParseRemoteForHost("https://gitlab.com/a/b", "github.com", "ghe.corp"); ok {
		t.Fatal("host não configurado não pode casar")
	}
	if _, _, ok := ParseRemoteForHost("git@ghe.corp:org/app.git", "github.com"); ok {
		t.Fatal("GHES não pode casar quando apenas github.com é permitido")
	}
}

func TestParseRemoteForHostTrailingDotAndCase(t *testing.T) {
	o, r, ok := ParseRemoteForHost("git@GHE.CORP:org/app.git", "ghe.corp")
	if !ok || o != "org" || r != "app" {
		t.Fatalf("case-insensitive host: %v %s %s", ok, o, r)
	}
}

func TestHostFromBaseURLDerivesGHESHost(t *testing.T) {
	if got := hostFromBaseURL("https://api.github.com"); got != "" {
		t.Fatalf("api.github.com não deve gerar host extra: %q", got)
	}
	if got := hostFromBaseURL("https://ghe.empresa.com/api/v3/"); got != "ghe.empresa.com" {
		t.Fatalf("esperava ghe.empresa.com, got %q", got)
	}
}

func TestParseLinkNext(t *testing.T) {
	header := `<https://api.github.com/repos/acme/app/pulls?page=2>; rel="next", <https://api.github.com/repos/acme/app/pulls?page=5>; rel="last"`
	next, ok := ParseLinkNext(header)
	if !ok || next != "https://api.github.com/repos/acme/app/pulls?page=2" {
		t.Fatalf("next=%q ok=%v", next, ok)
	}
	if _, ok := ParseLinkNext(`<https://api.github.com/repos/acme/app/pulls?page=1>; rel="prev"`); ok {
		t.Fatal("sem rel=next deveria retornar false")
	}
	if _, ok := ParseLinkNext(""); ok {
		t.Fatal("header vazio deveria retornar false")
	}
}

// TestRESTListPullRequestsFollowsLinkPages builds a fake repo with 130 open
// PRs served in pages of 100 and asserts the connector returns all of them.
func TestRESTListPullRequestsFollowsLinkPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/app/pulls", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "", "1":
			page := fakePRPage(1, 100)
			w.Header().Set("Link", `<http://`+r.Host+`/repos/acme/app/pulls?state=open&per_page=100&page=2>; rel="next"`)
			_ = json.NewEncoder(w).Encode(page)
		case "2":
			_ = json.NewEncoder(w).Encode(fakePRPage(101, 30))
		default:
			http.Error(w, "bad page", http.StatusBadRequest)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &REST{BaseURL: srv.URL, Token: "tok", Owner: "acme", Repo: "app"}
	prs, err := client.ListPullRequests(context.Background(), "open", 250)
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 130 {
		t.Fatalf("esperava 130 PRs paginados via Link, veio %d", len(prs))
	}
	if prs[0].Number != 1 || prs[129].Number != 130 {
		t.Fatalf("ordem incorreta: primeiro=%d último=%d", prs[0].Number, prs[129].Number)
	}
}

// TestRESTListPullRequestFilesFollowsLinkPages asserts >100 files come back.
func TestRESTListPullRequestFilesFollowsLinkPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/app/pulls/7/files", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "" {
			files := make([]map[string]any, 100)
			for i := range files {
				files[i] = map[string]any{"filename": fmt.Sprintf("f%d.go", i), "status": "modified", "additions": 1, "deletions": 1}
			}
			w.Header().Set("Link", `<http://`+r.Host+`/repos/acme/app/pulls/7/files?per_page=100&page=2>; rel="next"`)
			_ = json.NewEncoder(w).Encode(files)
			return
		}
		files := make([]map[string]any, 45)
		for i := range files {
			files[i] = map[string]any{"filename": fmt.Sprintf("g%d.go", i), "status": "added", "additions": 2, "deletions": 0}
		}
		_ = json.NewEncoder(w).Encode(files)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &REST{BaseURL: srv.URL, Token: "tok", Owner: "acme", Repo: "app"}
	files, err := client.ListPullRequestFiles(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 145 {
		t.Fatalf("esperava 145 arquivos paginados via Link, veio %d", len(files))
	}
}

func fakePRPage(start, count int) []map[string]any {
	out := make([]map[string]any, 0, count)
	for i := 0; i < count; i++ {
		n := start + i
		out = append(out, map[string]any{
			"number":   n,
			"title":    "PR " + strconv.Itoa(n),
			"html_url": "https://github.com/acme/app/pull/" + strconv.Itoa(n),
			"state":    "open",
			"user":     map[string]string{"login": "dev"},
			"head":     map[string]string{"ref": "b" + strconv.Itoa(n)},
			"base":     map[string]string{"ref": "main"},
		})
	}
	return out
}

func TestSharedHTTPClientIsSingleton(t *testing.T) {
	if SharedHTTPClient() != SharedHTTPClient() {
		t.Fatal("SharedHTTPClient deve devolver sempre a mesma instância")
	}
	if strings.TrimSpace(fmt.Sprint(SharedHTTPClient().Timeout)) == "0s" {
		t.Fatal("cliente compartilhado sem timeout")
	}
}
