package gitx

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
)

const integrationBranch = "hivemind/integracao"

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
