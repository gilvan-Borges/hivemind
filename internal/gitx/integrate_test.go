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

// TestAlignDeveloperWorktreeDiscardsStaleEdits reproduces dogfooding achado #11:
// a tracked file left dirty (uncommitted) by a previous run must NOT survive the
// alignment of the next run — otherwise it leaks into the new delivery.
func TestAlignDeveloperWorktreeDiscardsStaleEdits(t *testing.T) {
	repo := gitInit(t)
	g := Local{}
	ctx := context.Background()

	first, deliveryBranch, err := PrepareDeliveryWorktree(ctx, g, repo, "hivemind/run-1/entrega", false)
	if err != nil {
		t.Fatal(err)
	}
	_ = first
	devWT, err := AlignDeveloperWorktree(ctx, g, repo, "dev_1", "hivemind/run-1/dev_1", deliveryBranch)
	if err != nil {
		t.Fatal(err)
	}

	// Run 1 leaves a TRACKED file dirty and an untracked leftover behind.
	readme := filepath.Join(devWT, "README.md")
	if err := os.WriteFile(readme, []byte("versão suja do run 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devWT, "sobrando.txt"), []byte("lixo"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run 2 aligns the same dev worktree to a fresh delivery branch.
	_, run2Branch, err := PrepareDeliveryWorktree(ctx, g, repo, "hivemind/run-2/entrega", false)
	if err != nil {
		t.Fatal(err)
	}
	devWT2, err := AlignDeveloperWorktree(ctx, g, repo, "dev_1", "hivemind/run-2/dev_1", run2Branch)
	if err != nil {
		t.Fatal(err)
	}
	if devWT2 != devWT {
		t.Fatalf("worktree mudou de path: %q -> %q", devWT, devWT2)
	}

	status, err := g.Run(ctx, devWT, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(status) != "" {
		t.Fatalf("worktree de dev deve estar limpa entre runs; status = %q", status)
	}
	data, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "run 1") {
		t.Fatalf("edição suja do run anterior vazou para o novo run: %q", data)
	}
	if _, err := os.Stat(filepath.Join(devWT, "sobrando.txt")); err == nil {
		t.Fatal("arquivo não rastreado do run anterior deveria ter sido removido")
	}
}
