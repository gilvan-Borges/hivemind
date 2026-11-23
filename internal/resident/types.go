// Package resident implements Phase 1 of the Hive resident assistant
// (see docs/SPEC_HIVE_AGENT.md): local read-only chat, fail-closed policy
// broker, durable fsync audit trail, account-isolated personal memory, and
// untrusted-data fencing — all in pure Go without opening any network port.
package resident

import (
	"strings"
	"time"
)

// LaneID is the fixed lane identifier for the resident agent across the IDE
// and the existing Remote Bridge / SSE contract (§1.10, §10).
const LaneID = "resident"

// RunMode is the runMode value accepted on POST /api/v1/runs/start (§1.10).
const RunMode = "resident"

// Origin identifies where a resident turn or tool call came from (§4, §12.4).
// Always stamped by the host transport — never accepted from model arguments.
type Origin string

const (
	OriginLocal    Origin = "local"
	OriginRemote   Origin = "remote"
	OriginRoutine  Origin = "routine"
	OriginSubagent Origin = "subagent"
	OriginWebhook  Origin = "webhook"
	OriginSlack    Origin = "slack"
	OriginLoop     Origin = "loop"
)

// Tier classifies the privilege class of a tool call (§4, §12.4).
type Tier string

const (
	TierRead        Tier = "read"
	TierWrite       Tier = "write"
	TierExec        Tier = "exec"
	TierNetwork     Tier = "network"
	TierCredentials Tier = "credentials"
	TierDestructive Tier = "destructive"
)

// Call is a single structured tool invocation passed to the policy & broker (§4).
type Call struct {
	Name         string         `json:"name"`
	Args         map[string]any `json:"args,omitempty"`
	Origin       Origin         `json:"origin"`
	Workstream   string         `json:"workstream,omitempty"`
	ParentTier   Tier           `json:"parentTier,omitempty"`
	AllowedTiers []Tier         `json:"-"`
}

// SquadHandoff is the structured charge sent to the squad after approval.
// The host fills PermissionMode from team.yaml; the model cannot set it.
type SquadHandoff struct {
	Workspace      string
	Team           string
	Objective      string
	Facts          []string
	PermissionMode string
}

// Decision is the pure policy result for a Call (§4).
type Decision struct {
	Allow  bool   `json:"allow"`
	Ask    bool   `json:"ask"`
	Reason string `json:"reason,omitempty"`
}

// AuditEntry is one JSONL record written to audit.jsonl (§12.7).
// Decision lines are synced to disk BEFORE any tool execution; outcome lines
// share the same ApprovalID/CallID and are written AFTER execution.
type AuditEntry struct {
	Timestamp   string `json:"ts"`
	Phase       string `json:"phase"` // "decision" | "outcome"
	AccountID   string `json:"accountId"`
	DeviceID    string `json:"deviceId,omitempty"`
	Origin      Origin `json:"origin"`
	Workstream  string `json:"workstream,omitempty"`
	Tool        string `json:"tool"`
	Tier        Tier   `json:"tier"`
	Decision    string `json:"decision"` // "allow" | "deny" | "ask"
	ApprovalID  string `json:"approvalId,omitempty"`
	Sandbox     string `json:"sandbox,omitempty"` // "not_required" | "applied" | "unavailable"
	Target      string `json:"target,omitempty"`  // cru só até a gravação; o JSONL durável não leva caminho nem argv
	TargetClass string `json:"targetClass,omitempty"`
	TargetHash  string `json:"targetHash,omitempty"`
	Outcome     string `json:"outcome,omitempty"` // "ok" | "error" | "denied"
	ErrorClass  string `json:"errorClass,omitempty"`
	Reason      string `json:"reason,omitempty"`
	LoopID      string `json:"loopId,omitempty"`
	Iteration   int    `json:"iteration,omitempty"`
}

