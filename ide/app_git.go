package main

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// SaveTeamWizard creates or updates a team in .hivemind/team.yaml without altering .gitignore.
func (a *App) SaveTeamWizard(workspace string, input idecore.WizardInput) (team.File, error) {
	return idecore.BuildTeamFromWizard(workspace, input)
}

// SaveSkillAssignments updates settings.skill_assignments in .hivemind/team.yaml.
func (a *App) SaveSkillAssignments(workspace string, assignments []team.SkillAssignment) (team.File, error) {
	return idecore.SaveSkillAssignments(workspace, assignments)
}

// SetActiveTeam switches the active team in .hivemind/team.yaml.
func (a *App) SetActiveTeam(workspace, teamName string) (team.File, error) {
	cfg, err := team.Load(workspace)
	if err != nil {
		return team.File{}, err
	}
	if _, err := cfg.ActiveTeam(teamName); err != nil {
		return team.File{}, err
	}
	cfg.Active = teamName
	if err := team.Save(workspace, cfg); err != nil {
		return team.File{}, err
	}
	return cfg, nil
}

// DeleteTeam removes teamName from .hivemind/team.yaml.
func (a *App) DeleteTeam(workspace, teamName string) (team.File, error) {
	return idecore.DeleteTeam(workspace, teamName)
}

// GetDiff returns parsed unified diffs for the workspace.
func (a *App) GetDiff(workspace string) ([]idecore.FileDiff, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return idecore.WorkspaceDiff(ctx, workspace)
}

// GetWorkspaceGitRepos discovers root and nested Git repositories inside workspace along with their diffs.
func (a *App) GetWorkspaceGitRepos(workspace string) ([]idecore.WorkspaceGitRepo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return idecore.DiscoverGitRepos(ctx, workspace)
}

// CommitChanges commits (and optionally pushes) workspace changes.
func (a *App) CommitChanges(workspace, message string, push bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return idecore.CommitAndPush(ctx, workspace, message, push)
}

// OpenPR pushes the current branch and opens a GitHub pull request.
func (a *App) OpenPR(workspace, title, body, baseBranch string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pr, err := idecore.OpenPullRequest(ctx, workspace, title, body, baseBranch)
	if err != nil {
		return "", err
	}
	return pr.URL, nil
}

// CommentJira posts a comment to a Jira issue.
func (a *App) CommentJira(workspace, issueKey, comment string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return idecore.CommentJiraIssue(ctx, workspace, issueKey, comment)
}

// DiscardChanges restores tracked files to HEAD.
func (a *App) DiscardChanges(workspace string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return idecore.DiscardWorkspaceChanges(ctx, workspace)
}

// DiscardFile restores a single tracked file to HEAD or deletes an untracked file.
func (a *App) DiscardFile(workspace, relPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return idecore.DiscardFileChanges(ctx, workspace, relPath)
}

// StageFile stages or unstages a single file in the Git index.
func (a *App) StageFile(workspace, relPath string, stage bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return idecore.StageWorkspaceFile(ctx, workspace, relPath, stage)
}

type gitCall struct {
	cancel context.CancelFunc
	gen    uint64
}

func gitWorkspaceKey(workspace string) string {
	return strings.ToLower(filepath.Clean(strings.TrimSpace(workspace)))
}

// RunGitAction executes safe Source Control actions ("fetch", "pull", "push", "sync", "stash", "stash-pop").
// The timeout comes from HIVEMIND_GIT_TIMEOUT or the 2 minute default.
func (a *App) RunGitAction(workspace, action string) (string, error) {
	return a.RunGitActionTimeout(workspace, action, 0)
}

// RunGitActionTimeout is RunGitAction with a caller-chosen limit in seconds.
// Progress lines are emitted on "hivemind:git-progress". CancelGitAction kills the child.
func (a *App) RunGitActionTimeout(workspace, action string, timeoutSec int) (string, error) {
	ctx, done := a.beginGit(workspace, idecore.ResolveGitTimeout(timeoutSec))
	defer done()
	res, err := idecore.RunGitActionProgress(ctx, workspace, action, func(p idecore.GitProgress) {
		if a.ctx == nil || a.smokeCheck {
			return
		}
		wailsrt.EventsEmit(a.ctx, "hivemind:git-progress", map[string]any{
			"workspace": workspace,
			"action":    action,
			"phase":     p.Phase,
			"label":     p.Label,
			"line":      p.Line,
		})
	})
	if err != nil {
		return "", err
	}
	return res.Summary, nil
}

// CancelGitAction kills the git child running for workspace.
func (a *App) CancelGitAction(workspace string) bool {
	key := gitWorkspaceKey(workspace)
	a.gitMu.Lock()
	cur, ok := a.gitCalls[key]
	if ok {
		delete(a.gitCalls, key)
	}
	a.gitMu.Unlock()
	if !ok || cur.cancel == nil {
		return false
	}
	cur.cancel()
	return true
}

