package resident

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/sandboxx"
	"github.com/gilvan-Borges/HiveMind/internal/secretx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/tools"
)

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

func (b *Broker) handleBrowserOpen(ctx context.Context, args map[string]any) (string, error) {
	if err := b.refuseIfSurfaceOff(); err != nil {
		return "", err
	}
	log.Printf("residente: browser_open SEM isolamento (experimentalUnisolated); o cofre não é consultado")
	target, err := validatePublicURL(strArg(args, "url"))
	if err != nil {
		return "", err
	}
	if err := b.surface().OpenURL(ctx, target); err != nil {
		return "", err
	}
	return "aberto SEM isolamento", nil
}

func (b *Broker) handleDesktopClick(ctx context.Context, args map[string]any) (string, error) {
	if err := b.refuseIfSurfaceOff(); err != nil {
		return "", err
	}
	log.Printf("residente: desktop_click SEM isolamento (experimentalUnisolated); o cofre não é consultado")
	x, y := intArg(args, "x"), intArg(args, "y")
	if err := b.surface().Click(ctx, x, y); err != nil {
		return "", err
	}
	return "clique SEM isolamento", nil
}

func (b *Broker) handleDesktopType(ctx context.Context, args map[string]any) (string, error) {
	if err := b.refuseIfSurfaceOff(); err != nil {
		return "", err
	}
	log.Printf("residente: desktop_type SEM isolamento (experimentalUnisolated); o cofre não é consultado")
	text := strArg(args, "text")
	if err := b.surface().TypeText(ctx, text); err != nil {
		return "", err
	}
	return "digitação SEM isolamento", nil
}

func (b *Broker) requireWorkspace() (string, error) {
	ws := strings.TrimSpace(b.cfg.Workspace)
	if ws == "" {
		return "", fmt.Errorf("nenhum workspace aberto")
	}
	return ws, nil
}

func (b *Broker) handleReadFile(_ context.Context, args map[string]any) (string, error) {
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	rel := strArg(args, "path")
	if rel == "" {
		return "", fmt.Errorf("parâmetro path é obrigatório")
	}
	if IsSensitivePath(rel) {
		return "", fmt.Errorf("caminho sensível recusado: %s", RedactTarget(rel))
	}
	return tools.ReadFileReadOnly(ws, rel, intArg(args, "start_line"), intArg(args, "end_line"))
}

func (b *Broker) handleListDir(_ context.Context, args map[string]any) (string, error) {
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	rel := strArg(args, "path")
	if IsSensitivePath(rel) {
		return "", fmt.Errorf("caminho sensível recusado: %s", RedactTarget(rel))
	}
	entries, err := tools.ListDirReadOnly(ws, rel)
	if err != nil {
		return "", err
	}
	filtered := make([]string, 0, len(entries))
	for _, e := range entries {
		if IsSensitivePath(e) {
			continue
		}
		filtered = append(filtered, e)
	}
	if len(filtered) == 0 {
		return "(diretório vazio)", nil
	}
	return strings.Join(filtered, "\n"), nil
}

func (b *Broker) handleSearchText(ctx context.Context, args map[string]any) (string, error) {
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	sub := strArg(args, "path")
	if IsSensitivePath(sub) {
		return "", fmt.Errorf("caminho sensível recusado: %s", RedactTarget(sub))
	}
	return tools.SearchTextReadOnly(
		ctx,
		ws,
		strArg(args, "query"),
		sub,
		strArg(args, "glob"),
		boolArg(args, "regex"),
		boolArg(args, "case_sensitive"),
		intArg(args, "max_results"),
	)
}

func (b *Broker) handleFindFiles(ctx context.Context, args map[string]any) (string, error) {
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	sub := strArg(args, "path")
	if IsSensitivePath(sub) {
		return "", fmt.Errorf("caminho sensível recusado: %s", RedactTarget(sub))
	}
	return tools.FindFilesReadOnly(ctx, ws, strArg(args, "pattern"), sub, intArg(args, "max_results"))
}

