package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitInit(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não está instalado")
	}
	dir := t.TempDir()
	g := Local{}
	ctx := context.Background()
	must(t, g, ctx, dir, "init")
	must(t, g, ctx, dir, "config", "user.email", "hivemind@example.com")
	must(t, g, ctx, dir, "config", "user.name", "HiveMind Test")
	must(t, g, ctx, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("oi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, g, ctx, dir, "add", "README.md")
	must(t, g, ctx, dir, "commit", "-m", "inicial")
	return dir
}

func must(t *testing.T, g Local, ctx context.Context, dir string, args ...string) {
	t.Helper()
	if _, err := g.Run(ctx, dir, args...); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeAndCommit(t *testing.T) {
	repo := gitInit(t)
	g := Local{}
	ctx := context.Background()
	wt, err := g.EnsureWorktree(ctx, repo, "dev_1", "hivemind/proj-1/dev_1")
	if err != nil {
		t.Fatal(err)
	}
	if !g.IsRepo(ctx, wt) {
		t.Fatal("worktree não é um repositório")
	}
	again, err := g.EnsureWorktree(ctx, repo, "dev_1", "hivemind/proj-1/dev_1")
	if err != nil || again != wt {
		t.Fatalf("segunda chamada: %s %v", again, err)
	}
	if err := os.WriteFile(filepath.Join(wt, "hello.txt"), []byte("olá\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err := g.CommitAll(ctx, wt, "feat(PROJ-1): hello")
	if err != nil || !ok {
		t.Fatalf("commit: %v %v", ok, err)
	}
	log, err := g.Run(ctx, wt, "log", "-1", "--pretty=%s")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "PROJ-1") {
		t.Fatalf("log = %q", log)
	}
	clean, err := g.CommitAll(ctx, wt, "nada")
	if err != nil || clean {
		t.Fatalf("árvore limpa deveria ignorar o commit: %v %v", clean, err)
	}
	branch, err := g.Run(ctx, wt, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(branch) != "hivemind/proj-1/dev_1" {
		t.Fatalf("branch = %q", branch)
	}
}
