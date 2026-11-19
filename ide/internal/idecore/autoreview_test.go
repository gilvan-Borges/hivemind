package idecore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestAutoReviewFilterAndFingerprintDedup(t *testing.T) {
	now := time.Date(2026, 11, 19, 13, 0, 0, 0, time.UTC)
	cfg := normalizeAutoReviewSettings(team.AutoReviewSettings{Enabled: true})
	seen := map[string]autoReviewRecord{}

	prs := []ProjectPullRequest{
		{Number: 101, Title: "My own PR", IsMine: true, NeedsAttention: true, UpdatedAt: "2026-11-19T12:00:00Z"},
		{Number: 102, Title: "Draft PR", IsMine: false, IsDraft: true, NeedsAttention: true, UpdatedAt: "2026-11-19T12:01:00Z"},
		{Number: 103, Title: "Already approved no update", IsMine: false, NeedsAttention: false, MyResponse: "approved", UpdatedAt: "2026-11-19T12:02:00Z"},
		{Number: 104, Title: "Pending review B", IsMine: false, NeedsAttention: true, AttentionReason: "Sua revisão", HeadRef: "feat/b", UpdatedAt: "2026-11-19T12:10:00Z"},
		{Number: 105, Title: "Pending review A (older)", IsMine: false, ReviewRequested: true, HeadRef: "feat/a", UpdatedAt: "2026-11-19T12:05:00Z"},
	}

	got := SelectEligibleAutoReviewPRs(prs, cfg, seen, now)
	if len(got) != 2 {
		t.Fatalf("expected 2 eligible PRs (105, 104), got %d: %+v", len(got), got)
	}
	// FIFO oldest first: 105 before 104.
	if got[0].Number != 105 || got[1].Number != 104 {
		t.Fatalf("expected FIFO order [105, 104], got [%d, %d]", got[0].Number, got[1].Number)
	}

	// Mark 105 as reviewed with its fingerprint -> must not be selected again.
	seen["105"] = autoReviewRecord{Fingerprint: PRAutoReviewFingerprint(prs[4]), ReviewedAt: now}
	got2 := SelectEligibleAutoReviewPRs(prs, cfg, seen, now.Add(2*time.Minute))
	if len(got2) != 1 || got2[0].Number != 104 {
		t.Fatalf("expected only PR 104 after 105 reviewed, got %+v", got2)
	}

	// New commit / update on PR 105 changes UpdatedAt -> re-enters queue!
	prs[4].UpdatedAt = "2026-11-19T12:30:00Z"
	prs[4].NeedsAttention = true
	prs[4].AttentionReason = "Novo commit"
	got3 := SelectEligibleAutoReviewPRs(prs, cfg, seen, now.Add(3*time.Minute))
	if len(got3) != 2 || got3[1].Number != 105 {
		t.Fatalf("expected PR 105 to re-enter after new commit, got %+v", got3)
	}
}

