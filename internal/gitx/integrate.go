package gitx

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// IntegrationBranch is the dedicated branch used to merge parallel developer worktrees.
const IntegrationBranch = "hivemind/integracao"

const integrationBranch = IntegrationBranch

// Integrate commits nothing by itself. It merges branches that are ahead of
// hivemind/integracao into a dedicated worktree. The user's checkout is not
// switched. Branches with no new commits are skipped and never described as merged.
func (g Local) Integrate(ctx context.Context, repo string, branches []string) (merged []string, integration string, err error) {
	if !g.IsRepo(ctx, repo) || !g.HasCommit(ctx, repo) {
		return nil, "", nil
	}
	if !g.BranchExists(ctx, repo, integrationBranch) {
		if _, err = g.Run(ctx, repo, "branch", integrationBranch, "HEAD"); err != nil {
			return nil, "", err
		}
	}
	wt, err := g.EnsureWorktree(ctx, repo, "_integracao", integrationBranch)
	if err != nil {
		return nil, "", err
	}
	if len(branches) == 0 {
		if headSHA, shaErr := g.Run(ctx, repo, "rev-parse", "HEAD"); shaErr == nil && strings.TrimSpace(headSHA) != "" {
			_, _ = g.Run(ctx, wt, "merge", "--ff-only", strings.TrimSpace(headSHA))
		}
	}
	for _, branch := range branches {
		branch = strings.TrimSpace(branch)
		if branch == "" || branch == integrationBranch {
			continue
		}
		if !g.BranchExists(ctx, repo, branch) {
			continue
		}
		countRaw, err := g.Run(ctx, wt, "rev-list", "--count", "HEAD.."+branch)
		if err != nil {
			return merged, wt, err
		}
		count, _ := strconv.Atoi(strings.TrimSpace(countRaw))
		if count == 0 {
			continue
		}
		if _, err = g.Run(ctx, wt, "merge", "--no-ff", "--no-edit", branch); err != nil {
			return merged, wt, err
		}
		merged = append(merged, branch)
	}
	return merged, wt, nil
}

// EnsureHiveIgnore appends .hivemind/ to the project gitignore when missing.
func EnsureHiveIgnore(dir string) error {
	path := filepath.Join(dir, ".gitignore")
	data, err := osRead(path)
	if err != nil && !osNotExist(err) {
		return err
	}
	text := string(data)
	if strings.Contains(text, ".hivemind/") {
		return nil
	}
	var b strings.Builder
	b.WriteString(text)
	if text != "" && !strings.HasSuffix(text, "\n") {
		b.WriteByte('\n')
	}
	if text != "" {
		b.WriteByte('\n')
	}
	b.WriteString("# HiveMind\n.hivemind/\n")
	return osWrite(path, []byte(b.String()))
}

// PrepareDeliveryWorktree ensures .hivemind/worktrees/_integracao exists and is checked out
// on targetBranch (creating or updating it from repo HEAD unless preserveExisting is true).
// Returns the worktree path and the effective branch name without touching the user's checkout.
func PrepareDeliveryWorktree(ctx context.Context, g Git, repo, targetBranch string, preserveExisting bool) (string, string, error) {
	if !g.IsRepo(ctx, repo) || !g.HasCommit(ctx, repo) {
		return "", "", nil
	}
	branch := strings.TrimSpace(targetBranch)
	if branch == "" {
		branch = IntegrationBranch
	}
	// Avoid checking out a branch that is currently checked out in the main repo working tree.
	if cur, err := g.CurrentBranch(ctx, repo); err == nil && cur != "" && cur == branch {
		branch = branch + "-wt"
	}
	if !g.BranchExists(ctx, repo, IntegrationBranch) {
		_, _ = g.Run(ctx, repo, "branch", IntegrationBranch, "HEAD")
	}
	wt, err := g.EnsureWorktree(ctx, repo, "_integracao", IntegrationBranch)
	if err != nil {
		return "", "", err
	}
	if preserveExisting && !g.BranchExists(ctx, repo, branch) {
		// Best-effort fetch from origin if working on an existing remote PR branch.
		_, _ = g.Run(ctx, repo, "fetch", "origin", branch+":"+branch)
	}
	if g.BranchExists(ctx, repo, branch) {
		if _, err := g.Run(ctx, wt, "checkout", "-f", branch); err != nil {
			return "", "", err
		}
	} else {
		baseSHA, err := g.Run(ctx, repo, "rev-parse", "HEAD")
		if err != nil {
			return "", "", err
		}
		if _, err := g.Run(ctx, wt, "checkout", "-B", branch, strings.TrimSpace(baseSHA)); err != nil {
			return "", "", err
		}
	}
	_, _ = g.Run(ctx, wt, "clean", "-fd")
	return wt, branch, nil
}

