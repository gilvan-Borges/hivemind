package idecore

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/browserx"
	"github.com/gilvan-Borges/HiveMind/internal/loopx"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/resident"
	"github.com/gilvan-Borges/HiveMind/internal/sandboxx"
	"github.com/gilvan-Borges/HiveMind/internal/secretx"
	"github.com/gilvan-Borges/HiveMind/internal/skillx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// ResidentManager owns the Phase 2 Hive resident assistant inside the
// IDE and headless `hivemind serve` host (see docs/SPEC_HIVE_AGENT.md §3, §4, §12).
type ResidentManager struct {
	baseDir    string
	sessions   *SessionManager
	accountFn  func() (accountID, deviceID string)
	projectsFn func() []resident.ProjectRef
	audit      *resident.FileAuditLogger
	sandbox    sandboxx.Runner
	vault      secretx.Vault
	sched      *resident.Scheduler
	release    func()

	mu             sync.Mutex
	state          string // "ocioso" | "pensando" | "aguardando você"
	enabled        bool
	boundWorkspace string
	boundSession   string
	boundLane      string
	boundKind      string
	lastError      string
	eventTriggers  bool
	cancel         context.CancelFunc
	turnSeq        uint64
	detectorSeen   map[string]struct{}
	turns          sync.WaitGroup

	// testModel optionally overrides LLM creation in unit tests.
	testModel model.LLM
	ideAction resident.IDEActionFunc

	loopCtl       *loopx.Controller
	loopStop      map[string]context.CancelFunc
	loopWG        sync.WaitGroup
	testLoopExec  loopx.Executor
	testLoopCmd   loopx.CommandRunner
	testLoopJudge loopx.Judge
}

// ResidentManagerOptions configures ResidentManager.
type ResidentManagerOptions struct {
	BaseDir    string
	Sessions   *SessionManager
	AccountFn  func() (accountID, deviceID string)
	ProjectsFn func() []resident.ProjectRef
	Sandbox    sandboxx.Runner
	Vault      secretx.Vault
	TestModel  model.LLM
}

// NewResidentManager initializes the resident service without spawning any
// subprocesses, MCP servers, or background listeners (§13).
func NewResidentManager(opts ResidentManagerOptions) *ResidentManager {
	baseDir := strings.TrimSpace(opts.BaseDir)
	if baseDir == "" {
		baseDir = resident.DefaultBaseDir()
	}
	sb := opts.Sandbox
	if sb == nil {
		sb = sandboxx.NewOSRunner()
	}
	vault := opts.Vault
	if vault == nil {
		vault = secretx.NewOSVault()
	}
	cfg, _ := resident.LoadConfig(baseDir)
	m := &ResidentManager{
		baseDir:       baseDir,
		sessions:      opts.Sessions,
		accountFn:     opts.AccountFn,
		projectsFn:    opts.ProjectsFn,
		audit:         resident.NewFileAuditLogger(filepath.Join(baseDir, "audit.jsonl")),
		sandbox:       sb,
		vault:         vault,
		state:         "ocioso",
		enabled:       cfg.Enabled,
		eventTriggers: cfg.EventTriggers,
		testModel:     opts.TestModel,
	}
	if release, ok := resident.TryAcquireLock(baseDir); ok {
		m.release = release
		m.sched = resident.NewScheduler(baseDir, m.fireRoutine)
		if cfg.Enabled {
			m.sched.Arm()
		}
	}
	if m.sessions != nil {
		m.sessions.HostSignal = func(kind string) { m.NoteHostEvent(kind) }
		m.sessions.MirrorShared = m.mirrorIDELine
	}
	return m
}

// SetAllowFullUnattended persists the local switch for unattended Full loops.
// Cron and events stay governed without it. Only this settings call writes the flag.
func (m *ResidentManager) SetAllowFullUnattended(on bool) (resident.Status, error) {
	cfg, err := resident.LoadConfig(m.baseDir)
	if err != nil {
		return m.Status(), err
	}
	cfg.AllowFullUnattended = on
	if err := resident.SaveConfig(m.baseDir, cfg); err != nil {
		return m.Status(), err
	}
	return m.Status(), nil
}

// SetFullAccess persists the Full permission toggle. Off is governed.
func (m *ResidentManager) SetFullAccess(on bool) (resident.Status, error) {
	cfg, err := resident.LoadConfig(m.baseDir)
	if err != nil {
		return m.Status(), err
	}
	if on {
		cfg.PermissionMode = "full"
	} else {
		cfg.PermissionMode = "governed"
	}
	if err := resident.SaveConfig(m.baseDir, cfg); err != nil {
		return m.Status(), err
	}
	return m.Status(), nil
}

