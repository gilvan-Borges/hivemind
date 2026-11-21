package idecore

import (
	"context"

	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
)

// Session DTOs and listing functions moved to internal/sessionx (single
// source shared by the IDE and the headless `hivemind serve` host, Spec 09).
// The aliases below keep the existing idecore API surface compiling; the
// wrappers keep the exported function names.

type TokenStat = sessionx.TokenStat
type SessionSummary = sessionx.SessionSummary
type WorkspaceSessionsSummary = sessionx.WorkspaceSessionsSummary
type RestoredMessage = sessionx.RestoredMessage
type SessionDetail = sessionx.SessionDetail

// ListWorkspaceSessions reads all persisted sessions (newest first) and computes token totals.
func ListWorkspaceSessions(ctx context.Context, workspace string) (WorkspaceSessionsSummary, error) {
	return sessionx.ListWorkspaceSessions(ctx, workspace)
}

// LoadSessionDetail reconstructs the chat transcript and per-agent token stats for sessionID.
func LoadSessionDetail(ctx context.Context, workspace, sessionID string) (SessionDetail, error) {
	return sessionx.LoadSessionDetail(ctx, workspace, sessionID)
}

// DeleteWorkspaceSession removes a saved session from .hivemind/sessions.
func DeleteWorkspaceSession(ctx context.Context, workspace, sessionID string) error {
	return sessionx.DeleteWorkspaceSession(ctx, workspace, sessionID)
}