// Config is persisted in %LOCALAPPDATA%\HiveMind\resident\resident.json (mode 0600).
// It never stores secrets (§11).
type Config struct {
	Enabled       bool     `json:"enabled"`
	OrbDisabled   bool     `json:"orbDisabled,omitempty"`
	Provider      string   `json:"provider,omitempty"`
	Model         string   `json:"model,omitempty"`
	Workstream    string   `json:"workstream,omitempty"`
	Roots         []string `json:"roots,omitempty"`
	EventTriggers bool     `json:"eventTriggers,omitempty"`
	// ExperimentalUnisolated libera browser_open, desktop_click e desktop_type
	// na sessão real. O padrão é falso: sem o campo, a política continua recusando.
	ExperimentalUnisolated bool `json:"experimentalUnisolated,omitempty"`
	// IsolatedSandbox provisions browser_open inside an ephemeral Windows Sandbox (.wsb).
	IsolatedSandbox bool `json:"isolatedSandbox,omitempty"`
	// JevFailover (SPEC_JEV_ORQUESTRADOR / W2-10): failover opcional do
	// resident — quando ligado, uma rota que falha em construir aciona o
	// Score novamente excluindo o par que falhou, em vez de abortar o turno.
	JevFailover bool `json:"jevFailover,omitempty"`
	// MaxConcurrentSubagents limita spawns simultâneos (default 2).
	// Cada subagente consome tokens de API de forma independente: valores
	// altos multiplicam o custo linearmente.
	MaxConcurrentSubagents int `json:"maxConcurrentSubagents,omitempty"`
	// MaxSubagentSummaryChars cala o texto que um subagente devolve ao pai
	// (default 24.000). O excedente vira head+tail com o texto completo
	// persistido para recuperação via read_file.
	MaxSubagentSummaryChars int `json:"maxSubagentSummaryChars,omitempty"`
	// PermissionMode is "governed" (default) or "full". Only the IDE settings
	// write this. The model cannot.
	PermissionMode string `json:"permissionMode,omitempty"`
	// AllowFullUnattended lets a cron or event loop run in Full.
	// Only the IDE settings screen writes it. Default is false.
	AllowFullUnattended bool `json:"allowFullUnattended,omitempty"`
}

// IsFullAccess reports the explicit Full permission toggle. Default is off.
func (c Config) IsFullAccess() bool {
	switch strings.ToLower(strings.TrimSpace(c.PermissionMode)) {
	case "full", "full_access":
		return true
	default:
		return false
	}
}

// NormalizeResidentPermission accepts the Full toggle and anything else as governed.
func NormalizeResidentPermission(raw string) string {
	if (Config{PermissionMode: raw}).IsFullAccess() {
		return "full"
	}
	return "governed"
}

// EffectiveMaxConcurrentSubagents returns the spawn concurrency cap (default 2).
func (c Config) EffectiveMaxConcurrentSubagents() int {
	if c.MaxConcurrentSubagents > 0 {
		return c.MaxConcurrentSubagents
	}
	return 2
}

// EffectiveMaxSubagentSummaryChars returns the per-child summary cap in chars
// (default 24.000; floor 2.000 para nunca esmagar um relatório útil).
func (c Config) EffectiveMaxSubagentSummaryChars() int {
	if c.MaxSubagentSummaryChars >= 2_000 {
		return c.MaxSubagentSummaryChars
	}
	return 24_000
}

// MemoryFact is one personal long-term memory record stored in
// %LOCALAPPDATA%\HiveMind\resident\<account>\memory.jsonl (mode 0600, §6).
type MemoryFact struct {
	ID         string    `json:"id"`
	Workstream string    `json:"workstream,omitempty"`
	Text       string    `json:"text"`
	Pinned     bool      `json:"pinned,omitempty"`
	Source     string    `json:"source,omitempty"` // "user" (never from tool/web/mcp)
	CreatedAt  time.Time `json:"createdAt"`
}

// ProjectRef is a read-only summary of a known workspace returned by list_projects.
type ProjectRef struct {
	Name   string `json:"name"`
	Root   string `json:"root"`
	Branch string `json:"branch,omitempty"`
	Stack  string `json:"stack,omitempty"`
	Active bool   `json:"active,omitempty"`
}

