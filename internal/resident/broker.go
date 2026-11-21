package resident

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/sandboxx"
	"github.com/gilvan-Borges/HiveMind/internal/secretx"
)

// Broker core: transversal middleware (tier authorization, human approval,
// durable pre-effect audit, secret redaction, untrusted-data fencing). The
// concrete per-domain handlers live in handlers_fs.go, handlers_memory.go,
// handlers_net.go, handlers_surface.go, handlers_cron.go and
// handlers_subagent.go.

const defaultApprovalTimeout = 5 * time.Minute

// ApprovalFunc blocks until a human approves or rejects a gated tool call.
// Returning false or having ApprovalFunc == nil ALWAYS denies (fail closed, §4).
type ApprovalFunc func(ctx context.Context, approvalID, prompt string) bool

// ApprovalDetailFunc blocks until a human approves or rejects a structured request.
type ApprovalDetailFunc func(ctx context.Context, req ApprovalRequest) bool

// ToolHandler executes a validated call inside the broker.
type ToolHandler func(ctx context.Context, args map[string]any) (string, error)

// BrokerConfig configures a single-turn Phase 2 resident tool broker.
type BrokerConfig struct {
	Workspace       string
	Workstream      string
	Origin          Origin
	ParentTier      Tier
	AccountID       string
	DeviceID        string
	Audit           AuditWriter
	Memory          *MemoryStore
	Policy          Policy
	Sandbox         sandboxx.Runner
	Vault           secretx.Vault
	HTTPClient      *http.Client
	ApprovalTimeout time.Duration
	ListProjects    func() []ProjectRef
	Approve         ApprovalFunc
	ApproveDetailed ApprovalDetailFunc
	OnToolEvent     func(ev Event)
	// Phase3 enables cron_add, spawn_subagent and delegate_to_hive_team.
	Phase3         bool
	AllowedTiers   []Tier
	PermissionMode func(workspace string) string
	Spawn          func(ctx context.Context, objective string, roots, classes []string) (string, error)
	SquadHandoff   func(ctx context.Context, h SquadHandoff) (string, error)
	OnRoutineSaved func()
	BaseDir        string
	// ExperimentalUnisolated lets browser and desktop run on the real session.
	// Default is false. The vault is never read on that path.
	ExperimentalUnisolated bool
	// Surface is the host browser/desktop. Nil uses the OS implementation.
	Surface Surface
	// CustomHandlers optionally overrides built-in handlers (used in tests
	// to observe exact invocation order relative to Audit.RecordDecision).
	CustomHandlers map[string]ToolHandler
}

// Broker is the sole gateway through which every resident tool call passes (§4).
// It implements tools.ResidentBroker.
type Broker struct {
	cfg      BrokerConfig
	mu       sync.Mutex
	calls    int
	observed []Call
}

// NewBroker constructs a fail-closed Phase 2 broker.
func NewBroker(cfg BrokerConfig) *Broker {
	if cfg.AccountID == "" {
		cfg.AccountID = DefaultAccountID
	}
	if cfg.Workstream == "" {
		cfg.Workstream = DefaultWorkstream
	}
	if cfg.Sandbox == nil {
		cfg.Sandbox = sandboxx.NewOSRunner()
	}
	if cfg.Vault == nil {
		cfg.Vault = secretx.NewOSVault()
	}
	if cfg.ApprovalTimeout <= 0 {
		cfg.ApprovalTimeout = defaultApprovalTimeout
	}
	return &Broker{cfg: cfg}
}

// CallCount returns the total number of calls that reached the broker.
func (b *Broker) CallCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

// ObservedCalls returns a copy of all calls submitted to the broker (for tests).
func (b *Broker) ObservedCalls() []Call {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Call, len(b.observed))
	copy(out, b.observed)
	return out
}

