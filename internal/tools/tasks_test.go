package tools

import (
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestForRoleExposesUpdateTasksWhenCallbackConfigured(t *testing.T) {
	var captured []TaskItem
	tl, err := ForRole(team.RoleArchitect, Deps{
		Root: t.TempDir(),
		OnTasks: func(tasks []TaskItem) {
			captured = append([]TaskItem(nil), tasks...)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range tl {
		if item.Name() == "update_tasks" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("esperava tool update_tasks exposta quando OnTasks != nil")
	}

	without, err := ForRole(team.RoleArchitect, Deps{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range without {
		if item.Name() == "update_tasks" {
			t.Fatalf("não deveria expor update_tasks quando OnTasks == nil")
		}
	}
	_ = captured
}

func TestNormalizeTaskStatus(t *testing.T) {
	cases := map[string]string{
		"pending":     "pending",
		"in_progress": "in_progress",
		"running":     "in_progress",
		"done":        "done",
		"completed":   "done",
		"failed":      "failed",
		"skipped":     "skipped",
	}
	for in, want := range cases {
		got, ok := normalizeTaskStatus(in)
		if !ok || got != want {
			t.Fatalf("normalizeTaskStatus(%q) = (%q, %v), want (%q, true)", in, got, ok, want)
		}
	}
	if _, ok := normalizeTaskStatus("unknown"); ok {
		t.Fatal("esperava rejeição para status desconhecido")
	}
}

func TestForRoleLeanReviewToolsOmitsCreatePR(t *testing.T) {
	tl, err := ForRole(team.RoleArchitect, Deps{
		Root:            t.TempDir(),
		LeanReviewTools: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range tl {
		n := item.Name()
		if n == "github_create_pr" || n == "jira_get" || n == "jira_create_issue" {
			t.Fatalf("LeanReviewTools=true não deveria expor %s", n)
		}
	}
}
