package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestEveryConversationGetsIsolatedWorktreeRegardlessOfPRAuthorOrDirectMode(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não instalado")
	}
	ctx := context.Background()
	repo := t.TempDir()
	g := gitx.Local{}
	mustGit(t, g, ctx, repo, "init", "-b", "main")
	mustGit(t, g, ctx, repo, "config", "user.name", "Teste")
	mustGit(t, g, ctx, repo, "config", "user.email", "teste@example.com")
	mustGit(t, g, ctx, repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# acme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, g, ctx, repo, "add", ".")
	mustGit(t, g, ctx, repo, "commit", "-m", "init")
	// Create two PR branches and leave one checked out in the main workspace to
	// simulate the real collision scenario where the main checkout holds a PR branch.
	mustGit(t, g, ctx, repo, "checkout", "-b", "feat/pr-101")
	if err := os.WriteFile(filepath.Join(repo, "pr101.txt"), []byte("101\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, g, ctx, repo, "add", ".")
	mustGit(t, g, ctx, repo, "commit", "-m", "feat: pr 101")
	mustGit(t, g, ctx, repo, "checkout", "main")
	mustGit(t, g, ctx, repo, "checkout", "-b", "feat/pr-102")
	if err := os.WriteFile(filepath.Join(repo, "pr102.txt"), []byte("102\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, g, ctx, repo, "add", ".")
	mustGit(t, g, ctx, repo, "commit", "-m", "feat: pr 102")

	squad := team.FullSquadTeam("padrao", "", "scripted", "gemini", 1)
	project := team.File{
		Active: squad.Name,
		Settings: team.Settings{
			BaseBranch: "main",
		},
		Teams: []team.Team{squad},
	}
	project.Normalize()

	gh := &fakeGitHub{
		me: "dev-owner",
		prs: map[int]*githubx.PullRequestDetail{
			101: {
				Number:  101,
				Title:   "feat: tarefa 101",
				Author:  "dev-owner",
				HeadRef: "feat/pr-101",
				BaseRef: "main",
				State:   "OPEN",
			},
			102: {
				Number:  102,
				Title:   "feat: tarefa 102",
				Author:  "dev-owner",
				HeadRef: "feat/pr-102",
				BaseRef: "main",
				State:   "OPEN",
			},
		},
	}

	runDirectLane := func(laneID, objective, fileName string) string {
		r := &Runner{
			Workspace:    repo,
			Project:      project,
			Team:         squad,
			Git:          g,
			GitHub:       gh,
			LaneID:       laneID,
			EnableTriage: true,
			Out:          &strings.Builder{},
			Err:          &strings.Builder{},
			Streaming:    agent.StreamingModeNone,
			Models: map[string]model.LLM{
				"pm": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
					if call == 1 {
						return modelx.FunctionCall("write_file", "w1", map[string]any{
							"path":    fileName,
							"content": "isolado-" + laneID + "\n",
						})
					}
					return modelx.Text("Concluído na worktree isolada.")
				}},
			},
		}
		if _, err := r.Run(ctx, Request{Objective: objective, RunMode: "direct"}); err != nil {
			t.Fatalf("lane %s failed: %v", laneID, err)
		}
		return r.deliveryRoot
	}

	// Lane 1 handles own PR #101; Lane 2 handles own PR #102 (whose branch is checked out in main repo!).
	wt1 := runDirectLane("lane-pr-101", "Atender feedback do nosso PR #101 (feat/pr-101 -> main)", "edit-101.txt")
	wt2 := runDirectLane("lane-pr-102", "Atender feedback do nosso PR #102 (feat/pr-102 -> main)", "edit-102.txt")

	if wt1 == "" || wt2 == "" {
		t.Fatalf("ambas as conversas deveriam criar worktrees isoladas: wt1=%q wt2=%q", wt1, wt2)
	}
	if filepath.Clean(wt1) == filepath.Clean(repo) || filepath.Clean(wt2) == filepath.Clean(repo) {
		t.Fatalf("nenhuma conversa pode escrever na pasta principal do workspace: repo=%q wt1=%q wt2=%q", repo, wt1, wt2)
	}
	if filepath.Clean(wt1) == filepath.Clean(wt2) {
		t.Fatalf("conversas paralelas não podem compartilhar a mesma worktree: %q", wt1)
	}

	// Main workspace MUST remain completely untouched!
	if _, err := os.Stat(filepath.Join(repo, "edit-101.txt")); !os.IsNotExist(err) {
		t.Fatalf("edit-101.txt vazou para a pasta principal do workspace")
	}
	if _, err := os.Stat(filepath.Join(repo, "edit-102.txt")); !os.IsNotExist(err) {
		t.Fatalf("edit-102.txt vazou para a pasta principal do workspace")
	}
	if _, err := os.Stat(filepath.Join(wt1, "edit-101.txt")); err != nil {
		t.Fatalf("edit-101.txt deveria estar isolado em wt1 (%s): %v", wt1, err)
	}
	if _, err := os.Stat(filepath.Join(wt2, "edit-102.txt")); err != nil {
		t.Fatalf("edit-102.txt deveria estar isolado em wt2 (%s): %v", wt2, err)
	}
	if _, err := os.Stat(filepath.Join(wt1, "edit-102.txt")); !os.IsNotExist(err) {
		t.Fatalf("edit-102.txt não pode aparecer dentro de wt1")
	}
	// Verify wt2 actually got PR #102's branch contents (pr102.txt) even though feat/pr-102 was checked out in main!
	if _, err := os.Stat(filepath.Join(wt2, "pr102.txt")); err != nil {
		t.Fatalf("wt2 deveria espelhar o conteúdo da branch feat/pr-102 (pr102.txt): %v", err)
	}
}
