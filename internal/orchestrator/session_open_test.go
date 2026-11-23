package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
)

func TestResumeKeepsQAFeedbackAndNewObjectiveDropsIt(t *testing.T) {
	ctx := context.Background()
	svc, err := sessionx.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open session store: %v", err)
	}
	created, err := svc.Create(ctx, &session.CreateRequest{
		AppName: sessionx.AppName,
		UserID:  sessionx.UserID,
		State: map[string]any{
			"objective":   "criar endpoint /v1/users",
			"chief_plan":  "plano do chefe",
			"qa_feedback": "falta o teste do cache",
			"qa_report":   "go test ./... falhou",
			"qa_approved": "",
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	r := &Runner{Sessions: svc}
	resumed, err := r.openSession(ctx, created.Session.ID(), "continua", "", "squad")
	if err != nil {
		t.Fatalf("openSession continua: %v", err)
	}
	if got, _ := resumed.State().Get("qa_feedback"); fmt.Sprint(got) != "falta o teste do cache" {
		t.Fatalf("continua deveria manter qa_feedback, veio %v", got)
	}
	if got, _ := resumed.State().Get("qa_report"); fmt.Sprint(got) != "go test ./... falhou" {
		t.Fatalf("continua deveria manter qa_report, veio %v", got)
	}

	fresh, err := r.openSession(ctx, created.Session.ID(), "agora faz o endpoint de pedidos", "", "squad")
	if err != nil {
		t.Fatalf("openSession novo objetivo: %v", err)
	}
	if got, _ := fresh.State().Get("qa_feedback"); strings.TrimSpace(fmt.Sprint(got)) != "" {
		t.Fatalf("objetivo novo deveria limpar qa_feedback, veio %v", got)
	}
}

func TestCapWorktreeStatusAndRedundantPlan(t *testing.T) {
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, " M arquivo.go")
	}
	capped := capWorktreeStatus(strings.Join(lines, "\n"))
	if strings.Count(capped, "\n") > 41 || !strings.Contains(capped, "omitidos") {
		t.Fatalf("status longo deveria caber em 40 linhas: %q", capped)
	}
	loaded := map[string]string{"chief_plan": "plano único do chefe"}
	if !redundantPlanKey("pm_plan", "plano único do chefe", loaded) {
		t.Fatal("pm_plan idêntico ao chief_plan é redundante")
	}
	if redundantPlanKey("qa_feedback", "plano único do chefe", loaded) {
		t.Fatal("qa_feedback nunca é plano redundante")
	}
	if redundantPlanKey("architect_plan", "outro desenho", loaded) {
		t.Fatal("plano diferente do chefe deve ser enviado")
	}
}

func TestSummarizePriorSessionKeepsNewest(t *testing.T) {
	ctx := context.Background()
	svc, err := sessionx.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open session store: %v", err)
	}
	created, err := svc.Create(ctx, &session.CreateRequest{
		AppName: sessionx.AppName,
		UserID:  sessionx.UserID,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sess := created.Session
	for i := 0; i < 12; i++ {
		ev := &session.Event{Author: fmt.Sprintf("agent-%d", i)}
		ev.LLMResponse.Content = &genai.Content{
			Role:  genai.RoleModel,
			Parts: []*genai.Part{{Text: fmt.Sprintf("decisão %02d %s", i, strings.Repeat("x", 700))}},
		}
		if err := svc.AppendEvent(ctx, sess, ev); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	got := summarizePriorSessionContext(sess)
	if strings.Contains(got, "decisão 00") {
		t.Fatalf("a decisão mais antiga não deveria sobreviver à janela: %s", got[:80])
	}
	if !strings.Contains(got, "decisão 11") {
		t.Fatalf("a decisão mais nova deveria caber: %s", got)
	}
}

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
			"objective":    "Atender feedback do PR #6",
			"chief_plan":   "plano anterior",
			"qa_approved":  "1",
			"pr_number":    "6",
			"pr_branch":    "feat/example-ui",
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
