package jirax

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
}
