package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App exposes backend capabilities to the Wails frontend.
type App struct {
	ctx         context.Context
	initialDir  string
	smokeCheck  bool
	sessions    *idecore.SessionManager
	fileIndex   *idecore.FileIndex
	mu          sync.Mutex
	agyStatus   modelx.AgyStatus
	modelsCache []modelx.AgyModel
	agyCachedAt time.Time
}

// AgyBootstrap carries the cached or freshly probed agy status and model list.
type AgyBootstrap struct {
	Status modelx.AgyStatus  `json:"status"`
	Models []modelx.AgyModel `json:"models"`
}

// InitialState is returned when the IDE frontend boots.
type InitialState struct {
	InitialWorkspace string           `json:"initialWorkspace"`
	InitialProject   ProjectInfo      `json:"initialProject"`
	RecentProjects   []string         `json:"recentProjects"`
	AgyStatus        modelx.AgyStatus `json:"agyStatus"`
	Models           []modelx.AgyModel `json:"models"`
	GoVersion        string           `json:"goVersion"`
	OS               string           `json:"os"`
}

// ProjectInfo holds workspace metadata loaded when opening a folder.
type ProjectInfo struct {
	Root       string               `json:"root"`
	Name       string               `json:"name"`
	Branch     string               `json:"branch"`
	Stack      idecore.ProjectStack `json:"stack"`
	HasTeam    bool                 `json:"hasTeam"`
	TeamConfig team.File            `json:"teamConfig"`
	Files      []idecore.FileNode   `json:"files"`
}

// NewApp constructs the Wails App.
func NewApp(initialDir string, smokeCheck bool) *App {
	app := &App{
		initialDir: initialDir,
		smokeCheck: smokeCheck,
		fileIndex:  idecore.NewFileIndex(),
	}
	app.sessions = idecore.NewSessionManager(func(ev idecore.LaneEvent) {
		if app.ctx != nil {
			wailsrt.EventsEmit(app.ctx, "hivemind:lane-event", ev)
		}
	})
	return app
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) shutdown(_ context.Context) {
	if a.sessions != nil {
		a.sessions.CancelAll()
	}
}

// WindowMinimize minimizes the IDE window.
func (a *App) WindowMinimize() {
	if a.ctx != nil {
		wailsrt.WindowMinimise(a.ctx)
	}
}

// WindowToggleMaximize toggles between maximized and normal window state.
func (a *App) WindowToggleMaximize() {
	if a.ctx != nil {
		wailsrt.WindowToggleMaximise(a.ctx)
	}
}

// WindowClose closes the IDE window.
func (a *App) WindowClose() {
	if a.ctx != nil {
		wailsrt.Quit(a.ctx)
	}
}

// WindowIsMaximized returns whether the window is currently maximized.
func (a *App) WindowIsMaximized() bool {
	if a.ctx == nil {
		return false
	}
	return wailsrt.WindowIsMaximised(a.ctx)
}

// GetInitialState loads the initial workspace and recent list without blocking on agy.
func (a *App) GetInitialState() InitialState {
	dir := a.initialDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	proj, _ := a.OpenProject(dir)
	recent := idecore.LoadRecentProjects("")

	a.mu.Lock()
	agy := a.agyStatus
	models := append([]modelx.AgyModel(nil), a.modelsCache...)
	a.mu.Unlock()

	return InitialState{
		InitialWorkspace: dir,
		InitialProject:   proj,
		RecentProjects:   recent,
		AgyStatus:        agy,
		Models:           models,
		GoVersion:        runtime.Version(),
		OS:               runtime.GOOS,
	}
}

