package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func initTestGitRepo(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	g := Local{}
	ctx := context.Background()
	if _, err := g.Run(ctx, dir, "init", "-b", "main"); err != nil {
		t.Fatalf("git init %s: %v", dir, err)
	}
	_, _ = g.Run(ctx, dir, "config", "user.email", "test@hivemind.local")
	_, _ = g.Run(ctx, dir, "config", "user.name", "HiveMind Test")
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.Run(ctx, dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(ctx, dir, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
}

func TestCompositeNestedWorktreeLifecycle(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	root := t.TempDir()
	initTestGitRepo(t, root, map[string]string{
		".gitignore":     "ipg-mobile/\n",
		"api/Main.java":  "class Main {}\n",
		"README.md":      "root\n",
	})
	// Nested repo 1: explicitly gitignored (like ipg-mobile inside ipg).
	mobileDir := filepath.Join(root, "ipg-mobile")
	initTestGitRepo(t, mobileDir, map[string]string{
		"lib/app.dart": "void main() {}\n",
	})
	// Nested repo 2: NOT in .gitignore (like an unignored subproject), must stay isolated via info/exclude.
	otherDir := filepath.Join(root, "sub-app")
	initTestGitRepo(t, otherDir, map[string]string{
		"App.kt": "fun main() {}\n",
	})

	ctx := context.Background()
	g := Local{}

	discovered := DiscoverNestedRepos(ctx, g, root)
	if len(discovered) != 2 {
		t.Fatalf("DiscoverNestedRepos = %d, want 2 (%+v)", len(discovered), discovered)
	}

	parentWT, effBranch, err := PrepareDeliveryWorktreeForSlot(ctx, g, root, "lane-principal", "hivemind/ipg-929", false)
	if err != nil {
		t.Fatalf("PrepareDeliveryWorktreeForSlot: %v", err)
	}
	nestedWTs, err := PrepareNestedWorktrees(ctx, g, root, parentWT, effBranch, false)
	if err != nil {
		t.Fatalf("PrepareNestedWorktrees: %v", err)
	}
	if len(nestedWTs) != 2 {
		t.Fatalf("PrepareNestedWorktrees = %d, want 2", len(nestedWTs))
	}

	// Verify both nested repos exist physically inside parentWT on isolated branches.
	mountedDart := filepath.Join(parentWT, "ipg-mobile", "lib", "app.dart")
	if _, err := os.Stat(mountedDart); err != nil {
		t.Fatalf("expected nested file inside parent worktree at %s: %v", mountedDart, err)
	}

	// Verify editing nested repo inside parentWT does NOT dirty parent repo git status!
	if err := os.WriteFile(mountedDart, []byte("void main() { print(1); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentWT, "sub-app", "App.kt"), []byte("fun main() { println(1) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parentStatus, err := g.Status(ctx, parentWT)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(parentStatus) != "" {
		t.Fatalf("parent worktree status polluted by nested repo edits: %q", parentStatus)
	}

	// Re-running PrepareDeliveryWorktreeForSlot + PrepareNestedWorktrees for the same slot preserves WIP!
	parentWT2, _, err := PrepareDeliveryWorktreeForSlot(ctx, g, root, "lane-principal", "hivemind/ipg-929", false)
	if err != nil || parentWT2 != parentWT {
		t.Fatalf("reused parentWT = %q (%v), want %q", parentWT2, err, parentWT)
	}
	nestedWTs2, err := PrepareNestedWorktrees(ctx, g, root, parentWT2, effBranch, false)
	if err != nil || len(nestedWTs2) != 2 {
		t.Fatalf("reused PrepareNestedWorktrees: %v (%d)", err, len(nestedWTs2))
	}
	afterBytes, err := os.ReadFile(mountedDart)
	if err != nil || !strings.Contains(string(afterBytes), "print(1)") {
		t.Fatalf("expected nested WIP edit to survive turn reuse, got %q (%v)", string(afterBytes), err)
	}

	// Verify SyncCompositeUncommittedToDelivery copies nested edits between parallel dev worktree and delivery worktree.
	devWT, err := AlignDeveloperWorktree(ctx, g, root, "dev_1", "hivemind/dev_1-test", effBranch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AlignNestedWorktrees(ctx, g, devWT, "hivemind/dev_1-test", nestedWTs); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devWT, "ipg-mobile", "lib", "app.dart"), []byte("void main() { print(2); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	synced, err := SyncCompositeUncommittedToDelivery(ctx, g, []string{devWT}, parentWT, []string{"ipg-mobile", "sub-app"})
	if err != nil {
		t.Fatal(err)
	}
	if len(synced) == 0 || synced[0] != "ipg-mobile/lib/app.dart" {
		t.Fatalf("expected ipg-mobile/lib/app.dart synced, got %v", synced)
	}
	consolidated, _ := os.ReadFile(mountedDart)
	if !strings.Contains(string(consolidated), "print(2)") {
		t.Fatalf("delivery worktree nested file not updated: %q", string(consolidated))
	}

	// Commit inside nested worktree and verify HasCommitsAhead.
	committed, err := g.CommitAll(ctx, filepath.Join(parentWT, "ipg-mobile"), "feat: mobile change")
	if err != nil || !committed {
		t.Fatalf("CommitAll in nested worktree failed: committed=%v err=%v", committed, err)
	}
	if !HasCommitsAhead(ctx, g, mobileDir, nestedWTs[0].Branch, "main") {
		t.Fatalf("expected nested branch %s to have commits ahead of main", nestedWTs[0].Branch)
	}
	if HasCommitsAhead(ctx, g, root, effBranch, "main") {
		t.Fatalf("root branch %s should have 0 commits ahead of main", effBranch)
	}

	// Removing parent worktree cleans up nested worktrees cleanly first.
	RemoveWorktree(ctx, g, root, devWT, "hivemind/dev_1-test")
	RemoveWorktree(ctx, g, root, parentWT, effBranch)
	if _, err := os.Stat(parentWT); !os.IsNotExist(err) {
		t.Fatalf("expected parentWT %s to be removed", parentWT)
	}
}
