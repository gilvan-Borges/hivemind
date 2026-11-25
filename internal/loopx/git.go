package loopx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/procx"
)

// ErrNotRepo means the workspace is not a git checkout. The loop still runs; it just does not commit.
var ErrNotRepo = errors.New("workspace sem repositório git")

// Tree is the git surface the controller uses. Tests substitute it.
type Tree interface {
	Snapshot(ctx context.Context, workspace string) (fingerprint string, files map[string]string, err error)
	EnsureBranch(ctx context.Context, workspace, branch string) error
	Commit(ctx context.Context, workspace, message string) (string, error)
	OpenPR(ctx context.Context, workspace, title, body, base string) (string, error)
}

// GitWorktree talks to git in the workspace. OpenPull is optional.
type GitWorktree struct {
	OpenPull func(ctx context.Context, workspace, title, body, base string) (string, error)
}

func (g GitWorktree) Snapshot(ctx context.Context, workspace string) (string, map[string]string, error) {
	root, err := safeWorkspace(workspace)
	if err != nil {
		return "", nil, err
	}
	if _, err := gitCmd(ctx, root, "rev-parse", "--is-inside-work-tree"); err != nil {
		return "", nil, ErrNotRepo
	}
	out, err := gitCmd(ctx, root, "status", "--porcelain")
	if err != nil {
		return "", nil, err
	}
	files := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if i := strings.LastIndex(path, " -> "); i >= 0 {
			path = strings.TrimSpace(path[i+4:])
		}
		path = strings.Trim(path, `"`)
		relPath := filepathSlash(path)
		if relPath == "" || strings.Contains(relPath, "..") || filepath.IsAbs(relPath) || !filepath.IsLocal(relPath) {
			continue
		}
		sum := hashFile(filepath.Join(root, filepath.FromSlash(relPath)))
		files[relPath] = sum
	}
	return fingerprint(files), files, nil
}

func (g GitWorktree) EnsureBranch(ctx context.Context, workspace, branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return fmt.Errorf("branch vazia")
	}
	if _, err := gitCmd(ctx, workspace, "rev-parse", "--is-inside-work-tree"); err != nil {
		return ErrNotRepo
	}
	_, err := gitCmd(ctx, workspace, "checkout", "-B", branch)
	return err
}

func (g GitWorktree) Commit(ctx context.Context, workspace, message string) (string, error) {
	if _, err := gitCmd(ctx, workspace, "rev-parse", "--is-inside-work-tree"); err != nil {
		return "", ErrNotRepo
	}
	if _, err := gitCmd(ctx, workspace, "add", "-A"); err != nil {
		return "", err
	}
	out, err := gitCmd(ctx, workspace, "commit", "-m", message)
	if err != nil {
		if strings.Contains(out, "nothing to commit") || strings.Contains(err.Error(), "nothing to commit") {
			return "", nil
		}
		return "", err
	}
	sha, err := gitCmd(ctx, workspace, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(sha), nil
}

func (g GitWorktree) OpenPR(ctx context.Context, workspace, title, body, base string) (string, error) {
	if g.OpenPull == nil {
		return "", fmt.Errorf("abertura de PR não está ligada neste processo")
	}
	return g.OpenPull(ctx, workspace, title, body, base)
}

func gitCmd(ctx context.Context, dir string, args ...string) (string, error) {
	if strings.Contains(dir, "..") {
		return "", fmt.Errorf("workspace inválido")
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	procx.ConfigureContext(cmd)
	cmd.Dir = filepath.Clean(dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	text := strings.TrimRight(stdout.String(), "\r\n")
	errText := strings.TrimSpace(stderr.String())
	if err != nil {
		msg := strings.TrimSpace(text + " " + errText)
		if msg != "" {
			return text, fmt.Errorf("%w: %s", err, msg)
		}
		return text, err
	}
	return text, nil
}

func hashFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "missing"
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

func fingerprint(files map[string]string) string {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s %s\n", k, files[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ChangedFiles lists paths whose snapshot hash differs, including deletions.
func ChangedFiles(before, after map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for p, h := range after {
		if before[p] != h {
			seen[p] = true
			out = append(out, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok && !seen[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
