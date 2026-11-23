package gitx

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// WorktreeInfo describes one active or orphan HiveMind worktree entry.
type WorktreeInfo struct {
	Path      string `json:"path"`
	Branch    string `json:"branch,omitempty"`
	HEAD      string `json:"head,omitempty"`
	Agent     string `json:"agent,omitempty"`
	SizeBytes int64  `json:"sizeBytes"`
	IsMain    bool   `json:"isMain,omitempty"`
	Detached  bool   `json:"detached,omitempty"`
	Prunable  bool   `json:"prunable,omitempty"`
	Orphan    bool   `json:"orphan,omitempty"`
	Locked    bool   `json:"locked,omitempty"`
}

// PruneResult reports cleaned prunable Git entries and orphan directories.
type PruneResult struct {
	PrunedRefs     int      `json:"prunedRefs"`
	RemovedOrphans []string `json:"removedOrphans,omitempty"`
}

// ListWorktrees inspects `git worktree list --porcelain` and detects orphan
// directories inside workspace.WorktreesDir(repo).
func ListWorktrees(ctx context.Context, g Git, repo string) ([]WorktreeInfo, error) {
	out, err := g.Run(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	mainAbs, _ := filepath.Abs(strings.TrimSpace(repo))
	wtRoot := workspace.WorktreesDir(repo)
	wtRootClean := filepath.Clean(wtRoot)

	var list []WorktreeInfo
	seenPaths := map[string]bool{}
	var cur *WorktreeInfo
	flush := func() {
		if cur == nil || cur.Path == "" {
			cur = nil
			return
		}
		if cur.Path != "" {
			clean := filepath.Clean(cur.Path)
			cur.IsMain = sameNormalizedPath(clean, mainAbs)
			wtRootPrefix := strings.TrimSuffix(normPathKey(wtRootClean), "/") + "/"
			if !cur.IsMain && strings.HasPrefix(normPathKey(clean), wtRootPrefix) {
				cur.Agent = filepath.Base(clean)
			}
			cur.SizeBytes = dirSizeSafe(clean)
			seenPaths[normPathKey(clean)] = true
			list = append(list, *cur)
		}
		cur = nil
	}

	for _, rawLine := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &WorktreeInfo{Path: strings.TrimSpace(strings.TrimPrefix(line, "worktree "))}
		case cur != nil && strings.HasPrefix(line, "HEAD "):
			cur.HEAD = strings.TrimSpace(strings.TrimPrefix(line, "HEAD "))
		case cur != nil && strings.HasPrefix(line, "branch "):
			ref := strings.TrimSpace(strings.TrimPrefix(line, "branch "))
			cur.Branch = strings.TrimPrefix(ref, "refs/heads/")
		case cur != nil && line == "detached":
			cur.Detached = true
		case cur != nil && (line == "prunable" || strings.HasPrefix(line, "prunable ")):
			cur.Prunable = true
		case cur != nil && (line == "locked" || strings.HasPrefix(line, "locked ")):
			cur.Locked = true
		}
	}
	flush()

	// Detect unregistered orphan directories in WorktreesDir(repo).
	if entries, err := os.ReadDir(wtRoot); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			p := filepath.Join(wtRoot, entry.Name())
			if seenPaths[normPathKey(p)] {
				continue
			}
			list = append(list, WorktreeInfo{
				Path:      p,
				Agent:     entry.Name(),
				Orphan:    true,
				SizeBytes: dirSizeSafe(p),
			})
		}
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].IsMain != list[j].IsMain {
			return list[i].IsMain
		}
		return list[i].Path < list[j].Path
	})
	return list, nil
}

// ArchiveWorktree safely removes one HiveMind-managed worktree inside
// workspace.WorktreesDir(repo) while never touching the main repository.
func ArchiveWorktree(ctx context.Context, g Git, repo, wtPath string) error {
	wtRoot, err := filepath.Abs(workspace.WorktreesDir(repo))
	if err != nil {
		return err
	}
	target, err := filepath.Abs(strings.TrimSpace(wtPath))
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(wtRoot, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("recusado: %s não é uma worktree gerenciada dentro de %s", wtPath, wtRoot)
	}
	_ = unlinkWorktreeSymlinks(target)
	if g.IsRepo(ctx, target) {
		RemoveWorktree(ctx, g, repo, target, "")
		if _, err := os.Stat(target); os.IsNotExist(err) {
			return nil
		}
	}
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	_, _ = g.Run(ctx, repo, "worktree", "prune")
	return nil
}

// PruneWorktrees runs `git worktree prune` and removes unregistered orphan
// directories inside workspace.WorktreesDir(repo).
func PruneWorktrees(ctx context.Context, g Git, repo string) (PruneResult, error) {
	var res PruneResult
	listBefore, err := ListWorktrees(ctx, g, repo)
	if err != nil {
		return res, err
	}
	for _, wt := range listBefore {
		if wt.Prunable {
			res.PrunedRefs++
		}
	}
	if _, err := g.Run(ctx, repo, "worktree", "prune"); err != nil {
		return res, err
	}
	for _, wt := range listBefore {
		if !wt.Orphan {
			continue
		}
		if err := ArchiveWorktree(ctx, g, repo, wt.Path); err != nil {
			return res, err
		}
		res.RemovedOrphans = append(res.RemovedOrphans, wt.Path)
	}
	return res, nil
}

// CreateWorktreeWithProgress creates a worktree, hydrates .worktreeinclude and
// sharedDirectories, and emits structured step progress events.
func CreateWorktreeWithProgress(ctx context.Context, g Git, repo, agent, branch string, sharedDirs []string, onProgress func(step string, pct int)) (string, error) {
	emit := func(step string, pct int) {
		if onProgress != nil {
			onProgress(step, pct)
		}
	}
	emit("preparando worktree", 15)
	wtPath, err := g.EnsureWorktree(ctx, repo, agent, branch)
	if err != nil {
		return "", err
	}
	emit("aplicando .worktreeinclude", 55)
	if _, err := ApplyWorktreeInclude(repo, wtPath); err != nil {
		return wtPath, err
	}
	emit("vinculando sharedDirectories", 80)
	if _, err := LinkSharedDirectories(repo, wtPath, sharedDirs); err != nil {
		return wtPath, err
	}
	emit("concluído", 100)
	return wtPath, nil
}

func dirSizeSafe(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || d.Type()&os.ModeIrregular != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info, lErr := os.Lstat(path); lErr == nil && (info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeIrregular != 0) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		} else if lErr == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// unlinkWorktreeSymlinks unlinks symlinks and Windows directory junctions
// inside dir (both top-level and nested, e.g. frontend/node_modules) without
// descending into or modifying their targets.
func unlinkWorktreeSymlinks(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p == dir {
			return nil
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		isLink := d.Type()&(os.ModeSymlink|os.ModeIrregular) != 0
		if !isLink {
			if info, lErr := os.Lstat(p); lErr == nil && info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
				isLink = true
			}
		}
		if isLink {
			_ = os.Remove(p)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return nil
	})
}

func normPathKey(p string) string {
	clean := filepath.Clean(strings.TrimSpace(p))
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		clean = resolved
	}
	return strings.ToLower(filepath.ToSlash(clean))
}

func sameNormalizedPath(a, b string) bool {
	return normPathKey(a) == normPathKey(b)
}
