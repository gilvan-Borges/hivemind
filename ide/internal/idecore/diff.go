package idecore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	rest := strings.TrimPrefix(header, "diff --git ")
	if strings.HasPrefix(rest, `"`) {
		if _, second, ok := splitGitPathPair(rest); ok {
			return strings.TrimPrefix(gitx.UnquotePath(second), "b/")
		}
	}
	if idx := strings.LastIndex(rest, " b/"); idx >= 0 {
		return gitx.UnquotePath(strings.TrimSpace(rest[idx+3:]))
	}
	if fields := strings.Fields(rest); len(fields) >= 2 {
		return strings.TrimPrefix(gitx.UnquotePath(fields[len(fields)-1]), "b/")
	}
	return gitx.UnquotePath(header)
}

// splitGitPathPair splits the two quoted paths of a "diff --git" header.
func splitGitPathPair(s string) (first, second string, ok bool) {
	i := 0
	read := func() (string, bool) {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		if i >= len(s) || s[i] != '"' {
			return "", false
		}
		start := i
		i++
		for i < len(s) {
			if s[i] == '\\' && i+1 < len(s) {
				i += 2
				continue
			}
			if s[i] == '"' {
				i++
				return s[start:i], true
			}
			i++
		}
		return "", false
	}
	a, okA := read()
	b, okB := read()
	if !okA || !okB {
		return "", "", false
	}
	return a, b, true
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
			rel := gitx.UnquotePath(strings.TrimSpace(line))
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
		pathPart = gitx.UnquotePath(pathPart)
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
		_, err := runGit(ctx, workspaceDir, "add", "--", gitx.LiteralPathspec(relClean))
		return err
	}
	g := gitx.Local{}
	if g.HasCommit(ctx, workspaceDir) {
		_, err := runGit(ctx, workspaceDir, "reset", "HEAD", "--", gitx.LiteralPathspec(relClean))
		return err
	}
	_, err := runGit(ctx, workspaceDir, "rm", "--cached", "--", gitx.LiteralPathspec(relClean))
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

// ErrWorkspaceDirty is returned by SyncIntegrationToWorkspace when the user's
// working tree has local changes. Sync refuses to run in that case: copying the
// integration output over uncommitted work would destroy it (the IDE used to do
// this automatically after every successful run).
var ErrWorkspaceDirty = errors.New("workspace tem alterações locais não commitadas")

// SyncIntegrationToWorkspace copies added/modified/renamed/deleted files from the
// hivemind/integracao worktree into the user's open workspace working tree without
// switching branches or creating automatic commits on the user's branch.
//
// It is a no-op when the integration branch has nothing new. When the user's
// working tree is not clean it returns ErrWorkspaceDirty and changes nothing.
func SyncIntegrationToWorkspace(ctx context.Context, workspaceDir string) (int, error) {
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspaceDir) || !g.HasCommit(ctx, workspaceDir) {
		return 0, nil
	}
	if !g.BranchExists(ctx, workspaceDir, gitx.IntegrationBranch) {
		return 0, nil
	}
	intWT := workspace.WorktreePath(workspaceDir, "_integracao")
	if info, err := os.Stat(intWT); err != nil || !info.IsDir() {
		legacyWT := filepath.Join(workspaceDir, ".hivemind", "worktrees", "_integracao")
		if lInfo, lErr := os.Stat(legacyWT); lErr == nil && lInfo.IsDir() {
			intWT = legacyWT
		} else {
			return 0, nil
		}
	}
	status, err := runGit(ctx, workspaceDir, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(status) != "" && hasUserChangesInWorkspace(ctx, workspaceDir, status) {
		return 0, ErrWorkspaceDirty
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
		return atomicWriteFile(dst, data, info.Mode().Perm())
	}

	removeInWorkspace := func(relSlash string) error {
		dst, err := workspace.Resolve(workspaceDir, filepath.FromSlash(relSlash))
		if err != nil {
			return err
		}
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	synced := 0
	var failures []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		for i := range parts {
			parts[i] = gitx.UnquotePath(parts[i])
		}
		code := parts[0]
		switch {
		case strings.HasPrefix(code, "R") && len(parts) >= 3:
			// Copy the new path before removing the old one so a failed copy
			// never loses the user's file.
			if err := copyFromWT(parts[2]); err != nil {
				failures = append(failures, parts[2]+": "+err.Error())
				continue
			}
			if err := removeInWorkspace(parts[1]); err != nil {
				failures = append(failures, parts[1]+": "+err.Error())
				continue
			}
			synced++
		case code == "D":
			if err := removeInWorkspace(parts[1]); err != nil {
				failures = append(failures, parts[1]+": "+err.Error())
				continue
			}
			synced++
		default:
			if err := copyFromWT(parts[1]); err != nil {
				failures = append(failures, parts[1]+": "+err.Error())
				continue
			}
			synced++
		}
	}
	if len(failures) > 0 {
		return synced, fmt.Errorf("falha ao sincronizar %d arquivo(s): %s", len(failures), strings.Join(failures, "; "))
	}
	return synced, nil
}

// hasUserChanges reports whether git status --porcelain contains changes that
// are not HiveMind's own bookkeeping. .hivemind/ holds sessions and worktrees;
// it is expected to be untracked while the project has not committed the
// recommended .gitignore entry, and must not block a safe integration sync.
func hasUserChanges(porcelain string) bool {
	return hasUserChangesInWorkspace(context.Background(), "", porcelain)
}

func hasUserChangesInWorkspace(ctx context.Context, workspaceDir, porcelain string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(porcelain, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < 4 {
			continue
		}
		code := ""
		if len(line) >= 2 {
			code = line[:2]
		}
		path := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		path = gitx.UnquotePath(path)
		path = filepath.ToSlash(path)
		if path == ".hivemind" || strings.HasPrefix(path, ".hivemind/") {
			continue
		}
		if path == ".gitignore" && workspaceDir != "" && isHiveMindOnlyGitignoreChange(ctx, workspaceDir, code == "??") {
			continue
		}
		return true
	}
	return false
}

func isHiveMindIgnoreLine(raw string) bool {
	s := strings.TrimSpace(raw)
	return s == "" || s == "# HiveMind" || s == ".hivemind/" || s == ".hivemind"
}

// isHiveMindOnlyGitignoreChange returns true when .gitignore only contains
// HiveMind's own EnsureHiveIgnore addition ("# HiveMind\n.hivemind/\n"), so
// prepareDevs adding .hivemind/ does not falsely block integration sync.
func isHiveMindOnlyGitignoreChange(ctx context.Context, workspaceDir string, untracked bool) bool {
	if untracked {
		data, err := os.ReadFile(filepath.Join(workspaceDir, ".gitignore"))
		if err != nil {
			return false
		}
		sawEntry := false
		for _, ln := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			if !isHiveMindIgnoreLine(ln) {
				return false
			}
			if strings.TrimSpace(ln) == ".hivemind/" || strings.TrimSpace(ln) == ".hivemind" {
				sawEntry = true
			}
		}
		return sawEntry
	}
	diffOut, err := runGit(ctx, workspaceDir, "diff", "HEAD", "--", ".gitignore")
	if err != nil {
		return false
	}
	sawChange := false
	for _, ln := range strings.Split(strings.ReplaceAll(diffOut, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(ln, "+++") || strings.HasPrefix(ln, "---") || strings.HasPrefix(ln, "@@") || strings.HasPrefix(ln, "diff ") || strings.HasPrefix(ln, "index ") || strings.HasPrefix(ln, `\ No newline`) {
			continue
		}
		if strings.HasPrefix(ln, "-") {
			return false
		}
		if strings.HasPrefix(ln, "+") {
			if !isHiveMindIgnoreLine(ln[1:]) {
				return false
			}
			sawChange = true
		}
	}
	return sawChange
}

