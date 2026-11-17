package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntegrateDoesNotSwitchCheckoutOrClaimEmpty(t *testing.T) {
	repo := gitInit(t)
	g := Local{}
	ctx := context.Background()
	before, err := g.CurrentBranch(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := g.EnsureWorktree(ctx, repo, "dev_vazio", "hivemind/vazio/dev_vazio")
	if err != nil {
		t.Fatal(err)
	}
	merged, integration, err := g.Integrate(ctx, repo, []string{"hivemind/vazio/dev_vazio"})
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 0 {
		t.Fatalf("branch vazia não pode ser integrada: %v", merged)
	}
	if integration == "" || integration == repo {
		t.Fatalf("worktree de integração = %q", integration)
	}
	after, err := g.CurrentBranch(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("checkout mudou de %s para %s", before, after)
	}
	if _, err := os.Stat(filepath.Join(wt, "README.md")); err != nil {
		t.Fatal(err)
	}
	log, err := g.Run(ctx, integration, "log", "--oneline")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(log), "mesclad") {
		t.Fatalf("log não deve dizer mesclado: %s", log)
	}
	if err := EnsureHiveIgnore(repo); err != nil {
		t.Fatal(err)
	}
	ignore, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil || !strings.Contains(string(ignore), ".hivemind/") {
		t.Fatalf("gitignore = %s %v", ignore, err)
	}
}
