package idecore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// DiffLineKind classifies a line inside a unified diff.
type DiffLineKind string

const (
	DiffHeader  DiffLineKind = "header"
	DiffHunk    DiffLineKind = "hunk"
	DiffAdded   DiffLineKind = "added"
	DiffRemoved DiffLineKind = "removed"
	DiffContext DiffLineKind = "context"
)

// DiffLine is one colored line in the diff viewer.
type DiffLine struct {
	Kind DiffLineKind `json:"kind"`
	Text string       `json:"text"`
}

// FileDiff holds the parsed patch and line stats for one modified file.
type FileDiff struct {
	Path    string     `json:"path"`
	Added   int        `json:"added"`
	Removed int        `json:"removed"`
	Lines   []DiffLine `json:"lines"`
}

// ParseUnifiedDiff splits git unified diff output into per-file FileDiff entries.
func ParseUnifiedDiff(raw string) []FileDiff {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var diffs []FileDiff
	var current *FileDiff

	flush := func() {
		if current != nil {
			diffs = append(diffs, *current)
			current = nil
		}
	}

	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			path := parseDiffGitPath(line)
			current = &FileDiff{Path: path}
			current.Lines = append(current.Lines, DiffLine{Kind: DiffHeader, Text: line})
			continue
		}
		if current == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "--- "),
			strings.HasPrefix(line, "+++ "),
			strings.HasPrefix(line, "new file mode "),
			strings.HasPrefix(line, "deleted file mode "):
			current.Lines = append(current.Lines, DiffLine{Kind: DiffHeader, Text: line})
		case strings.HasPrefix(line, "@@"):
			current.Lines = append(current.Lines, DiffLine{Kind: DiffHunk, Text: line})
		case strings.HasPrefix(line, "+"):
			current.Added++
			current.Lines = append(current.Lines, DiffLine{Kind: DiffAdded, Text: line})
		case strings.HasPrefix(line, "-"):
			current.Removed++
			current.Lines = append(current.Lines, DiffLine{Kind: DiffRemoved, Text: line})
		default:
			current.Lines = append(current.Lines, DiffLine{Kind: DiffContext, Text: line})
		}
	}
	flush()
	return diffs
}

func parseDiffGitPath(header string) string {
	// Format: diff --git a/path b/path
	idx := strings.LastIndex(header, " b/")
	if idx >= 0 {
		return strings.TrimSpace(header[idx+3:])
	}
	fields := strings.Fields(header)
	if len(fields) >= 4 {
		return strings.TrimPrefix(fields[3], "b/")
	}
	return header
}

// WorkspaceDiff returns parsed unified diffs (staged + unstaged) for workspace.
func WorkspaceDiff(ctx context.Context, workspace string) ([]FileDiff, error) {
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspace) {
		return nil, nil
	}
	raw, err := runGit(ctx, workspace, "diff", "HEAD")
	if err != nil {
		// Repo without HEAD commit yet: fallback to plain diff
		raw, err = runGit(ctx, workspace, "diff")
		if err != nil {
			return nil, err
		}
	}
	return ParseUnifiedDiff(raw), nil
}

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

