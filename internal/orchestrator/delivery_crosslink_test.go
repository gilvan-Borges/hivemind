package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
)

type fakeCrossLinkGitHub struct {
	githubx.Disabled
	updated map[int]string
}

func (f *fakeCrossLinkGitHub) Enabled() bool { return true }
func (f *fakeCrossLinkGitHub) UpdatePullRequestBody(_ context.Context, number int, body string) error {
	if f.updated == nil {
		f.updated = make(map[int]string)
	}
	f.updated[number] = body
	return nil
}

func TestCrossLinkOpenedPRsUpdatesBodiesIdempotently(t *testing.T) {
	ghRoot := &fakeCrossLinkGitHub{}
	ghApp := &fakeCrossLinkGitHub{}
	r := &Runner{}

	opened := []openedPRRef{
		{
			client: ghRoot,
			pr: githubx.CrossLinkPR{
				Number: 12,
				Repo:   "acme-web",
				URL:    "https://github.com/acme/web/pull/12",
				Title:  "feat(api): add profile endpoint",
				Body:   "Backend change",
			},
		},
		{
			client: ghApp,
			pr: githubx.CrossLinkPR{
				Number: 34,
				Repo:   "acme-mobile",
				URL:    "https://github.com/acme/mobile/pull/34",
				Title:  "feat(ui): consume profile endpoint",
				Body:   "Mobile change",
			},
		},
	}

	r.crossLinkOpenedPRs(context.Background(), opened)
	if !strings.Contains(ghRoot.updated[12], "https://github.com/acme/mobile/pull/34") {
		t.Fatalf("expected root PR #12 body to reference nested PR #34, got %q", ghRoot.updated[12])
	}
	if !strings.Contains(ghApp.updated[34], "https://github.com/acme/web/pull/12") {
		t.Fatalf("expected nested PR #34 body to reference root PR #12, got %q", ghApp.updated[34])
	}

	// Idempotent second run when bodies are already populated.
	opened[0].pr.Body = ghRoot.updated[12]
	opened[1].pr.Body = ghApp.updated[34]
	r.crossLinkOpenedPRs(context.Background(), opened)
	if count := strings.Count(ghRoot.updated[12], "## PRs relacionados"); count != 1 {
		t.Fatalf("expected single ## PRs relacionados section, got %d", count)
	}
}
