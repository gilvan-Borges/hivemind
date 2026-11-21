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

// DiscoverGitRepos scans workspaceDir for Git repositories (root and nested up to depth 3),
// skipping build/vendor/worktree folders, and returns branch and working-tree diffs for each.
func DiscoverGitRepos(ctx context.Context, workspaceDir string) ([]WorkspaceGitRepo, error) {
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

	type discovered struct {
		root    string
		relPath string
		isRoot  bool
	}
	g := gitx.Local{}
	var found []discovered
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
			found = append(found, discovered{
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
	if len(found) == 0 {
		return []WorkspaceGitRepo{}, nil
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].isRoot != found[j].isRoot {
			return found[i].isRoot
		}
		return strings.ToLower(found[i].relPath) < strings.ToLower(found[j].relPath)
	})

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
			name := filepath.Base(item.root)
			if item.relPath != "" {
				name = filepath.Base(item.relPath)
			}
			repos[idx] = WorkspaceGitRepo{
				Root:    item.root,
				Name:    name,
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
