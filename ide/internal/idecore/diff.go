package idecore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/procx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
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
	Status  string     `json:"status,omitempty"` // "M", "U", "A", "D", "R"
	Staged  bool       `json:"staged,omitempty"`
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

// WorkspaceDiff returns parsed unified diffs (staged, unstaged, and untracked new files) for workspace.
func WorkspaceDiff(ctx context.Context, workspaceDir string) ([]FileDiff, error) {
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspaceDir) {
		return nil, nil
	}
	raw, err := runGit(ctx, workspaceDir, "diff", "HEAD")
	if err != nil {
		// Repo without HEAD commit yet: fallback to plain diff
		raw, err = runGit(ctx, workspaceDir, "diff")
		if err != nil {
			return nil, err
		}
	}
	diffs := ParseUnifiedDiff(raw)
	seen := make(map[string]bool, len(diffs))
	for _, d := range diffs {
		seen[d.Path] = true
	}

	if untrackedRaw, uErr := runGit(ctx, workspaceDir, "ls-files", "--others", "--exclude-standard"); uErr == nil {
		for _, line := range strings.Split(strings.ReplaceAll(untrackedRaw, "\r\n", "\n"), "\n") {
			rel := strings.TrimSpace(line)
			if rel == "" || seen[rel] || rel == ".hivemind" || strings.HasPrefix(rel, ".hivemind/") {
				continue
			}
			full, resErr := workspace.Resolve(workspaceDir, filepath.FromSlash(rel))
			if resErr != nil {
				continue
			}
			info, statErr := os.Stat(full)
			if statErr != nil || info.IsDir() || info.Size() > 512*1024 {
				continue
			}
			data, rdErr := os.ReadFile(full)
			if rdErr != nil {
				continue
			}
			fd := buildUntrackedFileDiff(rel, data)
			fd.Status = "U"
			diffs = append(diffs, fd)
			seen[rel] = true
		}
	}

	// Enrich tracked diffs with status letter (M/A/D/R) and staged flag from porcelain status.
	if stRaw, sErr := runGit(ctx, workspaceDir, "status", "--porcelain=v1", "--untracked-files=no"); sErr == nil {
		statusByPath, stagedByPath := parsePorcelainStatus(stRaw)
		for i := range diffs {
			if diffs[i].Status == "" {
				if st, ok := statusByPath[diffs[i].Path]; ok {
					diffs[i].Status = st
				} else {
					diffs[i].Status = "M"
				}
			}
			if stagedByPath[diffs[i].Path] {
				diffs[i].Staged = true
			}
		}
	}
	return diffs, nil
}

func parsePorcelainStatus(raw string) (map[string]string, map[string]bool) {
	statusMap := make(map[string]string)
	stagedMap := make(map[string]bool)
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if len(line) < 4 {
			continue
		}
		x := line[0]
		y := line[1]
		pathPart := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(pathPart, " -> "); idx >= 0 {
			pathPart = strings.TrimSpace(pathPart[idx+4:])
		}
		pathPart = strings.Trim(pathPart, `"`)
		pathPart = filepath.ToSlash(pathPart)
		if pathPart == "" {
			continue
		}
		code := "M"
		switch {
		case x == '?' && y == '?':
			code = "U"
		case x == 'A' || y == 'A':
			code = "A"
		case x == 'D' || y == 'D':
			code = "D"
		case x == 'R' || y == 'R':
			code = "R"
		}
		statusMap[pathPart] = code
		if x != ' ' && x != '?' {
			stagedMap[pathPart] = true
		}
	}
	return statusMap, stagedMap
}

// StageWorkspaceFile stages or unstages a single relative path in workspaceDir.
func StageWorkspaceFile(ctx context.Context, workspaceDir, relPath string, stage bool) error {
	relClean := filepath.ToSlash(strings.TrimSpace(relPath))
	if relClean == "" || relClean == ".hivemind" || strings.HasPrefix(relClean, ".hivemind/") {
		return fmt.Errorf("caminho de arquivo inválido: %s", relPath)
	}
	if _, err := workspace.Resolve(workspaceDir, filepath.FromSlash(relClean)); err != nil {
		return err
	}
	if stage {
		_, err := runGit(ctx, workspaceDir, "add", "--", relClean)
		return err
	}
	g := gitx.Local{}
	if g.HasCommit(ctx, workspaceDir) {
		_, err := runGit(ctx, workspaceDir, "reset", "HEAD", "--", relClean)
		return err
	}
	_, err := runGit(ctx, workspaceDir, "rm", "--cached", "--", relClean)
	return err
}

