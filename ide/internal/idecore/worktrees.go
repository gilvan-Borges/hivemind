package idecore

import (
	"context"
	"fmt"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

type (
	WorktreeInfo       = gitx.WorktreeInfo
	WorktreeCheckpoint = gitx.Checkpoint
	WorktreePruneResult = gitx.PruneResult
)

// ListWorktrees returns active and orphan worktrees for the project rooted at workspace.
func ListWorktrees(ctx context.Context, workspace string) ([]gitx.WorktreeInfo, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, nil
	}
	return gitx.ListWorktrees(ctx, gitx.Local{}, workspace)
}

// CreateWorktreeAsync creates and hydrates a worktree while emitting progress updates.
func CreateWorktreeAsync(ctx context.Context, workspace, agent, branch string, onProgress func(step string, pct int)) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", fmt.Errorf("workspace inválido")
	}
	var sharedDirs []string
	if proj, err := team.Load(workspace); err == nil {
		sharedDirs = proj.Settings.SharedDirectories
	}
	return gitx.CreateWorktreeWithProgress(ctx, gitx.Local{}, workspace, agent, branch, sharedDirs, onProgress)
}

// ArchiveWorktree removes a managed HiveMind worktree safely.
func ArchiveWorktree(ctx context.Context, workspace, wtPath string) error {
	return gitx.ArchiveWorktree(ctx, gitx.Local{}, workspace, wtPath)
}

// PruneWorktrees prunes stale Git worktree refs and deletes orphan directories.
func PruneWorktrees(ctx context.Context, workspace string) (gitx.PruneResult, error) {
	return gitx.PruneWorktrees(ctx, gitx.Local{}, workspace)
}

// ListWorktreeCheckpoints returns HiveMind checkpoints recorded for workspace.
func ListWorktreeCheckpoints(ctx context.Context, workspace string) ([]gitx.Checkpoint, error) {
	return gitx.ListCheckpoints(ctx, gitx.Local{}, workspace)
}

// RollbackWorktreeCheckpoint restores wtDir (or workspace if empty) to checkpointID.
func RollbackWorktreeCheckpoint(ctx context.Context, workspace, wtDir, checkpointID string) error {
	target := strings.TrimSpace(wtDir)
	if target == "" {
		target = workspace
	}
	return gitx.RollbackCheckpoint(ctx, gitx.Local{}, target, checkpointID)
}