// SetIDEAction connects ide_action to the open IDE. The model cannot replace it.
func (m *ResidentManager) SetIDEAction(fn resident.IDEActionFunc) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.ideAction = fn
	m.mu.Unlock()
}

// IsEnabled reports whether the resident assistant is enabled in resident.json.
func (m *ResidentManager) IsEnabled() bool {
	if m == nil {
		return false
	}
	if cfg, err := resident.LoadConfig(m.baseDir); err == nil {
		return cfg.Enabled
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enabled
}

// IsOrbEnabled reports whether the floating Orb should run (resident enabled and Orb not disabled).
func (m *ResidentManager) IsOrbEnabled() bool {
	if m == nil {
		return false
	}
	if cfg, err := resident.LoadConfig(m.baseDir); err == nil {
		return cfg.Enabled && !cfg.OrbDisabled
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enabled
}

// NoteHostEvent records an opt-in wake. It never starts a turn or a tool.
func (m *ResidentManager) NoteHostEvent(kind string) {
	if m == nil {
		return
	}
	switch kind {
	case "arquivo", "lane", "aprovacao", "pr_review", "ci_failed":
	default:
		return
	}
	m.mu.Lock()
	enabled := m.enabled
	on := m.eventTriggers
	m.mu.Unlock()
	if !enabled || !on {
		return
	}
	m.wakeLoops(kind)
	if m.sessions == nil || m.sessions.Emit == nil {
		return
	}
	m.sessions.Emit(LaneEvent{
		LaneID: resident.LaneID,
		Team:   "hive-resident",
		Event: orchestrator.Event{
			Kind:  "stage",
			Stage: "chief",
			Label: "Agent Hive",
			Text:  "evento " + kind,
		},
	})
}

// SetEnabled persists whether the Agent Hive resident assistant is active.
func (m *ResidentManager) SetEnabled(on bool) (resident.Status, error) {
	cfg, err := resident.LoadConfig(m.baseDir)
	if err != nil {
		return m.Status(), err
	}
	cfg.Enabled = on
	if err := resident.SaveConfig(m.baseDir, cfg); err != nil {
		return m.Status(), err
	}
	m.mu.Lock()
	m.enabled = on
	m.mu.Unlock()
	if !on {
		m.Cancel()
		if m.sched != nil {
			m.sched.Stop()
		}
	} else if m.sched != nil {
		m.sched.Arm()
	}
	return m.Status(), nil
}

// SetOrbEnabled persists whether the floating Orb mascot is enabled.
func (m *ResidentManager) SetOrbEnabled(on bool) (resident.Status, error) {
	cfg, err := resident.LoadConfig(m.baseDir)
	if err != nil {
		return m.Status(), err
	}
	cfg.OrbDisabled = !on
	if err := resident.SaveConfig(m.baseDir, cfg); err != nil {
		return m.Status(), err
	}
	return m.Status(), nil
}

// SetJevFailover persists whether resident retries alternate Jev routes when a build fails.
func (m *ResidentManager) SetJevFailover(on bool) (resident.Status, error) {
	cfg, err := resident.LoadConfig(m.baseDir)
	if err != nil {
		return m.Status(), err
	}
	cfg.JevFailover = on
	if err := resident.SaveConfig(m.baseDir, cfg); err != nil {
		return m.Status(), err
	}
	return m.Status(), nil
}

// SetEventTriggers persists the opt-in wake switch. Default is off.
func (m *ResidentManager) SetEventTriggers(on bool) (resident.Status, error) {
	cfg, err := resident.LoadConfig(m.baseDir)
	if err != nil {
		return m.Status(), err
	}
	cfg.EventTriggers = on
	if err := resident.SaveConfig(m.baseDir, cfg); err != nil {
		return m.Status(), err
	}
	m.mu.Lock()
	m.eventTriggers = on
	m.mu.Unlock()
	return m.Status(), nil
}

// SetExperimentalUnisolated persists the host-session browser and desktop switch.
// Default is off. On means those tools run without an isolated space.
func (m *ResidentManager) SetExperimentalUnisolated(on bool) (resident.Status, error) {
	cfg, err := resident.LoadConfig(m.baseDir)
	if err != nil {
		return m.Status(), err
	}
	cfg.ExperimentalUnisolated = on
	if err := resident.SaveConfig(m.baseDir, cfg); err != nil {
		return m.Status(), err
	}
	return m.Status(), nil
}

// SetIsolatedSandbox enables or disables Windows Sandbox (.wsb) provisioning for resident browser opens.
func (m *ResidentManager) SetIsolatedSandbox(on bool) (resident.Status, error) {
	cfg, err := resident.LoadConfig(m.baseDir)
	if err != nil {
		return m.Status(), err
	}
	cfg.IsolatedSandbox = on
	if err := resident.SaveConfig(m.baseDir, cfg); err != nil {
		return m.Status(), err
	}
	return m.Status(), nil
}

// Close espera o turno em curso soltar os arquivos e depois solta a trava.
// No Windows um handle aberto impede o teste de apagar o diretório temporário.
func (m *ResidentManager) Close() {
	if m == nil {
		return
	}
	m.Cancel()
	if m.sched != nil {
		m.sched.Stop()
	}
	m.pauseLoops()
	m.turns.Wait()
	m.loopWG.Wait()
	if m.release != nil {
		m.release()
		m.release = nil
	}
}

func (m *ResidentManager) fireRoutine(rt resident.Routine) {
	if !m.IsEnabled() {
		return
	}
	if strings.TrimSpace(rt.LoopID) != "" {
		_ = m.WakeLoop(rt.Workspace, rt.LoopID)
		return
	}
	_, _ = m.runTurn(context.Background(), "", rt.Workstream, rt.Text, "", resident.OriginRoutine, rt.Classes, turnOpts{})
}

func (m *ResidentManager) currentAccount() (accountID, deviceID string) {
	if m.accountFn != nil {
		acct, dev := m.accountFn()
		return resident.SanitizeAccountID(acct), strings.TrimSpace(dev)
	}
	return resident.DefaultAccountID, ""
}

func (m *ResidentManager) memoryForCurrentAccount() *resident.MemoryStore {
	acct, _ := m.currentAccount()
	return resident.NewMemoryStore(m.baseDir, acct)
}

func (m *ResidentManager) knownProjects() []resident.ProjectRef {
	if m.projectsFn != nil {
		if list := m.projectsFn(); len(list) > 0 {
			return list
		}
	}
	recents := LoadRecentProjects("")
	out := make([]resident.ProjectRef, 0, len(recents))
	for _, dir := range recents {
		out = append(out, resident.ProjectRef{
			Name: filepath.Base(dir),
			Root: dir,
		})
	}
	return out
}

// Status returns the live resident state without leaking any secrets (§11).
func (m *ResidentManager) Status() resident.Status {
	cfg, _ := resident.LoadConfig(m.baseDir)
	pol, _ := resident.LoadPolicy(m.baseDir)
	prov, mod := resident.ResolveModelRoute(cfg)
	acct, _ := m.currentAccount()
	mem := resident.NewMemoryStore(m.baseDir, acct)

	probe := sandboxx.ProbeResult{}
	if m.sandbox != nil {
		probe = m.sandbox.Probe()
	}
	vaultOK := false
	vaultReason := "cofre não inicializado"
	var vaultNames []string
	if m.vault != nil {
		vaultOK, vaultReason = m.vault.Available()
		if vaultOK {
			vaultNames, _ = m.vault.ListNames()
		}
	}

	m.mu.Lock()
	st := m.state
	lastErr := m.lastError
	m.mu.Unlock()
	if !cfg.Enabled {
		st = "desativado"
	} else if st == "" {
		st = "ocioso"
	}

	browserOK, browserReason := resident.IsolatedBrowserAvailable()
	desktopOK, desktopReason := resident.IsolatedDesktopAvailable()
	convID, sharedSID, laneID := "", "", ""
	if conv, ok := m.activeConversation(); ok {
		convID = conv.ID
		sharedSID = conv.SessionID
		laneID = conv.LaneID
	}
	warning := ""
	if cfg.ExperimentalUnisolated {
		warning = resident.ExperimentalUnisolatedWarning
	}
	return resident.Status{
		State:                  st,
		Enabled:                cfg.Enabled,
		OrbEnabled:             !cfg.OrbDisabled,
		JevFailover:            cfg.JevFailover,
		ReadOnly:               false,
		AccountID:              acct,
		Workstream:             resident.SanitizeWorkstream(cfg.Workstream),
		Provider:               prov,
		Model:                  mod,
		MemoryDir:              mem.AccountDir(),
		AuditPath:              m.audit.Path(),
		LastError:              lastErr,
		SandboxAvailable:       probe.Available,
		SandboxMechanism:       probe.Mechanism,
		SandboxReason:          probe.Reason,
		VaultAvailable:         vaultOK,
		VaultReason:            vaultReason,
		AllowedBinaries:        pol.AllowedBinaries,
		AllowedHosts:           pol.AllowedHosts,
		VaultSecrets:           vaultNames,
		EventTriggers:          cfg.EventTriggers,
		ExperimentalUnisolated: cfg.ExperimentalUnisolated,
		IsolatedSandbox:        cfg.IsolatedSandbox,
		ExperimentalWarning:    warning,
		IsolatedBrowser:        browserOK,
		IsolatedBrowserReason:  browserReason,
		IsolatedDesktop:        desktopOK,
		IsolatedDesktopReason:  desktopReason,
		ConversationID:         convID,
		SharedSessionID:        sharedSID,
		LaneID:                 laneID,
		PermissionMode:         resident.NormalizeResidentPermission(cfg.PermissionMode),
		FullAccess:             cfg.IsFullAccess(),
		AllowFullUnattended:    cfg.AllowFullUnattended,
	}
}

// GetSoul reads the bounded SOUL.md preferences file for the resident agent.
func (m *ResidentManager) GetSoul() (string, error) {
	return resident.LoadSoul(m.baseDir)
}

// SaveSoul writes SOUL.md atomically for the resident agent.
func (m *ResidentManager) SaveSoul(content string) error {
	return resident.SaveSoul(m.baseDir, content)
}

// SaveConfig persists local resident UI preferences (provider, model, active workstream).
// Only invoked by local Wails UI bindings — never exposed via remote HTTP (§10, §11).
func (m *ResidentManager) SaveConfig(provider, modelName, workstream string) (resident.Status, error) {
	cfg, err := resident.LoadConfig(m.baseDir)
	if err != nil {
		return m.Status(), err
	}
	cfg.Provider = strings.ToLower(strings.TrimSpace(provider))
	cfg.Model = strings.TrimSpace(modelName)
	if strings.TrimSpace(workstream) != "" {
		cfg.Workstream = resident.SanitizeWorkstream(workstream)
	}
	if err := resident.SaveConfig(m.baseDir, cfg); err != nil {
		return m.Status(), err
	}
	return m.Status(), nil
}

// Policy returns the current local fail-closed allowlists for binaries and hosts.
func (m *ResidentManager) Policy() resident.Policy {
	pol, err := resident.LoadPolicy(m.baseDir)
	if err != nil {
		return resident.Policy{Version: 2}
	}
	return pol
}

// SavePolicy validates and persists Phase 2 allowlists locally (never via remote HTTP).
func (m *ResidentManager) SavePolicy(allowedBinaries, allowedHosts []string) (resident.Status, error) {
	pol := resident.Policy{
		Version:         2,
		AllowedBinaries: allowedBinaries,
		AllowedHosts:    allowedHosts,
	}
	if err := resident.SavePolicy(m.baseDir, pol); err != nil {
		return m.Status(), err
	}
	return m.Status(), nil
}

// SetSecret stores a credential strictly in the OS vault (never returns the value).
func (m *ResidentManager) SetSecret(name, secret string) (resident.Status, error) {
	if m.vault == nil {
		return m.Status(), secretx.ErrVaultUnavailable
	}
	if err := m.vault.Set(name, secret); err != nil {
		return m.Status(), err
	}
	return m.Status(), nil
}

// DeleteSecret removes a named credential from the OS vault.
func (m *ResidentManager) DeleteSecret(name string) (resident.Status, error) {
	if m.vault == nil {
		return m.Status(), secretx.ErrVaultUnavailable
	}
	if err := m.vault.Delete(name); err != nil {
		return m.Status(), err
	}
	return m.Status(), nil
}

// RunTurn executes one resident turn and streams events through the shared
// SessionManager emitter on fixed lane "resident" (Wails + Remote Bridge SSE).
func (m *ResidentManager) RunTurn(
	parentCtx context.Context,
	workspaceDir, workstream, message string,
	origin resident.Origin,
) (resident.TurnResult, error) {
	return m.runTurn(parentCtx, workspaceDir, workstream, message, "", origin, nil, turnOpts{})
}

// RunTurnIn continues the shared conversation identified by sessionID.
// An empty sessionID keeps the conversation bound to the open IDE lane.
func (m *ResidentManager) RunTurnIn(
	parentCtx context.Context,
	workspaceDir, workstream, message, sessionID string,
	origin resident.Origin,
) (resident.TurnResult, error) {
	return m.runTurn(parentCtx, workspaceDir, workstream, message, sessionID, origin, nil, turnOpts{})
}

// turnOpts carries a loop iteration into the resident without a second public API.
type turnOpts struct {
	loopID       string
	iteration    int
	model        string
	readOnly     bool
	forceFull    *bool
	fresh        bool
	display      string
	modelMessage string
	skipCommand  bool
}

// RunBrowserExtensionTurn executes a completely isolated Agent Hive turn for the
// Chrome sidepanel extension:
//   - NEVER reads or touches any open IDE workspace, file, or shared conversation;
//   - NEVER emits events to IDE lanes/tabs or triggers SquadHandoff / IDE actions;
//   - Keeps its own private short-term session under workstream "chrome-extension".
func (m *ResidentManager) RunBrowserExtensionTurn(
	parentCtx context.Context,
	message string,
) (resident.TurnResult, error) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return resident.TurnResult{}, fmt.Errorf("mensagem vazia")
	}
	m.turns.Add(1)
	defer m.turns.Done()

	cfg, err := resident.LoadConfig(m.baseDir)
	if err != nil {
		return resident.TurnResult{}, err
	}
	if !cfg.Enabled {
		return resident.TurnResult{}, fmt.Errorf("Agent Hive está desativado nas Configurações")
	}
	pol, _ := resident.LoadPolicy(m.baseDir)
	acct, devID := m.currentAccount()
	mem := resident.NewMemoryStore(m.baseDir, acct)
	prov, mod := resident.ResolveModelRoute(cfg)
	soul, _ := resident.LoadSoul(m.baseDir)

	runner := &resident.Runner{
		BaseDir:      m.baseDir,
		Config:       cfg,
		Policy:       &pol,
		Sandbox:      m.sandbox,
		Vault:        m.vault,
		Audit:        m.audit,
		Memory:       mem,
		Model:        m.testModel,
		SkillsDir:    skillx.UserSkillsDir(),
		AllowedTiers: []resident.Tier{resident.TierRead, resident.TierNetwork},
		ApproveDetailed: func(_ context.Context, req resident.ApprovalRequest) bool {
			// Host-level approval happens inside the Chrome sidepanel itself.
			return req.ToolName == "chrome_tab"
		},
		OnEvent: func(ev resident.Event) {
			if ev.Kind == "usage" && (ev.Tokens > 0 || ev.InputTokens > 0 || ev.OutputTokens > 0) {
				modelx.RecordAIUsage(prov, mod, int64(ev.InputTokens), int64(ev.OutputTokens), int64(ev.Tokens))
			}
			switch ev.Kind {
			case "stage":
				if strings.TrimSpace(ev.Text) != "" {
					browserx.PushActivity("Agent Hive · " + strings.TrimSpace(ev.Text))
				}
			case "tool_call":
				lbl := strings.TrimSpace(ev.ToolName)
				if strings.TrimSpace(ev.Detail) != "" {
					lbl += " · " + strings.TrimSpace(ev.Detail)
				}
				browserx.PushActivity(lbl)
			case "text":
				if ev.Thought && strings.TrimSpace(ev.Text) != "" {
					firstLine := strings.SplitN(strings.TrimSpace(ev.Text), "\n", 2)[0]
					browserx.PushActivity("Pensando: " + firstLine)
				}
			}
		},
	}

	return runner.RunTurn(parentCtx, resident.TurnRequest{
		Message:    msg,
		Workspace:  "",
		Workstream: "chrome-extension",
		Origin:     resident.OriginLocal,
		AccountID:  acct,
		DeviceID:   devID,
		Soul:       soul,
	})
}

