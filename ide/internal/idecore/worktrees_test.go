package idecore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
)

func TestIDEWorktreesListArchivePruneAndRollback(t *testing.T) {
	t.Setenv("HIVEMIND_DATA_DIR", t.TempDir())
	repo := t.TempDir()
	g := gitx.Local{}
	ctx := context.Background()
	for _, args := range [][]string{
		{"init"},
		{"config", "core.autocrlf", "false"},
		{"config", "user.email", "hivemind@example.com"},
		{"config", "user.name", "HiveMind"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := g.Run(ctx, repo, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(ctx, repo, "add", "main.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(ctx, repo, "commit", "-m", "init"); err != nil {
		t.Fatal(err)
	}

	var pcts []int
	wt, err := CreateWorktreeAsync(ctx, repo, "dev_1", "feat/ide-wt", func(_ string, pct int) {
		pcts = append(pcts, pct)
	})
	if err != nil {
		t.Fatalf("CreateWorktreeAsync failed: %v", err)
	}
	if len(pcts) == 0 || pcts[len(pcts)-1] != 100 {
		t.Fatalf("expected 100%% progress, got %v", pcts)
	}

	list, err := ListWorktrees(ctx, repo)
	if err != nil || len(list) < 2 {
		t.Fatalf("ListWorktrees unexpected: %#v err=%v", list, err)
	}

	cp, err := gitx.CreateCheckpoint(ctx, g, wt, "turn-1", "antes dev")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(wt, "main.go"), []byte("CORRUPTED\n"), 0o644)
	cps, err := ListWorktreeCheckpoints(ctx, repo)
	if err != nil || len(cps) == 0 {
		t.Fatalf("ListWorktreeCheckpoints failed: %#v err=%v", cps, err)
	}
	if err := RollbackWorktreeCheckpoint(ctx, repo, wt, cp.ID); err != nil {
		t.Fatalf("RollbackWorktreeCheckpoint failed: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(wt, "main.go"))
	if strings.ReplaceAll(string(got), "\r\n", "\n") != "package main\nfunc main() {}\n" {
		t.Fatalf("expected rollback to restore main.go, got %q", string(got))
	}
	if err := ArchiveWorktree(ctx, repo, wt); err != nil {
		t.Fatalf("ArchiveWorktree failed: %v", err)
	}
}