// Execute enforces policy, mandatory OS sandbox availability, human approval with
// timeout denial, durable pre-effect audit fsync, execution, secret redaction,
// untrusted-data fencing, and outcome auditing (§5, §12).
func (b *Broker) Execute(ctx context.Context, name string, rawArgs map[string]any) (string, error) {
	cleanArgs := SanitizeArgs(rawArgs)
	call := Call{
		Name:         strings.TrimSpace(name),
		Args:         cleanArgs,
		Origin:       b.cfg.Origin,
		Workstream:   b.cfg.Workstream,
		ParentTier:   b.cfg.ParentTier,
		AllowedTiers: b.cfg.AllowedTiers,
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	b.mu.Lock()
	b.calls++
	b.observed = append(b.observed, call)
	b.mu.Unlock()

	toolName := strings.ToLower(call.Name)
	tier, _ := ClassifyTool(call)
	target := extractTarget(toolName, cleanArgs)
	callID := newShortID("res")
	sandboxState := sandboxRequirement(toolName)

	dec := DecideWithPolicy(call, b.cfg.Policy)
	if isHostSurfaceTool(toolName) && b.cfg.ExperimentalUnisolated {
		sandboxState = "unisolated"
		dec = decideExperimentalSurface(call, b.fullAccess())
	}

	// 1. Fail closed if denied by policy.
	if !dec.Allow && !dec.Ask {
		b.recordDenyBestEffort(callID, call, tier, target, sandboxState, "policy_denied", dec.Reason)
		return "", fmt.Errorf("recusado pela política do residente: %s", dec.Reason)
	}

	// 2. Fail closed if OS sandbox is mandatory for this action and cannot be applied (§12.3).
	if requiresOSSandbox(toolName) {
		if b.cfg.Sandbox == nil {
			err := fmt.Errorf("%w: runner de sandbox ausente", sandboxx.ErrSandboxUnavailable)
			b.recordDenyBestEffort(callID, call, tier, target, "unavailable", "sandbox_unavailable", err.Error())
			return "", err
		}
		probe := b.cfg.Sandbox.Probe()
		if !probe.Available {
			reason := strings.TrimSpace(probe.Reason)
			if reason == "" {
				reason = "sandbox do SO não disponível neste ambiente"
			}
			err := fmt.Errorf("%w: %s", sandboxx.ErrSandboxUnavailable, reason)
			b.recordDenyBestEffort(callID, call, tier, target, "unavailable", "sandbox_unavailable", err.Error())
			return "", err
		}
		sandboxState = probe.Mechanism
	}

	// 3. Explicit human approval gate (remote/routine require approval on EVERY tool;
	// write/exec/network/credentials/destructive ALWAYS require approval; timeout denies).
	if dec.Ask {
		if b.cfg.ApproveDetailed == nil && b.cfg.Approve == nil {
			reason := fmt.Sprintf("aprovação humana indisponível (%s · %s)", call.Origin, tier)
			b.recordDenyBestEffort(callID, call, tier, target, sandboxState, "approval_unavailable", reason)
			return "", fmt.Errorf("recusado: %s", reason)
		}
		riskLabel := RiskLabel(tier)
		safeTarget := RedactTarget(target)
		extra := ""
		if toolName == "delegate_to_hive_team" && b.cfg.PermissionMode != nil {
			mode := strings.TrimSpace(b.cfg.PermissionMode(b.cfg.Workspace))
			if mode == "" {
				mode = "indisponível"
			}
			extra = "\npermission_mode do time: " + mode
		}
		if isHostSurfaceTool(toolName) {
			extra += "\nAVISO: SEM isolamento. A ação ocorre na sessão real e o cofre não é lido."
		}
		prompt := fmt.Sprintf("Hive Residente · Risco: %s\nOrigem: %s · Classe: %s\nFerramenta: %s\nAlvo: %s%s\n\nPermitir esta ação?",
			riskLabel, call.Origin, tier, toolName, safeTarget, extra)
		req := ApprovalRequest{
			ApprovalID: callID,
			ToolName:   toolName,
			Tier:       tier,
			RiskLabel:  riskLabel,
			Origin:     call.Origin,
			Target:     safeTarget,
			Prompt:     prompt,
		}
		apprCtx, apprCancel := context.WithTimeout(ctx, b.cfg.ApprovalTimeout)
		var approved bool
		if b.cfg.ApproveDetailed != nil {
			approved = b.cfg.ApproveDetailed(apprCtx, req)
		} else {
			approved = b.cfg.Approve(apprCtx, callID, prompt)
		}
		apprCancel()
		if !approved {
			reason := "aprovação humana negada ou expirada"
			b.recordDenyBestEffort(callID, call, tier, target, sandboxState, "approval_denied", reason)
			return "", fmt.Errorf("recusado: %s", reason)
		}
	}

	// 4. Mandatory pre-effect durable audit write (fsync BEFORE execution, §5.6, §12.7).
	if b.cfg.Audit == nil {
		return "", fmt.Errorf("falha fechada: gravador de auditoria não configurado")
	}
	decisionEntry := AuditEntry{
		AccountID:  b.cfg.AccountID,
		DeviceID:   b.cfg.DeviceID,
		Origin:     call.Origin,
		Workstream: call.Workstream,
		Tool:       toolName,
		Tier:       tier,
		Decision:   "allow",
		ApprovalID: callID,
		Sandbox:    sandboxState,
		Target:     target,
		Reason:     RedactSecrets(dec.Reason),
	}
	if err := b.cfg.Audit.RecordDecision(decisionEntry); err != nil {
		return "", fmt.Errorf("falha na gravação do log de auditoria (chamada abortada antes do efeito): %w", err)
	}

	b.emit(Event{
		Kind:     "tool_call",
		Author:   "hive",
		Label:    "Hive Residente",
		ToolName: toolName,
		RiskTier: string(tier),
		Origin:   string(call.Origin),
		Detail:   RedactTarget(target),
	})

	handler, ok := b.resolveHandler(toolName)
	if !ok {
		err := fmt.Errorf("ferramenta %q não registrada no broker", toolName)
		_ = b.cfg.Audit.RecordOutcome(AuditEntry{
			AccountID:  b.cfg.AccountID,
			DeviceID:   b.cfg.DeviceID,
			Origin:     call.Origin,
			Workstream: call.Workstream,
			Tool:       toolName,
			Tier:       tier,
			Decision:   "deny",
			ApprovalID: callID,
			Sandbox:    sandboxState,
			Target:     target,
			Outcome:    "denied",
			ErrorClass: "unregistered_tool",
			Reason:     err.Error(),
		})
		return "", err
	}

	var resolvedSecrets []string
	handlerArgs := cleanArgs
	if toolName == "net_fetch" {
		credName := firstNonEmptyStr(strArg(cleanArgs, "credential_name"), strArg(cleanArgs, "secret_name"))
		if credName != "" {
			if b.cfg.Vault == nil {
				err := fmt.Errorf("%w: cofre ausente", secretx.ErrVaultUnavailable)
				b.recordOutcomeError(callID, call, tier, target, sandboxState, "vault_unavailable", err.Error())
				return "", err
			}
			secVal, err := b.cfg.Vault.Get(credName)
			if err != nil {
				b.recordOutcomeError(callID, call, tier, target, sandboxState, "vault_error", err.Error())
				return "", err
			}
			resolvedSecrets = append(resolvedSecrets, secVal)
			// Inject secret strictly in-memory inside broker handler args for HTTP header use only.
			cloned := make(map[string]any, len(cleanArgs)+1)
			for k, v := range cleanArgs {
				cloned[k] = v
			}
			cloned["_resolved_bearer"] = secVal
			handlerArgs = cloned
		}
	}

	rawOut, execErr := handler(ctx, handlerArgs)
	if execErr != nil {
		safeErr := RedactSecretValues(execErr.Error(), resolvedSecrets...)
		b.recordOutcomeError(callID, call, tier, target, sandboxState, classifyError(execErr), safeErr)
		b.emit(Event{
			Kind:     "tool_result",
			Author:   "hive",
			Label:    "Hive Residente",
			ToolName: toolName,
			RiskTier: string(tier),
			Origin:   string(call.Origin),
			Detail:   "erro: " + RedactTarget(safeErr),
		})
		return "", fmt.Errorf("%s", safeErr)
	}

	scrubbedOut := RedactSecretValues(rawOut, resolvedSecrets...)
	fenced := WrapUntrustedData("tool", RedactTarget(target), scrubbedOut)
	_ = b.cfg.Audit.RecordOutcome(AuditEntry{
		AccountID:  b.cfg.AccountID,
		DeviceID:   b.cfg.DeviceID,
		Origin:     call.Origin,
		Workstream: call.Workstream,
		Tool:       toolName,
		Tier:       tier,
		Decision:   "allow",
		ApprovalID: callID,
		Sandbox:    sandboxState,
		Target:     target,
		Outcome:    "ok",
	})
	b.emit(Event{
		Kind:     "tool_result",
		Author:   "hive",
		Label:    "Hive Residente",
		ToolName: toolName,
		RiskTier: string(tier),
		Origin:   string(call.Origin),
		Detail:   RedactTarget(target),
	})
	return fenced, nil
}

func requiresOSSandbox(toolName string) bool {
	switch toolName {
	case "write_file", "edit_file", "delete_file", "exec_command":
		return true
	default:
		return false
	}
}

func sandboxRequirement(toolName string) string {
	if requiresOSSandbox(toolName) {
		return "required"
	}
	return "not_required"
}

func (b *Broker) recordDenyBestEffort(callID string, call Call, tier Tier, target, sandboxState, errClass, reason string) {
	if b.cfg.Audit == nil {
		return
	}
	_ = b.cfg.Audit.RecordDecision(AuditEntry{
		AccountID:  b.cfg.AccountID,
		DeviceID:   b.cfg.DeviceID,
		Origin:     call.Origin,
		Workstream: call.Workstream,
		Tool:       strings.ToLower(call.Name),
		Tier:       tier,
		Decision:   "deny",
		ApprovalID: callID,
		Sandbox:    sandboxState,
		Target:     target,
		Outcome:    "denied",
		ErrorClass: errClass,
		Reason:     RedactSecrets(reason),
	})
}

func (b *Broker) recordOutcomeError(callID string, call Call, tier Tier, target, sandboxState, errClass, safeErr string) {
	if b.cfg.Audit == nil {
		return
	}
	_ = b.cfg.Audit.RecordOutcome(AuditEntry{
		AccountID:  b.cfg.AccountID,
		DeviceID:   b.cfg.DeviceID,
		Origin:     call.Origin,
		Workstream: call.Workstream,
		Tool:       strings.ToLower(call.Name),
		Tier:       tier,
		Decision:   "allow",
		ApprovalID: callID,
		Sandbox:    sandboxState,
		Target:     target,
		Outcome:    "error",
		ErrorClass: errClass,
		Reason:     safeErr,
	})
}

func (b *Broker) emit(ev Event) {
	if b.cfg.OnToolEvent == nil {
		return
	}
	ev.Text = RedactSecrets(ev.Text)
	ev.Detail = RedactSecrets(ev.Detail)
	b.cfg.OnToolEvent(ev)
}

// resolveHandler maps a tool name to its per-domain handler (see handlers_*.go).
func (b *Broker) resolveHandler(name string) (ToolHandler, bool) {
	if b.cfg.CustomHandlers != nil {
		if h, ok := b.cfg.CustomHandlers[name]; ok {
			return h, true
		}
	}
	switch name {
	case "read_file":
		return b.handleReadFile, true
	case "list_dir":
		return b.handleListDir, true
	case "search_text":
		return b.handleSearchText, true
	case "find_files":
		return b.handleFindFiles, true
	case "list_projects":
		return b.handleListProjects, true
	case "memory_search":
		return b.handleMemorySearch, true
	case "write_file":
		return b.handleWriteFile, true
	case "edit_file":
		return b.handleEditFile, true
	case "delete_file":
		return b.handleDeleteFile, true
	case "memory_write":
		return b.handleMemoryWrite, true
	case "memory_forget":
		return b.handleMemoryForget, true
	case "exec_command":
		return b.handleExecCommand, true
	case "net_fetch":
		return b.handleNetFetch, true
	case "cron_add":
		if b.cfg.Phase3 {
			return b.handleCronAdd, true
		}
	case "spawn_subagent":
		if b.cfg.Phase3 {
			return b.handleSpawn, true
		}
	case "delegate_to_hive_team":
		if b.cfg.Phase3 {
			return b.handleHandoff, true
		}
	case "browser_open":
		return b.handleBrowserOpen, true
	case "desktop_click":
		return b.handleDesktopClick, true
	case "desktop_type":
		return b.handleDesktopType, true
	}
	return nil, false
}

func (b *Broker) fullAccess() bool {
	if b.cfg.PermissionMode == nil {
		return false
	}
	return isFullAccessMode(b.cfg.PermissionMode(b.cfg.Workspace))
}

func (b *Broker) surface() Surface {
	if b.cfg.Surface != nil {
		return b.cfg.Surface
	}
	return defaultSurface()
}

func (b *Broker) refuseIfSurfaceOff() error {
	if b.cfg.ExperimentalUnisolated {
		return nil
	}
	return fmt.Errorf("recusado: experimental_unisolated desligado")
}

func extractTarget(toolName string, args map[string]any) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read_file", "list_dir", "write_file", "edit_file", "delete_file":
		if p := strArg(args, "path"); p != "" {
			return p
		}
		return "."
	case "search_text":
		q := strArg(args, "query")
		if p := strArg(args, "path"); p != "" {
			return p + " :: " + q
		}
		return q
	case "find_files":
		return strArg(args, "pattern")
	case "memory_search":
		return strArg(args, "query")
	case "memory_write", "memory_forget":
		return firstNonEmptyStr(strArg(args, "key"), strArg(args, "id"), strArg(args, "text"))
	case "list_projects":
		return "workspaces"
	case "exec_command":
		if argv := strSliceArg(args, "argv"); len(argv) > 0 {
			return strings.Join(argv, " ")
		}
		return "exec_command"
	case "net_fetch":
		if u := strArg(args, "url"); u != "" {
			if cred := firstNonEmptyStr(strArg(args, "credential_name"), strArg(args, "secret_name")); cred != "" {
				return fmt.Sprintf("%s (credencial=%s)", u, cred)
			}
			return u
		}
		return "net_fetch"
	case "browser_open":
		if u := strArg(args, "url"); u != "" {
			return u
		}
		return "browser_open"
	case "desktop_click":
		return fmt.Sprintf("click %d,%d", intArg(args, "x"), intArg(args, "y"))
	case "desktop_type":
		return strArg(args, "text")
	default:
		if p := strArg(args, "path"); p != "" {
			return p
		}
		if c := strArg(args, "command"); c != "" {
			return c
		}
		if u := strArg(args, "url"); u != "" {
			return u
		}
		return toolName
	}
}