// atomicWriteFile writes data to path through a temporary file in the same
// directory, then renames it into place. A crash mid-write cannot truncate the
// original file. When mode == 0 (new file), O_CREATE with 0o666 is used so the
// OS process umask is respected rather than bypassed via Chmod(0644).
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	var (
		tmp     *os.File
		tmpName string
		err     error
	)
	if mode == 0 {
		for range 8 {
			var rnd [6]byte
			_, _ = rand.Read(rnd[:])
			cand := filepath.Join(dir, "."+base+"."+hex.EncodeToString(rnd[:])+".tmp")
			tmp, err = os.OpenFile(cand, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666)
			if err == nil {
				tmpName = cand
				break
			}
			if !os.IsExist(err) {
				return err
			}
		}
		if err != nil {
			return err
		}
	} else {
		tmp, err = os.CreateTemp(dir, "."+base+".*.tmp")
		if err != nil {
			return err
		}
		tmpName = tmp.Name()
	}
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if mode != 0 {
		if err := os.Chmod(tmpName, mode); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
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
	client := jirax.FromWorkspace(workspace)
	if !client.Enabled() {
		return fmt.Errorf("credenciais Jira não configuradas (conecte pelo Conector Jira Nativo, mcp_config.json ou variáveis JIRA_*)")
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
	relClean, header, patch, err := buildSingleHunkPatch(workspaceDir, relPath, hunkHeader, hunkLines)
	if err != nil {
		return err
	}
	if strings.Contains(header, "@@ -0,0 ") {
		return DiscardFileChanges(ctx, workspaceDir, relClean)
	}
	return runGitApplyPatch(ctx, workspaceDir, patch, "--reverse", "--recount", "--whitespace=nowarn")
}

// StageDiffHunk stages (accepts) a single unified diff hunk into the Git index
// using `git apply --cached`, allowing hunk-by-hunk acceptance of agent edits.
// For untracked files (@@ -0,0 ...), stages the whole new file.
func StageDiffHunk(ctx context.Context, workspaceDir, relPath, hunkHeader string, hunkLines []string) error {
	relClean, header, patch, err := buildSingleHunkPatch(workspaceDir, relPath, hunkHeader, hunkLines)
	if err != nil {
		return err
	}
	if strings.Contains(header, "@@ -0,0 ") {
		return StageWorkspaceFile(ctx, workspaceDir, relClean, true)
	}
	return runGitApplyPatch(ctx, workspaceDir, patch, "--cached", "--recount", "--whitespace=nowarn")
}

// ApplyDiffHunk applies a single unified diff hunk onto relPath in the working tree
// using `git apply`.
func ApplyDiffHunk(ctx context.Context, workspaceDir, relPath, hunkHeader string, hunkLines []string) error {
	_, _, patch, err := buildSingleHunkPatch(workspaceDir, relPath, hunkHeader, hunkLines)
	if err != nil {
		return err
	}
	return runGitApplyPatch(ctx, workspaceDir, patch, "--recount", "--whitespace=nowarn")
}

// UnstagedFileDiff returns only the unstaged hunks (working tree vs Git index) for relPath,
// or an untracked new-file diff if relPath is not tracked yet. Used by the editor's inline
// hunk-by-hunk review bar so accepted (staged) hunks disappear immediately from pending review.
func UnstagedFileDiff(ctx context.Context, workspaceDir, relPath string) (*FileDiff, error) {
	relClean := filepath.ToSlash(strings.TrimSpace(relPath))
	if relClean == "" {
		return nil, fmt.Errorf("caminho de arquivo vazio")
	}
	full, err := workspace.Resolve(workspaceDir, filepath.FromSlash(relClean))
	if err != nil {
		return nil, err
	}
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspaceDir) {
		return nil, nil
	}
	raw, err := runGit(ctx, workspaceDir, "diff", "--no-color", "--", gitx.LiteralPathspec(relClean))
	if err == nil && strings.TrimSpace(raw) != "" {
		parsed := ParseUnifiedDiff(raw)
		if len(parsed) > 0 {
			parsed[0].Status = "M"
			return &parsed[0], nil
		}
	}
	// Check if file is untracked (new file not yet staged).
	untracked, uErr := runGit(ctx, workspaceDir, "ls-files", "--others", "--exclude-standard", "--", gitx.LiteralPathspec(relClean))
	if uErr == nil && strings.TrimSpace(untracked) != "" {
		data, rErr := os.ReadFile(full)
		if rErr == nil {
			fd := buildUntrackedFileDiff(relClean, data)
			fd.Status = "U"
			return &fd, nil
		}
	}
	return nil, nil
}