func (m *ResidentManager) runTurn(
	parentCtx context.Context,
	workspaceDir, workstream, message string,
	sessionID string,
	origin resident.Origin,
	classes []string,
	opts turnOpts,
) (resident.TurnResult, error) {
	m.turns.Add(1)
	defer m.turns.Done()
	cfg, err := resident.LoadConfig(m.baseDir)
	if err != nil {
		return resident.TurnResult{}, err
	}
	if !cfg.Enabled {
		return resident.TurnResult{}, fmt.Errorf("Agent Hive está desativado nas Configurações da IDE")
	}
	if !opts.skipCommand {
		if handled, res, herr := m.interceptLoop(parentCtx, workspaceDir, workstream, message, sessionID, origin); handled {
			return res, herr
		}
	}
	shown := message
	if opts.display != "" {
		shown = opts.display
	}
	if opts.modelMessage != "" {
		if opts.display == "" {
			shown = message
		}
		message = opts.modelMessage
	}
	if opts.model != "" {
		cfg.Model = opts.model
	}
	pol, _ := resident.LoadPolicy(m.baseDir)
	if strings.TrimSpace(workstream) == "" {
		workstream = cfg.Workstream
	}
	workstream = resident.SanitizeWorkstream(workstream)
	acct, devID := m.currentAccount()
	mem := resident.NewMemoryStore(m.baseDir, acct)
	shared, sharedErr := m.prepareSharedTurn(strings.TrimSpace(workspaceDir), workstream, shown, sessionID, string(origin))
	if sharedErr != nil {
		return resident.TurnResult{}, sharedErr
	}

	ctx, cancel := context.WithCancel(parentCtx)
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.turnSeq++
	mySeq := m.turnSeq
	m.cancel = cancel
	m.state = "pensando"
	m.lastError = ""
	m.mu.Unlock()

	defer func() {
		cancel()
		m.mu.Lock()
		if m.turnSeq == mySeq {
			m.cancel = nil
			m.state = "ocioso"
		}
		m.mu.Unlock()
	}()

	approveDetailed := func(appCtx context.Context, req resident.ApprovalRequest) bool {
		if req.ToolName == "chrome_tab" && (origin == resident.OriginLocal || string(origin) == "orb") {
			return true
		}
		if m.sessions == nil || m.sessions.Emit == nil {
			return false
		}
		m.mu.Lock()
		m.state = "aguardando você"
		m.mu.Unlock()
		label := fmt.Sprintf("Agent Hive · Risco: %s", req.RiskLabel)
		detail := fmt.Sprintf("risco:%s (%s) | origem:%s", req.RiskLabel, req.Tier, req.Origin)
		ok := m.sessions.RequestApprovalWithMeta(appCtx, resident.LaneID, "hive-resident", req.Prompt, ApprovalMetadata{
			Label:    label,
			ToolName: req.ToolName,
			Detail:   detail,
			Gate:     string(req.Tier),
		})
		m.mu.Lock()
		m.state = "pensando"
		m.mu.Unlock()
		return ok
	}

	narrow, err := resident.NarrowTiers(classes)
	if err != nil {
		return resident.TurnResult{}, err
	}
	if opts.readOnly {
		narrow = []resident.Tier{resident.TierRead}
	}
	prov, mod := resident.ResolveModelRoute(cfg)
	full := cfg.IsFullAccess() || workspacePermissionFull(workspaceDir)
	if opts.forceFull != nil {
		full = *opts.forceFull
	}
	if opts.readOnly {
		full = false
	}
	runner := &resident.Runner{
		BaseDir:         m.baseDir,
		Config:          cfg,
		Policy:          &pol,
		Sandbox:         m.sandbox,
		Vault:           m.vault,
		Audit:           m.audit,
		Memory:          mem,
		ListProjects:    m.knownProjects,
		ApproveDetailed: approveDetailed,
		Model:           m.testModel,
		SkillsDir:       skillx.UserSkillsDir(),
		IDE:             m.ideAction,
		Child:           opts.readOnly,
		FullAccess:      full,
		LoopID:          opts.loopID,
		Iteration:       opts.iteration,
		AllowedTiers:    narrow,
		PermissionMode:  m.permissionMode,
		SquadHandoff:    m.squadHandoff,
		OnRoutineSaved: func() {
			if m.sched != nil {
				m.sched.Arm()
			}
		},
		OnEvent: func(ev resident.Event) {
			if ev.Kind == "usage" && (ev.Tokens > 0 || ev.InputTokens > 0 || ev.OutputTokens > 0) {
				modelx.RecordAIUsage(prov, mod, int64(ev.InputTokens), int64(ev.OutputTokens), int64(ev.Tokens))
			}
			if ev.Kind == "text" && ev.MessageID == "" {
				ev.MessageID = shared.ReplyID
			}
			if ev.Kind == "user_message" && ev.MessageID == "" {
				ev.MessageID = shared.UserID
			}
			if ev.ConversationID == "" {
				ev.ConversationID = shared.Conv.ID
			}
			if ev.SessionID == "" {
				ev.SessionID = shared.Conv.SessionID
			}
			if ev.ToolName == "chrome_tab" && ev.Kind == "tool_call" {
				browserx.PushActivity("IDE → Chrome: " + strings.TrimSpace(ev.Detail))
			}
			if m.sessions != nil && m.sessions.Emit != nil {
				detail := ev.Detail
				if ev.RiskTier != "" && detail != "" && !strings.Contains(detail, "risco:") {
					detail = fmt.Sprintf("[%s] %s", ev.RiskTier, detail)
				}
				m.sessions.Emit(LaneEvent{
					LaneID: resident.LaneID,
					Team:   "hive-resident",
					Event: orchestrator.Event{
						Kind:           ev.Kind,
						Stage:          "chief",
						Author:         "hive",
						Label:          ev.Label,
						Text:           ev.Text,
						ToolName:       ev.ToolName,
						Detail:         detail,
						Gate:           ev.RiskTier,
						ApprovalID:     ev.ApprovalID,
						Partial:        ev.Partial,
						Thought:        ev.Thought,
						InputTokens:    ev.InputTokens,
						OutputTokens:   ev.OutputTokens,
						Tokens:         ev.Tokens,
						Origin:         string(origin),
						MessageID:      ev.MessageID,
						ConversationID: ev.ConversationID,
						SessionID:      ev.SessionID,
						LoopID:         ev.LoopID,
						Iteration:      ev.Iteration,
					},
				})
			}
		},
	}

	soul, _ := resident.LoadSoul(m.baseDir)
	history := shared.History
	if opts.fresh {
		history = nil
	}
	res, runErr := runner.RunTurn(ctx, resident.TurnRequest{
		Message:          message,
		Workspace:        strings.TrimSpace(workspaceDir),
		Workstream:       workstream,
		Origin:           origin,
		AccountID:        acct,
		DeviceID:         devID,
		Soul:             soul,
		ConversationID:   shared.Conv.ID,
		SharedSessionID:  shared.Conv.SessionID,
		UserMessageID:    shared.UserID,
		ReplyMessageID:   shared.ReplyID,
		SharedHistory:    history,
		SkipLocalHistory: true,
		FullAccess:       full,
		Display:          opts.display,
		LoopID:           opts.loopID,
		Iteration:        opts.iteration,
	})
	if runErr != nil {
		m.mu.Lock()
		m.lastError = resident.RedactSecrets(runErr.Error())
		m.mu.Unlock()
		return resident.TurnResult{}, runErr
	}
	m.finishSharedTurn(shared, res.FinalText)
	res.ConversationID = shared.Conv.ID
	res.SharedSessionID = shared.Conv.SessionID
	res.UserMessageID = shared.UserID
	res.ReplyMessageID = shared.ReplyID
	_ = resident.SaveRouteTokens(m.baseDir, res.InputTokens)
	return res, nil
}