func classifyError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, sandboxx.ErrSandboxUnavailable) {
		return "sandbox_unavailable"
	}
	if errors.Is(err, secretx.ErrVaultUnavailable) || errors.Is(err, secretx.ErrSecretNotFound) {
		return "vault_error"
	}
	low := strings.ToLower(err.Error())
	switch {
	case strings.Contains(low, "fora do workspace"):
		return "path_escape"
	case strings.Contains(low, ".git"), strings.Contains(low, ".hivemind"), strings.Contains(low, "sensível"):
		return "forbidden_path"
	case strings.Contains(low, "not exist"), strings.Contains(low, "no such file"), strings.Contains(low, "cannot find"):
		return "not_found"
	case strings.Contains(low, "context canceled"), strings.Contains(low, "deadline exceeded"), strings.Contains(low, "tempo esgotado"):
		return "canceled"
	case strings.Contains(low, "ssrf"), strings.Contains(low, "allowlist"):
		return "policy_denied"
	default:
		return "io_error"
	}
}

func strArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func intArg(args map[string]any, key string) int {
	if args == nil {
		return 0
	}
	v, ok := args[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	default:
		return 0
	}
}

func boolArg(args map[string]any, key string) bool {
	if args == nil {
		return false
	}
	v, ok := args[key]
	if !ok || v == nil {
		return false
	}
	b, ok := v.(bool)
	if ok {
		return b
	}
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(v)), "true")
}