func buildSingleHunkPatch(workspaceDir, relPath, hunkHeader string, hunkLines []string) (relClean, header, patch string, err error) {
	relClean = filepath.ToSlash(strings.TrimSpace(relPath))
	if relClean == "" {
		return "", "", "", fmt.Errorf("caminho de arquivo vazio")
	}
	if _, err := workspace.Resolve(workspaceDir, filepath.FromSlash(relClean)); err != nil {
		return "", "", "", err
	}
	header = strings.TrimSpace(hunkHeader)
	if !strings.HasPrefix(header, "@@") {
		return "", "", "", fmt.Errorf("cabeçalho de hunk inválido: %s", header)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", relClean, relClean))
	sb.WriteString(fmt.Sprintf("--- a/%s\n", relClean))
	sb.WriteString(fmt.Sprintf("+++ b/%s\n", relClean))
	sb.WriteString(header + "\n")
	for len(hunkLines) > 0 && hunkLines[len(hunkLines)-1] == "" {
		hunkLines = hunkLines[:len(hunkLines)-1]
	}
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
	return relClean, header, sb.String(), nil
}

func runGitApplyPatch(ctx context.Context, workspaceDir, patch string, flags ...string) error {
	tmpFile, err := os.CreateTemp("", "hivemind-hunk-*.patch")
	if err != nil {
		return err
	}
	patchPath := tmpFile.Name()
	defer os.Remove(patchPath)
	if _, err := tmpFile.WriteString(patch); err != nil {
		_ = tmpFile.Close()
		return err
	}
	_ = tmpFile.Close()

	args := append([]string{"apply"}, flags...)
	args = append(args, patchPath)
	_, err = runGit(ctx, workspaceDir, args...)
	return err
}

// GitHubStatusInfo reports the native GitHub connector / CLI authentication status and repo remote.
type GitHubStatusInfo struct {
	Installed      bool   `json:"installed"`
	LoggedIn       bool   `json:"loggedIn"`
	User           string `json:"user"`
	Remote         string `json:"remote"`
	Detail         string `json:"detail"`
	Mode           string `json:"mode,omitempty"`           // "rest-env", "rest-config", "rest-gh", "cli", "none"
	Source         string `json:"source,omitempty"`         // "env", "config", "gh-token", "gh-cli", ""
	ConnectorLabel string `json:"connectorLabel,omitempty"` // human label
	TokenMasked    string `json:"tokenMasked,omitempty"`
	BaseURL        string `json:"baseUrl,omitempty"`
}

func githubConnectorLabel(mode string) string {
	switch mode {
	case "rest-config":
		return "API Nativa (Token salvo)"
	case "rest-env":
		return "API Nativa (ENV)"
	case "rest-gh":
		return "API Nativa (gh auth)"
	case "cli":
		return "gh CLI"
	default:
		return "Desconectado"
	}
}

// CheckGitHubStatus inspects the native GitHub REST connector (saved PAT, env var, or `gh auth token`),
// falls back to `gh` CLI when needed, and reads the workspace `origin` remote.
func CheckGitHubStatus(ctx context.Context, workspace string) GitHubStatusInfo {
	conn := githubx.InspectConnector()
	info := GitHubStatusInfo{
		Mode:           conn.Mode,
		Source:         conn.Source,
		ConnectorLabel: githubConnectorLabel(conn.Mode),
		TokenMasked:    conn.TokenMasked,
		BaseURL:        conn.BaseURL,
	}

	_, ghLookErr := exec.LookPath("gh")
	ghInstalled := ghLookErr == nil
	info.Installed = conn.Configured || ghInstalled

	token, baseURL, _ := githubx.LoadCredentials()
	if token != "" {
		client := &githubx.REST{BaseURL: baseURL, Token: token, Owner: "_", Repo: "_"}
		if user, err := client.CurrentUser(ctx); err == nil && strings.TrimSpace(user) != "" {
			info.LoggedIn = true
			info.User = strings.TrimSpace(user)
			info.Detail = fmt.Sprintf("Conectado como @%s (%s)", info.User, info.ConnectorLabel)
		} else if err != nil {
			info.Detail = fmt.Sprintf("Token GitHub inválido ou sem acesso (%v)", err)
		}
	}

	if !info.LoggedIn && ghInstalled {
		cmd := exec.CommandContext(ctx, "gh", "api", "user", "--jq", ".login")
		procx.ConfigureContext(cmd)
		var out, errBuf bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errBuf
		if err := cmd.Run(); err == nil {
			info.LoggedIn = true
			info.User = strings.TrimSpace(out.String())
			info.Mode = "cli"
			info.Source = "gh-cli"
			info.ConnectorLabel = githubConnectorLabel("cli")
			info.Detail = "Conectado como @" + info.User + " (gh CLI)"
		} else if info.Detail == "" {
			info.Detail = "Não autenticado (configure o Conector GitHub ou rode `gh auth login`)"
		}
	}

	if !info.LoggedIn && info.Detail == "" {
		info.Detail = "Configure um Token GitHub no Conector Nativo ou autentique o `gh` CLI"
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

// ProjectSubtask represents a Jira subtask or child issue summarized inside a parent issue card.
type ProjectSubtask struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
	Status  string `json:"status,omitempty"`
	Done    bool   `json:"done"`
	URL     string `json:"url,omitempty"`
}

// ProjectIssue represents a Jira or GitHub issue listed in the IDE sidebar.
type ProjectIssue struct {
	Source         string           `json:"source"` // "jira" or "github"
	Key            string           `json:"key"`    // e.g. "PROJ-12" or "#42"
	Title          string           `json:"title"`
	Body           string           `json:"body,omitempty"`
	Status         string           `json:"status,omitempty"`
	IssueType      string           `json:"issueType,omitempty"`
	IsSubtask      bool             `json:"isSubtask,omitempty"`
	ParentKey      string           `json:"parentKey,omitempty"`
	ParentTitle    string           `json:"parentTitle,omitempty"`
	ParentURL      string           `json:"parentUrl,omitempty"`
	SubtaskTotal   int              `json:"subtaskTotal,omitempty"`
	SubtaskDone    int              `json:"subtaskDone,omitempty"`
	SubtaskPending int              `json:"subtaskPending,omitempty"`
	Subtasks       []ProjectSubtask `json:"subtasks,omitempty"`
	Author         string           `json:"author,omitempty"`
	URL            string           `json:"url,omitempty"`
	IsMine         bool             `json:"isMine,omitempty"`
}

func buildMyJiraIssuesJQL(projectKey string) string {
	projKey := strings.ToUpper(sanitizeJiraProjectKey(projectKey))
	if projKey != "" {
		return fmt.Sprintf(`project = "%s" AND assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC`, projKey)
	}
	return "assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC"
}

func matchJiraProjectFromNames(projs []jirax.Project, rawNames ...string) string {
	if len(projs) == 0 {
		return ""
	}
	normAlpha := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		return b.String()
	}

	// stripAffixes removes common mobile/web/org prefixes and suffixes to extract the core product name.
	stripAffixes := func(raw string) []string {
		s := strings.ToLower(strings.TrimSpace(raw))
		if s == "" {
			return nil
		}
		var out []string
		add := func(v string) {
			v = strings.Trim(v, "-_. ")
			if v == "" {
				return
			}
			for _, existing := range out {
				if existing == v {
					return
				}
			}
			out = append(out, v)
		}
		add(s)
		cur := s
		for changed := true; changed; {
			changed = false
			for _, pref := range []string{"app-", "app_", "hub-", "hub_", "auditoria-", "auditoria_", "vibetex-", "vibetex_", "projeto-", "projeto_"} {
				if strings.HasPrefix(cur, pref) && len(cur) > len(pref)+1 {
					cur = strings.TrimPrefix(cur, pref)
					add(cur)
					changed = true
				}
			}
			for _, camelPref := range []string{"auditoriavibe", "appvibe", "hubvibe", "vibetex"} {
				if strings.HasPrefix(cur, camelPref) && len(cur) > len(camelPref)+1 {
					rest := strings.TrimPrefix(cur, camelPref)
					if strings.HasPrefix(camelPref, "auditoriavibe") || strings.HasPrefix(camelPref, "appvibe") || strings.HasPrefix(camelPref, "hubvibe") {
						rest = "vibe" + rest
					}
					cur = rest
					add(cur)
					changed = true
				}
			}
			for _, camelPref := range []string{"app", "hub", "auditoria"} {
				if strings.HasPrefix(cur, camelPref) && len(cur) > len(camelPref)+2 && cur != "apple" {
					cur = strings.TrimPrefix(cur, camelPref)
					add(cur)
					changed = true
				}
			}
			for _, suf := range []string{
				"-mobile", "_mobile", "mobile",
				"-ios", "_ios", "-android", "_android",
				"-app", "_app", "app",
				"-web", "_web", "web",
				"-front", "_front", "-frontend", "_frontend",
				"-back", "_back", "-backend", "_backend",
				"-api", "_api", "api",
				"-core", "_core", "-service", "_service", "-srv", "_srv",
				"-demo", "_demo", "atualizado",
			} {
				if strings.HasSuffix(cur, suf) && len(cur) > len(suf)+1 {
					cur = strings.TrimSuffix(cur, suf)
					add(cur)
					changed = true
				}
			}
		}
		if parts := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' || r == '.' || r == ' ' }); len(parts) > 1 {
			for _, pt := range parts {
				if len(pt) >= 3 && pt != "app" && pt != "web" && pt != "mobile" && pt != "ios" && pt != "api" && pt != "vibe" && pt != "vibetex" {
					add(pt)
				}
			}
		}
		return out
	}

	var candidates []string
	addSource := func(raw string) {
		for _, v := range stripAffixes(raw) {
			seen := false
			for _, c := range candidates {
				if c == v {
					seen = true
					break
				}
			}
			if !seen {
				candidates = append(candidates, v)
			}
		}
	}
	for _, raw := range rawNames {
		addSource(raw)
	}

	// 1. Exact key match (e.g., "ipg" / "ipg-mobile" -> "IPG").
	for _, cand := range candidates {
		for _, p := range projs {
			if strings.EqualFold(p.Key, cand) {
				return sanitizeJiraProjectKey(p.Key)
			}
		}
	}

	// 2. Normalized project Name match (e.g., "vibePromotor" / "vibepromotor-drops" -> "VibePromotor (DROPS)" [VPD],
	// "AppSolBrilhante" -> "Sol Brilhante" [SB], "vibetexCedae" -> "Cedae" [CED], "suporte" -> "Vibe Suporte" [VS],
	// "PraticoMuralWeb" -> "Mural de Troca" [MDT]).
	for _, cand := range candidates {
		nc := normAlpha(cand)
		if len(nc) < 3 || nc == "vibe" || nc == "vibetex" || nc == "geral" {
			continue
		}
		for _, p := range projs {
			// Split Jira project name into main title and parenthetical alias, e.g. "VibePromotor (DROPS)" -> ["vibepromotor", "drops"]
			rawName := strings.ToLower(p.Name)
			mainTitle := rawName
			aliasTitle := ""
			if openIdx := strings.IndexByte(rawName, '('); openIdx >= 0 {
				mainTitle = rawName[:openIdx]
				if closeIdx := strings.IndexByte(rawName[openIdx:], ')'); closeIdx > 1 {
					aliasTitle = rawName[openIdx+1 : openIdx+closeIdx]
				}
			}
			nm := normAlpha(mainTitle)
			na := normAlpha(aliasTitle)
			// Strip generic "vibe" prefix from Jira project name for matching (e.g. "Vibe Suporte" -> "suporte")
			nmNoVibe := strings.TrimPrefix(nm, "vibe")

			if (nm != "" && (nc == nm || strings.Contains(nc, nm))) ||
				(na != "" && len(na) >= 3 && (nc == na || strings.Contains(nc, na))) ||
				(len(nmNoVibe) >= 4 && (nc == nmNoVibe || strings.HasSuffix(nc, nmNoVibe))) ||
				(strings.Contains(nc, "mural") && strings.Contains(nm, "mural")) {
				return sanitizeJiraProjectKey(p.Key)
			}
		}
	}
	return ""
}

