package idecore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTestRepoWithCommits(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Dev Synth",
			"GIT_AUTHOR_EMAIL=dev@example.com",
			"GIT_COMMITTER_NAME=Dev Synth",
			"GIT_COMMITTER_EMAIL=dev@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	run("init", "-b", "main")
	p := filepath.Join(dir, "main.go")
	if err := os.WriteFile(p, []byte("package main\n\nfunc v() string { return \"a\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "main.go")
	run("commit", "-m", "chore: initial main.go")

	if err := os.WriteFile(p, []byte("package main\n\nfunc v() string { return \"b\" }\nfunc extra() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "main.go")
	run("commit", "-m", "feat: add extra")
	return dir
}

func TestGitBlameAndFileHistory(t *testing.T) {
	ctx := context.Background()
	dir := initTestRepoWithCommits(t)

	blame, err := GitBlameFile(ctx, dir, "main.go")
	if err != nil {
		t.Fatalf("GitBlameFile: %v", err)
	}
	if len(blame) != 4 {
		t.Fatalf("expected 4 blame lines, got %d (%+v)", len(blame), blame)
	}
	if blame[0].Summary != "chore: initial main.go" || blame[0].Author != "Dev Synth" {
		t.Fatalf("unexpected line 1 blame: %+v", blame[0])
	}
	if blame[3].Summary != "feat: add extra" || blame[3].Line != 4 {
		t.Fatalf("unexpected line 4 blame: %+v", blame[3])
	}

	hist, err := GitFileHistory(ctx, dir, "main.go", 10)
	if err != nil {
		t.Fatalf("GitFileHistory: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("expected 2 commits in history, got %d", len(hist))
	}
	if hist[0].Subject != "feat: add extra" || hist[1].Subject != "chore: initial main.go" {
		t.Fatalf("unexpected history order: %+v", hist)
	}
}

func TestConflictResolutionFlowAndMarkResolved(t *testing.T) {
	ctx := context.Background()
	dir := initTestRepoWithCommits(t)
	run := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Dev Synth",
			"GIT_AUTHOR_EMAIL=dev@example.com",
			"GIT_COMMITTER_NAME=Dev Synth",
			"GIT_COMMITTER_EMAIL=dev@example.com",
		)
		b, err := cmd.CombinedOutput()
		return string(b), err
	}

	// Create branch feat with conflicting change on line 3
	if out, err := run("checkout", "-b", "feat/conflict"); err != nil {
		t.Fatalf("checkout -b: %v\n%s", err, out)
	}
	p := filepath.Join(dir, "main.go")
	if err := os.WriteFile(p, []byte("package main\n\nfunc v() string { return \"theirs\" }\nfunc extra() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run("commit", "-am", "feat: set theirs"); err != nil {
		t.Fatalf("commit theirs: %v\n%s", err, out)
	}

	if out, err := run("checkout", "main"); err != nil {
		t.Fatalf("checkout main: %v\n%s", err, out)
	}
	if err := os.WriteFile(p, []byte("package main\n\nfunc v() string { return \"ours\" }\nfunc extra() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run("commit", "-am", "main: set ours"); err != nil {
		t.Fatalf("commit ours: %v\n%s", err, out)
	}

	// Merge triggers a real Git conflict
	_, _ = run("merge", "feat/conflict")

	conflicts, err := ListConflictedFiles(ctx, dir)
	if err != nil {
		t.Fatalf("ListConflictedFiles: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0].Path != "main.go" || len(conflicts[0].Blocks) != 1 {
		t.Fatalf("expected 1 conflicted block in main.go, got %+v", conflicts)
	}

	// MarkConflictResolved must refuse while conflict markers exist
	if err := MarkConflictResolved(ctx, dir, "main.go"); err == nil {
		t.Fatal("expected MarkConflictResolved to fail while markers remain")
	}

	// Resolve with "theirs"
	updated, err := ResolveConflictBlock(ctx, dir, "main.go", 0, "theirs")
	if err != nil {
		t.Fatalf("ResolveConflictBlock: %v", err)
	}
	if len(updated.Blocks) != 0 {
		t.Fatalf("expected 0 remaining blocks, got %+v", updated.Blocks)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), `return "theirs"`) || strings.Contains(string(data), "<<<<<<<") {
		t.Fatalf("unexpected resolved file content:\n%s", string(data))
	}

	// Now MarkConflictResolved succeeds and stages the file
	if err := MarkConflictResolved(ctx, dir, "main.go"); err != nil {
		t.Fatalf("MarkConflictResolved after resolve: %v", err)
	}
	remaining, err := ListConflictedFiles(ctx, dir)
	if err != nil {
		t.Fatalf("ListConflictedFiles after resolve: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected 0 unmerged files after MarkConflictResolved, got %+v", remaining)
	}
}

func TestResolveConflictBlockBothChoice(t *testing.T) {
	ctx := context.Background()
	dir := initTestRepoWithCommits(t)
	p := filepath.Join(dir, "main.go")
	conflicted := "package main\n<<<<<<< HEAD\nvar a = 1\n=======\nvar b = 2\n>>>>>>> feat\n"
	if err := os.WriteFile(p, []byte(conflicted), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, err := ResolveConflictBlock(ctx, dir, "main.go", 0, "both")
	if err != nil {
		t.Fatalf("ResolveConflictBlock(both): %v", err)
	}
	if len(updated.Blocks) != 0 {
		t.Fatalf("expected 0 remaining blocks, got %+v", updated.Blocks)
	}
	data, _ := os.ReadFile(p)
	want := "package main\nvar a = 1\nvar b = 2\n"
	if string(data) != want {
		t.Fatalf("got:\n%q\nwant:\n%q", string(data), want)
	}
}