// Status reports the resident assistant's live state for the IDE UI (§11).
type Status struct {
	State                  string   `json:"state"` // "ocioso" | "pensando" | "aguardando você" | "desativado"
	Enabled                bool     `json:"enabled"`
	OrbEnabled             bool     `json:"orbEnabled"`
	JevFailover            bool     `json:"jevFailover"`
	ReadOnly               bool     `json:"readOnly"`
	AccountID              string   `json:"accountId"`
	Workstream             string   `json:"workstream"`
	Provider               string   `json:"provider"`
	Model                  string   `json:"model"`
	MemoryDir              string   `json:"memoryDir,omitempty"`
	AuditPath              string   `json:"auditPath,omitempty"`
	LastError              string   `json:"lastError,omitempty"`
	SandboxAvailable       bool     `json:"sandboxAvailable"`
	SandboxMechanism       string   `json:"sandboxMechanism,omitempty"`
	SandboxReason          string   `json:"sandboxReason,omitempty"`
	VaultAvailable         bool     `json:"vaultAvailable"`
	VaultReason            string   `json:"vaultReason,omitempty"`
	AllowedBinaries        []string `json:"allowedBinaries"`
	AllowedHosts           []string `json:"allowedHosts"`
	VaultSecrets           []string `json:"vaultSecrets"`
	EventTriggers          bool     `json:"eventTriggers"`
	ExperimentalUnisolated bool     `json:"experimentalUnisolated"`
	IsolatedSandbox        bool     `json:"isolatedSandbox"`
	ExperimentalWarning    string   `json:"experimentalWarning,omitempty"`
	IsolatedBrowser        bool     `json:"isolatedBrowser"`
	IsolatedBrowserReason  string   `json:"isolatedBrowserReason,omitempty"`
	IsolatedDesktop        bool     `json:"isolatedDesktop"`
	IsolatedDesktopReason  string   `json:"isolatedDesktopReason,omitempty"`
	ConversationID         string   `json:"conversationId,omitempty"`
	SharedSessionID        string   `json:"sharedSessionId,omitempty"`
	LaneID                 string   `json:"laneId,omitempty"`
	PermissionMode         string   `json:"permissionMode,omitempty"`
	FullAccess             bool     `json:"fullAccess,omitempty"`
	AllowFullUnattended    bool     `json:"allowFullUnattended,omitempty"`
}

// Policy defines the persistent fail-closed Phase 2 security allowlists configured locally.
// Empty allowlists deny execution and outbound network by default; wildcard "*" is always ignored/rejected.
type Policy struct {
	Version         int       `json:"version"`
	AllowedBinaries []string  `json:"allowedBinaries"`
	AllowedHosts    []string  `json:"allowedHosts"`
	UpdatedAt       time.Time `json:"updatedAt,omitempty"`
}

// ApprovalRequest describes an action awaiting human approval in the IDE or app.
type ApprovalRequest struct {
	ApprovalID string `json:"approvalId"`
	ToolName   string `json:"toolName"`
	Tier       Tier   `json:"tier"`
	RiskLabel  string `json:"riskLabel"`
	Origin     Origin `json:"origin"`
	Target     string `json:"target"`
	Prompt     string `json:"prompt"`
}

// Event is emitted during a resident turn (compatible with orchestrator.Event / SSE).
type Event struct {
	Kind           string `json:"kind"` // "stage" | "text" | "tool_call" | "tool_result" | "usage" | "approval_required" | "finish" | "error"
	Author         string `json:"author,omitempty"`
	Label          string `json:"label,omitempty"`
	Text           string `json:"text,omitempty"`
	ToolName       string `json:"toolName,omitempty"`
	Detail         string `json:"detail,omitempty"`
	Gate           string `json:"gate,omitempty"`
	RiskTier       string `json:"riskTier,omitempty"`
	Origin         string `json:"origin,omitempty"`
	ApprovalID     string `json:"approvalId,omitempty"`
	Partial        bool   `json:"partial,omitempty"`
	Thought        bool   `json:"thought,omitempty"`
	InputTokens    int32  `json:"inputTokens,omitempty"`
	OutputTokens   int32  `json:"outputTokens,omitempty"`
	Tokens         int32  `json:"tokens,omitempty"`
	MessageID      string `json:"messageId,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`
	SessionID      string `json:"sessionId,omitempty"`
	LoopID         string `json:"loopId,omitempty"`
	Iteration      int    `json:"iteration,omitempty"`
}

// TurnRequest holds one user turn for the resident agent.
type TurnRequest struct {
	Message          string
	Workspace        string
	Workstream       string
	Origin           Origin
	AccountID        string
	DeviceID         string
	Soul             string
	ConversationID   string
	SharedSessionID  string
	UserMessageID    string
	ReplyMessageID   string
	SharedHistory    []LocalTurnMessage
	SkipLocalHistory bool
	FullAccess       bool
	// Display is the short line shown in the transcript. Message is what the model reads.
	Display   string
	LoopID    string
	Iteration int
}

// TurnResult summarizes the completed turn.
type TurnResult struct {
	SessionID       string `json:"sessionId"`
	Workstream      string `json:"workstream"`
	FinalText       string `json:"finalText"`
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	InputTokens     int64  `json:"inputTokens"`
	OutputTokens    int64  `json:"outputTokens"`
	TotalTokens     int64  `json:"totalTokens"`
	ToolCalls       int    `json:"toolCalls"`
	PromptBuildMs   int64  `json:"promptBuildMs"`
	ConversationID  string `json:"conversationId,omitempty"`
	SharedSessionID string `json:"sharedSessionId,omitempty"`
	UserMessageID   string `json:"userMessageId,omitempty"`
	ReplyMessageID  string `json:"replyMessageId,omitempty"`
}