// inferJiraProjectKeyForWorkspace matches the current workspace/repository name
// (e.g. "ipg", "ipg-mobile", "appVibePromotor-drops", "vibetexCedae", "PraticoMuralWeb", "suporte")
// against the organization's Jira projects (e.g. IPG, VPD, CED, MDT, VS, VB, VBWHTS, VC, VOF, SB, RKSAM).
// If the repository has no corresponding project in Jira yet (e.g. "vibe-saude", "trace-pharma", "vibeservice-ponto"),
// it returns ("", true) when multiple Jira projects exist in the org so tasks from unrelated Jira projects are NEVER mixed in.
func inferJiraProjectKeyForWorkspace(ctx context.Context, workspaceDir string) (matchedKey string, hasMultipleOrgProjects bool) {
	if strings.TrimSpace(workspaceDir) == "" {
		return "", false
	}
	projs, err := jirax.ListProjectsForWorkspace(ctx, workspaceDir)
	if err != nil || len(projs) == 0 {
		return "", false
	}
	if len(projs) == 1 {
		return sanitizeJiraProjectKey(projs[0].Key), false
	}

	var rawNames []string
	g := gitx.Local{}
	if g.IsRepo(ctx, workspaceDir) {
		if remoteURL, rErr := g.RemoteURL(ctx, workspaceDir); rErr == nil {
			if _, repo, ok := githubx.ParseRemote(remoteURL); ok {
				rawNames = append(rawNames, repo)
			}
		}
	}
	rawNames = append(rawNames, filepath.Base(filepath.Clean(workspaceDir)))

	if matched := matchJiraProjectFromNames(projs, rawNames...); matched != "" {
		return matched, true
	}

	// 3. Fallback: inspect recent git commit messages for an explicit Jira key prefix (e.g. "feat(IPG-123): ...").
	if g.IsRepo(ctx, workspaceDir) && g.HasCommit(ctx, workspaceDir) {
		if logRaw, lErr := g.Run(ctx, workspaceDir, "log", "-n", "25", "--pretty=%s"); lErr == nil && logRaw != "" {
			counts := map[string]int{}
			bestKey := ""
			bestCount := 0
			upperLog := strings.ToUpper(logRaw)
			for _, p := range projs {
				k := sanitizeJiraProjectKey(p.Key)
				if k == "" {
					continue
				}
				c := strings.Count(upperLog, k+"-")
				if c > 0 {
					counts[k] = c
					if c > bestCount {
						bestCount = c
						bestKey = k
					}
				}
			}
			if bestKey != "" && bestCount >= 2 {
				return bestKey, true
			}
		}
	}

	return "", true
}

