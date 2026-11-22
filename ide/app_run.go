package main

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) GetInlineAIStatus(workspace string) idecore.InlineAIStatus {
	return idecore.GetInlineAIStatus(workspace)
}

// InlineComplete generates a low-latency FIM ghost-text completion at the editor cursor,
// cancelling any previous in-flight ghost-text request.
func (a *App) InlineComplete(workspace, relPath, prefix, suffix, model string) (idecore.InlineCompleteResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	a.inlineMu.Lock()
	if a.ghostCancel != nil {
		a.ghostCancel()
	}
	a.ghostCancel = cancel
	a.inlineMu.Unlock()
	defer func() {
		cancel()
		a.inlineMu.Lock()
		a.ghostCancel = nil
		a.inlineMu.Unlock()
	}()
	return idecore.CompleteInlineCode(ctx, idecore.InlineCompleteRequest{
		Workspace: workspace,
		FilePath:  relPath,
		Prefix:    prefix,
		Suffix:    suffix,
		Model:     model,
	})
}

// InlineEdit generates a surgical replacement and line diff preview for Ctrl+K editing.
func (a *App) InlineEdit(workspace, relPath, instruction, selectedText string, startLine, endLine int, beforeCtx, afterCtx, model string) (idecore.InlineEditResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	a.inlineMu.Lock()
	if a.editCancel != nil {
		a.editCancel()
	}
	a.editCancel = cancel
	a.inlineMu.Unlock()
	defer func() {
		cancel()
		a.inlineMu.Lock()
		a.editCancel = nil
		a.inlineMu.Unlock()
	}()
	return idecore.GenerateInlineEdit(ctx, idecore.InlineEditRequest{
		Workspace:     workspace,
		FilePath:      relPath,
		Instruction:   instruction,
		SelectedText:  selectedText,
		StartLine:     startLine,
		EndLine:       endLine,
		BeforeContext: beforeCtx,
		AfterContext:  afterCtx,
		Model:         model,
	})
}

// CancelInlineAI cancels any active Ghost Text or Ctrl+K Inline Edit request.
func (a *App) CancelInlineAI() {
	a.inlineMu.Lock()
	g := a.ghostCancel
	e := a.editCancel
	a.ghostCancel = nil
	a.editCancel = nil
	a.inlineMu.Unlock()
	if g != nil {
		g()
	}
	if e != nil {
		e()
	}
}

// StartRun runs one objective through Chefe -> Dev -> QA -> Chefe on the given lane.
func (a *App) StartRun(laneID, workspace, teamName, objective, jiraKey, sessionID string) (orchestrator.Result, error) {
	return a.StartRunWithImages(laneID, workspace, teamName, objective, jiraKey, sessionID, nil)
}

// StartRunWithImages runs one objective with optional base64/data-URL image attachments.
func (a *App) StartRunWithImages(
	laneID, workspace, teamName, objective, jiraKey, sessionID string,
	images []idecore.ImageInput,
) (orchestrator.Result, error) {
	return a.StartRunWithOptions(laneID, workspace, teamName, objective, jiraKey, sessionID, "auto", images)
}

// StartRunWithOptions runs one objective using an explicit multi-agent execution mode ("auto", "direct", "plan", "squad").
func (a *App) StartRunWithOptions(
	laneID, workspace, teamName, objective, jiraKey, sessionID, runMode string,
	images []idecore.ImageInput,
) (orchestrator.Result, error) {
	if a.mobile != nil {
		a.mobile.hub.RegisterRun(laneID, workspace, teamName, objective, sessionID, runMode)
	}
	baseCtx := a.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	res, err := a.sessions.RunTurnWithOptions(baseCtx, laneID, workspace, teamName, objective, jiraKey, sessionID, runMode, images)
	if a.mobile != nil && res.SessionID != "" {
		a.mobile.hub.SetSessionID(laneID, res.SessionID)
	}
	if a.fileIndex != nil && strings.TrimSpace(workspace) != "" {
		go func(root string) {
			_ = a.fileIndex.Refresh(root)
		}(workspace)
	}
	return res, err
}

// UpdateAgentModel switches the LLM model of a single agent inside .hivemind/team.yaml.
func (a *App) UpdateAgentModel(workspace, teamName, agentName, modelSpec string) (team.File, error) {
	return idecore.UpdateTeamAgentModel(workspace, teamName, agentName, modelSpec)
}

// UpdatePermissionMode switches settings.permission_mode ("governed", "auto_edit", "full_access") in .hivemind/team.yaml.
func (a *App) UpdatePermissionMode(workspace, permissionMode string) (team.File, error) {
	return idecore.UpdateProjectPermissionMode(workspace, permissionMode)
}

// UpdateProjectJira sets settings.jira_project in .hivemind/team.yaml ("", "NONE", "*", or a Jira project key like "IPG").
func (a *App) UpdateProjectJira(workspace, jiraProjectKey string) (team.File, error) {
	return idecore.UpdateProjectJiraKey(workspace, jiraProjectKey)
}

// RespondApproval delivers the user's decision on a pending approval gate (identified by approvalID or laneID).
func (a *App) RespondApproval(approvalID string, approved bool) bool {
	ok := a.sessions.ResolveApproval(approvalID, approved)
	if ok && a.mobile != nil {
		a.mobile.hub.MarkApprovalResolved(approvalID, approved)
	}
	return ok
}

// EnsureTeamMemory creates .hivemind/MEMORY.md if absent and returns its relative path for the editor.
func (a *App) EnsureTeamMemory(workspace string) (string, error) {
	return idecore.EnsureWorkspaceMemory(workspace)
}

// CancelRun cancels an active run on laneID.
func (a *App) CancelRun(laneID string) bool {
	if a.mobile != nil {
		a.mobile.hub.MarkCancel(laneID)
	}
	return a.sessions.Cancel(laneID)
}

// SteerRun sends a live roundtable directive into an active run on laneID without cancelling it.
func (a *App) SteerRun(laneID, message string) bool {
	return a.sessions.Steer(laneID, message)
}

// RunCommand executes a command inside workspace for the integrated IDE terminal (Ctrl+J),
// streaming stdout/stderr lines in real time via Wails event "hivemind:terminal-output".
func (a *App) RunCommand(workspace, command string) (idecore.CommandOutput, error) {
	key := strings.ToLower(strings.TrimSpace(filepath.Clean(workspace)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)

	a.termMu.Lock()
	if prev, ok := a.termCancels[key]; ok && prev != nil {
		prev()
	}
	a.termCancels[key] = cancel
	a.termMu.Unlock()

	defer func() {
		cancel()
		a.termMu.Lock()
		delete(a.termCancels, key)
		a.termMu.Unlock()
	}()

	return idecore.RunWorkspaceCommandStream(ctx, workspace, command, func(chunk string) {
		if a.ctx != nil && !a.smokeCheck {
			wailsrt.EventsEmit(a.ctx, "hivemind:terminal-output", map[string]any{
				"workspace": workspace,
				"chunk":     chunk,
			})
		}
	})
}

// CancelCommand aborts the currently running terminal command for workspace.
func (a *App) CancelCommand(workspace string) bool {
	key := strings.ToLower(strings.TrimSpace(filepath.Clean(workspace)))
	a.termMu.Lock()
	cancel, ok := a.termCancels[key]
	if ok {
		delete(a.termCancels, key)
	}
	a.termMu.Unlock()
	if ok && cancel != nil {
		cancel()
		return true
	}
	return false
}