// Cancel aborts an in-flight resident turn and denies any pending approval.
func (m *ResidentManager) Cancel() bool {
	m.mu.Lock()
	cancel := m.cancel
	m.cancel = nil
	m.state = "ocioso"
	m.mu.Unlock()
	canceled := false
	if cancel != nil {
		cancel()
		canceled = true
	}
	if m.sessions != nil && m.sessions.Cancel(resident.LaneID) {
		canceled = true
	}
	if m.pauseLoops() {
		canceled = true
	}
	return canceled
}

// SessionMessages returns the shared transcript when one is active, otherwise
// the legacy workstream file.
func (m *ResidentManager) SessionMessages(workstream string) ([]resident.LocalTurnMessage, error) {
	if conv, ok := m.activeConversation(); ok && len(conv.Messages) > 0 {
		out := make([]resident.LocalTurnMessage, 0, len(conv.Messages))
		for _, msg := range conv.Messages {
			out = append(out, resident.LocalTurnMessage{ID: msg.ID, Role: msg.Role, Text: msg.Text, At: msg.At})
		}
		return out, nil
	}
	return m.memoryForCurrentAccount().LoadSessionMessages(workstream, 40)
}

// ClearSession deletes the short-term conversation history for workstream.
func (m *ResidentManager) ClearSession(workstream string) error {
	if conv, ok := m.activeConversation(); ok {
		_ = m.conversations().ClearMessages(conv.ID)
	}
	return m.memoryForCurrentAccount().ClearSession(workstream)
}

