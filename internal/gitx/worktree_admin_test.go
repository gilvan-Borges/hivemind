package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWorktreeAdminListArchivePruneAndProgress(t *testing.T) {
	t.Setenv("HIVEMIND_DATA_DIR", t.TempDir())
	repo, g := initCheckpointRepo(t)
	ctx := context.Background()

	var steps []int
	wtPath, err := CreateWorktreeWithProgress(ctx, g, repo, "dev_1", "feat/wt-admin", nil, func(_ string, pct int) {
		steps = append(steps, pct)
	})
	if err != nil {
		t.Fatalf("CreateWorktreeWithProgress failed: %v", err)
	}
	if len(steps) < 3 || steps[len(steps)-1] != 100 {
		t.Fatalf("expected progress reaching 100%%, got %v", steps)
	}

	// Create an orphan dir under WorktreesDir to ensure PruneWorktrees detects & removes it.
	orphanDir := filepath.Join(filepath.Dir(wtPath), "orphan_agent")
	if err := os.MkdirAll(orphanDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphanDir, "note.txt"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := ListWorktrees(ctx, g, repo)
	if err != nil {
		t.Fatalf("ListWorktrees failed: %v", err)
	}
	var sawMain, sawManaged, sawOrphan bool
	for _, w := range list {
		if w.IsMain {
			sawMain = true
		}
		if w.Agent == "dev_1" && !w.Orphan && w.SizeBytes > 0 {
			sawManaged = true
		}
		if w.Agent == "orphan_agent" && w.Orphan {
			sawOrphan = true
		}
	}
	if !sawMain || !sawManaged || !sawOrphan {
		t.Fatalf("unexpected worktree list: main=%v managed=%v orphan=%v (%#v)", sawMain, sawManaged, sawOrphan, list)
	}

	// Archive refuses main repo.
	if err := ArchiveWorktree(ctx, g, repo, repo); err == nil {
		t.Fatal("expected ArchiveWorktree to refuse main repository")
	}

	// Prune removes orphan dir.
	pruneRes, err := PruneWorktrees(ctx, g, repo)
	if err != nil || len(pruneRes.RemovedOrphans) != 1 {
		t.Fatalf("PruneWorktrees failed: %+v err=%v", pruneRes, err)
	}
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Fatal("expected orphan dir deleted by PruneWorktrees")
	}

	// Archive removes active dev_1 worktree cleanly.
	if err := ArchiveWorktree(ctx, g, repo, wtPath); err != nil {
		t.Fatalf("ArchiveWorktree dev_1 failed: %v", err)
	}
}
