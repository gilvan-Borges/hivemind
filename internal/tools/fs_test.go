package tools

import (
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestRoleToolSets(t *testing.T) {
	root := t.TempDir()
	devs, err := ForRole(team.RoleDeveloper, Deps{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range devs {
		names[tl.Name()] = true
	}
	for _, want := range []string{"read_file", "list_dir", "write_file", "run_shell", "git_status", "git_diff"} {
		if !names[want] {
			t.Fatalf("desenvolvedor sem %s em %v", want, names)
		}
	}
	if names["git_commit"] || names["git_push"] {
		t.Fatalf("desenvolvedor não deve ter git_commit/git_push (exclusivos do Chefe/Arquiteto): %v", names)
	}
	if names["qa_approve"] {
		t.Fatal("desenvolvedor não aprova QA")
	}
	qa, err := ForRole(team.RoleQA, Deps{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	var hasApprove, hasWrite bool
	for _, tl := range qa {
		if tl.Name() == "qa_approve" {
			hasApprove = true
		}
		if tl.Name() == "write_file" {
			hasWrite = true
		}
	}
	if !hasApprove || hasWrite {
		t.Fatalf("QA approve=%v write=%v", hasApprove, hasWrite)
	}
	pm, err := ForRole(team.RoleProjectManager, Deps{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range pm {
		if strings.HasPrefix(tl.Name(), "jira_") {
			t.Fatalf("Jira desativado não deveria expor %s", tl.Name())
		}
	}
}