// ListMemory returns personal facts stored locally for the active account.
func (m *ResidentManager) ListMemory(workstream string) ([]resident.MemoryFact, error) {
	return m.memoryForCurrentAccount().ListFacts(workstream)
}

// AddMemory stores an explicit user fact in the active account's personal memory.
func (m *ResidentManager) AddMemory(workstream, text string, pinned bool) (resident.MemoryFact, error) {
	return m.memoryForCurrentAccount().AddFact(workstream, text, pinned)
}

// DeleteMemory deletes a personal fact by ID from the active account's memory.
func (m *ResidentManager) DeleteMemory(id string) (bool, error) {
	return m.memoryForCurrentAccount().DeleteFact(id)
}

// NoteDetectorRejected records a skipped detector once per file content.
// The tier is read: this is not use of the credential vault.
func (m *ResidentManager) NoteDetectorRejected(id, reason, fingerprint string) {
	if m == nil || m.audit == nil {
		return
	}
	key := fingerprint
	if key == "" {
		key = id + "\x00" + reason
	}
	m.mu.Lock()
	if m.detectorSeen == nil {
		m.detectorSeen = map[string]struct{}{}
	}
	if _, ok := m.detectorSeen[key]; ok {
		m.mu.Unlock()
		return
	}
	m.detectorSeen[key] = struct{}{}
	m.mu.Unlock()
	_ = m.audit.RecordDecision(resident.AuditEntry{
		Origin:     resident.OriginLocal,
		Tool:       "detector_rejected",
		Tier:       resident.TierRead,
		Decision:   "deny",
		Target:     id,
		Outcome:    "denied",
		ErrorClass: "detector_rejected",
		Reason:     reason,
	})
}

