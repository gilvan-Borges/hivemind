package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App exposes backend capabilities to the Wails frontend.
type App struct {
	ctx        context.Context
	initialDir string
	smokeCheck bool
	sessions   *idecore.SessionManager
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

// GetInitialState loads the initial workspace, recent list, agy status, and models.
func (a *App) GetInitialState() InitialState {
	dir := a.initialDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	proj, _ := a.OpenProject(dir)
	recent := idecore.LoadRecentProjects("")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	agy := modelx.ProbeAgy(ctx)
	var models []modelx.AgyModel
	if agy.LoggedIn {
		models, _ = modelx.ListAgyModels(ctx)
	}
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
	files, _ := idecore.ListFileTree(abs, 5)
	proj.Files = files
	return proj, nil
}

// GetRecentProjects returns the MRU list from %LOCALAPPDATA%\HiveMind\ide-recent.txt.
func (a *App) GetRecentProjects() []string {
	return idecore.LoadRecentProjects("")
}

// ListFiles refreshes the explorer file tree.
func (a *App) ListFiles(workspace string) ([]idecore.FileNode, error) {
	return idecore.ListFileTree(workspace, 5)
}

// SearchFiles returns matching relative paths for @arquivo autocomplete.
func (a *App) SearchFiles(workspace, query string) []string {
	return idecore.SearchFiles(workspace, query, 20)
}

// ReadFile reads a workspace file.
func (a *App) ReadFile(workspace, relOrAbs string) (string, error) {
	return idecore.ReadWorkspaceFile(workspace, relOrAbs)
}

// SaveFile writes content to a workspace file.
func (a *App) SaveFile(workspace, relOrAbs, content string) error {
	return idecore.WriteWorkspaceFile(workspace, relOrAbs, content)
}

// ListModels fetches available models from `agy models`.
func (a *App) ListModels() ([]modelx.AgyModel, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return modelx.ListAgyModels(ctx)
}

// DoctorAgy checks `agy` installation and login status.
func (a *App) DoctorAgy() modelx.AgyStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return modelx.ProbeAgy(ctx)
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
	baseCtx := a.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	return a.sessions.RunTurn(baseCtx, laneID, workspace, teamName, objective, jiraKey, sessionID)
}

// CancelRun cancels an active run on laneID.
func (a *App) CancelRun(laneID string) bool {
	return a.sessions.Cancel(laneID)
}