func buildUntrackedFileDiff(relSlash string, data []byte) FileDiff {
	fd := FileDiff{
		Path: relSlash,
		Lines: []DiffLine{
			{Kind: DiffHeader, Text: fmt.Sprintf("diff --git a/%s b/%s", relSlash, relSlash)},
			{Kind: DiffHeader, Text: "new file mode 100644"},
			{Kind: DiffHeader, Text: "--- /dev/null"},
			{Kind: DiffHeader, Text: "+++ b/" + relSlash},
		},
	}
	probe := data
	if len(probe) > 512 {
		probe = probe[:512]
	}
	for _, b := range probe {
		if b == 0 {
			fd.Lines = append(fd.Lines, DiffLine{Kind: DiffContext, Text: "(arquivo binário novo)"})
			return fd
		}
	}
	norm := strings.ReplaceAll(string(data), "\r\n", "\n")
	norm = strings.TrimSuffix(norm, "\n")
	if norm == "" {
		return fd
	}
	split := strings.Split(norm, "\n")
	fd.Added = len(split)
	fd.Lines = append(fd.Lines, DiffLine{
		Kind: DiffHunk,
		Text: fmt.Sprintf("@@ -0,0 +1,%d @@", len(split)),
	})
	for _, ln := range split {
		fd.Lines = append(fd.Lines, DiffLine{Kind: DiffAdded, Text: "+" + ln})
	}
	return fd
}

