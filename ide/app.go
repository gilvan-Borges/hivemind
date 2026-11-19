package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App exposes backend capabilities to the Wails frontend.
type App struct {
	ctx              context.Context
	initialDir       string
	smokeCheck       bool
	sessions         *idecore.SessionManager
	fileIndex        *idecore.FileIndex
	mu               sync.Mutex
	agyStatus        modelx.AgyStatus
	modelsCache      []modelx.AgyModel
	agyCachedAt      time.Time
	openCodeStatus   modelx.OpenCodeStatus
	openCodeGoModels []modelx.AgyModel
	openCodeModels   []modelx.AgyModel
	openCodeCachedAt time.Time
	termMu           sync.Mutex
	termCancels      map[string]context.CancelFunc
	inlineMu         sync.Mutex
	ghostCancel      context.CancelFunc
	editCancel       context.CancelFunc
	watcherMu        sync.Mutex
	watchers         map[string]*idecore.FileWatcher
}

// AgyBootstrap carries the cached or freshly probed agy status and model list.
type AgyBootstrap struct {
	Status modelx.AgyStatus  `json:"status"`
	Models []modelx.AgyModel `json:"models"`
}

// OpenCodeBootstrap carries the OpenCode Go & Zen token status and HTTP model catalogs.
type OpenCodeBootstrap struct {
	Status   modelx.OpenCodeStatus `json:"status"`
	GoModels []modelx.AgyModel     `json:"goModels"`
	Models   []modelx.AgyModel     `json:"models"`
}

// InitialState is returned when the IDE frontend boots.
type InitialState struct {
	InitialWorkspace string                `json:"initialWorkspace"`
	InitialProject   ProjectInfo           `json:"initialProject"`
	RecentProjects   []string              `json:"recentProjects"`
	AgyStatus        modelx.AgyStatus      `json:"agyStatus"`
	Models           []modelx.AgyModel     `json:"models"`
	OpenCodeStatus   modelx.OpenCodeStatus `json:"openCodeStatus"`
	OpenCodeGoModels []modelx.AgyModel     `json:"openCodeGoModels"`
	OpenCodeModels   []modelx.AgyModel     `json:"openCodeModels"`
	GoVersion        string                `json:"goVersion"`
	OS               string                `json:"os"`
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
		initialDir:  initialDir,
		smokeCheck:  smokeCheck,
		fileIndex:   idecore.NewFileIndex(),
		termCancels: make(map[string]context.CancelFunc),
		watchers:    make(map[string]*idecore.FileWatcher),
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
	applyWindowIcons()
	// Semeia as skills nativas padrão (planejamento e SDD) no catálogo da IDE,
	// sem sobrescrever edições do usuário feitas no painel de Skills.
	if _, err := idecore.SeedNativeSkills(); err != nil {
		log.Printf("aviso: falha ao semear skills nativas: %v", err)
	}
}

func (a *App) shutdown(_ context.Context) {
	if a.sessions != nil {
		a.sessions.CancelAll()
	}
	a.CancelInlineAI()
	a.termMu.Lock()
	for _, cancel := range a.termCancels {
		if cancel != nil {
			cancel()
		}
	}
	a.termCancels = make(map[string]context.CancelFunc)
	a.termMu.Unlock()
	a.watcherMu.Lock()
	for _, w := range a.watchers {
		if w != nil {
			w.Close()
		}
	}
	a.watchers = make(map[string]*idecore.FileWatcher)
	a.watcherMu.Unlock()
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
	ocStatus := a.openCodeStatus
	ocGoModels := append([]modelx.AgyModel(nil), a.openCodeGoModels...)
	ocModels := append([]modelx.AgyModel(nil), a.openCodeModels...)
	a.mu.Unlock()

	return InitialState{
		InitialWorkspace: dir,
		InitialProject:   proj,
		RecentProjects:   recent,
		AgyStatus:        agy,
		Models:           models,
		OpenCodeStatus:   ocStatus,
		OpenCodeGoModels: ocGoModels,
		OpenCodeModels:   ocModels,
		GoVersion:        runtime.Version(),
		OS:               runtime.GOOS,
	}
}

