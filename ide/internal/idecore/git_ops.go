package idecore

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/procx"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// FastCurrentBranch reads <workspace>/.git/HEAD directly without spawning git.
// Returns empty string when .git is absent, unreadable, or a worktree file pointer.
func FastCurrentBranch(workspace string) string {
	gitPath := filepath.Join(workspace, ".git")
	info, err := os.Stat(gitPath)
	if err != nil || !info.IsDir() {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(gitPath, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(data))
	if strings.HasPrefix(head, "ref: refs/heads/") {
		return strings.TrimSpace(strings.TrimPrefix(head, "ref: refs/heads/"))
	}
	if strings.HasPrefix(head, "ref: ") {
		ref := strings.TrimSpace(strings.TrimPrefix(head, "ref: "))
		if idx := strings.LastIndex(ref, "/"); idx >= 0 && idx+1 < len(ref) {
			return ref[idx+1:]
		}
		return ref
	}
	if len(head) >= 7 {
		return head[:7]
	}
	return ""
}

// CurrentBranch returns the active git branch or empty string if not a git repo.
func CurrentBranch(ctx context.Context, workspace string) string {
	if b := FastCurrentBranch(workspace); b != "" {
		return b
	}
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspace) {
		return ""
	}
	b, err := g.CurrentBranch(ctx, workspace)
	if err != nil {
		return ""
	}
	return b
}

// CommitAndPush commits staged changes (or all changes when nothing is staged) and optionally pushes.
func CommitAndPush(ctx context.Context, workspace, message string, push bool) (string, error) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "feat: alterações HiveMind IDE"
	}
	g := gitx.Local{}
	// If user explicitly staged specific files, commit only the staged index.
	if hasStagedChanges(ctx, workspace) {
		if _, err := runGit(ctx, workspace, "commit", "-m", msg); err != nil {
			return "", err
		}
	} else {
		committed, err := g.CommitAll(ctx, workspace, msg)
		if err != nil {
			return "", err
		}
		if !committed {
			return "Nenhuma alteração pendente para commitar.", nil
		}
	}
	branch, _ := g.CurrentBranch(ctx, workspace)
	if push && branch != "" {
		if err := g.Push(ctx, workspace, "origin", branch); err != nil {
			return fmt.Sprintf("Commit criado em %s, mas push falhou: %v", branch, err), err
		}
		return fmt.Sprintf("Commit e push concluídos na branch %s.", branch), nil
	}
	return fmt.Sprintf("Commit criado em %s.", branch), nil
}

func hasStagedChanges(ctx context.Context, workspace string) bool {
	cmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--quiet")
	procx.ConfigureContext(cmd)
	cmd.Dir = workspace
	err := cmd.Run()
	return err != nil
}

// DiscardWorkspaceChanges restores tracked files to HEAD and cleans untracked files (preserving .hivemind/).
func DiscardWorkspaceChanges(ctx context.Context, workspaceDir string) error {
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspaceDir) {
		return nil
	}
	if g.HasCommit(ctx, workspaceDir) {
		if _, err := runGit(ctx, workspaceDir, "checkout", "--", "."); err != nil {
			return err
		}
	}
	_, err := runGit(ctx, workspaceDir, "clean", "-fd", "-e", ".hivemind", "-e", ".hivemind/")
	return err
}