// BootstrapAgy probes agy and fetches its models in a single process invocation,
// caching the result for 5 minutes unless force is true.
func (a *App) BootstrapAgy(force bool) AgyBootstrap {
	a.mu.Lock()
	if !force && !a.agyCachedAt.IsZero() && time.Since(a.agyCachedAt) < 5*time.Minute && a.agyStatus.Found {
		out := AgyBootstrap{
			Status: a.agyStatus,
			Models: append([]modelx.AgyModel(nil), a.modelsCache...),
		}
		a.mu.Unlock()
		return out
	}
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	status, models := modelx.ProbeAndListAgy(ctx)

	a.mu.Lock()
	a.agyStatus = status
	a.modelsCache = models
	a.agyCachedAt = time.Now()
	a.mu.Unlock()

	return AgyBootstrap{
		Status: status,
		Models: models,
	}
}

// ChooseDirectory opens the native OS folder dialog.
func (a *App) ChooseDirectory() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("janela não inicializada")
	}
	return wailsrt.OpenDirectoryDialog(a.ctx, wailsrt.OpenDialogOptions{
		Title:            "Abrir Pasta do Projeto",
		DefaultDirectory: a.initialDir,
	})
}

// OpenProject loads workspace metadata and records it in ide-recent.txt.
func (a *App) OpenProject(dir string) (ProjectInfo, error) {
	abs, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil {
		return ProjectInfo{}, err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return ProjectInfo{}, fmt.Errorf("pasta inválida: %s", abs)
	}
	_, _ = idecore.RecordRecentProject("", abs)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	proj := ProjectInfo{
		Root:   abs,
		Name:   filepath.Base(abs),
		Branch: idecore.CurrentBranch(ctx, abs),
		Stack:  idecore.DetectStack(abs),
	}
	if cfg, err := team.Load(abs); err == nil {
		proj.HasTeam = true
		proj.TeamConfig = cfg
	}
	files, _ := idecore.ListDir(abs, "")
	proj.Files = files
	if a.fileIndex != nil {
		go func(target string) {
			_ = a.fileIndex.Refresh(target)
		}(abs)
	}
	return proj, nil
}

// GetRecentProjects returns the MRU list from %LOCALAPPDATA%\HiveMind\ide-recent.txt.
func (a *App) GetRecentProjects() []string {
	return idecore.LoadRecentProjects("")
}

// ListFiles refreshes the shallow root explorer entries.
func (a *App) ListFiles(workspace string) ([]idecore.FileNode, error) {
	return idecore.ListDir(workspace, "")
}

// ListDirectory lists immediate children of relDir for lazy tree expansion.
func (a *App) ListDirectory(workspace, relDir string) ([]idecore.FileNode, error) {
	return idecore.ListDir(workspace, relDir)
}

// SearchFiles returns matching relative paths for @arquivo and Ctrl+P Quick Open.
func (a *App) SearchFiles(workspace, query string) []string {
	if a.fileIndex != nil {
		return a.fileIndex.Search(workspace, query, 25)
	}
	return idecore.SearchFiles(workspace, query, 25)
}

// ReadFile reads a workspace file.
func (a *App) ReadFile(workspace, relOrAbs string) (string, error) {
	return idecore.ReadWorkspaceFile(workspace, relOrAbs)
}

// SaveFile writes content to a workspace file.
func (a *App) SaveFile(workspace, relOrAbs, content string) error {
	return idecore.WriteWorkspaceFile(workspace, relOrAbs, content)
}

// ListModels fetches available models using the cached agy bootstrap.
func (a *App) ListModels() ([]modelx.AgyModel, error) {
	boot := a.BootstrapAgy(false)
	if !boot.Status.LoggedIn && len(boot.Models) == 0 {
		return nil, fmt.Errorf("%s", boot.Status.Detail)
	}
	return boot.Models, nil
}

// DoctorAgy forces a fresh `agy` check and refreshes the model cache.
func (a *App) DoctorAgy() modelx.AgyStatus {
	return a.BootstrapAgy(true).Status
}

