package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
)

func TestOpenCloseProjectLifecycleTenRepos(t *testing.T) {
	app := NewApp("", false)
	defer app.shutdown(context.Background())

	// Prevent network GitHub calls during test while exercising real watcher & worker lifecycle.
	app.githubMonitor.IsGitHubFn = func(context.Context, string) bool { return false }

	roots := make([]string, 10)
	for i := range roots {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file_%d.txt", i)), []byte("ok\n"), 0o644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		if _, err := app.OpenProject(dir); err != nil {
			t.Fatalf("OpenProject(%d): %v", i, err)
		}
		roots[i] = dir
	}

	app.openMu.Lock()
	openCount := len(app.openProjects)
	app.openMu.Unlock()
	if openCount != 10 {
		t.Fatalf("expected 10 tracked open projects, got %d", openCount)
	}

	app.watcherMu.Lock()
	watcherCount := len(app.watchers)
	app.watcherMu.Unlock()
	if watcherCount != 10 {
		t.Fatalf("expected 10 active watchers, got %d", watcherCount)
	}

	// Double-opening root[0] increments refcount; closing once keeps it alive.
	if _, err := app.OpenProject(roots[0]); err != nil {
		t.Fatalf("second OpenProject(0): %v", err)
	}
	if closed := app.CloseProject(roots[0]); closed {
		t.Fatal("expected first CloseProject on doubly-opened project to decrement refcount without tearing down")
	}
	app.watcherMu.Lock()
	_, stillWatching := app.watchers[projectRefKey(roots[0])]
	app.watcherMu.Unlock()
	if !stillWatching {
		t.Fatal("expected watcher to stay alive while refcount > 0")
	}

	for _, dir := range roots {
		if closed := app.CloseProject(dir); !closed {
			t.Fatalf("expected final CloseProject(%s) to release workspace resources", dir)
		}
	}

	app.openMu.Lock()
	remOpen := len(app.openProjects)
	app.openMu.Unlock()
	if remOpen != 0 {
		t.Fatalf("expected 0 openProjects after closing all 10 repos, got %d", remOpen)
	}

	app.watcherMu.Lock()
	remWatchers := len(app.watchers)
	app.watcherMu.Unlock()
	if remWatchers != 0 {
		t.Fatalf("expected 0 watchers after closing all 10 repos, got %d", remWatchers)
	}

	// Re-closing already closed workspace is a safe no-op returning false.
	if app.CloseProject(roots[0]) {
		t.Fatal("expected duplicate CloseProject to return false")
	}
}

func TestAutoReviewReusesGitHubMonitorCache(t *testing.T) {
	app := NewApp("", true)
	defer app.shutdown(context.Background())

	ws := t.TempDir()
	var listCalls atomic.Int32
	app.githubMonitor.IsGitHubFn = func(context.Context, string) bool { return true }
	app.githubMonitor.ListPRsFn = func(context.Context, string) ([]idecore.ProjectPullRequest, error) {
		listCalls.Add(1)
		return []idecore.ProjectPullRequest{{Number: 42, Title: "Cache test"}}, nil
	}

	app.githubMonitor.EnsureWatching(context.Background(), ws)
	defer app.githubMonitor.StopWorkspace(ws)

	// Record fresh snapshot directly via RecordSnapshot (simulating active poll)
	app.githubMonitor.RecordSnapshot(ws, []idecore.ProjectPullRequest{
		{Number: 42, Title: "Cache test", Author: "reviewer"},
	})

	prs, err := app.autoReview.ListPRsFn(context.Background(), ws)
	if err != nil {
		t.Fatalf("ListPRsFn: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 42 {
		t.Fatalf("unexpected cached PRs: %+v", prs)
	}
	if calls := listCalls.Load(); calls != 0 {
		t.Fatalf("expected cached snapshot reuse with 0 direct ListPRs calls, got %d", calls)
	}

	// Expire cache via old maxAge check and verify fallback updates snapshot
	if _, ok := app.githubMonitor.CachedPRs(ws, 1*time.Nanosecond); ok {
		time.Sleep(2 * time.Millisecond)
		if _, ok2 := app.githubMonitor.CachedPRs(ws, 1*time.Nanosecond); ok2 {
			t.Fatal("expected nanosecond cache to expire")
		}
	}
}

func TestResolveInitialWorkspaceSkipsInstallDir(t *testing.T) {
	installDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(installDir, "hivemind-ide.exe"), []byte("bin"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, "unins000.exe"), []byte("unins"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isNonProjectLaunchDir(installDir) {
		t.Fatalf("expected %s to be detected as install dir", installDir)
	}

	realProject := t.TempDir()
	if err := os.WriteFile(filepath.Join(realProject, "go.mod"), []byte("module example.com/acme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isNonProjectLaunchDir(realProject) {
		t.Fatalf("expected %s with go.mod to be treated as real project", realProject)
	}

	if got := resolveInitialWorkspace(installDir, []string{installDir, realProject}, false); got != realProject {
		t.Fatalf("expected fallback to recent real project %s, got %q", realProject, got)
	}
	if got := resolveInitialWorkspace(installDir, []string{installDir}, false); got != "" {
		t.Fatalf("expected empty workspace on first install without recent projects, got %q", got)
	}
}
