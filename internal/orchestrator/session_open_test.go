package orchestrator

import (
	"context"
	"testing"

	"google.golang.org/adk/v2/session"

	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
)

func TestOpenSessionPreservesPRContextAfterQAApproved(t *testing.T) {
	ctx := context.Background()
	svc, err := sessionx.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open session store: %v", err)
	}
	created, err := svc.Create(ctx, &session.CreateRequest{
		AppName: sessionx.AppName,
		UserID:  sessionx.UserID,
		State: map[string]any{
			"objective":   "Atender feedback do PR #6",
			"chief_plan":  "plano anterior",
			"qa_approved": "1",
			"pr_number":   "6",
			"pr_branch":   "feat/example-ui",
			"pr_reviewers": "@reviewer",
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	r := &Runner{
		Sessions: svc,
	}
	sess, err := r.openSession(ctx, created.Session.ID(), "pode fazer o merge na main", "", "squad")
	if err != nil {
		t.Fatalf("openSession: %v", err)
	}
	if r.prNumber != 6 {
		t.Fatalf("expected prNumber=6 preserved after qa_approved=1, got %d", r.prNumber)
	}
	if r.prBranch != "feat/example-ui" {
		t.Fatalf("expected prBranch=feat/example-ui preserved, got %q", r.prBranch)
	}
	if len(r.prReviewers) != 1 || r.prReviewers[0] != "@reviewer" {
		t.Fatalf("expected prReviewers=[@reviewer] preserved, got %v", r.prReviewers)
	}
	if gotPR, _ := sess.State().Get("pr_number"); gotPR != "6" {
		t.Fatalf("expected state pr_number=6 kept, got %v", gotPR)
	}
	if gotBranch, _ := sess.State().Get("pr_branch"); gotBranch != "feat/example-ui" {
		t.Fatalf("expected state pr_branch kept, got %v", gotBranch)
	}
}
