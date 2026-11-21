package idecore

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMentionsUser(t *testing.T) {
	cases := []struct {
		body string
		user string
		want bool
	}{
		{"@gilvan-Borges consegue dar uma olhada?", "gilvan-Borges", true},
		{"Oi @GILVAN-BORGES, ajustei aqui!", "gilvan-Borges", true},
		{"Valeu (@gilvan-Borges).", "gilvan-Borges", true},
		{"Contato: foo@gilvan-Borges.com", "gilvan-Borges", false},
		{"@gilvan-Borges2 pode ver?", "gilvan-Borges", false},
		{"Sem menção aqui", "gilvan-Borges", false},
		{"", "gilvan-Borges", false},
	}
	for _, tc := range cases {
		if got := mentionsUser(tc.body, tc.user); got != tc.want {
			t.Errorf("mentionsUser(%q, %q) = %v, want %v", tc.body, tc.user, got, tc.want)
		}
	}
}

func TestComputePRFlowDetails_MentionInThirdPartyPR(t *testing.T) {
	t1 := time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)
	t2 := t1.Add(10 * time.Minute)

	// Someone mentions me in a third-party PR where I am NOT a requested reviewer.
	det := computePRFlowDetails(
		"gilvan-Borges", false, false, false,
		"marcus", "PR description", t1,
		nil,
		[]prActivityComment{{Author: "joao", Body: "Ei @gilvan-Borges, o que acha dessa mudança?", CreatedAt: t2}},
		time.Time{},
	)
	if !det.NeedsAttention || !det.MentionedMe || det.AttentionReason != "Você foi marcado" {
		t.Fatalf("expected mention attention, got %+v", det)
	}
	if det.LastActivityAuthor != "joao" || det.LastActivityKind != "mention" {
		t.Fatalf("unexpected activity metadata: %+v", det)
	}

	// Once I reply after t2, mention is cleared and PR leaves active flow.
	detAfter := computePRFlowDetails(
		"gilvan-Borges", false, false, false,
		"marcus", "PR description", t1,
		nil,
		[]prActivityComment{
			{Author: "joao", Body: "Ei @gilvan-Borges, o que acha?", CreatedAt: t2},
			{Author: "gilvan-Borges", Body: "Ficou ótimo!", CreatedAt: t2.Add(5 * time.Minute)},
		},
		time.Time{},
	)
	if detAfter.NeedsAttention || detAfter.MentionedMe {
		t.Fatalf("expected PR to leave active attention after reply, got %+v", detAfter)
	}
}

