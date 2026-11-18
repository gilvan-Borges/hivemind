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

func TestSyncUncommittedToDeliveryWithoutDevCommit(t *testing.T) {
	repo := gitInit(t)
	g := Local{}
	ctx := context.Background()

	deliveryWT, deliveryBranch, err := PrepareDeliveryWorktree(ctx, g, repo, "hivemind/proj-9/entrega", false)
	if err != nil {
		t.Fatal(err)
	}
	if deliveryBranch != "hivemind/proj-9/entrega" {
		t.Fatalf("deliveryBranch = %q", deliveryBranch)
	}
	devWT, err := AlignDeveloperWorktree(ctx, g, repo, "dev_1", "hivemind/proj-9/dev_1", deliveryBranch)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devWT, "feature.txt"), []byte("sem commit do dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := SyncUncommittedToDelivery(ctx, g, []string{devWT}, deliveryWT)
	if err != nil || len(files) != 1 || files[0] != "feature.txt" {
		t.Fatalf("synced = %v err=%v", files, err)
	}
	if _, err := os.Stat(filepath.Join(deliveryWT, "feature.txt")); err != nil {
		t.Fatalf("expected feature.txt in deliveryWT: %v", err)
	}
	// Verify dev worktree still has 0 extra commits beyond base!
	cnt, err := g.Run(ctx, devWT, "rev-list", "--count", "HEAD")
	if err != nil || strings.TrimSpace(cnt) != "1" {
		t.Fatalf("dev worktree should not have committed: count=%q err=%v", cnt, err)
	}
}
