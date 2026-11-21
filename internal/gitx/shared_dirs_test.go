package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinkSharedDirectories_LinksCacheAndBlocksSensitive(t *testing.T) {
	mainRepo := t.TempDir()
	wtDir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(mainRepo, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainRepo, "node_modules", "pkg", "index.js"), []byte("module.exports = 42;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mainRepo, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}

	rep, err := LinkSharedDirectories(mainRepo, wtDir, []string{"node_modules", ".ssh", "../bad", "absent_cache"})
	if err != nil {
		t.Fatalf("LinkSharedDirectories failed: %v", err)
	}
	if len(rep.Linked) != 1 || rep.Linked[0] != "node_modules" {
		t.Fatalf("expected node_modules linked, got %#v", rep.Linked)
	}
	if len(rep.Blocked) != 2 {
		t.Fatalf("expected 2 blocked entries (.ssh, ../bad), got %#v", rep.Blocked)
	}
	if len(rep.Skipped) != 1 || rep.Skipped[0] != "absent_cache" {
		t.Fatalf("expected absent_cache skipped, got %#v", rep.Skipped)
	}
	if len(rep.Warnings) == 0 {
		t.Fatal("expected destructive cache warning when linking shared directory")
	}

	got, err := os.ReadFile(filepath.Join(wtDir, "node_modules", "pkg", "index.js"))
	if err != nil || string(got) != "module.exports = 42;\n" {
		t.Fatalf("expected linked node_modules content readable inside worktree, got %q err=%v", string(got), err)
	}
}