// DiscardFileChanges restores a single tracked file to HEAD or deletes an untracked file inside workspaceDir.
func DiscardFileChanges(ctx context.Context, workspaceDir, relPath string) error {
	relClean := filepath.ToSlash(strings.TrimSpace(relPath))
	if relClean == "" || relClean == ".hivemind" || strings.HasPrefix(relClean, ".hivemind/") {
		return fmt.Errorf("caminho de arquivo inválido: %s", relPath)
	}
	full, err := workspace.Resolve(workspaceDir, filepath.FromSlash(relClean))
	if err != nil {
		return err
	}
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspaceDir) {
		return fmt.Errorf("o diretório não é um repositório Git")
	}
	// Check if file is tracked in HEAD (literal pathspec so file names with
	// metacharacters ("-", ":", ":(top)", globs) are never reinterpreted by git).
	if g.HasCommit(ctx, workspaceDir) {
		lit := gitx.LiteralPathspec(relClean)
		if _, err := runGit(ctx, workspaceDir, "ls-files", "--error-unmatch", "--", lit); err == nil {
			_, _ = runGit(ctx, workspaceDir, "reset", "HEAD", "--", lit)
			_, err := runGit(ctx, workspaceDir, "checkout", "--", lit)
			return err
		}
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// InitGitRepo initializes a git repository in workspace.
func InitGitRepo(ctx context.Context, workspace string) error {
	_, err := runGit(ctx, workspace, "init", "-b", "main")
	return err
}

// ListGitBranches returns local branch names for workspace.
func ListGitBranches(ctx context.Context, workspace string) ([]string, error) {
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspace) {
		return nil, nil
	}
	raw, err := runGit(ctx, workspace, "branch", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		b := strings.TrimSpace(line)
		if b != "" {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		if cur := FastCurrentBranch(workspace); cur != "" {
			out = append(out, cur)
		}
	}
	return out, nil
}

// CheckoutGitBranch switches to an existing branch or creates a new one when create is true.
func CheckoutGitBranch(ctx context.Context, workspace, branch string, create bool) error {
	name := strings.TrimSpace(branch)
	if err := gitx.ValidateBranchName(name); err != nil {
		return err
	}
	if create {
		_, err := runGit(ctx, workspace, "checkout", "-b", name)
		return err
	}
	_, err := runGit(ctx, workspace, "checkout", name)
	return err
}

// GitGraphCommit represents a single commit node for the VS Code-style Source Control Graph.
type GitGraphCommit struct {
	Hash    string   `json:"hash"`
	Short   string   `json:"short"`
	Parents []string `json:"parents"`
	Author  string   `json:"author"`
	Date    string   `json:"date"`
	Subject string   `json:"subject"`
	Refs    []string `json:"refs,omitempty"`
}

// ParseGitLogGraph parses `git log --format=%H%x1f%h%x1f%P%x1f%an%x1f%ar%x1f%s%x1f%D%x1e` records.
func ParseGitLogGraph(raw string) []GitGraphCommit {
	records := strings.Split(raw, "\x1e")
	out := make([]GitGraphCommit, 0, len(records))
	for _, rec := range records {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		parts := strings.Split(rec, "\x1f")
		if len(parts) < 6 {
			continue
		}
		var parents []string
		if p := strings.TrimSpace(parts[2]); p != "" {
			parents = strings.Fields(p)
		}
		var refs []string
		if len(parts) >= 7 && strings.TrimSpace(parts[6]) != "" {
			for _, r := range strings.Split(parts[6], ",") {
				trimmed := strings.TrimSpace(r)
				if trimmed != "" {
					refs = append(refs, trimmed)
				}
			}
		}
		out = append(out, GitGraphCommit{
			Hash:    strings.TrimSpace(parts[0]),
			Short:   strings.TrimSpace(parts[1]),
			Parents: parents,
			Author:  strings.TrimSpace(parts[3]),
			Date:    strings.TrimSpace(parts[4]),
			Subject: strings.TrimSpace(parts[5]),
			Refs:    refs,
		})
	}
	return out
}

// ListGitGraph returns recent commits in topological order for the Source Control Graph.
func ListGitGraph(ctx context.Context, workspaceDir string, limit int, allBranches bool) ([]GitGraphCommit, error) {
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspaceDir) || !g.HasCommit(ctx, workspaceDir) {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	args := []string{
		"log",
		"--topo-order",
		fmt.Sprintf("-n%d", limit),
		"--format=%H%x1f%h%x1f%P%x1f%an%x1f%ar%x1f%s%x1f%D%x1e",
	}
	if allBranches {
		args = append(args, "--all")
	}
	raw, err := runGit(ctx, workspaceDir, args...)
	if err != nil {
		return nil, err
	}
	return ParseGitLogGraph(raw), nil
}

// CommitDetail holds commit metadata and parsed file diffs for viewing a commit from the Graph.
type CommitDetail struct {
	Hash    string     `json:"hash"`
	Short   string     `json:"short"`
	Author  string     `json:"author"`
	Date    string     `json:"date"`
	Subject string     `json:"subject"`
	Body    string     `json:"body,omitempty"`
	Files   []FileDiff `json:"files"`
}

// GetCommitDetail returns commit metadata and per-file diffs for hash.
func GetCommitDetail(ctx context.Context, workspaceDir, hash string) (CommitDetail, error) {
	clean := strings.TrimSpace(hash)
	if clean == "" || strings.HasPrefix(clean, "-") {
		return CommitDetail{}, fmt.Errorf("hash de commit inválido")
	}
	metaRaw, err := runGit(ctx, workspaceDir, "show", "--no-patch", "--format=%H%x1f%h%x1f%an%x1f%ar%x1f%s%x1f%b", clean)
	if err != nil {
		return CommitDetail{}, err
	}
	parts := strings.SplitN(strings.TrimRight(metaRaw, "\r\n"), "\x1f", 6)
	detail := CommitDetail{Hash: clean}
	if len(parts) >= 5 {
		detail.Hash = parts[0]
		detail.Short = parts[1]
		detail.Author = parts[2]
		detail.Date = parts[3]
		detail.Subject = parts[4]
	}
	if len(parts) >= 6 {
		detail.Body = strings.TrimSpace(parts[5])
	}
	patchRaw, err := runGit(ctx, workspaceDir, "show", "--format=", clean)
	if err != nil {
		return detail, err
	}
	detail.Files = ParseUnifiedDiff(patchRaw)
	return detail, nil
}

// RunGitAction executes safe, allowlisted Source Control actions (pull, push, fetch, sync, stash, stash-pop).
func RunGitAction(ctx context.Context, workspaceDir, action string) (string, error) {
	res, err := RunGitActionProgress(ctx, workspaceDir, action, nil)
	if err != nil {
		return "", err
	}
	return res.Summary, nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	procx.ConfigureContext(cmd)
	cmd.Dir = dir
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}