func (a *App) beginGit(workspace string, timeout time.Duration) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	key := gitWorkspaceKey(workspace)
	a.gitMu.Lock()
	if a.gitCalls == nil {
		a.gitCalls = map[string]gitCall{}
	}
	if prev, ok := a.gitCalls[key]; ok && prev.cancel != nil {
		prev.cancel()
	}
	a.gitGen++
	gen := a.gitGen
	a.gitCalls[key] = gitCall{cancel: cancel, gen: gen}
	a.gitMu.Unlock()
	done := func() {
		cancel()
		a.gitMu.Lock()
		if cur, ok := a.gitCalls[key]; ok && cur.gen == gen {
			delete(a.gitCalls, key)
		}
		a.gitMu.Unlock()
	}
	return ctx, done
}

// ListGitGraph returns commits in topological order for the Source Control Graph.
func (a *App) ListGitGraph(workspace string, limit int, allBranches bool) ([]idecore.GitGraphCommit, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return idecore.ListGitGraph(ctx, workspace, limit, allBranches)
}

// GetCommitDetail returns commit metadata and file diffs for a commit hash.
func (a *App) GetCommitDetail(workspace, hash string) (idecore.CommitDetail, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return idecore.GetCommitDetail(ctx, workspace, hash)
}

// RevertHunk reverses a single unified diff hunk on relPath.
func (a *App) RevertHunk(workspace, relPath, hunkHeader string, hunkLines []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return idecore.RevertDiffHunk(ctx, workspace, relPath, hunkHeader, hunkLines)
}

// StageHunk stages (accepts) a single unified diff hunk on relPath into the Git index.
func (a *App) StageHunk(workspace, relPath, hunkHeader string, hunkLines []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return idecore.StageDiffHunk(ctx, workspace, relPath, hunkHeader, hunkLines)
}

// ApplyHunk applies a single unified diff hunk onto relPath in the working tree.
func (a *App) ApplyHunk(workspace, relPath, hunkHeader string, hunkLines []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return idecore.ApplyDiffHunk(ctx, workspace, relPath, hunkHeader, hunkLines)
}

// GetFileUnstagedDiff returns only the unstaged hunks (working tree vs index) for relPath.
func (a *App) GetFileUnstagedDiff(workspace, relPath string) (*idecore.FileDiff, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return idecore.UnstagedFileDiff(ctx, workspace, relPath)
}

// GetInlineAIStatus reports Ghost Text and Ctrl+K Inline Edit availability and models.

// GetAutoReviewStatus returns the current background Flow auto-review status for workspace.
func (a *App) GetAutoReviewStatus(workspace string) idecore.AutoReviewStatus {
	if a.autoReview == nil {
		return idecore.AutoReviewStatus{Workspace: workspace, State: "idle"}
	}
	return a.autoReview.GetStatus(workspace)
}

// SetAutoReviewConfig persists and applies automatic Flow PR review settings for workspace.
// Automatic reviews NEVER merge pull requests (hard-gated in tools/orchestrator).
func (a *App) SetAutoReviewConfig(workspace string, cfg team.AutoReviewSettings) (idecore.AutoReviewStatus, error) {
	if a.autoReview == nil {
		return idecore.AutoReviewStatus{Workspace: workspace, State: "idle"}, nil
	}
	baseCtx := a.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	return a.autoReview.Configure(baseCtx, workspace, cfg)
}

// TriggerAutoReviewPoll kicks an immediate Flow check for workspace when auto-review is enabled.
func (a *App) TriggerAutoReviewPoll(workspace string) idecore.AutoReviewStatus {
	if a.autoReview == nil {
		return idecore.AutoReviewStatus{Workspace: workspace, State: "idle"}
	}
	return a.autoReview.TriggerPoll(workspace)
}

// StopActiveAutoReview cancels only the currently running automatic PR review in workspace.
func (a *App) StopActiveAutoReview(workspace string) bool {
	if a.autoReview == nil {
		return false
	}
	return a.autoReview.StopActivePR(workspace)
}

// GitHubStatus returns the native GitHub connector / CLI authentication and workspace remote info.
func (a *App) GitHubStatus(workspace string) idecore.GitHubStatusInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return idecore.CheckGitHubStatus(ctx, workspace)
}

// SaveGitHubConfig stores or clears the native GitHub Personal Access Token (PAT) and optional API base URL,
// returning the refreshed connection status.
func (a *App) SaveGitHubConfig(token, baseURL, workspace string) (idecore.GitHubStatusInfo, error) {
	if err := githubx.SaveCredentials(token, baseURL); err != nil {
		return idecore.GitHubStatusInfo{}, err
	}
	st := a.GitHubStatus(workspace)
	if a.githubMonitor != nil && !a.smokeCheck && strings.TrimSpace(workspace) != "" && st.LoggedIn {
		baseCtx := a.ctx
		if baseCtx == nil {
			baseCtx = context.Background()
		}
		a.githubMonitor.EnsureWatching(baseCtx, workspace)
		a.githubMonitor.TriggerNow(workspace)
	}
	return st, nil
}

