package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/ide/internal/notify"
	"github.com/gilvan-Borges/HiveMind/internal/browserx"
	"github.com/gilvan-Borges/HiveMind/internal/loopx"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/relayclient"
	"github.com/gilvan-Borges/HiveMind/internal/remote"
	"github.com/gilvan-Borges/HiveMind/internal/resident"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/updater"
	"github.com/gilvan-Borges/HiveMind/internal/version"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App exposes backend capabilities to the Wails frontend.
type App struct {
	ctx                 context.Context
	initialDir          string
	smokeCheck          bool
	sessions            *idecore.SessionManager
	autoReview          *idecore.AutoReviewManager
	githubMonitor       *idecore.GitHubMonitor
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
	gitMu               sync.Mutex
	gitGen              uint64
	gitCalls            map[string]gitCall
	inlineMu            sync.Mutex
	ghostCancel         context.CancelFunc
	editCancel          context.CancelFunc
	watcherMu           sync.Mutex
	watchers            map[string]*idecore.FileWatcher
	openMu              sync.Mutex
	openProjects        map[string]int
	updaterBaseURL      string
	updaterHTTP         *http.Client
	updaterVersion      string
	mobile              *mobileBridge
	relay               *relayclient.Client
	resident            *idecore.ResidentManager
	designMode          *idecore.DesignModeService
	healthMonitorCancel context.CancelFunc
	desk                *notify.Hub
	orb                 orbHostState
	tray                nativeTray
	ideWindowOpen       bool
}

