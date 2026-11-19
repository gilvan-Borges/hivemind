package remote

import (
	"context"
	"strings"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
)

// Deps abstracts the IDE services the HTTP API drives. It is implemented by
// the Adapter below and used by both the IDE (Wails) and the headless
// `hivemind serve` host.
type Deps interface {
	RunStart(ctx context.Context, laneID, workspace, teamName, objective, jiraKey, sessionID, runMode string, images []idecore.ImageInput) error
	RunCancel(laneID string) bool
	ApprovalResolve(id string, approved bool) bool
	ListSessions(ctx context.Context, workspace string) (idecore.WorkspaceSessionsSummary, error)
	LoadSession(ctx context.Context, workspace, id string) (idecore.SessionDetail, error)
	Diff(ctx context.Context, workspace string) ([]idecore.FileDiff, error)
	ListPRs(ctx context.Context, workspace string) ([]idecore.ProjectPullRequest, error)
	ListIssues(ctx context.Context, workspace string) ([]idecore.ProjectIssue, error)
	AutoReviewStatus(workspace string) idecore.AutoReviewStatus
	AutoReviewPoll(workspace string) idecore.AutoReviewStatus
}

// Adapter is the production Deps implementation wired to a SessionManager and
// an AutoReviewManager.
type Adapter struct {
	Sessions   *idecore.SessionManager
	AutoReview *idecore.AutoReviewManager
	Hub        *Hub
}

var _ Deps = (*Adapter)(nil)

// RunStart registers the run in the Hub, echoes the mobile message into the
// IDE chat (bidirectional sync) and starts the turn asynchronously. Errors
// surface through SSE as lane error events.
func (a *Adapter) RunStart(
	ctx context.Context,
	laneID, workspace, teamName, objective, jiraKey, sessionID, runMode string,
	images []idecore.ImageInput,
) error {
	a.Hub.RegisterRun(laneID, workspace, teamName, objective, sessionID, runMode)
	if a.Sessions != nil && a.Sessions.Emit != nil {
		text := strings.TrimSpace(objective)
		if text != "" {
			a.Sessions.Emit(idecore.LaneEvent{
				LaneID: laneID,
				Team:   teamName,
				Event:  userMessageEvent(text),
			})
		}
	}
	go func() {
		if a.Sessions == nil {
			return
		}
		_, err := a.Sessions.RunTurnWithOptions(context.Background(), laneID, workspace, teamName, objective, jiraKey, sessionID, runMode, images)
		if err != nil {
			a.Hub.HandleRunError(laneID, workspace, err)
		}
	}()
	return nil
}

// RunCancel stops the active run on the lane.
func (a *Adapter) RunCancel(laneID string) bool {
	if a.Sessions == nil {
		return false
	}
	ok := a.Sessions.Cancel(laneID)
	a.Hub.MarkCancel(laneID)
	return ok
}

// ApprovalResolve approves or rejects a pending gate.
func (a *Adapter) ApprovalResolve(id string, approved bool) bool {
	if a.Sessions == nil {
		return false
	}
	ok := a.Sessions.ResolveApproval(id, approved)
	if ok {
		a.Hub.MarkApprovalResolved(id, approved)
	}
	return ok
}

// ListSessions lists saved sessions and total token consumption.
func (a *Adapter) ListSessions(ctx context.Context, workspace string) (idecore.WorkspaceSessionsSummary, error) {
	return idecore.ListWorkspaceSessions(ctx, workspace)
}

// LoadSession loads one saved session's messages to continue it.
func (a *Adapter) LoadSession(ctx context.Context, workspace, id string) (idecore.SessionDetail, error) {
	return idecore.LoadSessionDetail(ctx, workspace, id)
}

// Diff lists modified files and hunks for a workspace.
func (a *Adapter) Diff(ctx context.Context, workspace string) ([]idecore.FileDiff, error) {
	return idecore.WorkspaceDiff(ctx, workspace)
}

// ListPRs lists open pull requests with review state (Fluxo).
func (a *Adapter) ListPRs(ctx context.Context, workspace string) ([]idecore.ProjectPullRequest, error) {
	return idecore.ListProjectPullRequests(ctx, workspace)
}

// ListIssues lists the user's Jira tasks (assignee = currentUser()) + GitHub issues.
func (a *Adapter) ListIssues(ctx context.Context, workspace string) ([]idecore.ProjectIssue, error) {
	return idecore.ListProjectIssues(ctx, workspace)
}

// AutoReviewStatus returns the Auto-Review snapshot for a workspace.
func (a *Adapter) AutoReviewStatus(workspace string) idecore.AutoReviewStatus {
	if a.AutoReview == nil {
		return idecore.AutoReviewStatus{Workspace: workspace, State: "idle"}
	}
	return a.AutoReview.GetStatus(workspace)
}

// AutoReviewPoll triggers an immediate Auto-Review poll.
func (a *Adapter) AutoReviewPoll(workspace string) idecore.AutoReviewStatus {
	if a.AutoReview == nil {
		return idecore.AutoReviewStatus{Workspace: workspace, State: "idle"}
	}
	return a.AutoReview.TriggerPoll(workspace)
}
