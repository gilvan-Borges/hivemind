package tools

import (
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestQAApproveRejectsApprovalWithoutEvidence(t *testing.T) {
	err := validateQAApproval(qaApproveArgs{Note: "testes ok"})
	if err == nil {
		t.Fatal("approval without any evidence must be rejected")
	}
	for _, want := range []string{"command", "exit_code", "evidence", "pure_docs_justification"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("rejection should mention %q, got: %v", want, err)
		}
	}
}

func TestQAApproveAcceptsCommandEvidence(t *testing.T) {
	err := validateQAApproval(qaApproveArgs{
		Command:  "go test ./internal/tools/...",
		ExitCode: 0,
		Evidence: "ok  github.com/gilvan-Borges/HiveMind/internal/tools  0.6s",
		Note:     "todos os testes passaram",
	})
	if err != nil {
		t.Fatalf("valid command evidence must pass: %v", err)
	}
}

func TestQAApproveRejectsNonZeroExitCode(t *testing.T) {
	err := validateQAApproval(qaApproveArgs{
		Command:  "go test ./...",
		ExitCode: 1,
		Evidence: "FAIL  ./internal/gitx  exit status 1",
	})
	if err == nil {
		t.Fatal("non-zero exit code must be rejected")
	}
	if !strings.Contains(err.Error(), "exit_code=1") {
		t.Fatalf("rejection should cite the exit code, got: %v", err)
	}
}

func TestQAApproveRejectsCommandWithoutEvidenceExcerpt(t *testing.T) {
	err := validateQAApproval(qaApproveArgs{
		Command:  "go test ./...",
		ExitCode: 0,
	})
	if err == nil {
		t.Fatal("command without output evidence must be rejected")
	}
	if !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("rejection should mention evidence, got: %v", err)
	}
}

func TestQAApprovePureDocsRequiresExplicitJustification(t *testing.T) {
	short := validateQAApproval(qaApproveArgs{PureDocsJustification: "docs"})
	if short == nil {
		t.Fatal("short pure-docs justification must be rejected")
	}

	valid := validateQAApproval(qaApproveArgs{
		PureDocsJustification: "entrega pure-docs: apenas spec em docs/specs/, sem código executável no repositório para validar",
		Note:                  "spec revisada",
	})
	if valid != nil {
		t.Fatalf("explicit pure-docs justification must pass: %v", valid)
	}
}

func TestQAApproveRejectsCommandAndPureDocsTogether(t *testing.T) {
	err := validateQAApproval(qaApproveArgs{
		Command:               "go test ./...",
		ExitCode:              0,
		Evidence:              "ok",
		PureDocsJustification: "entrega pure-docs sem nada executável no repositório",
	})
	if err == nil {
		t.Fatal("command evidence and pure-docs justification together must be rejected")
	}
}

func TestQARoleCatalogCarriesApproveAndChanges(t *testing.T) {
	deps := Deps{Root: t.TempDir(), Settings: team.DefaultSettings()}
	list, err := ForRole(team.RoleQA, deps)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range list {
		names[tl.Name()] = true
	}
	if !names["qa_approve"] || !names["qa_request_changes"] {
		t.Fatalf("QA catalog must keep qa_approve and qa_request_changes, got %v", names)
	}
}