// AuditLog returns recent audit entries (read-only view, §11).
func (m *ResidentManager) AuditLog(limit int) ([]resident.AuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	return m.audit.ReadRecent(limit)
}

func workspacePermissionFull(workspace string) bool {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return false
	}
	cfg, err := team.Load(workspace)
	if err != nil {
		return false
	}
	return cfg.Settings.IsFullAccess()
}

func (m *ResidentManager) permissionMode(ws string) string {
	cfg, err := team.Load(strings.TrimSpace(ws))
	if err != nil {
		return "indisponível"
	}
	return cfg.Settings.EffectivePermissionMode()
}

func (m *ResidentManager) squadHandoff(_ context.Context, h resident.SquadHandoff) (string, error) {
	if m.sessions == nil {
		return "", fmt.Errorf("sessão indisponível para handoff")
	}
	ws := strings.TrimSpace(h.Workspace)
	if ws == "" {
		return "", fmt.Errorf("workspace ausente para handoff")
	}
	cfg, err := team.Load(ws)
	if err != nil {
		return "", err
	}
	selected, err := cfg.ActiveTeam(h.Team)
	if err != nil {
		return "", err
	}
	pkg := orchestrator.ExternalHandoff(resident.LaneID, h.Objective, h.Facts)
	if err := orchestrator.DeliverExternalHandoff(ws, pkg); err != nil {
		return "", err
	}
	text := pkg.Objective
	if len(pkg.Facts) > 0 {
		text += "\n\n" + strings.Join(pkg.Facts, "\n")
	}
	go func() {
		_, _ = m.sessions.RunTurnWithOptions(context.Background(), "resident-squad", ws, selected.Name, text, "", "", "auto", nil)
	}()
	return pkg.DeliveryHash, nil
}