// SaveTeamWizard creates or updates a team in .hivemind/team.yaml without altering .gitignore.
func (a *App) SaveTeamWizard(workspace string, input idecore.WizardInput) (team.File, error) {
	return idecore.BuildTeamFromWizard(workspace, input)
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

// GetDiff returns parsed unified diffs for the workspace.
func (a *App) GetDiff(workspace string) ([]idecore.FileDiff, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return idecore.WorkspaceDiff(ctx, workspace)
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

// GitHubStatus returns `gh` CLI authentication and workspace remote info.
func (a *App) GitHubStatus(workspace string) idecore.GitHubStatusInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return idecore.CheckGitHubStatus(ctx, workspace)
}

// InitGit initializes a Git repository in workspace.
func (a *App) InitGit(workspace string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return idecore.InitGitRepo(ctx, workspace)
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
	baseCtx := a.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	return a.sessions.RunTurnWithOptions(baseCtx, laneID, workspace, teamName, objective, jiraKey, sessionID, runMode, images)
}

// UpdateAgentModel switches the LLM model of a single agent inside .hivemind/team.yaml.
func (a *App) UpdateAgentModel(workspace, teamName, agentName, modelSpec string) (team.File, error) {
	return idecore.UpdateTeamAgentModel(workspace, teamName, agentName, modelSpec)
}

// EnsureTeamMemory creates .hivemind/MEMORY.md if absent and returns its relative path for the editor.
func (a *App) EnsureTeamMemory(workspace string) (string, error) {
	return idecore.EnsureWorkspaceMemory(workspace)
}

// CancelRun cancels an active run on laneID.
func (a *App) CancelRun(laneID string) bool {
	return a.sessions.Cancel(laneID)
}

// RunCommand executes a command inside workspace for the integrated IDE terminal (Ctrl+J).
func (a *App) RunCommand(workspace, command string) (idecore.CommandOutput, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	return idecore.RunWorkspaceCommand(ctx, workspace, command)
}

// ListSessions returns all persisted sessions and token telemetry for workspace.
func (a *App) ListSessions(workspace string) (idecore.WorkspaceSessionsSummary, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return idecore.ListWorkspaceSessions(ctx, workspace)
}

// LoadSession loads the transcript and per-agent token usage of sessionID.
func (a *App) LoadSession(workspace, sessionID string) (idecore.SessionDetail, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return idecore.LoadSessionDetail(ctx, workspace, sessionID)
}

// DeleteSession removes a saved session.
func (a *App) DeleteSession(workspace, sessionID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return idecore.DeleteWorkspaceSession(ctx, workspace, sessionID)
}

// SearchText performs global text search (Ctrl+Shift+F) across workspace files.
func (a *App) SearchText(workspace, query string) ([]idecore.TextSearchMatch, error) {
	return idecore.SearchWorkspaceText(workspace, query, 120)
}

// CreateFile creates a new empty file inside workspace.
func (a *App) CreateFile(workspace, relPath string) error {
	if err := idecore.CreateWorkspaceFile(workspace, relPath); err != nil {
		return err
	}
	if a.fileIndex != nil {
		go func() { _ = a.fileIndex.Refresh(workspace) }()
	}
	return nil
}

// CreateDir creates a new directory inside workspace.
func (a *App) CreateDir(workspace, relPath string) error {
	return idecore.CreateWorkspaceDir(workspace, relPath)
}

// RenamePath renames a file or directory inside workspace.
func (a *App) RenamePath(workspace, oldRel, newRel string) error {
	if err := idecore.RenameWorkspacePath(workspace, oldRel, newRel); err != nil {
		return err
	}
	if a.fileIndex != nil {
		go func() { _ = a.fileIndex.Refresh(workspace) }()
	}
	return nil
}

// DeletePath deletes a file or directory inside workspace.
func (a *App) DeletePath(workspace, relPath string) error {
	if err := idecore.DeleteWorkspacePath(workspace, relPath); err != nil {
		return err
	}
	if a.fileIndex != nil {
		go func() { _ = a.fileIndex.Refresh(workspace) }()
	}
	return nil
}

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

// ListIssues fetches open Jira and/or GitHub issues for workspace.
func (a *App) ListIssues(workspace string) ([]idecore.ProjectIssue, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return idecore.ListProjectIssues(ctx, workspace)
}