// InitGit initializes a Git repository in workspace.
func (a *App) InitGit(workspace string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return idecore.InitGitRepo(ctx, workspace)
}

// JiraStatus returns the native Jira Cloud connector status (env vars or saved config).

// ListBranches lists local Git branches in workspace.
func (a *App) ListBranches(workspace string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return idecore.ListGitBranches(ctx, workspace)
}

// CheckoutBranch switches or creates a Git branch in workspace.
func (a *App) CheckoutBranch(workspace, branch string, create bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return idecore.CheckoutGitBranch(ctx, workspace, branch, create)
}

// ListWorktrees returns active and orphan worktrees managed under workspace.
func (a *App) ListWorktrees(workspace string) ([]idecore.WorktreeInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return idecore.ListWorktrees(ctx, workspace)
}

// CreateWorktreeAsync creates and hydrates a worktree, emitting progress on "hivemind:worktree-progress".
func (a *App) CreateWorktreeAsync(workspace, agent, branch string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return idecore.CreateWorktreeAsync(ctx, workspace, agent, branch, func(step string, pct int) {
		if a.ctx == nil || a.smokeCheck {
			return
		}
		wailsrt.EventsEmit(a.ctx, "hivemind:worktree-progress", map[string]any{
			"workspace": workspace,
			"agent":     agent,
			"branch":    branch,
			"step":      step,
			"pct":       pct,
		})
	})
}

// ArchiveWorktree safely removes one managed HiveMind worktree.
func (a *App) ArchiveWorktree(workspace, wtPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return idecore.ArchiveWorktree(ctx, workspace, wtPath)
}

// PruneWorktrees prunes stale worktree refs and deletes orphan directories.
func (a *App) PruneWorktrees(workspace string) (idecore.WorktreePruneResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return idecore.PruneWorktrees(ctx, workspace)
}

// ListWorktreeCheckpoints lists recorded turn checkpoints for workspace.
func (a *App) ListWorktreeCheckpoints(workspace string) ([]idecore.WorktreeCheckpoint, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return idecore.ListWorktreeCheckpoints(ctx, workspace)
}

// RollbackWorktreeCheckpoint restores wtDir (or workspace if empty) to checkpointID.
func (a *App) RollbackWorktreeCheckpoint(workspace, wtDir, checkpointID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return idecore.RollbackWorktreeCheckpoint(ctx, workspace, wtDir, checkpointID)
}

// ListIssues fetches open Jira and/or GitHub issues for workspace.
func (a *App) ListIssues(workspace string) ([]idecore.ProjectIssue, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return idecore.ListProjectIssues(ctx, workspace)
}

// ListPullRequests fetches open GitHub pull requests for workspace.
func (a *App) ListPullRequests(workspace string) ([]idecore.ProjectPullRequest, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	prs, err := idecore.ListProjectPullRequests(ctx, workspace)
	if err == nil && a.githubMonitor != nil {
		a.githubMonitor.RecordSnapshot(workspace, prs)
	}
	return prs, err
}

// GetPullRequestDetail fetches stats and latest chronological reviews/comments for one PR on demand.
func (a *App) GetPullRequestDetail(workspace string, number int) (idecore.PullRequestActivityDetail, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return idecore.GetProjectPullRequestDetail(ctx, workspace, number)
}

// CloseOwnGitHubItem closes a GitHub PR ("pr") or issue ("issue") opened by the authenticated user.
func (a *App) CloseOwnGitHubItem(workspace, kind string, number int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return idecore.CloseOwnGitHubItem(ctx, workspace, kind, number)
}

// GitBlameFile returns per-line Git blame metadata for relPath inside workspace.
func (a *App) GitBlameFile(workspace, relPath string) ([]idecore.BlameLine, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return idecore.GitBlameFile(ctx, workspace, relPath)
}

// GitFileHistory returns commit history for relPath across renames via `git log --follow`.
func (a *App) GitFileHistory(workspace, relPath string, limit int) ([]idecore.FileCommitEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return idecore.GitFileHistory(ctx, workspace, relPath, limit)
}

// ListConflictedFiles returns unmerged files in workspace along with parsed conflict hunks.
func (a *App) ListConflictedFiles(workspace string) ([]idecore.ConflictedFile, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return idecore.ListConflictedFiles(ctx, workspace)
}

// ResolveConflictBlock applies "ours", "theirs", or "both" to one conflict hunk in relPath.
func (a *App) ResolveConflictBlock(workspace, relPath string, blockIndex int, choice string) (idecore.ConflictedFile, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return idecore.ResolveConflictBlock(ctx, workspace, relPath, blockIndex, choice)
}

// MarkConflictResolved verifies no conflict markers remain in relPath and stages the file.
func (a *App) MarkConflictResolved(workspace, relPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return idecore.MarkConflictResolved(ctx, workspace, relPath)
}