// ListRoutines returns the structured schedules. There is no shell field.
func (m *ResidentManager) ListRoutines() ([]resident.Routine, error) {
	return resident.LoadRoutines(m.baseDir)
}

// SaveRoutine writes one structured routine and rearms the in-process timer.
func (m *ResidentManager) SaveRoutine(rt resident.Routine) (resident.Routine, error) {
	saved, err := resident.SaveRoutine(m.baseDir, rt)
	if err != nil {
		return resident.Routine{}, err
	}
	if m.sched != nil {
		m.sched.Arm()
	}
	return saved, nil
}

// SetRoutineEnabled pauses or resumes one routine.
func (m *ResidentManager) SetRoutineEnabled(id string, enabled bool) error {
	if err := resident.SetRoutineEnabled(m.baseDir, id, enabled); err != nil {
		return err
	}
	if m.sched != nil {
		m.sched.Arm()
	}
	return nil
}

// DeleteRoutine removes one routine.
func (m *ResidentManager) DeleteRoutine(id string) error {
	if err := resident.DeleteRoutine(m.baseDir, id); err != nil {
		return err
	}
	if m.sched != nil {
		m.sched.Arm()
	}
	return nil
}

// ReviewSkill stores the hash of a native skill. Project skills are refused.
func (m *ResidentManager) ReviewSkill(name string) (string, error) {
	return resident.ReviewSkill(m.baseDir, skillx.UserSkillsDir(), name)
}

// FormatHeadlessStatus formats the one-line status printed by `hivemind serve` (§11).
func (m *ResidentManager) FormatHeadlessStatus() string {
	st := m.Status()
	sb := st.SandboxMechanism
	if !st.SandboxAvailable {
		sb = "indisponível (falha fechada)"
	}
	return fmt.Sprintf("Agent Hive (sandbox=%s): %s · conta=%s · modelo=%s/%s",
		sb, st.State, st.AccountID, st.Provider, st.Model)
}