// BootstrapOpenCode probes the OpenCode Go & Zen HTTP APIs using the configured token
// (OPENCODE_API_KEY or user credentials.json) and fetches both live model catalogs.
func (a *App) BootstrapOpenCode(force bool) OpenCodeBootstrap {
	a.mu.Lock()
	if !force && !a.openCodeCachedAt.IsZero() && time.Since(a.openCodeCachedAt) < 5*time.Minute && (len(a.openCodeGoModels) > 0 || len(a.openCodeModels) > 0) {
		out := OpenCodeBootstrap{
			Status:   a.openCodeStatus,
			GoModels: append([]modelx.AgyModel(nil), a.openCodeGoModels...),
			Models:   append([]modelx.AgyModel(nil), a.openCodeModels...),
		}
		a.mu.Unlock()
		return out
	}
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	status, goModels, zenModels := modelx.ProbeAndListOpenCode(ctx)

	a.mu.Lock()
	a.openCodeStatus = status
	a.openCodeGoModels = goModels
	a.openCodeModels = zenModels
	a.openCodeCachedAt = time.Now()
	a.mu.Unlock()

	return OpenCodeBootstrap{
		Status:   status,
		GoModels: goModels,
		Models:   zenModels,
	}
}

// SaveOpenCodeConfig stores or clears the OpenCode Zen API token in the user's local
// credentials.json (never inside the project Git repo) and refreshes the model catalog.
func (a *App) SaveOpenCodeConfig(apiKey, baseURL string) (OpenCodeBootstrap, error) {
	if err := modelx.SaveOpenCodeCredentials(apiKey, baseURL); err != nil {
		return OpenCodeBootstrap{}, err
	}
	return a.BootstrapOpenCode(true), nil
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
	a.startWatcher(abs)
	return proj, nil
}

// startWatcher keeps exactly one FileWatcher per project root. When the agent
// (or any external tool) edits files, the watcher refreshes the in-memory
// FileIndex and notifies the frontend via the "hivemind:fs-event" Wails event.
func (a *App) startWatcher(root string) {
	if a.smokeCheck {
		return
	}
	key := strings.ToLower(filepath.Clean(root))
	a.watcherMu.Lock()
	if _, exists := a.watchers[key]; exists {
		a.watcherMu.Unlock()
		return
	}
	a.watcherMu.Unlock()

	w, err := idecore.NewFileWatcher(root, a.onFSChange)
	if err != nil {
		return
	}
	a.watcherMu.Lock()
	if _, exists := a.watchers[key]; exists {
		a.watcherMu.Unlock()
		w.Close()
		return
	}
	a.watchers[key] = w
	a.watcherMu.Unlock()
	if err := w.Start(); err != nil {
		a.watcherMu.Lock()
		delete(a.watchers, key)
		a.watcherMu.Unlock()
	}
}

// onFSChange refreshes the FileIndex for the changed root and forwards the
// batch of changed relative paths to the frontend explorer and open editors.
func (a *App) onFSChange(ev idecore.FileChangeEvent) {
	if a.fileIndex != nil {
		go func(target string) {
			_ = a.fileIndex.Refresh(target)
		}(ev.Root)
	}
	if a.ctx != nil && !a.smokeCheck {
		wailsrt.EventsEmit(a.ctx, "hivemind:fs-event", map[string]any{
			"workspace": ev.Root,
			"changed":   ev.Changed,
			"overflow":  ev.Overflow,
		})
	}
}

// GetRecentProjects returns the MRU list from %LOCALAPPDATA%\HiveMind\ide-recent.txt.
func (a *App) GetRecentProjects() []string {
	return idecore.LoadRecentProjects("")
}

// ListSkills returns the combined skill catalog: IDE-native first, then project skills.
func (a *App) ListSkills(workspace string) ([]idecore.SkillInfo, error) {
	return idecore.ListSkills(workspace)
}

// ReadNativeSkill returns the SKILL.md body of an IDE-native skill for editing.
func (a *App) ReadNativeSkill(name string) (string, error) {
	return idecore.ReadNativeSkill(name)
}

// SaveNativeSkill creates or updates an IDE-native skill from the skill editor.
func (a *App) SaveNativeSkill(name, body string) error {
	return idecore.SaveNativeSkill(name, body)
}

// DeleteNativeSkill removes an IDE-native skill.
func (a *App) DeleteNativeSkill(name string) error {
	return idecore.DeleteNativeSkill(name)
}