func TestAutoReviewRateLimitAndBackoff(t *testing.T) {
	dir := t.TempDir()
	_, err := BuildTeamFromWizard(dir, WizardInput{TeamName: "principal", DevCount: 1, DefaultModel: "agy"})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 19, 13, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	failNext := true
	reviewedPRs := []int{}

	mgr := NewAutoReviewManager(nil, nil)
	mgr.NowFn = func() time.Time { return now }
	mgr.PrepareWTFn = nil
	mgr.ListPRsFn = func(_ context.Context, _ string) ([]ProjectPullRequest, error) {
		return []ProjectPullRequest{
			{Number: 201, Title: "PR Failing", Author: "dev1", HeadRef: "feat/201", BaseRef: "homolog", NeedsAttention: true, UpdatedAt: "2026-11-19T12:00:00Z"},
			{Number: 202, Title: "PR OK", Author: "dev2", HeadRef: "feat/202", BaseRef: "homolog", NeedsAttention: true, UpdatedAt: "2026-11-19T12:05:00Z"},
		}, nil
	}
	mgr.RunReviewFn = func(_ context.Context, _, _, _, objective string) (orchestrator.Result, error) {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(objective, "#201") && failNext {
			failNext = false
			return orchestrator.Result{}, errors.New("temporary network failure")
		}
		if strings.Contains(objective, "#202") {
			reviewedPRs = append(reviewedPRs, 202)
		} else {
			reviewedPRs = append(reviewedPRs, 201)
		}
		return orchestrator.Result{Summary: "Revisão publicada", TotalTokens: 12000, SessionID: "sess-1"}, nil
	}

	runTests := false
	cfg := normalizeAutoReviewSettings(team.AutoReviewSettings{
		Enabled:       true,
		RunTests:      &runTests,
		MaxPRsPerHour: 1,
	})
	w := &autoReviewWorker{
		workspace: dir,
		config:    cfg,
		state:     "idle",
		disk:      autoReviewDiskState{Seen: make(map[string]autoReviewRecord)},
		pollNowCh: make(chan struct{}, 1),
	}

	// 1st poll: PR #201 fails and enters 5-minute backoff.
	mgr.pollAndProcessOne(context.Background(), w)
	if w.state != "error" || w.disk.Seen["201"].FailCount != 1 {
		t.Fatalf("expected PR #201 in backoff error state, got state=%q rec=%+v", w.state, w.disk.Seen["201"])
	}

	// 2nd poll (1 min later): PR #201 skipped (backoff), PR #202 reviewed -> hits 1 PR/hour cap.
	now = now.Add(1 * time.Minute)
	mgr.pollAndProcessOne(context.Background(), w)
	if len(reviewedPRs) != 1 || reviewedPRs[0] != 202 {
		t.Fatalf("expected PR #202 reviewed while #201 is in backoff, got %+v", reviewedPRs)
	}

	// 3rd poll (6 min later, #201 backoff expired): must pause due to MaxPRsPerHour=1!
	now = now.Add(6 * time.Minute)
	mgr.pollAndProcessOne(context.Background(), w)
	if w.state != "paused_limit" {
		t.Fatalf("expected paused_limit when hourly cap reached, got state=%q", w.state)
	}
}

func TestAutoReviewEphemeralWorktreeCleanup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := InitGitRepo(ctx, dir); err != nil {
		t.Fatal(err)
	}
	_, _ = runGit(ctx, dir, "config", "user.email", "test@hivemind.local")
	_, _ = runGit(ctx, dir, "config", "user.name", "HiveMind Test")

	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = runGit(ctx, dir, "add", "main.txt")
	_, _ = runGit(ctx, dir, "commit", "-m", "base")

	// Create PR branch with a new file, then switch back to main/master with dirty user edit.
	baseBranch, _ := runGit(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	baseBranch = strings.TrimSpace(baseBranch)
	_, _ = runGit(ctx, dir, "checkout", "-b", "feat/pr-777")
	if err := os.WriteFile(filepath.Join(dir, "pr_file.txt"), []byte("from pr 777\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = runGit(ctx, dir, "add", "pr_file.txt")
	_, _ = runGit(ctx, dir, "commit", "-m", "pr 777 change")
	_, _ = runGit(ctx, dir, "checkout", baseBranch)

	// Uncommitted local edit in user's main checkout must remain untouched.
	if err := os.WriteFile(filepath.Join(dir, "main.txt"), []byte("user local WIP\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wtPath, cleanup, err := PrepareEphemeralPRWorktree(ctx, dir, ProjectPullRequest{
		Number:  777,
		HeadRef: "feat/pr-777",
		BaseRef: baseBranch,
	})
	if err != nil {
		t.Fatalf("PrepareEphemeralPRWorktree failed: %v", err)
	}

	// PR file exists in ephemeral worktree, while user's main checkout keeps WIP and no pr_file.txt.
	if _, err := os.Stat(filepath.Join(wtPath, "pr_file.txt")); err != nil {
		t.Fatalf("expected pr_file.txt inside ephemeral worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pr_file.txt")); !os.IsNotExist(err) {
		t.Fatal("pr_file.txt must not leak into user's checkout")
	}

	cleanup()
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("ephemeral worktree %s was not cleaned up", wtPath)
	}
	wip, err := os.ReadFile(filepath.Join(dir, "main.txt"))
	if err != nil || string(wip) != "user local WIP\n" {
		t.Fatalf("user local WIP was altered: %q err=%v", string(wip), err)
	}
}
