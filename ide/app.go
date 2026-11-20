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
	"github.com/gilvan-Borges/HiveMind/ide/internal/relayclient"
	"github.com/gilvan-Borges/HiveMind/internal/extsess"
	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/mcpx"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/resident"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App exposes backend capabilities to the Wails frontend.
type App struct {
	ctx                 context.Context
	initialDir          string
	smokeCheck          bool
	sessions            *idecore.SessionManager
	autoReview          *idecore.AutoReviewManager
	pty                 *idecore.PTYManager
	lsp                 *idecore.LSPManager
	fileIndex           *idecore.FileIndex
	mu                  sync.Mutex
	agyStatus           modelx.AgyStatus
	modelsCache         []modelx.AgyModel
	agyCachedAt         time.Time
	openCodeStatus      modelx.OpenCodeStatus
	openCodeGoModels    []modelx.AgyModel
	openCodeModels      []modelx.AgyModel
	openCodeCachedAt    time.Time
	termMu              sync.Mutex
	termCancels         map[string]context.CancelFunc
	inlineMu            sync.Mutex
	ghostCancel         context.CancelFunc
	editCancel          context.CancelFunc
	watcherMu           sync.Mutex
	watchers            map[string]*idecore.FileWatcher
	mobile              *mobileBridge
	relay               *relayclient.Client
	resident            *idecore.ResidentManager
	healthMonitorCancel context.CancelFunc
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
		if app.mobile != nil {
			app.mobile.hub.HandleLaneEvent(ev)
		}
	})
	app.autoReview = idecore.NewAutoReviewManager(app.sessions, func(st idecore.AutoReviewStatus) {
		if app.ctx != nil && !app.smokeCheck {
			wailsrt.EventsEmit(app.ctx, "hivemind:autoreview-status", st)
		}
		if app.mobile != nil {
			app.mobile.hub.HandleAutoReviewStatus(st)
		}
	})
	app.pty = idecore.NewPTYManager(func(ev idecore.PTYEvent) {
		if app.ctx != nil && !app.smokeCheck {
			wailsrt.EventsEmit(app.ctx, "hivemind:pty-output", ev)
		}
	})
	app.lsp = idecore.NewLSPManager(func(ev idecore.LSPDiagnosticsEvent) {
		if app.ctx != nil && !app.smokeCheck {
			wailsrt.EventsEmit(app.ctx, "hivemind:lsp-diagnostics", ev)
		}
	})
	app.resident = idecore.NewResidentManager(idecore.ResidentManagerOptions{
		Sessions: app.sessions,
		AccountFn: func() (string, string) {
			if app.relay == nil {
				return resident.DefaultAccountID, ""
			}
			st := app.relay.Status()
			if st.LoggedIn && strings.TrimSpace(st.Email) != "" {
				return st.Email, st.DeviceID
			}
			return resident.DefaultAccountID, st.DeviceID
		},
		ProjectsFn: func() []resident.ProjectRef {
			var out []resident.ProjectRef
			seen := map[string]bool{}
			if app.mobile != nil && app.mobile.hub != nil {
				for _, p := range app.mobile.hub.Projects() {
					key := strings.ToLower(filepath.Clean(p.Workspace))
					if key == "" || seen[key] {
						continue
					}
					seen[key] = true
					out = append(out, resident.ProjectRef{
						Name:   p.Name,
						Root:   p.Workspace,
						Branch: p.Branch,
						Stack:  p.Stack,
						Active: true,
					})
				}
			}
			for _, dir := range idecore.LoadRecentProjects("") {
				key := strings.ToLower(filepath.Clean(dir))
				if key == "" || seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, resident.ProjectRef{
					Name: filepath.Base(dir),
					Root: dir,
				})
			}
			return out
		},
	})
	app.mobile = newMobileBridge(app.sessions, app.resident, app.autoReview)
	app.relay = newRelayClient(app)
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
	if a.mobile != nil {
		a.mobile.start()
	}
	if a.relay != nil && !a.smokeCheck {
		a.relay.Start()
	}
	if !a.smokeCheck {
		a.startJevHealthMonitor(ctx)
	}
}