// CommitAndPush stages all changes, commits with message, and optionally pushes.
func CommitAndPush(ctx context.Context, workspace, message string, push bool) (string, error) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "feat: alterações HiveMind IDE"
	}
	g := gitx.Local{}
	committed, err := g.CommitAll(ctx, workspace, msg)
	if err != nil {
		return "", err
	}
	if !committed {
		return "Nenhuma alteração pendente para commitar.", nil
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

// OpenPullRequest pushes the current branch and opens a GitHub PR via `gh`.
func OpenPullRequest(ctx context.Context, workspace, title, body, baseBranch string) (githubx.PullRequest, error) {
	g := gitx.Local{}
	branch, err := g.CurrentBranch(ctx, workspace)
	if err != nil || branch == "" {
		return githubx.PullRequest{}, fmt.Errorf("não foi possível identificar a branch atual")
	}
	if baseBranch == "" {
		baseBranch = "main"
	}
	if err := g.Push(ctx, workspace, "origin", branch); err != nil {
		return githubx.PullRequest{}, fmt.Errorf("push falhou: %w", err)
	}
	url, err := g.RemoteURL(ctx, workspace)
	if err != nil {
		return githubx.PullRequest{}, err
	}
	owner, repo, ok := githubx.ParseRemote(url)
	if !ok {
		return githubx.PullRequest{}, fmt.Errorf("remote origin não é GitHub: %s", url)
	}
	client := githubx.Detect(owner, repo)
	if !client.Enabled() {
		return githubx.PullRequest{}, fmt.Errorf("gh CLI não está autenticado ou disponível")
	}
	if strings.TrimSpace(title) == "" {
		title = "HiveMind: " + branch
	}
	if strings.TrimSpace(body) == "" {
		body = "Pull Request aberto pela HiveMind IDE."
	}
	return client.CreatePullRequest(ctx, githubx.CreatePR{
		Title: title,
		Head:  branch,
		Base:  baseBranch,
		Body:  body,
	})
}

// CommentJiraIssue adds a comment to a Jira issue using environment or project credentials.
func CommentJiraIssue(ctx context.Context, workspace, issueKey, comment string) error {
	key := strings.TrimSpace(issueKey)
	if key == "" {
		return fmt.Errorf("informe a chave da issue Jira (ex: PROJ-123)")
	}
	client := jirax.FromEnv()
	if !client.Enabled() {
		return fmt.Errorf("credenciais Jira não configuradas (defina JIRA_BASE_URL, JIRA_EMAIL e JIRA_API_TOKEN)")
	}
	if cfg, err := team.Load(workspace); err == nil && !cfg.Settings.JiraEnabled {
		return fmt.Errorf("Jira está desabilitado nas configurações do time")
	}
	return client.Comment(ctx, key, comment)
}

// DiscardWorkspaceChanges restores tracked files in workspace to HEAD.
func DiscardWorkspaceChanges(ctx context.Context, workspace string) error {
	_, err := runGit(ctx, workspace, "checkout", "--", ".")
	return err
}

// GitHubStatusInfo reports whether the GitHub CLI (`gh`) is authenticated and the repo remote.
type GitHubStatusInfo struct {
	Installed bool   `json:"installed"`
	LoggedIn  bool   `json:"loggedIn"`
	User      string `json:"user"`
	Remote    string `json:"remote"`
	Detail    string `json:"detail"`
}

// CheckGitHubStatus inspects `gh` authentication and the workspace `origin` remote.
func CheckGitHubStatus(ctx context.Context, workspace string) GitHubStatusInfo {
	info := GitHubStatusInfo{}
	if _, err := exec.LookPath("gh"); err != nil {
		info.Detail = "gh CLI não instalado"
		return info
	}
	info.Installed = true
	cmd := exec.CommandContext(ctx, "gh", "api", "user", "--jq", ".login")
	hideWindow(cmd)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err == nil {
		info.LoggedIn = true
		info.User = strings.TrimSpace(out.String())
		info.Detail = "Conectado como @" + info.User
	} else {
		info.Detail = "Não autenticado (rode `gh auth login`)"
	}
	if workspace != "" {
		g := gitx.Local{}
		if g.IsRepo(ctx, workspace) {
			if url, err := g.RemoteURL(ctx, workspace); err == nil {
				info.Remote = url
			}
		}
	}
	return info
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
	if name == "" {
		return fmt.Errorf("informe o nome da branch")
	}
	if create {
		_, err := runGit(ctx, workspace, "checkout", "-b", name)
		return err
	}
	_, err := runGit(ctx, workspace, "checkout", name)
	return err
}

// ProjectIssue represents a Jira or GitHub issue listed in the IDE sidebar.
type ProjectIssue struct {
	Source  string `json:"source"` // "jira" or "github"
	Key     string `json:"key"`    // e.g. "PROJ-12" or "#42"
	Title   string `json:"title"`
	Body    string `json:"body,omitempty"`
	Status  string `json:"status,omitempty"`
}

// ListProjectIssues fetches open Jira and/or GitHub issues for one-click squad execution.
func ListProjectIssues(ctx context.Context, workspace string) ([]ProjectIssue, error) {
	var issues []ProjectIssue

	// 1. Jira issues if enabled and configured
	if cfg, err := team.Load(workspace); err == nil && cfg.Settings.JiraEnabled {
		jc := jirax.FromEnv()
		if jc.Enabled() {
			jql := "resolution = Unresolved ORDER BY updated DESC"
			if strings.TrimSpace(cfg.Settings.JiraProject) != "" {
				jql = fmt.Sprintf("project = %s AND resolution = Unresolved ORDER BY updated DESC", strings.TrimSpace(cfg.Settings.JiraProject))
			}
			if list, err := jc.Search(ctx, jql, 15); err == nil {
				for _, it := range list {
					issues = append(issues, ProjectIssue{
						Source: "jira",
						Key:    it.Key,
						Title:  it.Summary,
						Body:   it.Description,
						Status: it.Status,
					})
				}
			}
		}
	}

	// 2. GitHub issues if git origin is GitHub and `gh` is available
	g := gitx.Local{}
	if g.IsRepo(ctx, workspace) {
		if url, err := g.RemoteURL(ctx, workspace); err == nil {
			if owner, repo, ok := githubx.ParseRemote(url); ok {
				if ghBin, err := exec.LookPath("gh"); err == nil {
					cmd := exec.CommandContext(ctx, ghBin, "issue", "list", "--repo", owner+"/"+repo, "--state", "open", "--limit", "15", "--json", "number,title,body,state")
					hideWindow(cmd)
					var out bytes.Buffer
					cmd.Stdout = &out
					if err := cmd.Run(); err == nil {
						var raw []struct {
							Number int    `json:"number"`
							Title  string `json:"title"`
							Body   string `json:"body"`
							State  string `json:"state"`
						}
						if json.Unmarshal(out.Bytes(), &raw) == nil {
							for _, ghIt := range raw {
								issues = append(issues, ProjectIssue{
									Source: "github",
									Key:    fmt.Sprintf("#%d", ghIt.Number),
									Title:  ghIt.Title,
									Body:   ghIt.Body,
									Status: ghIt.State,
								})
							}
						}
					}
				}
			}
		}
	}

	return issues, nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	hideWindow(cmd)
	cmd.Dir = dir
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}
