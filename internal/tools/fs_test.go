package tools

import (
	"bytes"
	"os"
	"path/filepath"
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
	for _, want := range []string{"read_file", "list_dir", "write_file", "edit_file", "run_shell", "git_status", "git_diff"} {
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


func TestDocsPathAllowed(t *testing.T) {
	allowed := []string{
		"specs/01-spec.md",
		"specs/05-tarefas.md",
		"docs/adr/0001-stack.md",
		".specify/notes.md",
		"README.md",
		"readme.md",
		"docs/ARCHITECTURE.md",
	}
	for _, p := range allowed {
		if !docsPathAllowed(p) {
			t.Errorf("docsPathAllowed(%q) = false, queria true", p)
		}
	}
	blocked := []string{
		"cmd/app/main.go",
		"src/index.ts",
		"internal/tools/tools.go",
		"docs/adr/0001-stack.md.exe",
		"specs/Makefile",
		"README.txt",
		"../fora/do/workspace.md",
		"./cmd/../cmd/x.md",
		"",
	}
	for _, p := range blocked {
		if docsPathAllowed(p) {
			t.Errorf("docsPathAllowed(%q) = true, queria false", p)
		}
	}
}

func TestChiefDocsOnlyToolSet(t *testing.T) {
	root := t.TempDir()
	chief, err := ForRole(team.RoleProjectManager, Deps{Root: root, DisableGitWrite: true, DocsOnlyWrite: true})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range chief {
		names[tl.Name()] = true
	}
	if !names["write_doc"] {
		t.Fatalf("Chefe em fase SDD deveria ter write_doc: %v", names)
	}
	if names["write_file"] {
		t.Fatalf("Chefe em fase SDD n�o deveria ter write_file (c�digo bloqueado): %v", names)
	}
	if names["git_commit"] || names["git_push"] {
		t.Fatalf("Chefe em fase SDD n�o deveria ter git_commit/git_push: %v", names)
	}
}


func TestEditFileSurgicalPreservesBytesAndCRLF(t *testing.T) {
	root := t.TempDir()
	// Simula um .gitignore Windows (CRLF) com bytes Latin-1 / acentos na linha final que NÃO devem ser tocados.
	untouchedTail := []byte("# Se\xe7\xe3o legada \x97 n\xe3o alterar\r\n*.log\r\n")
	orig := append([]byte("# Environment / secrets\r\n.env\r\n\r\n"), untouchedTail...)
	giPath := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(giPath, orig, 0o644); err != nil {
		t.Fatal(err)
	}

	// O modelo envia LF (\n) no old_text/new_text, como é padrão de LLMs.
	msg, err := editFileSurgical(root, editArgs{
		Path:    ".gitignore",
		OldText: "# Environment / secrets\n.env",
		NewText: "# Environment / secrets\n.env\n# Credenciais locais MCP\nmcp_config.json",
	})
	if err != nil {
		t.Fatalf("editFileSurgical falhou: %v", err)
	}
	if !strings.Contains(msg, "linha L1") || !strings.Contains(msg, "+2") {
		t.Fatalf("mensagem inesperada: %s", msg)
	}

	got, err := os.ReadFile(giPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(got, untouchedTail) {
		t.Fatalf("edit_file alterou bytes não relacionados no fim do arquivo!\nesperado sufixo: %x\nobtido: %x", untouchedTail, got)
	}
	if !bytes.Contains(got, []byte(".env\r\n# Credenciais locais MCP\r\nmcp_config.json\r\n")) {
		t.Fatalf("edit_file não preservou CRLF no trecho inserido: %q", string(got))
	}
}

func TestEditFileSurgicalErrorsAndReplaceAll(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "app.txt")
	if err := os.WriteFile(p, []byte("foo\nbar\nfoo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := editFileSurgical(root, editArgs{Path: "app.txt", OldText: "nao_existe", NewText: "x"}); err == nil {
		t.Fatal("esperava erro quando old_text não existe")
	}
	if _, err := editFileSurgical(root, editArgs{Path: "app.txt", OldText: "foo", NewText: "baz"}); err == nil || !strings.Contains(err.Error(), "L1, L3") {
		t.Fatalf("esperava erro de ambiguidade citando L1, L3; obtido: %v", err)
	}
	if _, err := editFileSurgical(root, editArgs{Path: "app.txt", OldText: "foo", NewText: "baz", ReplaceAll: true}); err != nil {
		t.Fatalf("replace_all deveria suceder: %v", err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "baz\nbar\nbaz\n" {
		t.Fatalf("conteúdo inesperado: %q", string(got))
	}
	if _, err := editFileSurgical(root, editArgs{Path: "../fora.txt", OldText: "a", NewText: "b"}); err == nil {
		t.Fatal("esperava bloqueio de path traversal")
	}
}
