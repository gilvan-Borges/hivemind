package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveWorktreeDropsDraftBranchAndKeepsSymlinkTarget(t *testing.T) {
	t.Setenv("HIVEMIND_DATA_DIR", t.TempDir())
	repo, g := initCheckpointRepo(t)
	ctx := context.Background()

	wt, err := g.EnsureWorktree(ctx, repo, "dev_1", "hivemind/dev_1-close")
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "keep.txt"), []byte("stay\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(wt, "shared")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink não disponível neste host: %v", err)
	}

	RemoveWorktree(ctx, g, repo, wt, "hivemind/dev_1-close")

	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree deveria sumir, stat=%v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "keep.txt")); err != nil {
		t.Fatalf("alvo do symlink foi afetado: %v", err)
	}
	if g.BranchExists(ctx, repo, "hivemind/dev_1-close") {
		t.Fatal("branch de rascunho deveria ser apagada depois que a worktree saiu")
	}
}

func TestEnsureWorktreeRebuildsBrokenGitdir(t *testing.T) {
	t.Setenv("HIVEMIND_DATA_DIR", t.TempDir())
	repo, g := initCheckpointRepo(t)
	ctx := context.Background()

	wt, err := g.EnsureWorktree(ctx, repo, "dev_broken", "hivemind/dev_broken")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /no/such/gitdir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if g.IsRepo(ctx, wt) {
		t.Fatal("gitdir quebrado não deveria contar como repositório")
	}

	again, err := g.EnsureWorktree(ctx, repo, "dev_broken", "hivemind/dev_broken")
	if err != nil {
		t.Fatal(err)
	}
	if again != wt {
		t.Fatalf("path mudou: %s vs %s", again, wt)
	}
	if !g.IsRepo(ctx, again) {
		t.Fatal("worktree quebrada não foi recriada")
	}
}
