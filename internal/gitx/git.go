// Package gitx wraps the git binary. Agents and the orchestrator share it so
// tests can substitute a fake and production always shells out to git.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/procx"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// Git is the subset of git the CLI needs.
type Git interface {
	Run(ctx context.Context, dir string, args ...string) (string, error)
	IsRepo(ctx context.Context, dir string) bool
	HasCommit(ctx context.Context, dir string) bool
	EnsureWorktree(ctx context.Context, repo, agent, branch string) (string, error)
	CommitAll(ctx context.Context, dir, message string) (bool, error)
	Push(ctx context.Context, dir, remote, branch string) error
	Status(ctx context.Context, dir string) (string, error)
	Diff(ctx context.Context, dir string) (string, error)
	RemoteURL(ctx context.Context, dir string) (string, error)
	BranchExists(ctx context.Context, dir, branch string) bool
	CurrentBranch(ctx context.Context, dir string) (string, error)
	Integrate(ctx context.Context, repo string, branches []string) (merged []string, integration string, err error)
}

// Local runs the git executable on PATH.
type Local struct{}

func (Local) Run(ctx context.Context, dir string, args ...string) (string, error) {
	fullArgs := append([]string{"-c", "core.quotepath=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	procx.ConfigureContext(cmd)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := strings.TrimSpace(buf.String())
	if err != nil {
		return out, fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, out)
	}
	return out, nil
}

func (g Local) IsRepo(ctx context.Context, dir string) bool {
	out, err := g.Run(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

func (g Local) HasCommit(ctx context.Context, dir string) bool {
	_, err := g.Run(ctx, dir, "rev-parse", "--verify", "HEAD")
	return err == nil
}

func (g Local) BranchExists(ctx context.Context, dir, branch string) bool {
	_, err := g.Run(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// EnsureWorktree creates an external worktree (<AppDataRoot>/workspaces/<slug>/worktrees/<agent>)
// on branch when needed, automatically removing any legacy in-repo .hivemind/worktrees/<agent> first.
func (g Local) EnsureWorktree(ctx context.Context, repo, agent, branch string) (string, error) {
	legacyWT := filepath.Join(repo, ".hivemind", "worktrees", agent)
	if _, err := os.Stat(legacyWT); err == nil {
		_, _ = g.Run(ctx, repo, "worktree", "remove", "--force", legacyWT)
		_ = os.RemoveAll(legacyWT)
		_, _ = g.Run(ctx, repo, "worktree", "prune")
	}
	legacyRoot := filepath.Join(repo, ".hivemind", "worktrees")
	if entries, err := os.ReadDir(legacyRoot); err == nil && len(entries) == 0 {
		_ = os.Remove(legacyRoot)
	}

	wt := workspace.WorktreePath(repo, agent)
	if g.IsRepo(ctx, wt) {
		return wt, nil
	}
	_ = os.RemoveAll(wt)
	_, _ = g.Run(ctx, repo, "worktree", "prune")
	if err := os.MkdirAll(filepath.Dir(wt), 0o700); err != nil {
		return "", err
	}
	var err error
	if g.BranchExists(ctx, repo, branch) {
		_, err = g.Run(ctx, repo, "worktree", "add", "-f", wt, branch)
	} else {
		_, err = g.Run(ctx, repo, "worktree", "add", "-f", "-b", branch, wt, "HEAD")
	}
	if err != nil {
		return "", err
	}
	return wt, nil
}

// CommitAll stages everything and commits when the worktree is dirty.
// A clean tree returns false and a nil error.
func (g Local) CommitAll(ctx context.Context, dir, message string) (bool, error) {
	status, err := g.Run(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(status) == "" {
		return false, nil
	}
	// Never stage HiveMind's own state (.hivemind/sessions, worktrees, MEMORY.md).
	// Do not use :(exclude).hivemind pathspecs: when the directory exists and is
	// already gitignored, `git add` exits 1 ("paths are ignored").
	_ = EnsureHiveIgnore(dir)
	if _, err := g.Run(ctx, dir, "add", "-A", "--", "."); err != nil {
		return false, err
	}
	staged, err := g.Run(ctx, dir, "diff", "--cached", "--name-only")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(staged) == "" {
		return false, nil
	}
	if _, err := g.Run(ctx, dir, "commit", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

func (g Local) Push(ctx context.Context, dir, remote, branch string) error {
	if remote == "" {
		remote = "origin"
	}
	_, err := g.Run(ctx, dir, "push", "-u", remote, branch)
	return err
}

func (g Local) Status(ctx context.Context, dir string) (string, error) {
	return g.Run(ctx, dir, "status", "--short", "--branch")
}

func (g Local) Diff(ctx context.Context, dir string) (string, error) {
	return g.Run(ctx, dir, "diff", "--stat", "HEAD")
}

func (g Local) RemoteURL(ctx context.Context, dir string) (string, error) {
	return g.Run(ctx, dir, "remote", "get-url", "origin")
}

func (g Local) CurrentBranch(ctx context.Context, dir string) (string, error) {
	out, err := g.Run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	return strings.TrimSpace(out), err
}
