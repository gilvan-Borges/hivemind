package tools

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestArchitectReadOnlyFilesOmitsWriteAndEdit(t *testing.T) {
	dir := t.TempDir()
	roTools, err := ForRole(team.RoleArchitect, Deps{
		Root:          dir,
		Settings:      team.DefaultSettings(),
		ReadOnlyFiles: true,
	})
	if err != nil {
		t.Fatalf("ForRole ReadOnlyFiles=true failed: %v", err)
	}
	seenRO := map[string]bool{}
	for _, tl := range roTools {
		seenRO[tl.Name()] = true
	}
	if seenRO["write_file"] || seenRO["edit_file"] {
		t.Fatalf("ReadOnlyFiles=true must omit write_file and edit_file, got %v", seenRO)
	}
	for _, want := range []string{"read_file", "search_text", "run_shell"} {
		if !seenRO[want] {
			t.Fatalf("ReadOnlyFiles=true must keep %s", want)
		}
	}

	rwTools, err := ForRole(team.RoleArchitect, Deps{
		Root:          dir,
		Settings:      team.DefaultSettings(),
		ReadOnlyFiles: false,
	})
	if err != nil {
		t.Fatalf("ForRole ReadOnlyFiles=false failed: %v", err)
	}
	seenRW := map[string]bool{}
	for _, tl := range rwTools {
		seenRW[tl.Name()] = true
	}
	if !seenRW["write_file"] || !seenRW["edit_file"] {
		t.Fatalf("ReadOnlyFiles=false must keep write_file and edit_file, got %v", seenRW)
	}
}

func TestDecorateMissingToolErrorIncludesDiagnostic(t *testing.T) {
	err := decorateMissingToolError("flutter test foo_test.dart", exec.ErrNotFound)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "[DIAGNÓSTICO DE AMBIENTE]") || !strings.Contains(msg, "https://docs.flutter.dev") {
		t.Fatalf("unexpected diagnostic error: %s", msg)
	}
}
