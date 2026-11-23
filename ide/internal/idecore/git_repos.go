package idecore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
)

// WorkspaceGitRepo describes a Git repository discovered inside a workspace folder
// (either the workspace root itself or a nested project repository such as HiveMind-App).
type WorkspaceGitRepo struct {
	Root    string     `json:"root"`
	Name    string     `json:"name"`
	RelPath string     `json:"relPath"`
	Branch  string     `json:"branch"`
	IsRoot  bool       `json:"isRoot"`
	Diffs   []FileDiff `json:"diffs"`
}

const maxGitRepoDiscoveryDepth = 3

var repoScanExtraSkipped = map[string]bool{
	".hivemind":  true,
	".gradle":    true,
	".dart_tool": true,
	"vendor":     true,
	"out":        true,
	"coverage":   true,
}

func shouldSkipRepoScanDir(name string) bool {
	return ignoredDirs[name] || repoScanExtraSkipped[name]
}

type discoveredRepo struct {
	root    string
	relPath string
	isRoot  bool
}

func repoDisplayName(root, relPath string) string {
	if relPath != "" {
		return filepath.Base(relPath)
	}
	return filepath.Base(root)
}

// discoverGitRepoPaths lists Git repositories inside workspaceDir without reading diffs.
func discoverGitRepoPaths(ctx context.Context, workspaceDir string) ([]discoveredRepo, error) {
	absRoot, err := filepath.Abs(strings.TrimSpace(workspaceDir))
	if err != nil {
		return nil, err
	}
	absRoot = filepath.Clean(absRoot)
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace is not a directory: %s", absRoot)
	}

	g := gitx.Local{}
	var found []discoveredRepo
	seen := make(map[string]bool)

	var walk func(dir, rel string, depth int)
	walk = func(dir, rel string, depth int) {
		if ctx.Err() != nil {
			return
		}
		key := strings.ToLower(filepath.Clean(dir))
		if seen[key] {
			return
		}
		seen[key] = true

		if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr == nil && g.IsRepo(ctx, dir) {
			found = append(found, discoveredRepo{
				root:    dir,
				relPath: filepath.ToSlash(rel),
				isRoot:  depth == 0,
			})
		}

		if depth >= maxGitRepoDiscoveryDepth {
			return
		}
		entries, rdErr := os.ReadDir(dir)
		if rdErr != nil {
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if shouldSkipRepoScanDir(name) {
				continue
			}
			nextRel := name
			if rel != "" {
				nextRel = filepath.Join(rel, name)
			}
			walk(filepath.Join(dir, name), nextRel, depth+1)
		}
	}

	walk(absRoot, "", 0)
	sort.Slice(found, func(i, j int) bool {
		if found[i].isRoot != found[j].isRoot {
			return found[i].isRoot
		}
		return strings.ToLower(found[i].relPath) < strings.ToLower(found[j].relPath)
	})
	return found, nil
}

// DiscoverGitRepos scans workspaceDir for Git repositories (root and nested up to depth 3),
// skipping build/vendor/worktree folders, and returns branch and working-tree diffs for each.
func DiscoverGitRepos(ctx context.Context, workspaceDir string) ([]WorkspaceGitRepo, error) {
	found, err := discoverGitRepoPaths(ctx, workspaceDir)
	if err != nil || len(found) == 0 {
		if err != nil {
			return nil, err
		}
		return []WorkspaceGitRepo{}, nil
	}

	repos := make([]WorkspaceGitRepo, len(found))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup

	for idx, item := range found {
		idx, item := idx, item
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			branch := CurrentBranch(ctx, item.root)
			diffs, diffErr := WorkspaceDiff(ctx, item.root)
			if diffErr != nil || diffs == nil {
				diffs = []FileDiff{}
			}
			repos[idx] = WorkspaceGitRepo{
				Root:    item.root,
				Name:    repoDisplayName(item.root, item.relPath),
				RelPath: item.relPath,
				Branch:  branch,
				IsRoot:  item.isRoot,
				Diffs:   diffs,
			}
		}()
	}
	wg.Wait()

	return repos, nil
}

// DiscoverGitReposWithActiveWorktree returns DiscoverGitRepos. When the
// workspace is a single clean root with exactly one active worktree, that
// worktree is appended as its own entry. The open folder stays the root, so
// its changes are not replaced by the worktree's.
func DiscoverGitReposWithActiveWorktree(ctx context.Context, workspaceDir string) ([]WorkspaceGitRepo, error) {
	repos, err := DiscoverGitRepos(ctx, workspaceDir)
	if err != nil || len(repos) != 1 || !repos[0].IsRoot || len(repos[0].Diffs) > 0 {
		return repos, err
	}
	rootPath := filepath.Clean(repos[0].Root)
	rootKey := canonicalPathKey(rootPath)
	wts, wtErr := gitx.ListWorktrees(ctx, gitx.Local{}, rootPath)
	if wtErr != nil || len(wts) == 0 {
		return repos, nil
	}
	var active []gitx.WorktreeInfo
	for _, wt := range wts {
		if wt.IsMain || wt.Prunable || wt.Orphan || strings.TrimSpace(wt.Path) == "" {
			continue
		}
		if canonicalPathKey(wt.Path) == rootKey {
			continue
		}
		if info, statErr := os.Stat(wt.Path); statErr != nil || !info.IsDir() {
			continue
		}
		active = append(active, wt)
	}
	if len(active) != 1 {
		return repos, nil
	}
	wt := active[0]
	wtDiffs, diffErr := WorkspaceDiff(ctx, wt.Path)
	if diffErr != nil || wtDiffs == nil {
		wtDiffs = []FileDiff{}
	}
	branch := strings.TrimSpace(wt.Branch)
	if branch == "" {
		branch = CurrentBranch(ctx, wt.Path)
	}
	if len(wtDiffs) == 0 {
		return repos, nil
	}
	wtRoot := filepath.Clean(wt.Path)
	rel := ""
	if relPath, relErr := filepath.Rel(rootPath, wtRoot); relErr == nil && relPath != "." && !strings.HasPrefix(relPath, "..") {
		rel = filepath.ToSlash(relPath)
	}
	name := branch
	if name == "" {
		name = filepath.Base(wtRoot)
	}
	repos = append(repos, WorkspaceGitRepo{
		Root:    wtRoot,
		Name:    name,
		RelPath: rel,
		Branch:  branch,
		IsRoot:  false,
		Diffs:   wtDiffs,
	})
	return repos, nil
}

// canonicalPathKey resolves symlinks and Windows 8.3 short names (e.g. RUNNER~1)
// so paths from t.TempDir() and `git worktree list` compare reliably.
func canonicalPathKey(p string) string {
	clean := filepath.Clean(strings.TrimSpace(p))
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil && resolved != "" {
		clean = resolved
	}
	return strings.ToLower(filepath.ToSlash(filepath.Clean(clean)))
}
