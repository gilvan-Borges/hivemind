package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

func TestNonInteractiveEnvPinsCanonicalValues(t *testing.T) {
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	t.Setenv("GCM_INTERACTIVE", "Always")
	t.Setenv("GIT_ASKPASS", `C:\ask.exe`)
	t.Setenv("SSH_ASKPASS", "askpass")

	env := NonInteractiveEnv()
	seen := map[string]string{}
	for _, entry := range env {
		name, val, _ := strings.Cut(entry, "=")
		seen[strings.ToUpper(name)] = val
	}
	if v, ok := seen["GIT_TERMINAL_PROMPT"]; !ok || v != "0" {
		t.Fatalf("GIT_TERMINAL_PROMPT deve ser 0, got %q (present=%v)", v, ok)
	}
	if v, ok := seen["GCM_INTERACTIVE"]; !ok || !strings.EqualFold(v, "Never") {
		t.Fatalf("GCM_INTERACTIVE deve ser Never, got %q (present=%v)", v, ok)
	}
	if v, ok := seen["GIT_ASKPASS"]; !ok || strings.TrimSpace(v) != "" {
		t.Fatalf("GIT_ASKPASS deve estar vazio (askpass neutrado), got %q (present=%v)", v, ok)
	}
	if v, ok := seen["SSH_ASKPASS"]; !ok || strings.TrimSpace(v) != "" {
		t.Fatalf("SSH_ASKPASS deve estar vazio (askpass neutrado), got %q (present=%v)", v, ok)
	}
}

func TestNonInteractiveEnvDropsInheritedPrompters(t *testing.T) {
	t.Setenv("GIT_ASKPASS", `C:\inherited-ask.exe`)
	env := NonInteractiveEnv()
	count := 0
	for _, entry := range env {
		name, val, _ := strings.Cut(entry, "=")
		if strings.ToUpper(name) == "GIT_ASKPASS" {
			count++
			if strings.TrimSpace(val) != "" {
				t.Fatalf("askpass herdado não pode sobreviver: %s", entry)
			}
		}
	}
	if count != 1 {
		t.Fatalf("GIT_ASKPASS deve aparecer exatamente 1 vez (valor vazio), apareceu %d", count)
	}
}

// TestEnsureWorktreeNeverRemovesWorktreeWithGitMetadata simulates a transient
// failure: the worktree directory exists and keeps its .git pointer, but the
// IsRepo probe fails (dangling gitdir). EnsureWorktree must refuse instead of
// wiping a possibly-valid worktree (Spec 08 §3.1).
func TestEnsureWorktreeNeverRemovesWorktreeWithGitMetadata(t *testing.T) {
	repo := gitInit(t)
	g := Local{}
	ctx := context.Background()

	wt := workspace.WorktreePath(repo, "dev_zomb")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	// Dangling .git file: present metadata, but rev-parse fails.
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+filepath.Join(repo, ".git", "worktrees", "nao-existe")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "trabalho.txt"), []byte("conteúdo não descartável\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := g.EnsureWorktree(ctx, repo, "dev_zomb", "hivemind/proj-1/dev_zomb")
	if err == nil {
		t.Fatal("EnsureWorktree deveria recusar diante de worktree com .git e probe falhando")
	}
	if !strings.Contains(err.Error(), "nada foi removido") {
		t.Fatalf("erro deveria explicitar que nada foi removido: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(wt, "trabalho.txt")); statErr != nil {
		t.Fatalf("conteúdo da worktree foi destruído: %v", statErr)
	}
}

// TestEnsureWorktreeCleansDirtyDirWithoutGitMetadata verifies the legitimate
// cleanup path: a plain leftover directory (no .git at all) is removed so the
// worktree can be re-created.
func TestEnsureWorktreeCleansDirtyDirWithoutGitMetadata(t *testing.T) {
	repo := gitInit(t)
	g := Local{}
	ctx := context.Background()

	wt := workspace.WorktreePath(repo, "dev_suja")
	if err := os.MkdirAll(filepath.Join(wt, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "sub", "lixo.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := g.EnsureWorktree(ctx, repo, "dev_suja", "hivemind/proj-1/dev_suja")
	if err != nil {
		t.Fatalf("worktree suja sem .git deveria ser limpa e recriada: %v", err)
	}
	if !g.IsRepo(ctx, got) {
		t.Fatal("worktree recriada não é um repositório")
	}
}

// TestEnsureWorktreeRefusesCancelledContext ensures a cancelled caller can
// never trigger destructive cleanup.
func TestEnsureWorktreeRefusesCancelledContext(t *testing.T) {
	repo := gitInit(t)
	g := Local{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	wt := workspace.WorktreePath(repo, "dev_cancel")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "keep.txt"), []byte("intacto"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := g.EnsureWorktree(ctx, repo, "dev_cancel", "hivemind/proj-1/dev_cancel")
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx cancelado deveria abortar sem destruir, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(wt, "keep.txt")); statErr != nil {
		t.Fatalf("diretório foi destruído com ctx cancelado: %v", statErr)
	}
}
