// Package resident implements Phase 1 of the Hive resident assistant
// (see docs/SPEC_HIVE_AGENT.md): local read-only chat, fail-closed policy
// broker, durable fsync audit trail, account-isolated personal memory, and
// untrusted-data fencing — all in pure Go without opening any network port.
package resident

import "time"

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
	Name       string         `json:"name"`
	Args       map[string]any `json:"args,omitempty"`
	Origin     Origin         `json:"origin"`
	Workstream string         `json:"workstream,omitempty"`
	ParentTier Tier           `json:"parentTier,omitempty"`
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
	Timestamp  string `json:"ts"`
	Phase      string `json:"phase"` // "decision" | "outcome"
	AccountID  string `json:"accountId"`
	DeviceID   string `json:"deviceId,omitempty"`
	Origin     Origin `json:"origin"`
	Workstream string `json:"workstream,omitempty"`
	Tool       string `json:"tool"`
	Tier       Tier   `json:"tier"`
	Decision   string `json:"decision"` // "allow" | "deny" | "ask"
	ApprovalID string `json:"approvalId,omitempty"`
	Sandbox    string `json:"sandbox,omitempty"` // "not_required" | "applied" | "unavailable"
	Target     string `json:"target,omitempty"`
	Outcome    string `json:"outcome,omitempty"` // "ok" | "error" | "denied"
	ErrorClass string `json:"errorClass,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// Config is persisted in %LOCALAPPDATA%\HiveMind\resident\resident.json (mode 0600).
// It never stores secrets (§11).
type Config struct {
	Enabled    bool     `json:"enabled"`
	Provider   string   `json:"provider,omitempty"`
	Model      string   `json:"model,omitempty"`
	Workstream string   `json:"workstream,omitempty"`
	Roots      []string `json:"roots,omitempty"`
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
	State            string   `json:"state"` // "ocioso" | "pensando" | "aguardando você"
	Phase            int      `json:"phase"`
	ReadOnly         bool     `json:"readOnly"`
	AccountID        string   `json:"accountId"`
	Workstream       string   `json:"workstream"`
	Provider         string   `json:"provider"`
	Model            string   `json:"model"`
	MemoryDir        string   `json:"memoryDir,omitempty"`
	AuditPath        string   `json:"auditPath,omitempty"`
	LastError        string   `json:"lastError,omitempty"`
	SandboxAvailable bool     `json:"sandboxAvailable"`
	SandboxMechanism string   `json:"sandboxMechanism,omitempty"`
	SandboxReason    string   `json:"sandboxReason,omitempty"`
	VaultAvailable   bool     `json:"vaultAvailable"`
	VaultReason      string   `json:"vaultReason,omitempty"`
	AllowedBinaries  []string `json:"allowedBinaries"`
	AllowedHosts     []string `json:"allowedHosts"`
	VaultSecrets     []string `json:"vaultSecrets"`
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
	Kind         string `json:"kind"` // "stage" | "text" | "tool_call" | "tool_result" | "usage" | "approval_required" | "finish" | "error"
	Author       string `json:"author,omitempty"`
	Label        string `json:"label,omitempty"`
	Text         string `json:"text,omitempty"`
	ToolName     string `json:"toolName,omitempty"`
	Detail       string `json:"detail,omitempty"`
	Gate         string `json:"gate,omitempty"`
	RiskTier     string `json:"riskTier,omitempty"`
	Origin       string `json:"origin,omitempty"`
	ApprovalID   string `json:"approvalId,omitempty"`
	Partial      bool   `json:"partial,omitempty"`
	Thought      bool   `json:"thought,omitempty"`
	InputTokens  int32  `json:"inputTokens,omitempty"`
	OutputTokens int32  `json:"outputTokens,omitempty"`
	Tokens       int32  `json:"tokens,omitempty"`
}

// TurnRequest holds one user turn for the resident agent.
type TurnRequest struct {
	Message    string
	Workspace  string
	Workstream string
	Origin     Origin
	AccountID  string
	DeviceID   string
}

// TurnResult summarizes the completed turn.
type TurnResult struct {
	SessionID    string `json:"sessionId"`
	Workstream   string `json:"workstream"`
	FinalText    string `json:"finalText"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	TotalTokens  int64  `json:"totalTokens"`
	ToolCalls    int    `json:"toolCalls"`
	PromptBuildMs int64 `json:"promptBuildMs"`
}