func (b *Broker) handleListProjects(_ context.Context, _ map[string]any) (string, error) {
	if b.cfg.ListProjects == nil {
		return "[]", nil
	}
	refs := b.cfg.ListProjects()
	raw, err := json.MarshalIndent(refs, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (b *Broker) handleMemorySearch(_ context.Context, args map[string]any) (string, error) {
	if b.cfg.Memory == nil {
		return "Memória pessoal indisponível.", nil
	}
	ws := strArg(args, "workstream")
	if ws == "" {
		ws = b.cfg.Workstream
	}
	query := strArg(args, "query")
	res, err := b.cfg.Memory.Retrieve(ws, query)
	if err != nil {
		return "", err
	}
	if len(res.Pinned) == 0 && len(res.Matches) == 0 {
		return "Nenhum fato encontrado na memória pessoal desta conta.", nil
	}
	var sb strings.Builder
	if len(res.Pinned) > 0 {
		sb.WriteString("# Fatos fixados\n")
		for _, f := range res.Pinned {
			fmt.Fprintf(&sb, "- [%s · id=%s] %s\n", f.Workstream, f.ID, f.Text)
		}
	}
	if len(res.Matches) > 0 {
		sb.WriteString("# Fatos relevantes\n")
		for _, f := range res.Matches {
			fmt.Fprintf(&sb, "- [%s · id=%s] %s\n", f.Workstream, f.ID, f.Text)
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

func (b *Broker) handleWriteFile(_ context.Context, args map[string]any) (string, error) {
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	rel := strArg(args, "path")
	if rel == "" {
		return "", errors.New("parâmetro path é obrigatório")
	}
	if IsSensitivePath(rel) {
		return "", fmt.Errorf("caminho sensível recusado: %s", RedactTarget(rel))
	}
	content, _ := args["content"].(string)
	return tools.WriteFileWorkspace(ws, rel, content)
}

func (b *Broker) handleEditFile(_ context.Context, args map[string]any) (string, error) {
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	rel := strArg(args, "path")
	if rel == "" {
		return "", errors.New("parâmetro path é obrigatório")
	}
	if IsSensitivePath(rel) {
		return "", fmt.Errorf("caminho sensível recusado: %s", RedactTarget(rel))
	}
	oldText, _ := args["old_text"].(string)
	newText, _ := args["new_text"].(string)
	return tools.EditFileWorkspace(ws, rel, oldText, newText, boolArg(args, "replace_all"))
}

func (b *Broker) handleDeleteFile(_ context.Context, args map[string]any) (string, error) {
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	rel := strArg(args, "path")
	if rel == "" {
		return "", errors.New("parâmetro path é obrigatório")
	}
	if IsSensitivePath(rel) {
		return "", fmt.Errorf("remoção de caminho sensível recusada: %s", RedactTarget(rel))
	}
	return tools.DeleteFileWorkspace(ws, rel)
}

func (b *Broker) handleMemoryWrite(_ context.Context, args map[string]any) (string, error) {
	if b.cfg.Memory == nil {
		return "", errors.New("memória pessoal indisponível")
	}
	ws := strArg(args, "workstream")
	if ws == "" {
		ws = b.cfg.Workstream
	}
	text := strArg(args, "text")
	if text == "" {
		return "", errors.New("parâmetro text é obrigatório")
	}
	fact, err := b.cfg.Memory.AddFact(ws, text, boolArg(args, "pin"))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("fato salvo na memória pessoal (id=%s · workstream=%s)", fact.ID, fact.Workstream), nil
}

func (b *Broker) handleMemoryForget(_ context.Context, args map[string]any) (string, error) {
	if b.cfg.Memory == nil {
		return "", errors.New("memória pessoal indisponível")
	}
	id := firstNonEmptyStr(strArg(args, "id"), strArg(args, "key"))
	if id == "" {
		return "", errors.New("parâmetro key/id é obrigatório")
	}
	removed, err := b.cfg.Memory.DeleteFact(id)
	if err != nil {
		return "", err
	}
	if !removed {
		return fmt.Sprintf("nenhum fato encontrado com id=%s", id), nil
	}
	return fmt.Sprintf("fato %s removido da memória pessoal", id), nil
}

func (b *Broker) handleExecCommand(ctx context.Context, args map[string]any) (string, error) {
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(ws); statErr != nil || !info.IsDir() {
		return "", fmt.Errorf("workspace inválido para sandbox: %w", statErr)
	}
	argv := strSliceArg(args, "argv")
	if err := ValidateExecCommand(argv, b.cfg.Policy.AllowedBinaries); err != nil {
		return "", err
	}
	timeout := sandboxx.DefaultTimeout
	if sec := intArg(args, "timeout_sec"); sec > 0 {
		if sec > 120 {
			sec = 120
		}
		timeout = time.Duration(sec) * time.Second
	}

	res, err := b.cfg.Sandbox.Run(ctx, sandboxx.Options{
		Argv:         argv,
		WorkDir:      ws,
		ReadRoots:    []string{ws},
		WriteRoots:   []string{ws},
		AllowNetwork: false,
		Timeout:      timeout,
	})
	var sb strings.Builder
	fmt.Fprintf(&sb, "sandbox=%s exit=%d duration=%dms\n", res.Mechanism, res.ExitCode, res.Duration.Milliseconds())
	if strings.TrimSpace(res.Stdout) != "" {
		fmt.Fprintf(&sb, "[stdout]\n%s\n", res.Stdout)
	}
	if strings.TrimSpace(res.Stderr) != "" {
		fmt.Fprintf(&sb, "[stderr]\n%s\n", res.Stderr)
	}
	if err != nil {
		out := strings.TrimSpace(sb.String())
		if out != "" {
			return out, fmt.Errorf("%v\n%s", err, strx.TruncateBytes(out, 2048, "…"))
		}
		return "", err
	}
	return strings.TrimSpace(sb.String()), nil
}

func (b *Broker) handleNetFetch(ctx context.Context, args map[string]any) (string, error) {
	rawURL := strArg(args, "url")
	u, err := ValidateOutboundURL(rawURL, b.cfg.Policy.AllowedHosts)
	if err != nil {
		return "", err
	}
	method := strings.ToUpper(strArg(args, "method"))
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodHead {
		return "", fmt.Errorf("método HTTP %q não permitido (apenas GET/HEAD)", method)
	}

	client := b.cfg.HTTPClient
	if client == nil {
		client = NewGuardedHTTPClient(b.cfg.Policy.AllowedHosts, 15*time.Second)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return "", err
	}
	if bearer, ok := args["_resolved_bearer"].(string); ok && bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("User-Agent", "HiveMind-Resident/2.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	const maxBodyBytes = 64 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return "", err
	}
	truncated := len(body) > maxBodyBytes
	if truncated {
		body = body[:maxBodyBytes]
	}
	out := fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(body))
	if truncated {
		out += "\n… [resposta truncada]"
	}
	return out, nil
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

func (b *Broker) handleCronAdd(_ context.Context, args map[string]any) (string, error) {
	base := strings.TrimSpace(b.cfg.BaseDir)
	if base == "" {
		return "", fmt.Errorf("diretório do residente ausente")
	}
	rt, err := SaveRoutine(base, Routine{
		Expr:       strArg(args, "expr"),
		Timezone:   strArg(args, "timezone"),
		Text:       strArg(args, "text"),
		Workstream: firstNonEmptyStr(strArg(args, "workstream"), b.cfg.Workstream),
		Classes:    strSliceArg(args, "classes"),
		Enabled:    true,
	})
	if err != nil {
		return "", err
	}
	if b.cfg.OnRoutineSaved != nil {
		b.cfg.OnRoutineSaved()
	}
	return "rotina gravada: " + rt.ID, nil
}

func (b *Broker) handleSpawn(ctx context.Context, args map[string]any) (string, error) {
	if b.cfg.Spawn == nil {
		return "", fmt.Errorf("subagente indisponível")
	}
	out, err := b.cfg.Spawn(ctx, strArg(args, "objective"), strSliceArg(args, "roots"), strSliceArg(args, "classes"))
	if err != nil {
		return "", err
	}
	return out, nil
}

func (b *Broker) handleHandoff(ctx context.Context, args map[string]any) (string, error) {
	if b.cfg.SquadHandoff == nil {
		return "", fmt.Errorf("handoff para o time indisponível")
	}
	mode := ""
	if b.cfg.PermissionMode != nil {
		mode = b.cfg.PermissionMode(b.cfg.Workspace)
	}
	facts := strSliceArg(args, "facts")
	if len(facts) == 0 {
		if s, ok := args["facts"].(string); ok && strings.TrimSpace(s) != "" {
			facts = []string{strings.TrimSpace(s)}
		}
	}
	hash, err := b.cfg.SquadHandoff(ctx, SquadHandoff{
		Workspace:      b.cfg.Workspace,
		Team:           strArg(args, "team"),
		Objective:      strArg(args, "objective"),
		Facts:          facts,
		PermissionMode: mode,
	})
	if err != nil {
		return "", err
	}
	return "handoff aceito: " + hash, nil
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