func (a *App) shutdown(_ context.Context) {
	if a.healthMonitorCancel != nil {
		a.healthMonitorCancel()
	}
	if a.pty != nil {
		a.pty.CloseAll()
	}
	if a.lsp != nil {
		a.lsp.CloseAll()
	}
	if a.autoReview != nil {
		a.autoReview.StopAll()
	}
	if a.relay != nil {
		a.relay.Stop()
	}
	if a.mobile != nil {
		a.mobile.stop()
	}
	if a.sessions != nil {
		a.sessions.CancelAll()
	}
	if a.resident != nil {
		a.resident.Cancel()
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

	idecore.CleanupLegacyProjectState(ctx, abs)

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
	if a.fileIndex != nil {
		go func(target string) {
			_ = a.fileIndex.Refresh(target)
		}(abs)
	}
	a.startWatcher(abs)
	if a.autoReview != nil && !a.smokeCheck {
		go func(root string) {
			baseCtx := a.ctx
			if baseCtx == nil {
				baseCtx = context.Background()
			}
			a.autoReview.SyncWorkspace(baseCtx, root)
		}(abs)
	}
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

// ScanAllProviders scans every supported LLM provider and saved account in parallel
// (Antigravity Google pool, OpenCode, OpenRouter, Anthropic, OpenAI/Grok, Gemini/Vertex,
// and Cursor CLI) and refreshes cached IDE bootstrap statuses.
func (a *App) ScanAllProviders(workspace string) modelx.ProvidersScanReport {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	report := modelx.ScanAllProviders(ctx, workspace)
	// Refresh Jev health summary quietly in background after scan.
	hs := modelx.DefaultJevHealthStore()
	hs.SyncCredentialFingerprints(modelx.JevCredentialFingerprints())
	a.emitProviderHealth(hs.Summary(a.currentJevPolicy(), modelx.JevCredentialPresence()), false)
	return report
}

// GetAPIKeysStatus returns the masked persisted LLM API keys/settings
// (env vars always take precedence at call time; this shows the saved store).
func (a *App) GetAPIKeysStatus() map[string]string {
	return modelx.StoredAPIKeysStatus()
}

// SaveAPIKeys persists LLM API keys/settings in the user config dir
// (outside the repo) and returns the new masked status. Empty values clear.
func (a *App) SaveAPIKeys(entries map[string]string) (map[string]string, error) {
	if entries != nil {
		_, hasOCKey := entries["OPENCODE_API_KEY"]
		_, hasOCBase := entries["OPENCODE_BASE_URL"]
		if hasOCKey || hasOCBase {
			curKey, curBase, _ := modelx.LoadOpenCodeCredentials()
			newKey := curKey
			newBase := curBase
			if hasOCKey {
				newKey = strings.TrimSpace(entries["OPENCODE_API_KEY"])
			}
			if hasOCBase {
				newBase = strings.TrimSpace(entries["OPENCODE_BASE_URL"])
			}
			if err := modelx.SaveOpenCodeCredentials(newKey, newBase); err != nil {
				return nil, err
			}
		}
	}
	if _, err := modelx.SaveStoredAPIKeys(entries); err != nil {
		return nil, err
	}
	// Re-read unified status including mirrored OpenCode credentials.
	return modelx.StoredAPIKeysStatus(), nil
}

// GetProviderProfilesOverview returns detected workspace org info, saved multi-account
// provider profiles (secrets masked), and resolved profile per provider.
func (a *App) GetProviderProfilesOverview(workspace string) modelx.ProfilesOverview {
	return modelx.ListProfilesOverview(workspace)
}

// SaveProviderProfile creates or updates a named account/profile for agy or any LLM provider.
func (a *App) SaveProviderProfile(req modelx.SaveProfileRequest) (modelx.ProfilesOverview, error) {
	return modelx.SaveProviderProfile(req)
}

// DeleteProviderProfile removes a saved provider profile by ID.
func (a *App) DeleteProviderProfile(profileID, workspace string) (modelx.ProfilesOverview, error) {
	return modelx.DeleteProviderProfile(profileID, workspace)
}

// BindProviderWorkspace pins or unpins a workspace+provider to a profile ("", "auto", "global", or profileID).
func (a *App) BindProviderWorkspace(workspace, provider, profileID string) (modelx.ProfilesOverview, error) {
	return modelx.BindProviderWorkspace(workspace, provider, profileID)
}

// LoginAgyProfile opens an interactive terminal window running `gcloud auth application-default login`
// inside the isolated CLOUDSDK_CONFIG directory for the given AGY profile ID.
func (a *App) LoginAgyProfile(profileID string) (string, error) {
	return modelx.LaunchAgyADCLogin(profileID)
}

// ConnectNewAgyGoogleAccount backs up the current Antigravity Google OAuth session into its
// isolated profile folder, clears the live slot, opens the browser Google OAuth login flow via
// `agy`, and waits for the user to paste the authorization code (`4/0A...`) in the modal.
func (a *App) ConnectNewAgyGoogleAccount(workspace string) (modelx.ProfilesOverview, error) {
	return modelx.ConnectNewAgyGoogleAccount(workspace)
}

// SubmitAgyOAuthCode completes the Google OAuth flow by sending the authorization code (`4/0A...`)
// copied from `antigravity.google/oauth-callback` to the waiting `agy` process, then saves the
// new Google account by its email (`id_token_email`) into its isolated vault folder.
func (a *App) SubmitAgyOAuthCode(code, workspace string) (modelx.ProfilesOverview, error) {
	return modelx.SubmitAgyOAuthCode(code, workspace)
}

// CancelAgyOAuthLogin cancels an in-progress Google OAuth login and restores the previous account.
func (a *App) CancelAgyOAuthLogin(workspace string) modelx.ProfilesOverview {
	return modelx.CancelAgyOAuthLogin(workspace)
}

// ActivateAgyAccount switches the live Antigravity Google OAuth session to the saved isolated
// session of profileID (preserving the previous account's session in its own folder).
func (a *App) ActivateAgyAccount(profileID, workspace string) (modelx.ProfilesOverview, error) {
	if err := modelx.ActivateAgyProfileSession(profileID); err != nil {
		return modelx.ListProfilesOverview(workspace), err
	}
	return modelx.ListProfilesOverview(workspace), nil
}

// ListOpenRouterModels returns the full public OpenRouter catalog (cached ~30min).
func (a *App) ListOpenRouterModels() ([]modelx.OpenRouterModel, error) {
	return modelx.OpenRouterModels(a.ctx)
}

// CursorCLIStatus reports the official Cursor CLI without revealing the API key.
func (a *App) CursorCLIStatus() modelx.CursorStatus {
	return modelx.ProbeCursor()
}

// ListCursorModels returns auto plus whatever `agent models` lists for this key.
func (a *App) ListCursorModels() ([]modelx.AgyModel, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	models, _, err := modelx.ListCursorModels(ctx)
	return models, err
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

// JiraStatusForWorkspace returns Jira status including auto-discovery from workspace/parent MCP configs.
func (a *App) JiraStatusForWorkspace(workspace string) jirax.ConnectorStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return jirax.CheckConnectionForWorkspace(ctx, workspace)
}

// ListJiraProjects fetches available Jira Cloud projects dynamically for the active credentials.
func (a *App) ListJiraProjects() ([]jirax.Project, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	return jirax.ListProjects(ctx)
}

// ListJiraProjectsForWorkspace fetches Jira Cloud projects using workspace/MCP-aware credentials.
func (a *App) ListJiraProjectsForWorkspace(workspace string) ([]jirax.Project, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	return jirax.ListProjectsForWorkspace(ctx, workspace)
}

// ImportJiraFromMCP imports Jira credentials from an existing mcp_config.json (workspace, parent, or user home)
// into the user's native jira-connector.json and returns the live connection status.
func (a *App) ImportJiraFromMCP(workspace string) (jirax.ConnectorStatus, error) {
	if _, err := jirax.ImportFromMCP(workspace); err != nil {
		return jirax.ConnectorStatus{}, err
	}
	st := a.JiraStatusForWorkspace(workspace)
	if st.Connected {
		idecore.EnableProjectJira(workspace, st.DefaultProject)
	}
	return st, nil
}

// SaveJiraConfig stores (or clears, when clear=true) the native Jira Cloud
// credentials outside project repos, returning the refreshed connection status.
func (a *App) SaveJiraConfig(baseURL, email, token, defaultProject string, clear bool) (jirax.ConnectorStatus, error) {
	if err := jirax.SaveCredentials(baseURL, email, token, defaultProject, clear); err != nil {
		return jirax.ConnectorStatus{}, err
	}
	return a.JiraStatus(), nil
}

// SaveJiraProfile creates or updates a named Jira Cloud profile (with optional org patterns
// and optional workspace pinning) and returns refreshed status for bindWorkspace.
func (a *App) SaveJiraProfile(profile jirax.JiraProfile, bindWorkspace string) (jirax.ConnectorStatus, error) {
	if err := jirax.SaveProfile(profile, bindWorkspace); err != nil {
		return jirax.ConnectorStatus{}, err
	}
	st := a.JiraStatusForWorkspace(bindWorkspace)
	if st.Connected && strings.TrimSpace(bindWorkspace) != "" {
		idecore.EnableProjectJira(bindWorkspace, st.DefaultProject)
	}
	return st, nil
}

// DeleteJiraProfile removes a Jira Cloud profile by ID and returns refreshed status for workspace.
func (a *App) DeleteJiraProfile(profileID, workspace string) (jirax.ConnectorStatus, error) {
	if err := jirax.DeleteProfile(profileID); err != nil {
		return jirax.ConnectorStatus{}, err
	}
	return a.JiraStatusForWorkspace(workspace), nil
}

// BindJiraWorkspace binds workspace to a profile ("", "auto", "none", or profileID).
func (a *App) BindJiraWorkspace(workspace, target string) (jirax.ConnectorStatus, error) {
	if err := jirax.BindWorkspace(workspace, target); err != nil {
		return jirax.ConnectorStatus{}, err
	}
	st := a.JiraStatusForWorkspace(workspace)
	if st.Connected && !strings.EqualFold(strings.TrimSpace(target), "none") {
		idecore.EnableProjectJira(workspace, st.DefaultProject)
	}
	return st, nil
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

func (a *App) currentJevPolicy() jev.Policy {
	pol, err := jev.LoadPolicy()
	if err != nil {
		return jev.DefaultPolicy()
	}
	return pol
}

func (a *App) emitProviderHealth(views []jev.ProviderHealthView, force bool) {
	if a.ctx != nil && !a.smokeCheck {
		wailsrt.EventsEmit(a.ctx, "hivemind:provider-health", views)
	}
	if a.mobile != nil && a.mobile.hub != nil {
		a.mobile.hub.HandleProviderHealth(views, force)
	}
}

func (a *App) startJevHealthMonitor(parent context.Context) {
	monCtx, cancel := context.WithCancel(parent)
	a.healthMonitorCancel = cancel

	hs := modelx.DefaultJevHealthStore()
	hs.SetOnChange(func(opened, allClosed bool) {
		views := hs.Summary(a.currentJevPolicy(), modelx.JevCredentialPresence())
		a.emitProviderHealth(views, opened || allClosed)
	})

	go func() {
		a.emitProviderHealth(a.GetProviderHealth(), false)
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-monCtx.Done():
				return
			case <-ticker.C:
				probeCtx, cancelProbe := context.WithTimeout(monCtx, 15*time.Second)
				views := modelx.ProbeAllProvidersHealth(probeCtx, hs, a.currentJevPolicy())
				cancelProbe()
				a.emitProviderHealth(views, false)
			}
		}
	}()
}

// GetProviderHealth returns the current Jev circuit breaker and daily spend snapshot for every known provider.
func (a *App) GetProviderHealth() []jev.ProviderHealthView {
	hs := modelx.DefaultJevHealthStore()
	hs.SyncCredentialFingerprints(modelx.JevCredentialFingerprints())
	return hs.Summary(a.currentJevPolicy(), modelx.JevCredentialPresence())
}

// ProbeProviders runs recovery probes for degraded/quota-tracked providers and broadcasts the updated health state.
func (a *App) ProbeProviders() []jev.ProviderHealthView {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	hs := modelx.DefaultJevHealthStore()
	views := modelx.ProbeAllProvidersHealth(ctx, hs, a.currentJevPolicy())
	a.emitProviderHealth(views, true)
	return views
}

// StartPTY ensures an interactive PTY shell (ConPTY on Windows) is running for termID in workspace,
// returning any buffered scrollback if the session was already active.
func (a *App) StartPTY(termID, workspace string, cols, rows int) (string, error) {
	if a.pty == nil {
		return "", fmt.Errorf("gerenciador PTY não inicializado")
	}
	return a.pty.Start(termID, workspace, cols, rows)
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

// --- Agente Residente Hive (Fase 2 · Sandbox e Cofre) ---

// ExternalSessions lists outside processes by executable name. It does not control them.
func (a *App) ExternalSessions() ([]extsess.ExternalSession, error) {
	extsess.OnReject = func(r extsess.Reject) {
		if a != nil && a.resident != nil {
			a.resident.NoteDetectorRejected(r.ID, r.Reason)
		}
	}
	return extsess.List()
}

// ResidentStatus returns the resident agent's live state (zero secrets).
func (a *App) ResidentStatus() resident.Status {
	if a.resident == nil {
		return resident.Status{State: "ocioso", Phase: 2, ReadOnly: false, AccountID: resident.DefaultAccountID, Workstream: resident.DefaultWorkstream}
	}
	return a.resident.Status()
}

// ResidentSavePolicy saves Phase 2 binary & outbound host allowlists locally.
func (a *App) ResidentSavePolicy(allowedBinaries, allowedHosts []string) (resident.Status, error) {
	if a.resident == nil {
		return resident.Status{}, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.SavePolicy(allowedBinaries, allowedHosts)
}

// ResidentSetSecret stores a named secret directly inside the OS credential vault.
func (a *App) ResidentSetSecret(name, value string) (resident.Status, error) {
	if a.resident == nil {
		return resident.Status{}, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.SetSecret(name, value)
}

// ResidentDeleteSecret removes a named credential from the OS credential vault.
func (a *App) ResidentDeleteSecret(name string) (resident.Status, error) {
	if a.resident == nil {
		return resident.Status{}, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.DeleteSecret(name)
}

// ResidentSaveConfig persists local resident preferences (provider, model, workstream).
func (a *App) ResidentSaveConfig(provider, modelName, workstream string) (resident.Status, error) {
	if a.resident == nil {
		return resident.Status{}, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.SaveConfig(provider, modelName, workstream)
}

// ResidentStartTurn executes a local read-only chat turn with the Hive resident agent.
func (a *App) ResidentStartTurn(workspace, workstream, message string) (resident.TurnResult, error) {
	if a.resident == nil {
		return resident.TurnResult{}, fmt.Errorf("agente residente indisponível")
	}
	if a.mobile != nil && a.mobile.hub != nil {
		a.mobile.hub.RegisterRun(resident.LaneID, workspace, "hive-resident", message, "", resident.RunMode)
	}
	res, err := a.resident.RunTurn(context.Background(), workspace, workstream, message, resident.OriginLocal)
	if err != nil && a.mobile != nil && a.mobile.hub != nil {
		a.mobile.hub.HandleRunError(resident.LaneID, workspace, err)
	}
	return res, err
}

// ResidentCancel stops the active resident turn.
func (a *App) ResidentCancel() bool {
	if a.resident == nil {
		return false
	}
	return a.resident.Cancel()
}

// ResidentSessionMessages loads short-term conversation messages for the active account/workstream.
func (a *App) ResidentSessionMessages(workstream string) ([]resident.LocalTurnMessage, error) {
	if a.resident == nil {
		return nil, nil
	}
	return a.resident.SessionMessages(workstream)
}

// ResidentClearSession clears the short-term conversation history for workstream.
func (a *App) ResidentClearSession(workstream string) error {
	if a.resident == nil {
		return nil
	}
	return a.resident.ClearSession(workstream)
}

// ResidentListMemory lists personal memory facts isolated to the active account.
func (a *App) ResidentListMemory(workstream string) ([]resident.MemoryFact, error) {
	if a.resident == nil {
		return nil, nil
	}
	return a.resident.ListMemory(workstream)
}

// ResidentAddMemory saves an explicit user fact to the active account's local memory.
func (a *App) ResidentAddMemory(workstream, text string, pinned bool) (resident.MemoryFact, error) {
	if a.resident == nil {
		return resident.MemoryFact{}, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.AddMemory(workstream, text, pinned)
}

// ResidentDeleteMemory deletes a personal memory fact by ID for the active account.
func (a *App) ResidentDeleteMemory(id string) (bool, error) {
	if a.resident == nil {
		return false, nil
	}
	return a.resident.DeleteMemory(id)
}

// ResidentAuditLog returns recent audit entries (read-only).
func (a *App) ResidentAuditLog(limit int) ([]resident.AuditEntry, error) {
	if a.resident == nil {
		return nil, nil
	}
	return a.resident.AuditLog(limit)
}