// SyncIntegrationToWorkspace copies added/modified/renamed/deleted files from the
// hivemind/integracao worktree into the user's open workspace working tree without
// switching branches or creating automatic commits on the user's branch.
func SyncIntegrationToWorkspace(ctx context.Context, workspaceDir string) (int, error) {
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspaceDir) || !g.HasCommit(ctx, workspaceDir) {
		return 0, nil
	}
	if !g.BranchExists(ctx, workspaceDir, gitx.IntegrationBranch) {
		return 0, nil
	}
	intWT := filepath.Join(workspaceDir, ".hivemind", "worktrees", "_integracao")
	if info, err := os.Stat(intWT); err != nil || !info.IsDir() {
		return 0, nil
	}
	raw, err := runGit(ctx, workspaceDir, "diff", "--name-status", "HEAD.."+gitx.IntegrationBranch)
	if err != nil || strings.TrimSpace(raw) == "" {
		return 0, err
	}

	copyFromWT := func(relSlash string) error {
		relOS := filepath.FromSlash(relSlash)
		src, err := workspace.Resolve(intWT, relOS)
		if err != nil {
			return err
		}
		dst, err := workspace.Resolve(workspaceDir, relOS)
		if err != nil {
			return err
		}
		info, err := os.Stat(src)
		if err != nil {
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

	removeInWorkspace := func(relSlash string) {
		if dst, err := workspace.Resolve(workspaceDir, filepath.FromSlash(relSlash)); err == nil {
			_ = os.Remove(dst)
		}
	}

	synced := 0
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
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
			removeInWorkspace(parts[1])
			if copyFromWT(parts[2]) == nil {
				synced++
			}
		case code == "D":
			removeInWorkspace(parts[1])
			synced++
		default:
			if copyFromWT(parts[1]) == nil {
				synced++
			}
		}
	}
	return synced, nil
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
	// Check if file is tracked in HEAD
	if g.HasCommit(ctx, workspaceDir) {
		if _, err := runGit(ctx, workspaceDir, "ls-files", "--error-unmatch", "--", relClean); err == nil {
			_, _ = runGit(ctx, workspaceDir, "reset", "HEAD", "--", relClean)
			_, err := runGit(ctx, workspaceDir, "checkout", "--", relClean)
			return err
		}
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RevertDiffHunk reverses a single unified diff hunk on relPath using `git apply --reverse`.
// If the hunk starts at -0,0 (entire untracked file), it falls back to DiscardFileChanges.
func RevertDiffHunk(ctx context.Context, workspaceDir, relPath, hunkHeader string, hunkLines []string) error {
	relClean := filepath.ToSlash(strings.TrimSpace(relPath))
	if relClean == "" {
		return fmt.Errorf("caminho de arquivo vazio")
	}
	if _, err := workspace.Resolve(workspaceDir, filepath.FromSlash(relClean)); err != nil {
		return err
	}
	header := strings.TrimSpace(hunkHeader)
	if !strings.HasPrefix(header, "@@") {
		return fmt.Errorf("cabeçalho de hunk inválido: %s", header)
	}
	if strings.Contains(header, "@@ -0,0 ") {
		return DiscardFileChanges(ctx, workspaceDir, relClean)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", relClean, relClean))
	sb.WriteString(fmt.Sprintf("--- a/%s\n", relClean))
	sb.WriteString(fmt.Sprintf("+++ b/%s\n", relClean))
	sb.WriteString(header + "\n")
	for _, ln := range hunkLines {
		if ln == "" {
			sb.WriteString(" \n")
			continue
		}
		first := ln[0]
		if first == '+' || first == '-' || first == ' ' || first == '\\' {
			sb.WriteString(ln + "\n")
		} else {
			sb.WriteString(" " + ln + "\n")
		}
	}

	tmpFile, err := os.CreateTemp("", "hivemind-hunk-*.patch")
	if err != nil {
		return err
	}
	patchPath := tmpFile.Name()
	defer os.Remove(patchPath)
	if _, err := tmpFile.WriteString(sb.String()); err != nil {
		_ = tmpFile.Close()
		return err
	}
	_ = tmpFile.Close()

	_, err = runGit(ctx, workspaceDir, "apply", "--reverse", "--recount", "--whitespace=nowarn", patchPath)
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
	procx.ConfigureContext(cmd)
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
			if projKey := sanitizeJiraProjectKey(cfg.Settings.JiraProject); projKey != "" {
				jql = fmt.Sprintf(`project = "%s" AND resolution = Unresolved ORDER BY updated DESC`, projKey)
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
					procx.ConfigureContext(cmd)
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

// ProjectPullRequest represents an open GitHub pull request listed in the IDE sidebar.
type ProjectPullRequest struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
	URL       string `json:"url"`
	State     string `json:"state"`
	IsDraft   bool   `json:"isDraft"`
	Author    string `json:"author,omitempty"`
	HeadRef   string `json:"headRef,omitempty"`
	BaseRef   string `json:"baseRef,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// ParseGHPRList parses the JSON output from `gh pr list --json ...`.
func ParseGHPRList(data []byte) ([]ProjectPullRequest, error) {
	var raw []struct {
		Number    int    `json:"number"`
		Title     string `json:"title"`
		Body      string `json:"body"`
		URL       string `json:"url"`
		State     string `json:"state"`
		IsDraft   bool   `json:"isDraft"`
		UpdatedAt string `json:"updatedAt"`
		HeadRef   string `json:"headRefName"`
		BaseRef   string `json:"baseRefName"`
		Author    struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]ProjectPullRequest, 0, len(raw))
	for _, item := range raw {
		out = append(out, ProjectPullRequest{
			Number:    item.Number,
			Title:     item.Title,
			Body:      item.Body,
			URL:       item.URL,
			State:     item.State,
			IsDraft:   item.IsDraft,
			Author:    item.Author.Login,
			HeadRef:   item.HeadRef,
			BaseRef:   item.BaseRef,
			UpdatedAt: item.UpdatedAt,
		})
	}
	return out, nil
}

// ListProjectPullRequests fetches open pull requests for a GitHub-backed workspace.
func ListProjectPullRequests(ctx context.Context, workspace string) ([]ProjectPullRequest, error) {
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspace) {
		return nil, nil
	}
	remoteURL, err := g.RemoteURL(ctx, workspace)
	if err != nil {
		return nil, nil
	}
	owner, repo, ok := githubx.ParseRemote(remoteURL)
	if !ok {
		return nil, nil
	}

	if ghBin, err := exec.LookPath("gh"); err == nil {
		cmd := exec.CommandContext(ctx, ghBin,
			"pr", "list",
			"--repo", owner+"/"+repo,
			"--state", "open",
			"--limit", "20",
			"--json", "number,title,body,url,state,isDraft,author,headRefName,baseRefName,updatedAt",
		)
		procx.ConfigureContext(cmd)
		var out, errBuf bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errBuf
		if err := cmd.Run(); err == nil {
			if prs, parseErr := ParseGHPRList(out.Bytes()); parseErr == nil {
				return prs, nil
			}
		} else if token := githubEnvToken(); token == "" {
			return nil, fmt.Errorf("gh pr list: %s", strings.TrimSpace(errBuf.String()))
		}
	}

	token := githubEnvToken()
	if token == "" {
		return nil, fmt.Errorf("instale e autentique o `gh` CLI ou configure GITHUB_TOKEN")
	}
	return listPullRequestsREST(ctx, owner, repo, token)
}

func githubEnvToken() string {
	if t := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); t != "" {
		return t
	}
	return strings.TrimSpace(os.Getenv("GH_TOKEN"))
}

func listPullRequestsREST(ctx context.Context, owner, repo, token string) ([]ProjectPullRequest, error) {
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls?state=open&per_page=20",
		url.PathEscape(owner), url.PathEscape(repo))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github API %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var raw []struct {
		Number    int    `json:"number"`
		Title     string `json:"title"`
		Body      string `json:"body"`
		HTMLURL   string `json:"html_url"`
		State     string `json:"state"`
		Draft     bool   `json:"draft"`
		UpdatedAt string `json:"updated_at"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	prs := make([]ProjectPullRequest, 0, len(raw))
	for _, item := range raw {
		prs = append(prs, ProjectPullRequest{
			Number:    item.Number,
			Title:     item.Title,
			Body:      item.Body,
			URL:       item.HTMLURL,
			State:     strings.ToUpper(item.State),
			IsDraft:   item.Draft,
			Author:    item.User.Login,
			HeadRef:   item.Head.Ref,
			BaseRef:   item.Base.Ref,
			UpdatedAt: item.UpdatedAt,
		})
	}
	return prs, nil
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
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspaceDir) {
		return "", fmt.Errorf("esta pasta não é um repositório Git")
	}
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "fetch":
		if _, err := runGit(ctx, workspaceDir, "fetch", "--all", "--prune"); err != nil {
			return "", err
		}
		return "Fetch concluído (remotes atualizados).", nil
	case "pull":
		if _, err := runGit(ctx, workspaceDir, "pull", "--rebase"); err != nil {
			return "", err
		}
		return "Pull concluído.", nil
	case "push":
		branch, err := g.CurrentBranch(ctx, workspaceDir)
		if err != nil || branch == "" {
			return "", fmt.Errorf("branch atual não identificada")
		}
		if err := g.Push(ctx, workspaceDir, "origin", branch); err != nil {
			return "", err
		}
		return fmt.Sprintf("Push concluído na branch %s.", branch), nil
	case "sync":
		if _, err := runGit(ctx, workspaceDir, "pull", "--rebase"); err != nil {
			return "", err
		}
		branch, _ := g.CurrentBranch(ctx, workspaceDir)
		if branch != "" {
			if err := g.Push(ctx, workspaceDir, "origin", branch); err != nil {
				return "", err
			}
		}
		return "Sync (Pull + Push) concluído.", nil
	case "stash":
		out, err := runGit(ctx, workspaceDir, "stash", "push", "-u", "-m", "HiveMind IDE stash")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(out), nil
	case "stash-pop":
		out, err := runGit(ctx, workspaceDir, "stash", "pop")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(out), nil
	default:
		return "", fmt.Errorf("ação Git não suportada: %s", action)
	}
}

func sanitizeJiraProjectKey(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	for _, r := range s {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return ""
		}
	}
	return s
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
