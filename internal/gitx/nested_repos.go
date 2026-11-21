package gitx

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const maxNestedRepoDepth = 3

var nestedRepoSkipDirs = map[string]bool{
	".git":         true,
	".hivemind":    true,
	"node_modules": true,
	"target":       true,
	"dist":         true,
	"build":        true,
	".next":        true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
	".gradle":      true,
	".dart_tool":   true,
	"vendor":       true,
	"out":          true,
	"coverage":     true,
}

// NestedRepo describes an independent Git repository nested inside a parent
// workspace (e.g. ipg-mobile inside ipg, or HiveMind-App inside hivemind).
type NestedRepo struct {
	RepoDir    string
	RelPath    string
	BaseBranch string
}

// NestedWorktree describes an isolated Git worktree for a nested repository
// mounted at the exact same relative path inside a parent worktree.
type NestedWorktree struct {
	NestedRepo
	WorktreeDir string
	Branch      string
}

// DiscoverNestedRepos scans workspaceRoot (depth 1..maxNestedRepoDepth) for
// independent Git repositories that already have at least one commit.
func DiscoverNestedRepos(ctx context.Context, g Git, workspaceRoot string) []NestedRepo {
	if g == nil || strings.TrimSpace(workspaceRoot) == "" {
		return nil
	}
	absRoot, err := filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil {
		return nil
	}
	absRoot = filepath.Clean(absRoot)
	info, err := os.Stat(absRoot)
	if err != nil || !info.IsDir() {
		return nil
	}

	var out []NestedRepo
	seen := make(map[string]bool)

	var walk func(dir, rel string, depth int)
	walk = func(dir, rel string, depth int) {
		if ctx != nil && ctx.Err() != nil {
			return
		}
		key := strings.ToLower(filepath.Clean(dir))
		if seen[key] {
			return
		}
		seen[key] = true

		if depth > 0 {
			if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr == nil && g.IsRepo(ctx, dir) && g.HasCommit(ctx, dir) {
				baseBr, _ := g.CurrentBranch(ctx, dir)
				baseBr = strings.TrimSpace(baseBr)
				if baseBr == "" || baseBr == "HEAD" {
					baseBr = "main"
				}
				out = append(out, NestedRepo{
					RepoDir:    dir,
					RelPath:    filepath.ToSlash(rel),
					BaseBranch: baseBr,
				})
				// Do not recurse into a discovered nested repository's build/subfolders.
				return
			}
		}

		if depth >= maxNestedRepoDepth {
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
			if nestedRepoSkipDirs[name] || strings.HasPrefix(name, ".") {
				continue
			}
			childRel := name
			if rel != "" {
				childRel = filepath.Join(rel, name)
			}
			walk(filepath.Join(dir, name), childRel, depth+1)
		}
	}

	walk(absRoot, "", 0)
	sort.Slice(out, func(i, j int) bool {
		return out[i].RelPath < out[j].RelPath
	})
	return out
}

// ensureNestedExcludes adds each unignored nested repo path to the parent
// repository's local .git/info/exclude so parent `git status`, `git clean -fd`,
// and `git add -A` never sweep nested repo files or gitlinks into the parent repo.
func ensureNestedExcludes(ctx context.Context, g Git, parentRepo string) {
	nested := DiscoverNestedRepos(ctx, g, parentRepo)
	if len(nested) == 0 {
		return
	}
	commonDirRaw, err := g.Run(ctx, parentRepo, "rev-parse", "--git-common-dir")
	if err != nil {
		return
	}
	commonDir := strings.TrimSpace(commonDirRaw)
	if commonDir == "" {
		return
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(parentRepo, commonDir)
	}
	excludePath := filepath.Join(filepath.Clean(commonDir), "info", "exclude")
	existingBytes, _ := os.ReadFile(excludePath)
	existing := string(existingBytes)

	var missing []string
	for _, nr := range nested {
		if _, ignErr := g.Run(ctx, parentRepo, "check-ignore", "-q", nr.RelPath); ignErr == nil {
			continue
		}
		entry := nr.RelPath + "/"
		if containsExcludeLine(existing, nr.RelPath) {
			continue
		}
		missing = append(missing, entry)
	}
	if len(missing) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return
	}
	var b strings.Builder
	b.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		b.WriteByte('\n')
	}
	if !strings.Contains(existing, "# HiveMind nested repositories") {
		b.WriteString("# HiveMind nested repositories\n")
	}
	for _, m := range missing {
		b.WriteString(m)
		b.WriteByte('\n')
	}
	_ = os.WriteFile(excludePath, []byte(b.String()), 0o644)
}

func containsExcludeLine(content, relSlash string) bool {
	trimmedTarget := strings.TrimSuffix(strings.TrimSpace(relSlash), "/")
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		clean := strings.TrimSuffix(strings.TrimSpace(line), "/")
		if clean == trimmedTarget {
			return true
		}
	}
	return false
}

