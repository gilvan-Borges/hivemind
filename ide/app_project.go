package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/internal/loopx"
	"github.com/gilvan-Borges/HiveMind/internal/mcpx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// ChooseDirectory opens the native OS folder dialog.
func (a *App) ChooseDirectory() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("janela não inicializada")
	}
	defaultDir := a.initialDir
	if defaultDir == "" || isNonProjectLaunchDir(defaultDir) {
		if home, err := os.UserHomeDir(); err == nil {
			defaultDir = home
		}
	}
	return wailsrt.OpenDirectoryDialog(a.ctx, wailsrt.OpenDialogOptions{
		Title:            "Abrir Pasta do Projeto",
		DefaultDirectory: defaultDir,
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
	if !isNonProjectLaunchDir(abs) {
		_, _ = idecore.RecordRecentProject("", abs)
	}
	_, _ = loopx.PauseRunning(abs)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if !a.smokeCheck {
		go func(root string) {
			cctx, ccancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer ccancel()
			idecore.CleanupLegacyProjectState(cctx, root)
		}(abs)
	}

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
	if a.mobile != nil {
		activeTeam := ""
		if proj.HasTeam {
			activeTeam = proj.TeamConfig.Active
		}
		a.mobile.hub.RegisterProject(abs, proj.Name, proj.Branch, proj.Stack.Summary, activeTeam)
	}
	files, _ := idecore.ListDir(abs, "")
	proj.Files = files
	refKey := projectRefKey(abs)
	a.openMu.Lock()
	if a.openProjects == nil {
		a.openProjects = make(map[string]int)
	}
	a.openProjects[refKey]++
	a.openMu.Unlock()
	if a.fileIndex != nil {
		go func(target string) {
			_ = a.fileIndex.Refresh(target)
		}(abs)
	}
	if !a.smokeCheck {
		a.startWatcher(abs)
	}
	if a.autoReview != nil && !a.smokeCheck {
		go func(root string) {
			baseCtx := a.ctx
			if baseCtx == nil {
				baseCtx = context.Background()
			}
			a.autoReview.SyncWorkspace(baseCtx, root)
		}(abs)
	}
	if a.githubMonitor != nil && !a.smokeCheck {
		baseCtx := a.ctx
		if baseCtx == nil {
			baseCtx = context.Background()
		}
		a.githubMonitor.EnsureWatching(baseCtx, abs)
	}
	a.orbSetProject(proj.Name)
	return proj, nil
}

func projectRefKey(workspace string) string {
	trimmed := strings.TrimSpace(workspace)
	if trimmed == "" {
		return ""
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		abs = trimmed
	}
	return strings.ToLower(filepath.Clean(abs))
}

// CloseProject decrements the open-tab reference count for workspace and releases
// watchers, background pollers, LSP, PTY, MCP and Design Mode resources when the
// last tab referencing that workspace closes.
func (a *App) CloseProject(workspace string) bool {
	key := projectRefKey(workspace)
	if key == "" {
		return false
	}
	a.openMu.Lock()
	if a.openProjects == nil {
		a.openMu.Unlock()
		return false
	}
	cnt, exists := a.openProjects[key]
	if !exists {
		a.openMu.Unlock()
		return false
	}
	if cnt > 1 {
		a.openProjects[key] = cnt - 1
		a.openMu.Unlock()
		return false
	}
	delete(a.openProjects, key)
	a.openMu.Unlock()

	a.stopWatcher(workspace)
	if a.githubMonitor != nil {
		a.githubMonitor.StopWorkspace(workspace)
	}
	if a.autoReview != nil {
		a.autoReview.StopWorkspace(workspace)
	}
	if a.lsp != nil {
		a.lsp.CloseWorkspace(workspace)
	}
	if a.pty != nil {
		a.pty.CloseWorkspace(workspace)
	}
	if a.designMode != nil {
		a.designMode.CloseWorkspace(workspace)
	}
	if a.sessions != nil {
		a.sessions.ReleaseWorkspace(workspace)
	}
	a.CancelGitAction(workspace)
	a.CancelCommand(workspace)
	return true
}

func (a *App) stopWatcher(root string) {
	key := projectRefKey(root)
	if key == "" {
		return
	}
	a.watcherMu.Lock()
	w, ok := a.watchers[key]
	if ok {
		delete(a.watchers, key)
	}
	a.watcherMu.Unlock()
	if ok && w != nil {
		w.Close()
	}
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

	go func() {
		if err := w.Start(); err != nil {
			a.watcherMu.Lock()
			if a.watchers[key] == w {
				delete(a.watchers, key)
			}
			a.watcherMu.Unlock()
		}
	}()
}

// onFSChange refreshes the FileIndex for the changed root and forwards the
// batch of changed relative paths to the frontend explorer and open editors.
func (a *App) onFSChange(ev idecore.FileChangeEvent) {
	hasFSChanges := len(ev.Changed) > 0 || ev.Overflow
	if hasFSChanges && a.fileIndex != nil {
		go func(target string) {
			_ = a.fileIndex.Refresh(target)
		}(ev.Root)
	}
	if a.resident != nil {
		a.resident.NoteHostEvent("arquivo")
	}
	if a.ctx == nil || a.smokeCheck {
		return
	}
	if ev.GitChanged {
		wailsrt.EventsEmit(a.ctx, "hivemind:git-changed", map[string]any{
			"workspace": ev.Root,
			"branch":    idecore.FastCurrentBranch(ev.Root),
		})
	}
	if hasFSChanges {
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

// SetNativeSkillEnabled liga ou desliga uma skill nativa (untangle-codebase nasce desligada).
func (a *App) SetNativeSkillEnabled(name string, enabled bool) error {
	return idecore.SetNativeSkillEnabled(name, enabled)
}

// ListMCPServers devolve o catálogo MCP efetivo (padrões + usuário + projeto), sem subir processos.
func (a *App) ListMCPServers(workspace string) ([]mcpx.ServerView, error) {
	cfg, err := mcpx.Load(workspace)
	if err != nil {
		return nil, err
	}
	return mcpx.Describe(cfg), nil
}

// SetMCPServerEnabled grava o interruptor. projectOnly limita a alteração a este projeto.
func (a *App) SetMCPServerEnabled(workspace, name string, enabled, projectOnly bool) ([]mcpx.ServerView, error) {
	cfg, err := mcpx.SetEnabled(workspace, name, enabled, projectOnly)
	if err != nil {
		return nil, err
	}
	return mcpx.Describe(cfg), nil
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

// ReadLocalMedia returns a data:image/... URI for a local screenshot/image inside allowed roots.
func (a *App) ReadLocalMedia(workspace, rawPath string) (string, error) {
	return idecore.ReadLocalMediaDataURI(workspace, rawPath)
}

// OpenLocalFile opens a local file (e.g. PDF) using the system default viewer.
func (a *App) OpenLocalFile(workspace, rawPath string) error {
	return idecore.OpenLocalFile(workspace, rawPath)
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
	if err := idecore.DeleteWorkspaceSession(ctx, workspace, sessionID); err != nil {
		return err
	}
	if a.mobile != nil && a.mobile.hub != nil {
		a.mobile.hub.ClearSession(workspace, sessionID)
		if sum, err := idecore.ListWorkspaceSessions(ctx, workspace); err == nil && len(sum.Sessions) == 0 {
			a.mobile.hub.ClearWorkspaceLanes(workspace)
		}
	}
	return nil
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

// StartPTY ensures an interactive PTY shell (ConPTY on Windows) is running for termID in workspace,
// returning any buffered scrollback if the session was already active.
func (a *App) StartPTY(termID, workspace string, cols, rows int) (string, error) {
	if a.pty == nil {
		return "", fmt.Errorf("gerenciador PTY não inicializado")
	}
	return a.pty.Start(termID, workspace, cols, rows)
}

// StartLanePTY starts or attaches an interactive PTY bound to a team lane while
// propagating HIVEMIND_SESSION_ID so CLI/TUI inherits the active lane session.
func (a *App) StartLanePTY(termID, workspace, sessionID string, cols, rows int) (string, error) {
	if a.pty == nil {
		return "", fmt.Errorf("gerenciador PTY não inicializado")
	}
	var env map[string]string
	if sid := strings.TrimSpace(sessionID); sid != "" {
		env = map[string]string{"HIVEMIND_SESSION_ID": sid}
	}
	return a.pty.StartWithEnv(termID, workspace, cols, rows, env)
}

func (a *App) ensureDesignMode() *idecore.DesignModeService {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.designMode == nil {
		a.designMode = idecore.NewDesignModeService()
	}
	return a.designMode
}

// DesignModeListPages lists open Chrome tabs via Chrome DevTools MCP for Design Mode.
func (a *App) DesignModeListPages(workspace string) ([]idecore.DesignModePage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return a.ensureDesignMode().ListPages(ctx, workspace)
}

// DesignModePickElement captures selector/HTML/computed styles from Chrome DevTools MCP.
func (a *App) DesignModePickElement(workspace string, pageIdx int, selector string) (idecore.DesignModeElement, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return a.ensureDesignMode().PickElement(ctx, workspace, pageIdx, selector)
}

// WritePTY sends keystrokes, commands, or control bytes (e.g. Ctrl+C) to a running PTY session.
func (a *App) WritePTY(termID, data string) error {
	if a.pty == nil {
		return fmt.Errorf("gerenciador PTY não inicializado")
	}
	return a.pty.Write(termID, data)
}

// ResizePTY updates the rows/cols dimensions of a running PTY session.
func (a *App) ResizePTY(termID string, cols, rows int) error {
	if a.pty == nil {
		return nil
	}
	return a.pty.Resize(termID, cols, rows)
}

// RestartPTY terminates the current PTY shell and spawns a clean shell in workspace.
func (a *App) RestartPTY(termID, workspace string, cols, rows int) error {
	if a.pty == nil {
		return fmt.Errorf("gerenciador PTY não inicializado")
	}
	return a.pty.Restart(termID, workspace, cols, rows)
}

// ClosePTY terminates a single PTY session cleanly.
func (a *App) ClosePTY(termID string) {
	if a.pty != nil {
		a.pty.Close(termID)
	}
}

// LSPStatus reports whether gopls is available and running for workspace.
func (a *App) LSPStatus(workspace string) idecore.LSPStatus {
	if a.lsp == nil {
		return idecore.LSPStatus{Server: "gopls"}
	}
	return a.lsp.Status(workspace)
}

// LSPDidOpen registers an open Go file with gopls for diagnostics, hover, and go-to-definition.
func (a *App) LSPDidOpen(workspace, relPath, text string) error {
	if a.lsp == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return a.lsp.DidOpen(ctx, workspace, relPath, text)
}

// LSPDidChange sends updated buffer content for an open Go file to gopls.
func (a *App) LSPDidChange(workspace, relPath, text string) error {
	if a.lsp == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.lsp.DidChange(ctx, workspace, relPath, text)
}

// LSPDidSave notifies gopls that a Go file was saved.
func (a *App) LSPDidSave(workspace, relPath, text string) error {
	if a.lsp == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.lsp.DidSave(ctx, workspace, relPath, text)
}

// LSPDidClose unregisters a closed Go tab from gopls.
func (a *App) LSPDidClose(workspace, relPath string) error {
	if a.lsp == nil {
		return nil
	}
	return a.lsp.DidClose(workspace, relPath)
}

// LSPDefinition resolves 1-based Go-to-Definition targets via gopls.
func (a *App) LSPDefinition(workspace, relPath string, line, col int) ([]idecore.LSPLocation, error) {
	if a.lsp == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return a.lsp.Definition(ctx, workspace, relPath, line, col)
}

// LSPHover returns 1-based symbol signature and documentation via gopls.
func (a *App) LSPHover(workspace, relPath string, line, col int) (idecore.LSPHover, error) {
	if a.lsp == nil {
		return idecore.LSPHover{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return a.lsp.Hover(ctx, workspace, relPath, line, col)
}

// CheckWorkspaceProblems executes the project's build/vet/test check and returns parsed clickable problems.
func (a *App) CheckWorkspaceProblems(workspace, customCmd string) (idecore.ProblemsCheckResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	return idecore.CheckWorkspaceProblems(ctx, workspace, customCmd)
}

// ParseOutputProblems extracts clickable ProblemItem diagnostics from arbitrary terminal/build text.
func (a *App) ParseOutputProblems(workspace, rawOutput string) []idecore.ProblemItem {
	return idecore.ParseBuildProblems(rawOutput, workspace)
}
