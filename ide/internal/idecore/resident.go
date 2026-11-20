package idecore

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/resident"
	"github.com/gilvan-Borges/HiveMind/internal/sandboxx"
	"github.com/gilvan-Borges/HiveMind/internal/secretx"
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

	mu           sync.Mutex
	state        string // "ocioso" | "pensando" | "aguardando você"
	lastError    string
	cancel       context.CancelFunc
	detectorSeen map[string]struct{}

	// testModel optionally overrides LLM creation in unit tests.
	testModel model.LLM
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
	return &ResidentManager{
		baseDir:    baseDir,
		sessions:   opts.Sessions,
		accountFn:  opts.AccountFn,
		projectsFn: opts.ProjectsFn,
		audit:      resident.NewFileAuditLogger(filepath.Join(baseDir, "audit.jsonl")),
		sandbox:    sb,
		vault:      vault,
		state:      "ocioso",
		testModel:  opts.TestModel,
	}
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
	if st == "" {
		st = "ocioso"
	}

	return resident.Status{
		State:            st,
		Phase:            2,
		ReadOnly:         false,
		AccountID:        acct,
		Workstream:       resident.SanitizeWorkstream(cfg.Workstream),
		Provider:         prov,
		Model:            mod,
		MemoryDir:        mem.AccountDir(),
		AuditPath:        m.audit.Path(),
		LastError:        lastErr,
		SandboxAvailable: probe.Available,
		SandboxMechanism: probe.Mechanism,
		SandboxReason:    probe.Reason,
		VaultAvailable:   vaultOK,
		VaultReason:      vaultReason,
		AllowedBinaries:  pol.AllowedBinaries,
		AllowedHosts:     pol.AllowedHosts,
		VaultSecrets:     vaultNames,
	}
}

// SaveConfig persists local resident UI preferences (provider, model, active workstream).
// Only invoked by local Wails UI bindings — never exposed via remote HTTP (§10, §11).
func (m *ResidentManager) SaveConfig(provider, modelName, workstream string) (resident.Status, error) {
	cfg, err := resident.LoadConfig(m.baseDir)
	if err != nil {
		return m.Status(), err
	}
	cfg.Enabled = true
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
	cfg, err := resident.LoadConfig(m.baseDir)
	if err != nil {
		return resident.TurnResult{}, err
	}
	pol, _ := resident.LoadPolicy(m.baseDir)
	if strings.TrimSpace(workstream) == "" {
		workstream = cfg.Workstream
	}
	workstream = resident.SanitizeWorkstream(workstream)
	acct, devID := m.currentAccount()
	mem := resident.NewMemoryStore(m.baseDir, acct)

	ctx, cancel := context.WithCancel(parentCtx)
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.cancel = cancel
	m.state = "pensando"
	m.lastError = ""
	m.mu.Unlock()

	defer func() {
		cancel()
		m.mu.Lock()
		m.cancel = nil
		m.state = "ocioso"
		m.mu.Unlock()
	}()

	approveDetailed := func(appCtx context.Context, req resident.ApprovalRequest) bool {
		if m.sessions == nil || m.sessions.Emit == nil {
			return false
		}
		m.mu.Lock()
		m.state = "aguardando você"
		m.mu.Unlock()
		label := fmt.Sprintf("Hive Residente · Risco: %s", req.RiskLabel)
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

	prov, mod := resident.ResolveModelRoute(cfg)
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
		OnEvent: func(ev resident.Event) {
			if ev.Kind == "usage" && (ev.Tokens > 0 || ev.InputTokens > 0 || ev.OutputTokens > 0) {
				modelx.RecordAIUsage(prov, mod, int64(ev.InputTokens), int64(ev.OutputTokens), int64(ev.Tokens))
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
						Kind:         ev.Kind,
						Stage:        "chief",
						Author:       "hive",
						Label:        ev.Label,
						Text:         ev.Text,
						ToolName:     ev.ToolName,
						Detail:       detail,
						ApprovalID:   ev.ApprovalID,
						Partial:      ev.Partial,
						Thought:      ev.Thought,
						InputTokens:  ev.InputTokens,
						OutputTokens: ev.OutputTokens,
						Tokens:       ev.Tokens,
					},
				})
			}
		},
	}

	res, runErr := runner.RunTurn(ctx, resident.TurnRequest{
		Message:    message,
		Workspace:  strings.TrimSpace(workspaceDir),
		Workstream: workstream,
		Origin:     origin,
		AccountID:  acct,
		DeviceID:   devID,
	})
	if runErr != nil {
		m.mu.Lock()
		m.lastError = resident.RedactSecrets(runErr.Error())
		m.mu.Unlock()
		return resident.TurnResult{}, runErr
	}
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
	return canceled
}

// SessionMessages returns recent local conversation turns for the current account & workstream.
func (m *ResidentManager) SessionMessages(workstream string) ([]resident.LocalTurnMessage, error) {
	return m.memoryForCurrentAccount().LoadSessionMessages(workstream, 40)
}

// ClearSession deletes the short-term conversation history for workstream.
func (m *ResidentManager) ClearSession(workstream string) error {
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

// FormatHeadlessStatus formats the one-line status printed by `hivemind serve` (§11).
func (m *ResidentManager) FormatHeadlessStatus() string {
	st := m.Status()
	sb := st.SandboxMechanism
	if !st.SandboxAvailable {
		sb = "indisponível (falha fechada)"
	}
	return fmt.Sprintf("Hive Residente (Fase %d · sandbox=%s): %s · conta=%s · modelo=%s/%s",
		st.Phase, sb, st.State, st.AccountID, st.Provider, st.Model)
}