// AlignDeveloperWorktree resets or creates a developer worktree aligned to baseRef
// so stale uncommitted edits from previous runs do not leak into a new task.
// Plain `git checkout -B` KEEPS modifications to tracked files and `clean -fd`
// only removes untracked ones, so the reset must discard tracked edits explicitly
// before switching (dogfooding achado #11: dirty state from run N-1 was
// consolidated as the delivery of run N).
func AlignDeveloperWorktree(ctx context.Context, g Git, repo, agentADK, branch, baseRef string) (string, error) {
	wt, err := g.EnsureWorktree(ctx, repo, agentADK, branch)
	if err != nil {
		return "", err
	}
	base := strings.TrimSpace(baseRef)
	if base == "" {
		sha, shaErr := g.Run(ctx, repo, "rev-parse", "HEAD")
		if shaErr != nil {
			return "", shaErr
		}
		base = strings.TrimSpace(sha)
	}
	if _, err := g.Run(ctx, wt, "reset", "--hard"); err != nil {
		return "", err
	}
	if _, err := g.Run(ctx, wt, "checkout", "-f", "-B", branch, base); err != nil {
		return "", err
	}
	if _, err := g.Run(ctx, wt, "clean", "-fd"); err != nil {
		return "", err
	}
	return wt, nil
}

// SyncUncommittedToDelivery copies uncommitted file additions, modifications, and deletions
// from each developer worktree into deliveryWT without creating commits in developer worktrees.
// Commit authority remains exclusively with the Chief or Architect on deliveryWT.
func SyncUncommittedToDelivery(ctx context.Context, g Git, devRoots []string, deliveryWT string) ([]string, error) {
	if deliveryWT == "" {
		return nil, nil
	}
	seen := map[string]bool{}
	for _, devRoot := range devRoots {
		if devRoot == "" || devRoot == deliveryWT {
			continue
		}
		// Diff tracked modifications/deletions against HEAD + list untracked files.
		trackedRaw, _ := g.Run(ctx, devRoot, "diff", "--name-status", "HEAD")
		for _, line := range strings.Split(strings.ReplaceAll(trackedRaw, "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) < 2 {
				continue
			}
			code := parts[0]
			switch {
			case strings.HasPrefix(code, "R") && len(parts) >= 3:
				removeInTree(deliveryWT, parts[1])
				if err := copyBetweenTrees(devRoot, deliveryWT, parts[2]); err == nil {
					seen[parts[2]] = true
				}
			case code == "D":
				removeInTree(deliveryWT, parts[1])
				seen[parts[1]] = true
			default:
				if err := copyBetweenTrees(devRoot, deliveryWT, parts[1]); err == nil {
					seen[parts[1]] = true
				}
			}
		}
		untrackedRaw, _ := g.Run(ctx, devRoot, "ls-files", "--others", "--exclude-standard")
		for _, line := range strings.Split(strings.ReplaceAll(untrackedRaw, "\r\n", "\n"), "\n") {
			rel := strings.TrimSpace(line)
			if rel == "" || rel == ".hivemind" || strings.HasPrefix(rel, ".hivemind/") {
				continue
			}
			if err := copyBetweenTrees(devRoot, deliveryWT, rel); err == nil {
				seen[rel] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for rel := range seen {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out, nil
}

// UpdateIntegrationRef fast-forwards/points hivemind/integracao at deliveryWT's HEAD
// so TUI and IDE diff viewers always find the latest consolidated commit.
func UpdateIntegrationRef(ctx context.Context, g Git, repo, deliveryWT string) {
	if repo == "" || deliveryWT == "" {
		return
	}
	cur, err := g.CurrentBranch(ctx, deliveryWT)
	if err != nil || cur == IntegrationBranch {
		return
	}
	if sha, shaErr := g.Run(ctx, deliveryWT, "rev-parse", "HEAD"); shaErr == nil && strings.TrimSpace(sha) != "" {
		_, _ = g.Run(ctx, repo, "branch", "-f", IntegrationBranch, strings.TrimSpace(sha))
	}
}

func copyBetweenTrees(srcRoot, dstRoot, relSlash string) error {
	relOS := filepath.FromSlash(relSlash)
	src, err := workspace.Resolve(srcRoot, relOS)
	if err != nil {
		return err
	}
	dst, err := workspace.Resolve(dstRoot, relOS)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil || info.IsDir() {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode().Perm())
}

func removeInTree(dstRoot, relSlash string) {
	if dst, err := workspace.Resolve(dstRoot, filepath.FromSlash(relSlash)); err == nil {
		_ = os.Remove(dst)
	}
}
