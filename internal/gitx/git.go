// Package gitx wraps the git binary. Agents and the orchestrator share it so
// tests can substitute a fake and production always shells out to git.
package gitx

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	cmd.Env = NonInteractiveEnv()
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
	if ctx.Err() != nil {
		// Transient cancellation: never destroy anything when the caller is gone.
		return "", ctx.Err()
	}
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
	// A directory with a live gitdir is left alone: a lock or a cancelled
	// probe must not wipe uncommitted work. A gitdir pointer that no longer
	// exists is only rebuilt automatically when no uncommitted/untracked files
	// live inside the worktree (Spec 08 §3.1).
	if _, statErr := os.Stat(wt); statErr == nil {
		if _, gitErr := os.Stat(filepath.Join(wt, ".git")); gitErr == nil {
			if !worktreeGitDirMissing(wt) || g.worktreeHasUncommittedFiles(ctx, repo, wt, branch) {
				return "", fmt.Errorf("worktree existente em %s falhou a verificação de repositório (possível falha transitória); nada foi removido — rode novamente ou inspecione manualmente", wt)
			}
			RemoveWorktree(ctx, g, repo, wt, "")
			if g.IsRepo(ctx, wt) {
				return wt, nil
			}
			if _, still := os.Stat(wt); still == nil {
				return "", fmt.Errorf("worktree quebrada em %s não pôde ser removida", wt)
			}
		} else {
			_ = os.RemoveAll(wt)
		}
	}
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

// worktreeGitDirMissing reports a worktree whose .git file points at a
// gitdir that is gone. A real .git directory is never treated as missing.
func worktreeGitDirMissing(wt string) bool {
	gitPath := filepath.Join(wt, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil || info.IsDir() {
		return false
	}
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return true
	}
	line := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if len(line) < len(prefix) || !strings.EqualFold(line[:len(prefix)], prefix) {
		return true
	}
	gitdir := strings.TrimSpace(line[len(prefix):])
	if gitdir == "" {
		return true
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(wt, gitdir)
	}
	_, err = os.Stat(gitdir)
	return os.IsNotExist(err)
}

// worktreeHasUncommittedFiles reports whether wt holds any regular file not
// committed identically in repo (on branch if it exists, otherwise HEAD).
// Fails closed (returns true) on any probe error so EnsureWorktree never
// destroys uncommitted work.
func (g Local) worktreeHasUncommittedFiles(ctx context.Context, repo, wt, branch string) bool {
	treeOIDs, ok := g.trackedBlobOIDs(ctx, repo, branch)
	if !ok {
		entries, err := os.ReadDir(wt)
		if err != nil {
			return true
		}
		for _, e := range entries {
			if e.Name() != ".git" && e.Name() != ".hivemind" {
				return true
			}
		}
		return false
	}
	dirty := false
	walkErr := filepath.WalkDir(wt, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			dirty = true
			return fs.SkipAll
		}
		if p == wt {
			return nil
		}
		rel, rErr := filepath.Rel(wt, p)
		if rErr != nil {
			dirty = true
			return fs.SkipAll
		}
		relSlash := filepath.ToSlash(rel)
		if relSlash == ".git" || relSlash == ".hivemind" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info, lErr := os.Lstat(p); lErr == nil && info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		expectedOID, tracked := treeOIDs[relSlash]
		if !tracked {
			dirty = true
			return fs.SkipAll
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil || !gitBlobMatchesOID(data, expectedOID) {
			dirty = true
			return fs.SkipAll
		}
		return nil
	})
	return dirty || walkErr != nil
}

func (g Local) trackedBlobOIDs(ctx context.Context, repo, branch string) (map[string]string, bool) {
	ref := "HEAD"
	if br := strings.TrimSpace(branch); br != "" && g.BranchExists(ctx, repo, br) {
		ref = br
	} else if !g.HasCommit(ctx, repo) {
		return nil, false
	}
	raw, err := g.Run(ctx, repo, "ls-tree", "-r", "-z", ref)
	if err != nil {
		return nil, false
	}
	out := make(map[string]string)
	for _, rec := range strings.Split(raw, "\x00") {
		if rec == "" {
			continue
		}
		meta, relPath, ok := strings.Cut(rec, "\t")
		if !ok || relPath == "" {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) == 3 && fields[1] == "blob" {
			out[relPath] = fields[2]
		}
	}
	return out, true
}

func gitBlobMatchesOID(data []byte, expectedOID string) bool {
	useSHA256 := len(expectedOID) == 64
	if gitBlobOID(data, useSHA256) == expectedOID {
		return true
	}
	if bytes.Contains(data, []byte("\r\n")) {
		norm := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
		return gitBlobOID(norm, useSHA256) == expectedOID
	}
	return false
}

func gitBlobOID(data []byte, useSHA256 bool) string {
	header := "blob " + strconv.Itoa(len(data)) + "\x00"
	if useSHA256 {
		h := sha256.New()
		_, _ = h.Write([]byte(header))
		_, _ = h.Write(data)
		return hex.EncodeToString(h.Sum(nil))
	}
	h := sha1.New()
	_, _ = h.Write([]byte(header))
	_, _ = h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
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
