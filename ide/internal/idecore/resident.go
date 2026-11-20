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
)

// ResidentManager owns the Phase 1 read-only Hive resident assistant inside the
// IDE and headless `hivemind serve` host (see docs/SPEC_HIVE_AGENT.md §3, §4).
type ResidentManager struct {
	baseDir    string
	sessions   *SessionManager
	accountFn  func() (accountID, deviceID string)
	projectsFn func() []resident.ProjectRef
	audit      *resident.FileAuditLogger

	mu        sync.Mutex
	state     string // "ocioso" | "pensando" | "aguardando você"
	lastError string
	cancel    context.CancelFunc

	// testModel optionally overrides LLM creation in unit tests.
	testModel model.LLM
}

// ResidentManagerOptions configures ResidentManager.
type ResidentManagerOptions struct {
	BaseDir    string
	Sessions   *SessionManager
	AccountFn  func() (accountID, deviceID string)
	ProjectsFn func() []resident.ProjectRef
	TestModel  model.LLM
}

// NewResidentManager initializes the resident service without spawning any
// subprocesses, MCP servers, or background listeners (§13).
func NewResidentManager(opts ResidentManagerOptions) *ResidentManager {
	baseDir := strings.TrimSpace(opts.BaseDir)
	if baseDir == "" {
		baseDir = resident.DefaultBaseDir()
	}
	return &ResidentManager{
		baseDir:    baseDir,
		sessions:   opts.Sessions,
		accountFn:  opts.AccountFn,
		projectsFn: opts.ProjectsFn,
		audit:      resident.NewFileAuditLogger(filepath.Join(baseDir, "audit.jsonl")),
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
	prov, mod := resident.ResolveModelRoute(cfg)
	acct, _ := m.currentAccount()
	mem := resident.NewMemoryStore(m.baseDir, acct)

	m.mu.Lock()
	st := m.state
	lastErr := m.lastError
	m.mu.Unlock()
	if st == "" {
		st = "ocioso"
	}

	return resident.Status{
		State:      st,
		Phase:      1,
		ReadOnly:   true,
		AccountID:  acct,
		Workstream: resident.SanitizeWorkstream(cfg.Workstream),
		Provider:   prov,
		Model:      mod,
		MemoryDir:  mem.AccountDir(),
		AuditPath:  m.audit.Path(),
		LastError:  lastErr,
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

// RunTurn executes one read-only resident turn and streams events through the
// shared SessionManager emitter on fixed lane "resident" (Wails + Remote Bridge SSE).
func (m *ResidentManager) RunTurn(
	parentCtx context.Context,
	workspaceDir, workstream, message string,
	origin resident.Origin,
) (resident.TurnResult, error) {
	cfg, err := resident.LoadConfig(m.baseDir)
	if err != nil {
		return resident.TurnResult{}, err
	}
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

	approveFn := func(appCtx context.Context, _ string, prompt string) bool {
		if m.sessions == nil || m.sessions.Emit == nil {
			return false
		}
		m.mu.Lock()
		m.state = "aguardando você"
		m.mu.Unlock()
		ok := m.sessions.RequestApproval(appCtx, resident.LaneID, "hive-resident", prompt)
		m.mu.Lock()
		m.state = "pensando"
		m.mu.Unlock()
		return ok
	}

	prov, mod := resident.ResolveModelRoute(cfg)
	runner := &resident.Runner{
		BaseDir:      m.baseDir,
		Config:       cfg,
		Audit:        m.audit,
		Memory:       mem,
		ListProjects: m.knownProjects,
		Approve:      approveFn,
		Model:        m.testModel,
		OnEvent: func(ev resident.Event) {
			if ev.Kind == "usage" && (ev.Tokens > 0 || ev.InputTokens > 0 || ev.OutputTokens > 0) {
				modelx.RecordAIUsage(prov, mod, int64(ev.InputTokens), int64(ev.OutputTokens), int64(ev.Tokens))
			}
			if m.sessions != nil && m.sessions.Emit != nil {
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
						Detail:       ev.Detail,
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
	return fmt.Sprintf("Hive Residente (Fase %d · somente leitura): %s · conta=%s · modelo=%s/%s",
		st.Phase, st.State, st.AccountID, st.Provider, st.Model)
}