func sameGitCommonDir(ctx context.Context, g Git, a, b string) bool {
	if !g.IsRepo(ctx, a) || !g.IsRepo(ctx, b) {
		return false
	}
	ra, errA := g.Run(ctx, a, "rev-parse", "--git-common-dir")
	rb, errB := g.Run(ctx, b, "rev-parse", "--git-common-dir")
	if errA != nil || errB != nil {
		return false
	}
	ca := strings.TrimSpace(ra)
	if !filepath.IsAbs(ca) {
		ca = filepath.Join(a, ca)
	}
	cb := strings.TrimSpace(rb)
	if !filepath.IsAbs(cb) {
		cb = filepath.Join(b, cb)
	}
	return strings.EqualFold(filepath.Clean(ca), filepath.Clean(cb))
}

func resolveUniqueNestedBranch(ctx context.Context, g Git, repoDir, desiredBranch, targetWT string) string {
	branch := strings.TrimSpace(desiredBranch)
	if branch == "" {
		branch = "hivemind/nested"
	}
	if ValidateBranchName(branch) != nil {
		branch = "hivemind/nested"
	}
	if cur, err := g.CurrentBranch(ctx, repoDir); err == nil && cur == branch {
		branch += "-wt"
	}
	if isBranchCheckedOutElsewhere(ctx, g, repoDir, branch, targetWT) {
		branch += "-sub"
	}
	return branch
}

// PrepareNestedWorktrees mounts an isolated git worktree for every nested Git
// repository found in workspaceRoot at the exact same relative path inside parentWT.
func PrepareNestedWorktrees(ctx context.Context, g Git, workspaceRoot, parentWT, targetBranch string, preserveExisting bool) ([]NestedWorktree, error) {
	if g == nil || strings.TrimSpace(parentWT) == "" {
		return nil, nil
	}
	nested := DiscoverNestedRepos(ctx, g, workspaceRoot)
	if len(nested) == 0 {
		return nil, nil
	}
	ensureNestedExcludes(ctx, g, workspaceRoot)

	mounted := make([]NestedWorktree, 0, len(nested))
	for _, nr := range nested {
		subWT := filepath.Join(parentWT, filepath.FromSlash(nr.RelPath))
		if _, err := os.Stat(filepath.Join(subWT, ".git")); err == nil && sameGitCommonDir(ctx, g, nr.RepoDir, subWT) {
			curBr, _ := g.CurrentBranch(ctx, subWT)
			curBr = strings.TrimSpace(curBr)
			if curBr == "" {
				curBr = resolveUniqueNestedBranch(ctx, g, nr.RepoDir, targetBranch, subWT)
			}
			mounted = append(mounted, NestedWorktree{
				NestedRepo:  nr,
				WorktreeDir: subWT,
				Branch:      curBr,
			})
			continue
		}

		_ = os.RemoveAll(subWT)
		if err := os.MkdirAll(filepath.Dir(subWT), 0o755); err != nil {
			continue
		}
		_, _ = g.Run(ctx, nr.RepoDir, "worktree", "prune")

		subBranch := resolveUniqueNestedBranch(ctx, g, nr.RepoDir, targetBranch, subWT)
		if preserveExisting && !g.BranchExists(ctx, nr.RepoDir, subBranch) {
			_, _ = g.Run(ctx, nr.RepoDir, "fetch", "origin", subBranch+":"+subBranch)
		}

		var wtErr error
		if g.BranchExists(ctx, nr.RepoDir, subBranch) {
			_, wtErr = g.Run(ctx, nr.RepoDir, "worktree", "add", "-f", subWT, subBranch)
		} else {
			baseSHA, shaErr := g.Run(ctx, nr.RepoDir, "rev-parse", "HEAD")
			if shaErr != nil {
				continue
			}
			_, wtErr = g.Run(ctx, nr.RepoDir, "worktree", "add", "-f", "-b", subBranch, subWT, strings.TrimSpace(baseSHA))
		}
		if wtErr != nil {
			continue
		}
		mounted = append(mounted, NestedWorktree{
			NestedRepo:  nr,
			WorktreeDir: subWT,
			Branch:      subBranch,
		})
	}
	return mounted, nil
}