func TestDiffGitHubPullRequests(t *testing.T) {
	base := []ProjectPullRequest{
		{
			Number:         10,
			Title:          "feat: meu PR",
			IsMine:         true,
			NeedsAttention: false,
			URL:            "https://github.com/org/repo/pull/10",
		},
		{
			Number:         20,
			Title:          "fix: PR de outro",
			Author:         "marcus",
			IsMine:         false,
			NeedsAttention: false,
			URL:            "https://github.com/org/repo/pull/20",
		},
	}

	// Same state -> zero events.
	if evs := DiffGitHubPullRequests(base, base); len(evs) != 0 {
		t.Fatalf("expected 0 events for identical snapshot, got %+v", evs)
	}

	// 1) Comment on my PR + 2) Mention on third-party PR + 3) New review request.
	next := []ProjectPullRequest{
		{
			Number:              10,
			Title:               "feat: meu PR",
			IsMine:              true,
			NeedsAttention:      true,
			AttentionReason:     "Nova resposta",
			LastActivityAt:      "2026-11-20T15:00:00Z",
			LastActivityAuthor:  "ana",
			LastActivityKind:    "comment",
			LastActivitySnippet: "Pode extrair esse helper?",
			URL:                 "https://github.com/org/repo/pull/10",
		},
		{
			Number:              20,
			Title:               "fix: PR de outro",
			Author:              "marcus",
			IsMine:              false,
			NeedsAttention:      true,
			MentionedMe:         true,
			AttentionReason:     "Você foi marcado",
			LastActivityAt:      "2026-11-20T15:02:00Z",
			LastActivityAuthor:  "marcus",
			LastActivityKind:    "mention",
			LastActivitySnippet: "@gilvan-Borges valida essa regra?",
			URL:                 "https://github.com/org/repo/pull/20",
		},
		{
			Number:          30,
			Title:           "chore: novo PR no fluxo",
			Author:          "joao",
			IsMine:          false,
			ReviewRequested: true,
			NeedsAttention:  true,
			AttentionReason: "Sua revisão",
			URL:             "https://github.com/org/repo/pull/30",
		},
	}

	evs := DiffGitHubPullRequests(base, next)
	if len(evs) != 3 {
		t.Fatalf("expected 3 events, got %d: %+v", len(evs), evs)
	}
	if evs[0].Kind != "my_pr_comment" || evs[0].PRNumber != 10 || evs[0].Author != "ana" {
		t.Fatalf("unexpected event[0]: %+v", evs[0])
	}
	if evs[1].Kind != "mention" || evs[1].PRNumber != 20 || evs[1].Author != "marcus" {
		t.Fatalf("unexpected event[1]: %+v", evs[1])
	}
	if evs[2].Kind != "review_requested" || evs[2].PRNumber != 30 {
		t.Fatalf("unexpected event[2]: %+v", evs[2])
	}

	// Second comment on my PR (timestamp advances) MUST emit again!
	next2 := append([]ProjectPullRequest(nil), next...)
	next2[0].LastActivityAt = "2026-11-20T15:10:00Z"
	next2[0].LastActivityAuthor = "carlos"
	next2[0].LastActivitySnippet = "Concordo com a Ana."
	evs2 := DiffGitHubPullRequests(next, next2)
	if len(evs2) != 1 || evs2[0].PRNumber != 10 || evs2[0].Author != "carlos" {
		t.Fatalf("expected follow-up comment event on PR #10, got %+v", evs2)
	}
}

func TestGitHubMonitor_BaselineThenNotify(t *testing.T) {
	var (
		mu      sync.Mutex
		emitted []GitHubSyncEvent
		step    int
	)
	emittedCh := make(chan GitHubSyncEvent, 8)

	mon := NewGitHubMonitor(func(ev GitHubSyncEvent) {
		mu.Lock()
		emitted = append(emitted, ev)
		mu.Unlock()
		emittedCh <- ev
	})
	mon.interval = time.Hour
	mon.InitialDelay = 0
	mon.IsGitHubFn = func(ctx context.Context, workspace string) bool { return true }
	mon.ListPRsFn = func(ctx context.Context, workspace string) ([]ProjectPullRequest, error) {
		mu.Lock()
		defer mu.Unlock()
		step++
		if step == 1 {
			return []ProjectPullRequest{
				{Number: 1, Title: "PR inicial", IsMine: true},
			}, nil
		}
		return []ProjectPullRequest{
			{
				Number:              1,
				Title:               "PR inicial",
				IsMine:              true,
				NeedsAttention:      true,
				AttentionReason:     "Nova resposta",
				LastActivityAt:      "2026-11-20T16:00:00Z",
				LastActivityAuthor:  "revisor",
				LastActivityKind:    "comment",
				LastActivitySnippet: "LGTM mas ajusta um detalhe",
			},
		}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mon.EnsureWatching(ctx, "/tmp/demo-repo")
	defer mon.StopAll()

	// 1) Baseline poll: populates PRs silently with 0 notification events.
	select {
	case ev1 := <-emittedCh:
		if len(ev1.PRs) != 1 || len(ev1.Events) != 0 {
			t.Fatalf("baseline poll should emit 1 PR and 0 events, got %+v", ev1)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for baseline sync")
	}

	// 2) Next poll: new comment on own PR triggers 1 notification event.
	mon.TriggerNow("/tmp/demo-repo")
	select {
	case ev2 := <-emittedCh:
		if len(ev2.Events) != 1 || ev2.Events[0].Kind != "my_pr_comment" {
			t.Fatalf("expected 1 my_pr_comment event, got %+v", ev2.Events)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for second sync")
	}

	// 3) Third poll with same state: 0 events.
	mon.TriggerNow("/tmp/demo-repo")
	select {
	case ev3 := <-emittedCh:
		if len(ev3.Events) != 0 {
			t.Fatalf("expected 0 events when nothing changed, got %+v", ev3.Events)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for third sync")
	}
}