// ListProjectIssues fetches open Jira tasks assigned to the current user and/or open GitHub issues for one-click squad execution.
func ListProjectIssues(ctx context.Context, workspace string) ([]ProjectIssue, error) {
	var issues []ProjectIssue

	// 1. Jira issues when a profile/connector is resolved for this workspace (unless explicitly opted out with bindingMode=="none").
	res := jirax.ResolveForWorkspace(workspace)
	if res.MatchMode != "isolated_none" {
		jc := jirax.FromWorkspace(workspace)
		if jc.Enabled() {
			var cfgProj string
			var cfgLoaded bool
			var cfgJiraEnabled bool
			if cfg, err := team.Load(workspace); err == nil {
				cfgLoaded = true
				cfgProj = strings.TrimSpace(cfg.Settings.JiraProject)
				cfgJiraEnabled = cfg.Settings.JiraEnabled
			}
			if cfgLoaded && !cfgJiraEnabled && strings.EqualFold(cfgProj, "NONE") {
				// User explicitly set "Sem Jira neste repositório" from the quick selector.
			} else {
				projKey := sanitizeJiraProjectKey(cfgProj)
				if strings.EqualFold(projKey, "NONE") {
					projKey = ""
				}
				hasMultiOrgProjs := false
				if projKey == "" && !strings.EqualFold(cfgProj, "*") {
					projKey, hasMultiOrgProjs = inferJiraProjectKeyForWorkspace(ctx, workspace)
					if projKey != "" {
						// Persist the inferred project key in team.yaml so subsequent runs & tools know it immediately.
						EnableProjectJira(workspace, projKey)
					}
				}
				if projKey == "" && !hasMultiOrgProjs {
					projKey = sanitizeJiraProjectKey(res.DefaultProject)
				}
				// Only query Jira when we matched a specific Jira project (or when the user explicitly chose "*" for all projects,
				// or when the Jira instance only has a single project). Repositories that don't exist in Jira yet (e.g. vibe-saude)
				// won't pull unrelated tasks from other company projects.
				shouldQueryJira := projKey != "" || strings.EqualFold(cfgProj, "*") || !hasMultiOrgProjs
				if shouldQueryJira {
					jql := buildMyJiraIssuesJQL(projKey)
					list, err := jc.Search(ctx, jql, 50)
					if err == nil {
						// For top-level issues that may have child issues linked via `parent` (e.g. Epic/Parent in team-managed projects)
						// rather than inline `fields.subtasks`, batch-fetch child issues in a single JQL call.
						var parentCandidates []string
						for _, it := range list {
							if it.Key != "" && len(it.Subtasks) == 0 && !strings.Contains(strings.ToLower(it.IssueType), "sub") {
								parentCandidates = append(parentCandidates, fmt.Sprintf("%q", it.Key))
							}
						}
						childrenByParent := make(map[string][]jirax.SubtaskItem)
						if len(parentCandidates) > 0 {
							if len(parentCandidates) > 25 {
								parentCandidates = parentCandidates[:25]
							}
							childCtx, cancelChild := context.WithTimeout(ctx, 5*time.Second)
							childJQL := fmt.Sprintf("parent in (%s) ORDER BY key ASC", strings.Join(parentCandidates, ","))
							if children, cErr := jc.Search(childCtx, childJQL, 100); cErr == nil {
								for _, ch := range children {
									pk := strings.TrimSpace(ch.ParentKey)
									if pk == "" || ch.Key == "" {
										continue
									}
									childrenByParent[pk] = append(childrenByParent[pk], jirax.SubtaskItem{
										Key:     ch.Key,
										Summary: ch.Summary,
										Status:  ch.Status,
										Done:    jirax.IsDoneStatus(ch.Status),
									})
								}
							}
							cancelChild()
						}

						base := strings.TrimRight(strings.TrimSpace(res.BaseURL), "/")
						for _, it := range list {
							browseURL := ""
							if base != "" && it.Key != "" {
								browseURL = base + "/browse/" + url.PathEscape(it.Key)
							}
							parentURL := ""
							if base != "" && it.ParentKey != "" {
								parentURL = base + "/browse/" + url.PathEscape(it.ParentKey)
							}

							rawSubs := it.Subtasks
							if len(rawSubs) == 0 {
								rawSubs = childrenByParent[it.Key]
							} else if extra := childrenByParent[it.Key]; len(extra) > 0 {
								seen := make(map[string]bool, len(rawSubs))
								for _, s := range rawSubs {
									seen[s.Key] = true
								}
								for _, s := range extra {
									if !seen[s.Key] {
										rawSubs = append(rawSubs, s)
									}
								}
							}

							var projSubs []ProjectSubtask
							doneCount := 0
							for _, st := range rawSubs {
								isDone := st.Done || jirax.IsDoneStatus(st.Status)
								if isDone {
									doneCount++
								}
								stURL := ""
								if base != "" && st.Key != "" {
									stURL = base + "/browse/" + url.PathEscape(st.Key)
								}
								projSubs = append(projSubs, ProjectSubtask{
									Key:     st.Key,
									Summary: st.Summary,
									Status:  st.Status,
									Done:    isDone,
									URL:     stURL,
								})
							}
							totalSubs := len(projSubs)
							pendingSubs := totalSubs - doneCount

							issues = append(issues, ProjectIssue{
								Source:         "jira",
								Key:            it.Key,
								Title:          it.Summary,
								Body:           it.Description,
								Status:         it.Status,
								IssueType:      it.IssueType,
								IsSubtask:      it.IsSubtask || it.ParentKey != "",
								ParentKey:      it.ParentKey,
								ParentTitle:    it.ParentSummary,
								ParentURL:      parentURL,
								SubtaskTotal:   totalSubs,
								SubtaskDone:    doneCount,
								SubtaskPending: pendingSubs,
								Subtasks:       projSubs,
								URL:            browseURL,
								IsMine:         true,
							})
						}
					}
				}
			}
		}
	}

	// 2. GitHub issues via native GitHub REST connector first, then `gh` CLI fallback
	g := gitx.Local{}
	if g.IsRepo(ctx, workspace) {
		if remoteURL, err := g.RemoteURL(ctx, workspace); err == nil {
			if owner, repo, ok := githubx.ParseRemote(remoteURL); ok {
				currentUser, _ := githubx.Detect(owner, repo).CurrentUser(ctx)
				token, baseURL, _ := githubx.LoadCredentials()
				if token != "" {
					if restIssues, err := listIssuesREST(ctx, baseURL, owner, repo, token, currentUser); err == nil {
						issues = append(issues, restIssues...)
						return issues, nil
					}
				}
				if ghBin, err := exec.LookPath("gh"); err == nil {
					cmd := exec.CommandContext(ctx, ghBin, "issue", "list", "--repo", owner+"/"+repo, "--state", "open", "--limit", "20", "--json", "number,title,body,state,author,url")
					procx.ConfigureContext(cmd)
					var out bytes.Buffer
					cmd.Stdout = &out
					if err := cmd.Run(); err == nil {
						var raw []struct {
							Number int    `json:"number"`
							Title  string `json:"title"`
							Body   string `json:"body"`
							State  string `json:"state"`
							URL    string `json:"url"`
							Author struct {
								Login string `json:"login"`
							} `json:"author"`
						}
						if json.Unmarshal(out.Bytes(), &raw) == nil {
							for _, ghIt := range raw {
								author := strings.TrimSpace(ghIt.Author.Login)
								issues = append(issues, ProjectIssue{
									Source: "github",
									Key:    fmt.Sprintf("#%d", ghIt.Number),
									Title:  ghIt.Title,
									Body:   ghIt.Body,
									Status: ghIt.State,
									Author: author,
									URL:    ghIt.URL,
									IsMine: currentUser != "" && strings.EqualFold(author, currentUser),
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

func listIssuesREST(ctx context.Context, baseURL, owner, repo, token, currentUser string) ([]ProjectIssue, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = githubx.DefaultAPIBaseURL
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues?state=open&per_page=30", base, url.PathEscape(owner), url.PathEscape(repo))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "HiveMind-IDE")

	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
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
		Number      int             `json:"number"`
		Title       string          `json:"title"`
		Body        string          `json:"body"`
		State       string          `json:"state"`
		HTMLURL     string          `json:"html_url"`
		PullRequest json.RawMessage `json:"pull_request"`
		User        struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	var out []ProjectIssue
	for _, it := range raw {
		if len(bytes.TrimSpace(it.PullRequest)) > 0 && string(bytes.TrimSpace(it.PullRequest)) != "null" {
			continue // GitHub's /issues endpoint also returns PRs; skip them here.
		}
		author := strings.TrimSpace(it.User.Login)
		out = append(out, ProjectIssue{
			Source: "github",
			Key:    fmt.Sprintf("#%d", it.Number),
			Title:  it.Title,
			Body:   it.Body,
			Status: strings.ToUpper(it.State),
			Author: author,
			URL:    it.HTMLURL,
			IsMine: currentUser != "" && strings.EqualFold(author, currentUser),
		})
	}
	return out, nil
}

// ProjectPullRequest represents an open GitHub pull request listed in the IDE sidebar.
type ProjectPullRequest struct {
	Number              int      `json:"number"`
	Title               string   `json:"title"`
	Body                string   `json:"body,omitempty"`
	URL                 string   `json:"url"`
	State               string   `json:"state"`
	IsDraft             bool     `json:"isDraft"`
	Author              string   `json:"author,omitempty"`
	HeadRef             string   `json:"headRef,omitempty"`
	BaseRef             string   `json:"baseRef,omitempty"`
	UpdatedAt           string   `json:"updatedAt,omitempty"`
	ReviewDecision      string   `json:"reviewDecision,omitempty"`
	RequestedReviewers  []string `json:"requestedReviewers,omitempty"`
	IsMine              bool     `json:"isMine,omitempty"`
	ReviewRequested     bool     `json:"reviewRequested,omitempty"`
	MyResponse          string   `json:"myResponse,omitempty"`          // "", "approved", "changes_requested", "commented"
	NeedsAttention      bool     `json:"needsAttention,omitempty"`      // true when the PR needs currentUser's attention now
	AttentionReason     string   `json:"attentionReason,omitempty"`     // "Sua revisão", "Re-revisão", "Nova resposta", "Novo commit", "Você foi marcado", "Ajustes", "Pronto p/ merge"
	MentionedMe         bool     `json:"mentionedMe,omitempty"`         // true when another human mentioned @me after my last interaction
	LastActivityAt      string   `json:"lastActivityAt,omitempty"`      // RFC3339 timestamp of the latest relevant external activity
	LastActivityAuthor  string   `json:"lastActivityAuthor,omitempty"`  // login of the user behind LastActivityAt
	LastActivityKind    string   `json:"lastActivityKind,omitempty"`    // "mention", "comment", "review_approved", "review_changes", "review_requested", "commit"
	LastActivitySnippet string   `json:"lastActivitySnippet,omitempty"` // short excerpt of the latest comment/review
}

type prActivityReview struct {
	Author      string
	State       string
	Body        string
	SubmittedAt time.Time
}

type prActivityComment struct {
	Author          string
	Body            string
	ViewerDidAuthor bool
	CreatedAt       time.Time
}

type prFlowDetails struct {
	MyResponse          string
	NeedsAttention      bool
	AttentionReason     string
	MentionedMe         bool
	LastActivityAt      string
	LastActivityAuthor  string
	LastActivityKind    string
	LastActivitySnippet string
}

func isGitHubHandleChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-'
}

// mentionsUser reports whether body contains an @username mention with valid GitHub handle boundaries.
func mentionsUser(body, username string) bool {
	user := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(username, "@")))
	if user == "" || strings.TrimSpace(body) == "" {
		return false
	}
	lower := strings.ToLower(body)
	needle := "@" + user
	offset := 0
	for {
		idx := strings.Index(lower[offset:], needle)
		if idx < 0 {
			return false
		}
		pos := offset + idx
		end := pos + len(needle)
		beforeOK := pos == 0 || !isGitHubHandleChar(lower[pos-1]) && lower[pos-1] != '_' && lower[pos-1] != '.'
		afterOK := end == len(lower) || !isGitHubHandleChar(lower[end]) && lower[end] != '_'
		if beforeOK && afterOK {
			return true
		}
		offset = pos + 1
	}
}

func compactActivitySnippet(raw string, maxRunes int) string {
	s := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if s == "" || maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}

func parseGHTime(raw string) time.Time {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}
	}
	if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return ts.UTC()
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts.UTC()
	}
	return time.Time{}
}

func isBotLogin(login string) bool {
	low := strings.ToLower(strings.TrimSpace(login))
	return strings.HasSuffix(low, "[bot]") || low == "dependabot" || low == "renovate" || low == "github-actions"
}

// computePRFlowState derives the user's latest response and whether the PR needs their attention now.
func computePRFlowState(me string, isMine, reviewRequested, hasPendingReviewers bool, reviews []prActivityReview, comments []prActivityComment, latestCommitAt time.Time) (myResp string, needsAttention bool, reason string) {
	det := computePRFlowDetails(me, isMine, reviewRequested, hasPendingReviewers, "", "", time.Time{}, reviews, comments, latestCommitAt)
	return det.MyResponse, det.NeedsAttention, det.AttentionReason
}

// computePRFlowDetails derives the full flow state plus mention and latest external activity metadata:
//   - Third-party PRs return to "Para revisar" when review is requested (or re-requested),
//     when another human mentions @me in the PR body/comment/review after currentUser's last action,
//     when another human comments/replies after currentUser's last review/comment,
//     or when a new commit is pushed after CHANGES_REQUESTED.
//   - Own PRs appear in active "Meus PRs" ONLY when action is needed from the author:
//     (1) an external reviewer approved and the PR is ready to merge, or
//     (2) another human commented/requested changes after the author's last comment or commit.
//     Otherwise (author pushed/commented last and is waiting for reviewer), it moves to "Aguardando / Já respondidos".
func computePRFlowDetails(
	me string,
	isMine, reviewRequested, hasPendingReviewers bool,
	prAuthor, prBody string,
	prUpdatedAt time.Time,
	reviews []prActivityReview,
	comments []prActivityComment,
	latestCommitAt time.Time,
) prFlowDetails {
	me = strings.TrimSpace(me)
	if me == "" {
		return prFlowDetails{}
	}
	var (
		myLastAt       time.Time
		otherLastAt    time.Time
		otherChangesAt time.Time
		otherAuthor    string
		otherKind      string
		otherSnippet   string
		mentionAt      time.Time
		mentionAuthor  string
		mentionSnippet string
		approveAt      time.Time
		approveAuthor  string
		approveSnippet string
		myResp         string
	)
	recordMy := func(ts time.Time) {
		if ts.After(myLastAt) {
			myLastAt = ts
		}
	}
	recordOther := func(author, kind, body string, ts time.Time) {
		a := strings.TrimSpace(author)
		if a == "" || strings.EqualFold(a, me) || isBotLogin(a) {
			return
		}
		if mentionsUser(body, me) && (mentionAt.IsZero() || ts.After(mentionAt)) {
			mentionAt = ts
			mentionAuthor = a
			mentionSnippet = compactActivitySnippet(body, 120)
		}
		if ts.After(otherLastAt) || (otherLastAt.IsZero() && otherAuthor == "") {
			otherLastAt = ts
			otherAuthor = a
			otherKind = kind
			otherSnippet = compactActivitySnippet(body, 120)
		}
	}

	// Check if another human mentioned @me directly in the PR description.
	if !isMine && prAuthor != "" && !strings.EqualFold(prAuthor, me) && !isBotLogin(prAuthor) && mentionsUser(prBody, me) {
		mentionAt = prUpdatedAt
		mentionAuthor = strings.TrimSpace(prAuthor)
		mentionSnippet = compactActivitySnippet(prBody, 120)
	}

	extLatestState := map[string]string{}
	for _, rv := range reviews {
		author := strings.TrimSpace(rv.Author)
		st := strings.ToUpper(strings.TrimSpace(rv.State))
		if strings.EqualFold(author, me) {
			recordMy(rv.SubmittedAt)
			if !isMine {
				switch st {
				case "APPROVED":
					myResp = "approved"
				case "CHANGES_REQUESTED":
					myResp = "changes_requested"
				case "COMMENTED":
					if myResp == "" {
						myResp = "commented"
					}
				}
			}
		} else {
			kind := "comment"
			if st == "CHANGES_REQUESTED" {
				kind = "review_changes"
			} else if st == "APPROVED" {
				kind = "review_approved"
			}
			recordOther(author, kind, rv.Body, rv.SubmittedAt)
			if author != "" && !isBotLogin(author) {
				low := strings.ToLower(author)
				switch st {
				case "APPROVED", "CHANGES_REQUESTED":
					extLatestState[low] = st
					if st == "CHANGES_REQUESTED" && rv.SubmittedAt.After(otherChangesAt) {
						otherChangesAt = rv.SubmittedAt
					}
					if st == "APPROVED" && (approveAt.IsZero() || rv.SubmittedAt.After(approveAt)) {
						approveAt = rv.SubmittedAt
						approveAuthor = author
						approveSnippet = compactActivitySnippet(rv.Body, 120)
					}
				case "DISMISSED":
					delete(extLatestState, low)
				}
			}
		}
	}

	for _, cm := range comments {
		if cm.ViewerDidAuthor || strings.EqualFold(strings.TrimSpace(cm.Author), me) {
			recordMy(cm.CreatedAt)
			if !isMine && myResp == "" {
				myResp = "commented"
			}
		} else {
			recordOther(cm.Author, "comment", cm.Body, cm.CreatedAt)
		}
	}

	formatTS := func(ts time.Time) string {
		if ts.IsZero() {
			return ""
		}
		return ts.UTC().Format(time.RFC3339)
	}

	if isMine {
		authorLast := myLastAt
		if latestCommitAt.After(authorLast) {
			authorLast = latestCommitAt
		}
		approvedCount := 0
		changesCount := 0
		for _, st := range extLatestState {
			if st == "APPROVED" {
				approvedCount++
			} else if st == "CHANGES_REQUESTED" {
				changesCount++
			}
		}
		// Ready to merge once at least one external reviewer approved with no pending re-review or changes requested.
		if !hasPendingReviewers && approvedCount > 0 && changesCount == 0 {
			return prFlowDetails{
				NeedsAttention:      true,
				AttentionReason:     "Pronto p/ merge",
				LastActivityAt:      formatTS(approveAt),
				LastActivityAuthor:  approveAuthor,
				LastActivityKind:    "review_approved",
				LastActivitySnippet: approveSnippet,
			}
		}
		// Needs author's reply/fix if an external human commented or reviewed AFTER the author's last comment/commit.
		if !otherLastAt.IsZero() && (authorLast.IsZero() || otherLastAt.After(authorLast)) {
			mentioned := mentionAuthor != "" && (authorLast.IsZero() || mentionAt.After(authorLast))
			if !otherChangesAt.IsZero() && (authorLast.IsZero() || otherChangesAt.After(authorLast)) {
				return prFlowDetails{
					NeedsAttention:      true,
					AttentionReason:     "Ajustes",
					MentionedMe:         mentioned,
					LastActivityAt:      formatTS(otherLastAt),
					LastActivityAuthor:  otherAuthor,
					LastActivityKind:    "review_changes",
					LastActivitySnippet: otherSnippet,
				}
			}
			kind := otherKind
			if mentioned {
				kind = "mention"
			}
			return prFlowDetails{
				NeedsAttention:      true,
				AttentionReason:     "Nova resposta",
				MentionedMe:         mentioned,
				LastActivityAt:      formatTS(otherLastAt),
				LastActivityAuthor:  otherAuthor,
				LastActivityKind:    kind,
				LastActivitySnippet: otherSnippet,
			}
		}
		return prFlowDetails{}
	}

	hasFreshMention := mentionAuthor != "" && (myLastAt.IsZero() || mentionAt.After(myLastAt))

	if myResp == "" {
		if hasFreshMention {
			return prFlowDetails{
				NeedsAttention:      true,
				AttentionReason:     "Você foi marcado",
				MentionedMe:         true,
				LastActivityAt:      formatTS(mentionAt),
				LastActivityAuthor:  mentionAuthor,
				LastActivityKind:    "mention",
				LastActivitySnippet: mentionSnippet,
			}
		}
		if reviewRequested {
			return prFlowDetails{
				NeedsAttention:   true,
				AttentionReason:  "Sua revisão",
				LastActivityKind: "review_requested",
			}
		}
		return prFlowDetails{}
	}

	// Already responded before — check if the PR needs attention again!
	if hasFreshMention {
		return prFlowDetails{
			MyResponse:          myResp,
			NeedsAttention:      true,
			AttentionReason:     "Você foi marcado",
			MentionedMe:         true,
			LastActivityAt:      formatTS(mentionAt),
			LastActivityAuthor:  mentionAuthor,
			LastActivityKind:    "mention",
			LastActivitySnippet: mentionSnippet,
		}
	}
	if reviewRequested && (myResp == "approved" || myResp == "changes_requested") {
		return prFlowDetails{
			MyResponse:       myResp,
			NeedsAttention:   true,
			AttentionReason:  "Re-revisão",
			LastActivityKind: "review_requested",
		}
	}
	if !myLastAt.IsZero() && otherLastAt.After(myLastAt) {
		return prFlowDetails{
			MyResponse:          myResp,
			NeedsAttention:      true,
			AttentionReason:     "Nova resposta",
			LastActivityAt:      formatTS(otherLastAt),
			LastActivityAuthor:  otherAuthor,
			LastActivityKind:    otherKind,
			LastActivitySnippet: otherSnippet,
		}
	}
	if myResp == "changes_requested" && !myLastAt.IsZero() && latestCommitAt.After(myLastAt) {
		return prFlowDetails{
			MyResponse:         myResp,
			NeedsAttention:     true,
			AttentionReason:    "Novo commit",
			LastActivityAt:     formatTS(latestCommitAt),
			LastActivityAuthor: strings.TrimSpace(prAuthor),
			LastActivityKind:   "commit",
		}
	}
	return prFlowDetails{MyResponse: myResp}
}

// ParseGHPRList parses the JSON output from `gh pr list --json ...` and enriches personal flow fields for currentUser.
func ParseGHPRList(data []byte, currentUser string) ([]ProjectPullRequest, error) {
	var raw []struct {
		Number         int    `json:"number"`
		Title          string `json:"title"`
		Body           string `json:"body"`
		URL            string `json:"url"`
		State          string `json:"state"`
		IsDraft        bool   `json:"isDraft"`
		UpdatedAt      string `json:"updatedAt"`
		HeadRef        string `json:"headRefName"`
		BaseRef        string `json:"baseRefName"`
		ReviewDecision string `json:"reviewDecision"`
		Author         struct {
			Login string `json:"login"`
		} `json:"author"`
		ReviewRequests []struct {
			Login string `json:"login"`
			Name  string `json:"name"`
			Slug  string `json:"slug"`
		} `json:"reviewRequests"`
		Reviews []struct {
			State       string `json:"state"`
			Body        string `json:"body"`
			SubmittedAt string `json:"submittedAt"`
			Author      struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"reviews"`
		Comments []struct {
			Body            string `json:"body"`
			ViewerDidAuthor bool   `json:"viewerDidAuthor"`
			CreatedAt       string `json:"createdAt"`
			Author          struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	me := strings.TrimSpace(currentUser)
	out := make([]ProjectPullRequest, 0, len(raw))
	for _, item := range raw {
		author := strings.TrimSpace(item.Author.Login)
		isMine := me != "" && strings.EqualFold(author, me)

		var reqs []string
		reviewRequested := false
		for _, rr := range item.ReviewRequests {
			name := strings.TrimSpace(rr.Login)
			if name == "" {
				name = strings.TrimSpace(rr.Slug)
			}
			if name == "" {
				name = strings.TrimSpace(rr.Name)
			}
			if name != "" {
				reqs = append(reqs, name)
				if me != "" && strings.EqualFold(name, me) {
					reviewRequested = true
				}
			}
		}

		revs := make([]prActivityReview, 0, len(item.Reviews))
		for _, rv := range item.Reviews {
			revs = append(revs, prActivityReview{
				Author:      rv.Author.Login,
				State:       rv.State,
				Body:        rv.Body,
				SubmittedAt: parseGHTime(rv.SubmittedAt),
			})
		}
		cmts := make([]prActivityComment, 0, len(item.Comments))
		for _, cm := range item.Comments {
			cmts = append(cmts, prActivityComment{
				Author:          cm.Author.Login,
				Body:            cm.Body,
				ViewerDidAuthor: cm.ViewerDidAuthor,
				CreatedAt:       parseGHTime(cm.CreatedAt),
			})
		}
		flow := computePRFlowDetails(me, isMine, reviewRequested, len(reqs) > 0, author, item.Body, parseGHTime(item.UpdatedAt), revs, cmts, time.Time{})

		out = append(out, ProjectPullRequest{
			Number:              item.Number,
			Title:               item.Title,
			Body:                item.Body,
			URL:                 item.URL,
			State:               item.State,
			IsDraft:             item.IsDraft,
			Author:              author,
			HeadRef:             item.HeadRef,
			BaseRef:             item.BaseRef,
			UpdatedAt:           item.UpdatedAt,
			ReviewDecision:      item.ReviewDecision,
			RequestedReviewers:  reqs,
			IsMine:              isMine,
			ReviewRequested:     reviewRequested,
			MyResponse:          flow.MyResponse,
			NeedsAttention:      flow.NeedsAttention,
			AttentionReason:     flow.AttentionReason,
			MentionedMe:         flow.MentionedMe,
			LastActivityAt:      flow.LastActivityAt,
			LastActivityAuthor:  flow.LastActivityAuthor,
			LastActivityKind:    flow.LastActivityKind,
			LastActivitySnippet: flow.LastActivitySnippet,
		})
	}
	return out, nil
}

// ListProjectPullRequests fetches open pull requests for a GitHub-backed workspace,
// preferring the native GitHub GraphQL/REST connector and falling back to `gh` CLI.
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
	client := githubx.Detect(owner, repo)
	currentUser, _ := client.CurrentUser(ctx)

	token, baseURL, _ := githubx.LoadCredentials()
	if token != "" {
		if prs, gqlErr := listPullRequestsGraphQL(ctx, baseURL, owner, repo, token, currentUser); gqlErr == nil {
			return prs, nil
		}
	}

	if ghBin, err := exec.LookPath("gh"); err == nil {
		cmd := exec.CommandContext(ctx, ghBin,
			"pr", "list",
			"--repo", owner+"/"+repo,
			"--state", "open",
			"--limit", "30",
			"--json", "number,title,body,url,state,isDraft,author,headRefName,baseRefName,updatedAt,reviewRequests,reviews,comments,reviewDecision",
		)
		procx.ConfigureContext(cmd)
		var out, errBuf bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errBuf
		if err := cmd.Run(); err == nil {
			if prs, parseErr := ParseGHPRList(out.Bytes(), currentUser); parseErr == nil {
				return prs, nil
			}
		} else if token == "" {
			return nil, fmt.Errorf("gh pr list: %s", strings.TrimSpace(errBuf.String()))
		}
	}

	if token == "" {
		return nil, fmt.Errorf("configure o Conector GitHub Nativo na IDE, defina GITHUB_TOKEN ou autentique o `gh` CLI")
	}
	return listPullRequestsREST(ctx, baseURL, owner, repo, token, currentUser)
}

// CloseOwnGitHubItem closes an open PR ("pr") or GitHub issue ("issue") ONLY if authored by the authenticated user.
// Uses the native GitHub REST connector when available, falling back to `gh` CLI.
func CloseOwnGitHubItem(ctx context.Context, workspace, kind string, number int) error {
	if number <= 0 {
		return fmt.Errorf("número inválido: %d", number)
	}
	normKind := strings.ToLower(strings.TrimSpace(kind))
	if normKind != "pr" && normKind != "issue" {
		return fmt.Errorf("tipo inválido: %s", kind)
	}

	g := gitx.Local{}
	if !g.IsRepo(ctx, workspace) {
		return fmt.Errorf("esta pasta não é um repositório Git")
	}
	remoteURL, err := g.RemoteURL(ctx, workspace)
	if err != nil {
		return err
	}
	owner, repo, ok := githubx.ParseRemote(remoteURL)
	if !ok {
		return fmt.Errorf("remote origin não é GitHub")
	}
	client := githubx.Detect(owner, repo)
	if !client.Enabled() {
		return fmt.Errorf("GitHub não está configurado. Configure o Conector GitHub Nativo ou autentique o `gh`")
	}
	currentUser, err := client.CurrentUser(ctx)
	if err != nil || strings.TrimSpace(currentUser) == "" {
		return fmt.Errorf("não foi possível identificar o usuário GitHub autenticado")
	}

	// Verify ownership directly on GitHub before closing.
	var author string
	if normKind == "pr" {
		pr, err := client.GetPullRequest(ctx, number)
		if err != nil {
			return fmt.Errorf("falha ao verificar autoria de pr #%d: %w", number, err)
		}
		author = pr.Author
	} else {
		iss, err := client.GetIssue(ctx, number)
		if err != nil {
			return fmt.Errorf("falha ao verificar autoria de issue #%d: %w", number, err)
		}
		author = iss.Author
	}
	if !strings.EqualFold(strings.TrimSpace(author), strings.TrimSpace(currentUser)) {
		return fmt.Errorf("somente itens abertos por @%s podem ser fechados aqui (autor: @%s)", currentUser, author)
	}

	if normKind == "pr" {
		if err := client.ClosePullRequest(ctx, number); err != nil {
			return fmt.Errorf("falha ao fechar pr #%d: %w", number, err)
		}
		return nil
	}
	if err := client.CloseIssue(ctx, number); err != nil {
		return fmt.Errorf("falha ao fechar issue #%d: %w", number, err)
	}
	return nil
}

func listPullRequestsGraphQL(ctx context.Context, baseURL, owner, repo, token, currentUser string) ([]ProjectPullRequest, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = githubx.DefaultAPIBaseURL
	}
	endpoint := base + "/graphql"
	if strings.HasSuffix(base, "/api/v3") {
		endpoint = strings.TrimSuffix(base, "/v3") + "/graphql"
	}

	const query = `query($owner:String!,$repo:String!){
  repository(owner:$owner,name:$repo){
    pullRequests(states:OPEN,first:30,orderBy:{field:UPDATED_AT,direction:DESC}){
      nodes{
        number
        title
        body
        url
        state
        isDraft
        headRefName
        baseRefName
        updatedAt
        reviewDecision
        author{ login }
        reviewRequests(first:20){
          nodes{
            requestedReviewer{
              ... on User { login }
            }
          }
        }
        reviews(last:50){
          nodes{
            state
            body
            submittedAt
            author{ login }
          }
        }
        comments(last:50){
          nodes{
            body
            createdAt
            author{ login }
          }
        }
        reviewThreads(last:30){
          nodes{
            comments(last:20){
              nodes{
                body
                createdAt
                author{ login }
              }
            }
          }
        }
        commits(last:1){
          nodes{
            commit{
              committedDate
              pushedDate
            }
          }
        }
      }
    }
  }
}`
	payloadBytes, err := json.Marshal(map[string]any{
		"query": query,
		"variables": map[string]string{
			"owner": owner,
			"repo":  repo,
		},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "HiveMind-IDE")

	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github graphql %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var env struct {
		Data struct {
			Repository struct {
				PullRequests struct {
					Nodes []struct {
						Number         int    `json:"number"`
						Title          string `json:"title"`
						Body           string `json:"body"`
						URL            string `json:"url"`
						State          string `json:"state"`
						IsDraft        bool   `json:"isDraft"`
						HeadRefName    string `json:"headRefName"`
						BaseRefName    string `json:"baseRefName"`
						UpdatedAt      string `json:"updatedAt"`
						ReviewDecision string `json:"reviewDecision"`
						Author         *struct {
							Login string `json:"login"`
						} `json:"author"`
						ReviewRequests struct {
							Nodes []struct {
								RequestedReviewer struct {
									Login string `json:"login"`
								} `json:"requestedReviewer"`
							} `json:"nodes"`
						} `json:"reviewRequests"`
						Reviews struct {
							Nodes []struct {
								State       string `json:"state"`
								Body        string `json:"body"`
								SubmittedAt string `json:"submittedAt"`
								Author      *struct {
									Login string `json:"login"`
								} `json:"author"`
							} `json:"nodes"`
						} `json:"reviews"`
						Comments struct {
							Nodes []struct {
								Body      string `json:"body"`
								CreatedAt string `json:"createdAt"`
								Author    *struct {
									Login string `json:"login"`
								} `json:"author"`
							} `json:"nodes"`
						} `json:"comments"`
						ReviewThreads struct {
							Nodes []struct {
								Comments struct {
									Nodes []struct {
										Body      string `json:"body"`
										CreatedAt string `json:"createdAt"`
										Author    *struct {
											Login string `json:"login"`
										} `json:"author"`
									} `json:"nodes"`
								} `json:"comments"`
							} `json:"nodes"`
						} `json:"reviewThreads"`
						Commits struct {
							Nodes []struct {
								Commit struct {
									CommittedDate string `json:"committedDate"`
									PushedDate    string `json:"pushedDate"`
								} `json:"commit"`
							} `json:"nodes"`
						} `json:"commits"`
					} `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	if len(env.Errors) > 0 && len(env.Data.Repository.PullRequests.Nodes) == 0 {
		return nil, fmt.Errorf("github graphql: %s", env.Errors[0].Message)
	}

	me := strings.TrimSpace(currentUser)
	out := make([]ProjectPullRequest, 0, len(env.Data.Repository.PullRequests.Nodes))
	for _, n := range env.Data.Repository.PullRequests.Nodes {
		author := ""
		if n.Author != nil {
			author = strings.TrimSpace(n.Author.Login)
		}
		isMine := me != "" && strings.EqualFold(author, me)
		var reqs []string
		reviewRequested := false
		for _, rr := range n.ReviewRequests.Nodes {
			login := strings.TrimSpace(rr.RequestedReviewer.Login)
			if login == "" {
				continue
			}
			reqs = append(reqs, login)
			if me != "" && strings.EqualFold(login, me) {
				reviewRequested = true
			}
		}

		revs := make([]prActivityReview, 0, len(n.Reviews.Nodes))
		for _, rv := range n.Reviews.Nodes {
			login := ""
			if rv.Author != nil {
				login = rv.Author.Login
			}
			revs = append(revs, prActivityReview{
				Author:      login,
				State:       rv.State,
				Body:        rv.Body,
				SubmittedAt: parseGHTime(rv.SubmittedAt),
			})
		}

		cmts := make([]prActivityComment, 0, len(n.Comments.Nodes)+len(n.ReviewThreads.Nodes))
		for _, cm := range n.Comments.Nodes {
			login := ""
			if cm.Author != nil {
				login = cm.Author.Login
			}
			cmts = append(cmts, prActivityComment{
				Author:    login,
				Body:      cm.Body,
				CreatedAt: parseGHTime(cm.CreatedAt),
			})
		}
		for _, th := range n.ReviewThreads.Nodes {
			for _, tc := range th.Comments.Nodes {
				login := ""
				if tc.Author != nil {
					login = tc.Author.Login
				}
				cmts = append(cmts, prActivityComment{
					Author:    login,
					Body:      tc.Body,
					CreatedAt: parseGHTime(tc.CreatedAt),
				})
			}
		}

		var latestCommitAt time.Time
		for _, cn := range n.Commits.Nodes {
			if ts := parseGHTime(cn.Commit.PushedDate); ts.After(latestCommitAt) {
				latestCommitAt = ts
			} else if ts := parseGHTime(cn.Commit.CommittedDate); ts.After(latestCommitAt) {
				latestCommitAt = ts
			}
		}

		flow := computePRFlowDetails(me, isMine, reviewRequested, len(reqs) > 0, author, n.Body, parseGHTime(n.UpdatedAt), revs, cmts, latestCommitAt)

		out = append(out, ProjectPullRequest{
			Number:              n.Number,
			Title:               n.Title,
			Body:                n.Body,
			URL:                 n.URL,
			State:               n.State,
			IsDraft:             n.IsDraft,
			Author:              author,
			HeadRef:             n.HeadRefName,
			BaseRef:             n.BaseRefName,
			UpdatedAt:           n.UpdatedAt,
			ReviewDecision:      n.ReviewDecision,
			RequestedReviewers:  reqs,
			IsMine:              isMine,
			ReviewRequested:     reviewRequested,
			MyResponse:          flow.MyResponse,
			NeedsAttention:      flow.NeedsAttention,
			AttentionReason:     flow.AttentionReason,
			MentionedMe:         flow.MentionedMe,
			LastActivityAt:      flow.LastActivityAt,
			LastActivityAuthor:  flow.LastActivityAuthor,
			LastActivityKind:    flow.LastActivityKind,
			LastActivitySnippet: flow.LastActivitySnippet,
		})
	}
	return out, nil
}

func listPullRequestsREST(ctx context.Context, baseURL, owner, repo, token, currentUser string) ([]ProjectPullRequest, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = githubx.DefaultAPIBaseURL
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/pulls?state=open&per_page=30",
		base, url.PathEscape(owner), url.PathEscape(repo))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "HiveMind-IDE")

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
		RequestedReviewers []struct {
			Login string `json:"login"`
		} `json:"requested_reviewers"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	me := strings.TrimSpace(currentUser)
	prs := make([]ProjectPullRequest, 0, len(raw))
	for _, item := range raw {
		author := strings.TrimSpace(item.User.Login)
		var reqs []string
		reviewReq := false
		for _, rr := range item.RequestedReviewers {
			login := strings.TrimSpace(rr.Login)
			if login == "" {
				continue
			}
			reqs = append(reqs, login)
			if me != "" && strings.EqualFold(login, me) {
				reviewReq = true
			}
		}
		prs = append(prs, ProjectPullRequest{
			Number:             item.Number,
			Title:              item.Title,
			Body:               item.Body,
			URL:                item.HTMLURL,
			State:              strings.ToUpper(item.State),
			IsDraft:            item.Draft,
			Author:             author,
			HeadRef:            item.Head.Ref,
			BaseRef:            item.Base.Ref,
			UpdatedAt:          item.UpdatedAt,
			RequestedReviewers: reqs,
			IsMine:             me != "" && strings.EqualFold(author, me),
			ReviewRequested:    reviewReq,
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
