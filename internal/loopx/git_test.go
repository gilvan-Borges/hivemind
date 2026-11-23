package loopx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitWorktreeCommitSeesEdits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git ausente")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init")
	git("config", "user.email", "dev@example.com")
	git("config", "user.name", "Dev")
	if err := os.WriteFile(filepath.Join(dir, "nota.txt"), []byte("um\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "nota.txt")
	git("commit", "-m", "inicio")

	g := GitWorktree{}
	ctx := context.Background()
	before, _, err := g.Snapshot(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nota.txt"), []byte("dois\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, files, err := g.Snapshot(ctx, dir)
	if err != nil || after == before || files["nota.txt"] == "" {
		t.Fatalf("snapshot: before=%s after=%s files=%v err=%v", before, after, files, err)
	}
	if err := g.EnsureBranch(ctx, dir, "loop/nota"); err != nil {
		t.Fatal(err)
	}
	sha, err := g.Commit(ctx, dir, "loop(nota): iteração 1")
	if err != nil || sha == "" {
		t.Fatalf("commit: %s %v", sha, err)
	}
}
