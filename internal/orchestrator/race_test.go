package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestParseRaceCount(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"race", 2, true},
		{"race:1", 2, true},
		{"race:3", 3, true},
		{"race:99", 4, true},
		{"race:bad", 2, true},
		{"squad", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		got, ok := ParseRaceCount(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("ParseRaceCount(%q) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestRunnerRaceModeSelectsWinnerSyncsSafeFilesAndCleansWorktrees(t *testing.T) {
	t.Setenv("HIVEMIND_DATA_DIR", t.TempDir())
	repo := t.TempDir()
	ctx := context.Background()
	g := gitx.Local{}
	for _, args := range [][]string{
		{"init"},
		{"config", "core.autocrlf", "false"},
		{"config", "user.email", "hivemind@example.com"},
		{"config", "user.name", "HiveMind"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := g.Run(ctx, repo, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "app.txt"), []byte("v0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(ctx, repo, "add", "app.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(ctx, repo, "commit", "-m", "init"); err != nil {
		t.Fatal(err)
	}

	r := &Runner{
		Workspace: repo,
		Git:       g,
		Team: team.Team{
			Name: "race-squad",
			Agents: []team.Agent{
				{Name: "pm", Role: team.RoleProjectManager, Model: "gemini-flash-latest"},
				{Name: "dev_1", Role: team.RoleDeveloper, Model: "gemini-flash-latest"},
				{Name: "qa", Role: team.RoleQA, Model: "gemini-flash-latest"},
			},
		},
		RaceCandidateExec: func(_ context.Context, idx int, wtDir, _ string) (string, int64, int64, error) {
			if idx == 1 {
				// Candidate 1 produces higher cost and also creates a sensitive file that must never sync.
				_ = os.WriteFile(filepath.Join(wtDir, ".env"), []byte("SECRET=fake\n"), 0o600)
				_ = os.WriteFile(filepath.Join(wtDir, "app.txt"), []byte("candidate-1\n"), 0o644)
				return "cand-1", 5000, 1000, nil
			}
			if idx == 2 {
				// Candidate 2 is cheaper -> wins tie-breaker cleanly.
				_ = os.WriteFile(filepath.Join(wtDir, "app.txt"), []byte("candidate-2-winner\n"), 0o644)
				_ = os.WriteFile(filepath.Join(wtDir, "extra.txt"), []byte("ok\n"), 0o644)
				return "cand-2", 900, 300, nil
			}
			return "", 0, 0, fmt.Errorf("candidate %d failed", idx)
		},
	}

	res, err := r.Run(ctx, Request{
		Objective: "melhorar app",
		RunMode:   "race:3",
	})
	if err != nil {
		t.Fatalf("Runner.Run(race:3) failed: %v", err)
	}
	if res.TotalTokens != 1300 || res.CostMicroUSD != 5900 {
		t.Fatalf("unexpected totals: tokens=%d cost=%d", res.TotalTokens, res.CostMicroUSD)
	}
	if !strings.Contains(res.Summary, "Vencedor: #2") {
		t.Fatalf("expected winner #2 in summary, got: %s", res.Summary)
	}
	gotApp, _ := os.ReadFile(filepath.Join(repo, "app.txt"))
	if strings.TrimSpace(string(gotApp)) != "candidate-2-winner" {
		t.Fatalf("expected synced app.txt from candidate #2, got %q", string(gotApp))
	}
	if _, err := os.Stat(filepath.Join(repo, ".env")); !os.IsNotExist(err) {
		t.Fatalf("expected .env never to exist in main workspace")
	}
	wts, err := gitx.ListWorktrees(ctx, g, repo)
	if err != nil || len(wts) != 1 {
		t.Fatalf("expected race temporary worktrees cleaned up leaving only main, got %#v err=%v", wts, err)
	}
}