// UpdateCheckResult summarizes whether a newer HiveMind release is available.
type UpdateCheckResult struct {
	Available      bool   `json:"available"`
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	Commit         string `json:"commit,omitempty"`
	BuildDate      string `json:"buildDate,omitempty"`
	ArtifactName   string `json:"artifactName,omitempty"`
	DownloadURL    string `json:"downloadUrl,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
	Size           int64  `json:"size,omitempty"`
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
		initialDir:   initialDir,
		smokeCheck:   smokeCheck,
		fileIndex:    idecore.NewFileIndex(),
		termCancels:  make(map[string]context.CancelFunc),
		gitCalls:     make(map[string]gitCall),
		watchers:     make(map[string]*idecore.FileWatcher),
		openProjects: make(map[string]int),
	}
	// The mobile bridge hub is created up-front so session and auto-review
	// events fan out to it from the very first turn (the bridge server itself
	// is only assembled later by newMobileBridge).
	bridgeHub := remote.NewHub()
	app.sessions = idecore.NewSessionManager(func(ev idecore.LaneEvent) {
		if app.ctx != nil {
			wailsrt.EventsEmit(app.ctx, "hivemind:lane-event", ev)
		}
		bridgeHub.HandleLaneEvent(ev)
	})
	app.autoReview = idecore.NewAutoReviewManager(app.sessions, func(st idecore.AutoReviewStatus) {
		if app.ctx != nil && !app.smokeCheck {
			wailsrt.EventsEmit(app.ctx, "hivemind:autoreview-status", st)
		}
		bridgeHub.HandleAutoReviewStatus(st)
	})
	app.githubMonitor = idecore.NewGitHubMonitor(func(ev idecore.GitHubSyncEvent) {
		if app.ctx != nil && !app.smokeCheck {
			wailsrt.EventsEmit(app.ctx, "hivemind:github-sync", ev)
		}
	})
	app.autoReview.ListPRsFn = func(ctx context.Context, workspace string) ([]idecore.ProjectPullRequest, error) {
		if app.githubMonitor != nil {
			if cached, ok := app.githubMonitor.CachedPRs(workspace, 45*time.Second); ok {
				return cached, nil
			}
		}
		prs, err := idecore.ListProjectPullRequests(ctx, workspace)
		if err == nil && app.githubMonitor != nil {
			app.githubMonitor.RecordSnapshot(workspace, prs)
		}
		return prs, err
	}
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
			for _, p := range bridgeHub.Projects() {
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
	app.resident.SetIDEAction(app.hiveIDEAction)
	app.mobile = newMobileBridge(app.sessions, app.resident, app.autoReview, bridgeHub)
	app.relay = newRelayClient(app)
	return app
}

func computeStartupWindowSize(screenW, screenH int) (w, h int, maximize bool) {
	if screenW <= 0 || screenH <= 0 {
		return 1180, 700, false
	}
	availW := screenW - 40
	availH := screenH - 80
	w = min(1360, max(760, availW))
	h = min(840, max(520, availH))
	if w > screenW {
		w = screenW
	}
	if h > screenH-40 && screenH > 40 {
		h = screenH - 40
	}
	maximize = screenW <= 1440 || screenH <= 900
	return w, h, maximize
}

func (a *App) fitWindowToScreen(ctx context.Context) {
	if ctx == nil || a.smokeCheck {
		return
	}
	screens, err := wailsrt.ScreenGetAll(ctx)
	if err != nil || len(screens) == 0 {
		return
	}
	target := screens[0]
	for _, sc := range screens {
		if sc.IsCurrent || sc.IsPrimary {
			target = sc
			break
		}
	}
	w, h, maximize := computeStartupWindowSize(target.Size.Width, target.Size.Height)
	wailsrt.WindowSetSize(ctx, w, h)
	wailsrt.WindowCenter(ctx)
	if maximize {
		wailsrt.WindowMaximise(ctx)
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.setIDEWindowOpen(true)
	a.fitWindowToScreen(ctx)
	applyWindowIcons()
	// Semeia as skills nativas padrão (planejamento e SDD) no catálogo da IDE,
	// sem sobrescrever edições do usuário feitas no painel de Skills.
	if _, err := idecore.SeedNativeSkills(); err != nil {
		log.Printf("aviso: falha ao semear skills nativas: %v", err)
	}
	for _, dir := range idecore.LoadRecentProjects("") {
		if _, err := loopx.PauseRunning(dir); err != nil {
			log.Printf("aviso: pausar loops de %s: %v", dir, err)
		}
	}
	if a.mobile != nil {
		a.mobile.start()
	}
	if a.relay != nil && !a.smokeCheck {
		a.relay.Start()
	}
	if !a.smokeCheck {
		a.startJevHealthMonitor(ctx)
		a.startDesktopNotices()
		a.startSystemTray()
		a.startOrbHost()
		a.startBrowserBridge()
	}
}

func (a *App) shutdown(_ context.Context) {
	_ = browserx.Close()
	if a.desk != nil {
		_ = a.desk.Close()
	}
	a.stopOrbHost()
	a.stopSystemTray()
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
	if a.githubMonitor != nil {
		a.githubMonitor.StopAll()
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
		a.resident.Close()
	}
	if a.designMode != nil {
		a.designMode.Close()
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
	a.openMu.Lock()
	a.openProjects = make(map[string]int)
	a.openMu.Unlock()
	modelx.FlushAIUsage()
}

// AppVersion returns the running HiveMind version string.
func (a *App) AppVersion() string {
	if a != nil && strings.TrimSpace(a.updaterVersion) != "" {
		return strings.TrimSpace(a.updaterVersion)
	}
	return version.String()
}

func (a *App) updaterClient() *updater.Client {
	base := updater.DefaultBaseURL
	if a != nil && strings.TrimSpace(a.updaterBaseURL) != "" {
		base = a.updaterBaseURL
	}
	c := updater.New(base)
	if a != nil && a.updaterHTTP != nil {
		c.HTTP = a.updaterHTTP
	}
	return c
}

func (a *App) currentUpdaterVersion() string {
	if a != nil && strings.TrimSpace(a.updaterVersion) != "" {
		return strings.TrimSpace(a.updaterVersion)
	}
	return version.Version
}

// CheckForUpdates queries GitHub Releases manifest.json for a newer installer artifact.
func (a *App) CheckForUpdates() (UpdateCheckResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cur := a.currentUpdaterVersion()
	upd, err := a.updaterClient().Check(ctx, cur, "installer")
	if err != nil {
		if errors.Is(err, updater.ErrNoUpdate) {
			return UpdateCheckResult{
				Available:      false,
				CurrentVersion: cur,
				LatestVersion:  cur,
			}, nil
		}
		return UpdateCheckResult{}, err
	}
	return UpdateCheckResult{
		Available:      true,
		CurrentVersion: cur,
		LatestVersion:  upd.Release.Version,
		Commit:         upd.Release.Commit,
		BuildDate:      upd.Release.BuildDate,
		ArtifactName:   upd.Artifact.Name,
		DownloadURL:    upd.URL,
		SHA256:         upd.Artifact.SHA256,
		Size:           upd.Artifact.Size,
	}, nil
}

// DownloadUpdate downloads and SHA-256 verifies a release installer into the user cache directory.
func (a *App) DownloadUpdate(versionStr, artifactName, downloadURL, sha256Hex string, size int64) (string, error) {
	name := filepath.Base(strings.TrimSpace(artifactName))
	urlStr := strings.TrimSpace(downloadURL)
	digest := strings.TrimSpace(sha256Hex)
	if name == "" || name == "." || urlStr == "" || digest == "" {
		return "", fmt.Errorf("metadados de atualização incompletos")
	}
	baseDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(baseDir) == "" {
		baseDir = os.TempDir()
	}
	destDir := filepath.Join(baseDir, "HiveMind", "updates")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	upd := &updater.Update{
		Release: updater.Release{Version: strings.TrimSpace(versionStr)},
		Artifact: updater.Artifact{
			Name:   name,
			SHA256: digest,
			Size:   size,
		},
		URL: urlStr,
	}
	return a.updaterClient().DownloadArtifact(ctx, upd, destDir)
}

func (a *App) setIDEWindowOpen(open bool) {
	a.mu.Lock()
	a.ideWindowOpen = open
	a.mu.Unlock()
}

// IDEWindowOpen reports whether the main window is on screen.
// Hidden to the tray is closed for notifications. Minimised is checked by the frontend.
func (a *App) IDEWindowOpen() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ideWindowOpen
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

// ShowDesktopNotification sends one native OS notice. The click brings the IDE forward.
func (a *App) ShowDesktopNotification(id, title, body string, sound bool) error {
	if a.desk == nil {
		return fmt.Errorf("notificações do sistema indisponíveis")
	}
	return a.desk.Send(notify.Notice{ID: id, Title: title, Body: body, Sound: sound})
}

// PlayAlertSound plays the 2-second synthesized attention/error alarm natively on the host OS.
func (a *App) PlayAlertSound(mood string) bool {
	return playNativeAlertAlarm(mood)
}

// WindowClose hides the IDE to background when the floating Orb is active,
// or quits normally when the Orb is not running.
func (a *App) WindowClose() {
	if a.ctx == nil {
		return
	}
	if a.beforeClose(a.ctx) {
		return
	}
	wailsrt.Quit(a.ctx)
}

// WindowIsMaximized returns whether the window is currently maximized.
func (a *App) WindowIsMaximized() bool {
	if a.ctx == nil {
		return false
	}
	return wailsrt.WindowIsMaximised(a.ctx)
}