// InstallSkillToProject copies an IDE-native skill into the project's .agents/skills catalog.
func (a *App) InstallSkillToProject(workspace, name string) error {
	return idecore.InstallSkillToProject(name, workspace)
}

// RemoveProjectSkill removes a skill from the project's .agents/skills catalog.
func (a *App) RemoveProjectSkill(workspace, name string) error {
	return idecore.RemoveProjectSkill(name, workspace)
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

// RunGitAction executes safe Source Control actions ("fetch", "pull", "push", "sync", "stash", "stash-pop").
func (a *App) RunGitAction(workspace, action string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return idecore.RunGitAction(ctx, workspace, action)
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
	return a.GitHubStatus(workspace), nil
}

// InitGit initializes a Git repository in workspace.
func (a *App) InitGit(workspace string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return idecore.InitGitRepo(ctx, workspace)
}

// JiraStatus returns the native Jira Cloud connector status (env vars or saved config).
func (a *App) JiraStatus() jirax.ConnectorStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return jirax.CheckConnection(ctx)
}

// SaveJiraConfig stores (or clears, when clear=true) the native Jira Cloud
// credentials outside project repos, returning the refreshed connection status.
func (a *App) SaveJiraConfig(baseURL, email, token, defaultProject string, clear bool) (jirax.ConnectorStatus, error) {
	if err := jirax.SaveCredentials(baseURL, email, token, defaultProject, clear); err != nil {
		return jirax.ConnectorStatus{}, err
	}
	return a.JiraStatus(), nil
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
	res, err := a.sessions.RunTurnWithOptions(baseCtx, laneID, workspace, teamName, objective, jiraKey, sessionID, runMode, images)
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

// RespondApproval delivers the user's decision on a pending approval gate (identified by approvalID or laneID).
func (a *App) RespondApproval(approvalID string, approved bool) bool {
	return a.sessions.ResolveApproval(approvalID, approved)
}

// EnsureTeamMemory creates .hivemind/MEMORY.md if absent and returns its relative path for the editor.
func (a *App) EnsureTeamMemory(workspace string) (string, error) {
	return idecore.EnsureWorkspaceMemory(workspace)
}

// CancelRun cancels an active run on laneID.
func (a *App) CancelRun(laneID string) bool {
	return a.sessions.Cancel(laneID)
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

// SearchText performs parallel global text search (Ctrl+Shift+F) reusing the in-memory FileIndex.
func (a *App) SearchText(workspace, query string) ([]idecore.TextSearchMatch, error) {
	return idecore.SearchWorkspaceTextIndexed(a.fileIndex, workspace, query, 120)
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

// ListPullRequests fetches open GitHub pull requests for workspace.
func (a *App) ListPullRequests(workspace string) ([]idecore.ProjectPullRequest, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return idecore.ListProjectPullRequests(ctx, workspace)
}

// CloseOwnGitHubItem closes a GitHub PR ("pr") or issue ("issue") opened by the authenticated user.
func (a *App) CloseOwnGitHubItem(workspace, kind string, number int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return idecore.CloseOwnGitHubItem(ctx, workspace, kind, number)
}

// OpenExternalURL opens an http/https URL in the system default browser.
func (a *App) OpenExternalURL(rawURL string) error {
	trimmed := strings.TrimSpace(rawURL)
	if !strings.HasPrefix(trimmed, "https://") && !strings.HasPrefix(trimmed, "http://") {
		return fmt.Errorf("URL inválida: %s", rawURL)
	}
	if a.ctx == nil {
		return fmt.Errorf("contexto da janela ainda não está pronto")
	}
	wailsrt.BrowserOpenURL(a.ctx, trimmed)
	return nil
}

// GetAIUsage returns live Antigravity (/usage) and OpenCode Go quota windows (5h/weekly/monthly) plus local HiveMind usage per AI provider.
func (a *App) GetAIUsage() modelx.AIUsageReport {
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	return modelx.GetAIUsageReport(ctx)
}

// SaveAIUsageLimits stores optional 5h and 7d reference token limits for a provider and returns the updated report.
func (a *App) SaveAIUsageLimits(provider string, fiveHourTokens, weeklyTokens int64) (modelx.AIUsageReport, error) {
	if err := modelx.SaveAIUsageLimits(provider, fiveHourTokens, weeklyTokens); err != nil {
		return modelx.AIUsageReport{}, err
	}
	return a.GetAIUsage(), nil
}
