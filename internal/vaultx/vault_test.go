package vaultx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateWorktreeVaultAndGitExclusion(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não instalado")
	}
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.name", "Teste")
	runGit("config", "user.email", "teste@example.com")
	runGit("config", "commit.gpgsign", "false")

	// Create sample Go and Java modules + specs
	if err := os.MkdirAll(filepath.Join(repo, "mod-api", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "mod-api", "go.mod"), []byte("module example.com/api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "mod-api", "src", "service.go"), []byte("package src\n\ntype PedidoService struct{}\n\nfunc (p *PedidoService) CriarPedido() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "specs", "001-pedidos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "specs", "001-pedidos", "spec.md"), []byte("# Spec 001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "init")

	sum, err := GenerateWorktreeVault(Options{
		Root:         repo,
		Branch:       "feat/PROJ-123",
		LaneSlot:     "lane-proj-123",
		Objective:    "Implementar criação de pedido",
		JiraKey:      "PROJ-123",
		StackSummary: "Go",
		TestHint:     "go test ./...",
	})
	if err != nil {
		t.Fatalf("GenerateWorktreeVault failed: %v", err)
	}
	if sum.ModuleCount == 0 || sum.SymbolFiles == 0 || sum.SpecCount != 1 {
		t.Fatalf("unexpected vault summary: %+v", sum)
	}

	idxBytes, err := os.ReadFile(sum.IndexPath)
	if err != nil {
		t.Fatal(err)
	}
	idxText := string(idxBytes)
	for _, want := range []string{"[[repomap]]", "[[SPECS]]", "[[SQUAD_BOARD]]", "[[DECISIONS]]", "[[lanes/lane-proj-123]]", "[[modules/mod-api]]", "PROJ-123"} {
		if !strings.Contains(idxText, want) {
			t.Fatalf("INDEX.md missing %q:\n%s", want, idxText)
		}
	}

	rmBytes, err := os.ReadFile(sum.RepoMapPath)
	if err != nil {
		t.Fatal(err)
	}
	rmText := string(rmBytes)
	if !strings.Contains(rmText, "type PedidoService") || !strings.Contains(rmText, "func CriarPedido") {
		t.Fatalf("repomap.md missing Go symbols:\n%s", rmText)
	}

	// Verify `.hivemind/vault` is completely ignored by git status!
	stOut, err := exec.Command("git", "-C", repo, "status", "--short").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stOut), ".hivemind") {
		t.Fatalf(".hivemind/vault deveria estar excluído do git status: %q", string(stOut))
	}

	// Verify SquadBoard & AppendLaneNote persistence
	if err := SyncSquadBoard(repo, SquadBoardSnapshot{
		Branch:     "feat/PROJ-123",
		Objective:  "Criar pedido",
		Owners:     map[string][]string{"dev_1": {"mod-api/"}},
		Touched:    map[string][]string{"dev_1": {"mod-api/src/service.go"}},
		Notes:      []SquadNote{{Author: "dev_1", To: "qa", Topic: "contrato", Content: "Endpoint pronto"}},
		QAFailures: []string{"Rodada 1: faltou testar valor zero"},
	}); err != nil {
		t.Fatal(err)
	}
	boardBytes, err := os.ReadFile(filepath.Join(sum.VaultDir, "SQUAD_BOARD.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(boardBytes), "Endpoint pronto") || !strings.Contains(string(boardBytes), "faltou testar valor zero") {
		t.Fatalf("SQUAD_BOARD.md missing persisted notes:\n%s", string(boardBytes))
	}

	if err := AppendLaneNote(repo, LaneEntry{
		Slot:     "lane-proj-123",
		Branch:   "feat/PROJ-123",
		JiraKey:  "PROJ-123",
		PRNumber: 42,
		Status:   "concluído",
		Summary:  "Implementado e testado.",
		Files:    []string{"mod-api/src/service.go"},
	}); err != nil {
		t.Fatal(err)
	}
	laneBytes, err := os.ReadFile(filepath.Join(sum.VaultDir, "lanes", "lane-proj-123.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(laneBytes), "Implementado e testado.") || !strings.Contains(string(laneBytes), "#42") {
		t.Fatalf("lane diary missing entry:\n%s", string(laneBytes))
	}
}
