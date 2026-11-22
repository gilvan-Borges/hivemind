package idecore

import (
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
)

func TestBuildPullRequestActivityDetail(t *testing.T) {
	detail := githubx.PullRequestDetail{
		Number:       42,
		Body:         "  Resumo da entrega de exemplo.  ",
		ChangedFiles: 3,
		Additions:    28,
		Deletions:    7,
		Reviews: []githubx.PRReview{
			{ID: 1, Author: "reviewer", State: "COMMENTED", Body: "   ", SubmittedAt: "2026-11-20T10:00:00Z"},
			{ID: 2, Author: "reviewer", State: "CHANGES_REQUESTED", Body: "Faltou tratar erro.", SubmittedAt: "2026-11-20T11:00:00Z"},
			{ID: 3, Author: "reviewer-two", State: "APPROVED", Body: "LGTM", SubmittedAt: "2026-11-20T14:00:00Z"},
		},
		ReviewComments: []githubx.Comment{
			{ID: 10, Author: "reviewer", Body: "Validar nil aqui.", Path: "src/example.go", Line: 18, CreatedAt: "2026-11-20T12:00:00Z"},
		},
		IssueComments: []githubx.Comment{
			{ID: 20, Author: "dev-alpha", Body: "Ajuste aplicado no último commit.", CreatedAt: "2026-11-20T13:00:00Z"},
		},
	}

	got := BuildPullRequestActivityDetail(detail, 3)
	if got.Number != 42 || got.ChangedFiles != 3 || got.Additions != 28 || got.Deletions != 7 {
		t.Fatalf("unexpected metadata: %+v", got)
	}
	if got.Body != "Resumo da entrega de exemplo." {
		t.Fatalf("body not trimmed: %q", got.Body)
	}
	if got.TotalEvents != 4 {
		t.Fatalf("TotalEvents = %d, want 4 (empty COMMENTED review should be skipped)", got.TotalEvents)
	}
	if len(got.Events) != 3 {
		t.Fatalf("len(Events) = %d, want 3", len(got.Events))
	}
	if got.Events[0].Kind != "approved" || got.Events[0].Author != "reviewer-two" {
		t.Fatalf("events[0] = %+v, want newest APPROVED first", got.Events[0])
	}
	if got.Events[1].Kind != "comment" || got.Events[1].Author != "dev-alpha" {
		t.Fatalf("events[1] = %+v, want comment from dev-alpha", got.Events[1])
	}
	if got.Events[2].Kind != "review_comment" || got.Events[2].Path != "src/example.go" || got.Events[2].Line != 18 {
		t.Fatalf("events[2] = %+v, want inline comment at src/example.go:18", got.Events[2])
	}
}