// AlignNestedWorktrees mounts/resets isolated worktrees for nested repositories
// inside a parallel developer's isolated dev worktree (`devWT`), aligned to the
// conversation's delivery branch or HEAD.
func AlignNestedWorktrees(ctx context.Context, g Git, devWT, devBranch string, baseNested []NestedWorktree) ([]NestedWorktree, error) {
	if g == nil || strings.TrimSpace(devWT) == "" || len(baseNested) == 0 {
		return nil, nil
	}
	aligned := make([]NestedWorktree, 0, len(baseNested))
	for _, baseNW := range baseNested {
		subWT := filepath.Join(devWT, filepath.FromSlash(baseNW.RelPath))
		subBranch := resolveUniqueNestedBranch(ctx, g, baseNW.RepoDir, devBranch, subWT)
		baseRef := strings.TrimSpace(baseNW.Branch)
		if baseRef == "" || !g.BranchExists(ctx, baseNW.RepoDir, baseRef) {
			sha, err := g.Run(ctx, baseNW.RepoDir, "rev-parse", "HEAD")
			if err != nil {
				continue
			}
			baseRef = strings.TrimSpace(sha)
		}

		if _, err := os.Stat(filepath.Join(subWT, ".git")); err != nil || !sameGitCommonDir(ctx, g, baseNW.RepoDir, subWT) {
			_ = os.RemoveAll(subWT)
			if err := os.MkdirAll(filepath.Dir(subWT), 0o755); err != nil {
				continue
			}
			_, _ = g.Run(ctx, baseNW.RepoDir, "worktree", "prune")
			if _, err := g.Run(ctx, baseNW.RepoDir, "worktree", "add", "-f", "-B", subBranch, subWT, baseRef); err != nil {
				continue
			}
		} else {
			_, _ = g.Run(ctx, subWT, "reset", "--hard")
			if _, err := g.Run(ctx, subWT, "checkout", "-f", "-B", subBranch, baseRef); err != nil {
				continue
			}
			_, _ = g.Run(ctx, subWT, "clean", "-fd")
		}
		aligned = append(aligned, NestedWorktree{
			NestedRepo:  baseNW.NestedRepo,
			WorktreeDir: subWT,
			Branch:      subBranch,
		})
	}
	return aligned, nil
}

// SyncCompositeUncommittedToDelivery consolidates uncommitted changes across the
// root repository worktree AND every nested repository worktree mounted inside it.
func SyncCompositeUncommittedToDelivery(ctx context.Context, g Git, devRoots []string, deliveryRoot string, nestedRelPaths []string) ([]string, error) {
	synced, err := SyncUncommittedToDelivery(ctx, g, devRoots, deliveryRoot)
	if err != nil {
		return synced, err
	}
	for _, rel := range nestedRelPaths {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		relOS := filepath.FromSlash(rel)
		subDelivery := filepath.Join(deliveryRoot, relOS)
		subDevs := make([]string, 0, len(devRoots))
		for _, dr := range devRoots {
			if strings.TrimSpace(dr) == "" {
				continue
			}
			subDevs = append(subDevs, filepath.Join(dr, relOS))
		}
		subSynced, subErr := SyncUncommittedToDelivery(ctx, g, subDevs, subDelivery)
		if subErr != nil {
			return synced, subErr
		}
		for _, f := range subSynced {
			synced = append(synced, rel+"/"+f)
		}
	}
	sort.Strings(synced)
	return synced, nil
}

// HasCommitsAhead reports whether branch has at least one commit ahead of baseBranch
// (checking baseBranch, origin/<baseBranch>, or repo HEAD).
func HasCommitsAhead(ctx context.Context, g Git, repoDir, branch, baseBranch string) bool {
	if g == nil || strings.TrimSpace(repoDir) == "" || strings.TrimSpace(branch) == "" {
		return false
	}
	if !g.BranchExists(ctx, repoDir, branch) {
		return false
	}
	baseRef := ""
	for _, cand := range []string{strings.TrimSpace(baseBranch), "origin/" + strings.TrimSpace(baseBranch), "HEAD"} {
		if cand == "" || cand == "origin/" {
			continue
		}
		if _, err := g.Run(ctx, repoDir, "rev-parse", "--verify", "--quiet", cand); err == nil {
			baseRef = cand
			break
		}
	}
	if baseRef == "" {
		return false
	}
	raw, err := g.Run(ctx, repoDir, "rev-list", "--count", baseRef+".."+branch)
	if err != nil {
		return false
	}
	n, _ := strconv.Atoi(strings.TrimSpace(raw))
	return n > 0
}

// removeNestedWorktreesInside tears down any nested repository worktrees inside
// wtPath in deepest-first order before the parent worktree is removed.
func removeNestedWorktreesInside(ctx context.Context, g Git, parentRepo, wtPath, deleteBranch string) {
	nested := DiscoverNestedRepos(ctx, g, parentRepo)
	if len(nested) == 0 {
		return
	}
	// Reverse order ensures deeper paths are removed before shallower ones.
	for i := len(nested) - 1; i >= 0; i-- {
		nr := nested[i]
		subWT := filepath.Join(wtPath, filepath.FromSlash(nr.RelPath))
		if _, err := os.Stat(subWT); err != nil {
			continue
		}
		subBranch := strings.TrimSpace(deleteBranch)
		if subBranch == "" {
			if cur, err := g.CurrentBranch(ctx, subWT); err == nil {
				subBranch = cur
			}
		}
		removeSingleWorktree(ctx, g, nr.RepoDir, subWT, subBranch)
	}
}
