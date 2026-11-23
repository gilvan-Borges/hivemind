package idecore

import (
	"context"
	"fmt"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/remote"
	"github.com/gilvan-Borges/HiveMind/internal/resident"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// Adapter is the IDE implementation of remote.Deps: it wires the bridge HTTP
// API to the SessionManager, ResidentManager and AutoReviewManager services.
// It lives in the IDE module (not in internal/remote) because the managers are
// IDE-built; the headless CLI serve host provides its own remote.Deps.
type Adapter struct {
	Sessions   *SessionManager
	Resident   *ResidentManager
	AutoReview *AutoReviewManager
	Hub        *remote.Hub
}

var _ remote.Deps = (*Adapter)(nil)
var _ remote.ProjectControls = (*Adapter)(nil)

// RunStart registers the run in the Hub, echoes the mobile message into the
// IDE chat (bidirectional sync) and starts the turn asynchronously. Errors
// surface through SSE as lane error events.
//
// When runMode == "resident", the turn routes exclusively to ResidentManager on
// fixed lane "resident" with OriginRemote â€” it never touches the squad runner
// or inherits permission_mode (Â§1.10, Â§10).
func (a *Adapter) RunStart(
	ctx context.Context,
	laneID, workspace, teamName, objective, jiraKey, sessionID, runMode string,
	images []remote.ImageInput,
) error {
	if strings.EqualFold(strings.TrimSpace(runMode), resident.RunMode) {
		if a.Resident == nil {
			return fmt.Errorf("agente residente indisponÃ­vel neste host")
		}
		laneID = resident.LaneID
		teamName = "hive-resident"
		a.Hub.RegisterRun(laneID, workspace, teamName, objective, sessionID, resident.RunMode)
		if a.Sessions != nil && a.Sessions.Emit != nil {
			text := strings.TrimSpace(objective)
			if text != "" {
				a.Sessions.Emit(remote.LaneEvent{
					LaneID: laneID,
					Team:   teamName,
					Event:  remote.UserMessageEvent(text),
				})
			}
		}
		go func() {
			_, err := a.Resident.RunTurnIn(context.Background(), workspace, "", objective, sessionID, resident.OriginRemote)
			if err != nil {
				a.Hub.HandleRunError(laneID, workspace, err)
			}
		}()
		return nil
	}

	// Mobile often omits sessionId on follow-up messages; reuse the lane binding
	// so each App turn does not open a fresh .hivemind/sessions file.
	if strings.TrimSpace(sessionID) == "" && a.Hub != nil {
		sessionID = a.Hub.LaneSessionID(laneID)
	}
	a.Hub.RegisterRun(laneID, workspace, teamName, objective, sessionID, runMode)
	if a.Sessions != nil && a.Sessions.Emit != nil {
		text := strings.TrimSpace(objective)
		if text != "" {
			a.Sessions.Emit(remote.LaneEvent{
				LaneID: laneID,
				Team:   teamName,
				Event:  remote.UserMessageEvent(text),
			})
		}
	}
	go func() {
		if a.Sessions == nil {
			return
		}
		res, err := a.Sessions.RunTurnWithOptions(context.Background(), laneID, workspace, teamName, objective, jiraKey, sessionID, runMode, images)
		if res.SessionID != "" {
			a.Hub.SetSessionID(laneID, res.SessionID)
		}
		if err != nil {
			a.Hub.HandleRunError(laneID, workspace, err)
		}
	}()
	return nil
}

// RunSteer injects a live note into the running lane without cancelling it.
func (a *Adapter) RunSteer(laneID, message string) bool {
	if a == nil || a.Sessions == nil {
		return false
	}
	return a.Sessions.Steer(laneID, message)
}

// RunCancel stops the active run on the lane.
func (a *Adapter) RunCancel(laneID string) bool {
	if laneID == resident.LaneID && a.Resident != nil {
		ok := a.Resident.Cancel()
		a.Hub.MarkCancel(laneID)
		return ok
	}
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
func (a *Adapter) ListSessions(ctx context.Context, workspace string) (WorkspaceSessionsSummary, error) {
	return ListWorkspaceSessions(ctx, workspace)
}

// LoadSession loads one saved session's messages to continue it.
func (a *Adapter) LoadSession(ctx context.Context, workspace, id string) (SessionDetail, error) {
	return LoadSessionDetail(ctx, workspace, id)
}

// Diff lists modified files and hunks for a workspace.
func (a *Adapter) Diff(ctx context.Context, workspace string) ([]FileDiff, error) {
	return WorkspaceDiff(ctx, workspace)
}

// ListPRs lists open pull requests with review state (Fluxo).
func (a *Adapter) ListPRs(ctx context.Context, workspace string) ([]ProjectPullRequest, error) {
	return ListProjectPullRequests(ctx, workspace)
}

// ListIssues lists the user's Jira tasks (assignee = currentUser()) + GitHub issues.
func (a *Adapter) ListIssues(ctx context.Context, workspace string) ([]ProjectIssue, error) {
	return ListProjectIssues(ctx, workspace)
}

// AutoReviewStatus returns the Auto-Review snapshot for a workspace.
func (a *Adapter) AutoReviewStatus(workspace string) AutoReviewStatus {
	if a.AutoReview == nil {
		return AutoReviewStatus{Workspace: workspace, State: "idle"}
	}
	return a.AutoReview.GetStatus(workspace)
}

// SetPermissionMode stores the project's Full / governed mode in team.yaml.
func (a *Adapter) SetPermissionMode(workspace, mode string) error {
	_, err := UpdateProjectPermissionMode(workspace, mode)
	return err
}

func (a *Adapter) PermissionMode(workspace string) string {
	cfg, err := team.Load(workspace)
	if err != nil {
		return ""
	}
	return cfg.Settings.EffectivePermissionMode()
}

func (a *Adapter) Commit(ctx context.Context, workspace, message string, push bool) (string, error) {
	return CommitAndPush(ctx, workspace, message, push)
}

func (a *Adapter) OpenPullRequest(ctx context.Context, workspace, title, body, baseBranch string) (string, error) {
	pr, err := OpenPullRequest(ctx, workspace, title, body, baseBranch)
	if err != nil {
		return "", err
	}
	return pr.URL, nil
}

func (a *Adapter) ListRoutines() ([]remote.RoutineInfo, error) {
	if a.Resident == nil {
		return []remote.RoutineInfo{}, nil
	}
	items, err := a.Resident.ListRoutines()
	if err != nil {
		return nil, err
	}
	out := make([]remote.RoutineInfo, 0, len(items))
	for _, rt := range items {
		out = append(out, remote.RoutineInfo{
			ID: rt.ID, Expr: rt.Expr, Timezone: rt.Timezone, Text: rt.Text,
			Workstream: rt.Workstream, Enabled: rt.Enabled,
		})
	}
	return out, nil
}

func (a *Adapter) SetRoutineEnabled(id string, enabled bool) error {
	if a.Resident == nil {
		return fmt.Errorf("agente residente indisponível")
	}
	return a.Resident.SetRoutineEnabled(id, enabled)
}

func (a *Adapter) MarkSessionRead(ctx context.Context, workspace, sessionID string) error {
	return sessionx.MarkSessionRead(ctx, workspace, sessionID)
}

func (a *Adapter) AutoReviewPoll(workspace string) AutoReviewStatus {
	if a.AutoReview == nil {
		return AutoReviewStatus{Workspace: workspace, State: "idle"}
	}
	return a.AutoReview.TriggerPoll(workspace)
}
