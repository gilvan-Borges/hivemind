package resident

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/gilvan-Borges/HiveMind/internal/tools"
)

// ApprovalFunc blocks until a human approves or rejects a remote/routine tool call.
// Returning false or having ApprovalFunc == nil ALWAYS denies (fail closed, §4).
type ApprovalFunc func(ctx context.Context, approvalID, prompt string) bool

// ToolHandler executes a validated read-only call inside the broker.
type ToolHandler func(ctx context.Context, args map[string]any) (string, error)

// BrokerConfig configures a single-turn resident tool broker.
type BrokerConfig struct {
	Workspace    string
	Workstream   string
	Origin       Origin
	ParentTier   Tier
	AccountID    string
	DeviceID     string
	Audit        AuditWriter
	Memory       *MemoryStore
	ListProjects func() []ProjectRef
	Approve      ApprovalFunc
	OnToolEvent  func(ev Event)
	// CustomHandlers optionally overrides built-in read handlers (used in tests
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

// NewBroker constructs a fail-closed Phase 1 read-only broker.
func NewBroker(cfg BrokerConfig) *Broker {
	if cfg.AccountID == "" {
		cfg.AccountID = DefaultAccountID
	}
	if cfg.Workstream == "" {
		cfg.Workstream = DefaultWorkstream
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

// Execute enforces policy, approval, durable pre-effect audit fsync, execution,
// secret redaction, untrusted-data fencing, and outcome auditing (§5, §12).
func (b *Broker) Execute(ctx context.Context, name string, rawArgs map[string]any) (string, error) {
	cleanArgs := SanitizeArgs(rawArgs)
	call := Call{
		Name:       strings.TrimSpace(name),
		Args:       cleanArgs,
		Origin:     b.cfg.Origin,
		Workstream: b.cfg.Workstream,
		ParentTier: b.cfg.ParentTier,
	}

	b.mu.Lock()
	b.calls++
	b.observed = append(b.observed, call)
	b.mu.Unlock()

	tier, _ := ClassifyTool(call)
	target := extractTarget(call.Name, cleanArgs)
	callID := newShortID("res")

	dec := Decide(call)

	// Fail closed if denied by policy.
	if !dec.Allow && !dec.Ask {
		b.recordDenyBestEffort(callID, call, tier, target, dec.Reason)
		return "", fmt.Errorf("recusado pela política do residente: %s", dec.Reason)
	}

	// Remote / routine / subagent reads require explicit human approval (§10, §12.4).
	if dec.Ask {
		if b.cfg.Approve == nil {
			reason := "aprovação humana indisponível para origem " + string(call.Origin)
			b.recordDenyBestEffort(callID, call, tier, target, reason)
			return "", fmt.Errorf("recusado: %s", reason)
		}
		prompt := fmt.Sprintf("Hive Residente (origem: %s · classe: %s)\nFerramenta: %s\nAlvo: %s\n\nPermitir esta leitura?",
			call.Origin, tier, call.Name, RedactTarget(target))
		if !b.cfg.Approve(ctx, callID, prompt) {
			reason := "aprovação humana negada ou expirada"
			b.recordDenyBestEffort(callID, call, tier, target, reason)
			return "", fmt.Errorf("recusado: %s", reason)
		}
	}

	// Mandatory pre-effect durable audit write (fsync BEFORE execution, §5.6, §12.7).
	// Failure to persist the decision MUST abort the call so nothing executes.
	if b.cfg.Audit == nil {
		return "", fmt.Errorf("falha fechada: gravador de auditoria não configurado")
	}
	decisionEntry := AuditEntry{
		AccountID:  b.cfg.AccountID,
		DeviceID:   b.cfg.DeviceID,
		Origin:     call.Origin,
		Workstream: call.Workstream,
		Tool:       strings.ToLower(call.Name),
		Tier:       tier,
		Decision:   "allow",
		ApprovalID: callID,
		Sandbox:    "not_required",
		Target:     target,
		Reason:     dec.Reason,
	}
	if err := b.cfg.Audit.RecordDecision(decisionEntry); err != nil {
		return "", fmt.Errorf("falha na gravação do log de auditoria (chamada abortada antes do efeito): %w", err)
	}

	b.emit(Event{
		Kind:     "tool_call",
		Author:   "hive",
		Label:    "Hive Residente",
		ToolName: call.Name,
		Detail:   RedactTarget(target),
	})

	handler, ok := b.resolveHandler(strings.ToLower(call.Name))
	if !ok {
		err := fmt.Errorf("ferramenta %q não registrada no modo somente leitura", call.Name)
		_ = b.cfg.Audit.RecordOutcome(AuditEntry{
			AccountID:  b.cfg.AccountID,
			DeviceID:   b.cfg.DeviceID,
			Origin:     call.Origin,
			Workstream: call.Workstream,
			Tool:       strings.ToLower(call.Name),
			Tier:       tier,
			Decision:   "deny",
			ApprovalID: callID,
			Sandbox:    "not_required",
			Target:     target,
			Outcome:    "denied",
			ErrorClass: "unregistered_tool",
			Reason:     err.Error(),
		})
		return "", err
	}

	rawOut, execErr := handler(ctx, cleanArgs)
	if execErr != nil {
		safeErr := RedactSecrets(execErr.Error())
		_ = b.cfg.Audit.RecordOutcome(AuditEntry{
			AccountID:  b.cfg.AccountID,
			DeviceID:   b.cfg.DeviceID,
			Origin:     call.Origin,
			Workstream: call.Workstream,
			Tool:       strings.ToLower(call.Name),
			Tier:       tier,
			Decision:   "allow",
			ApprovalID: callID,
			Sandbox:    "not_required",
			Target:     target,
			Outcome:    "error",
			ErrorClass: classifyError(execErr),
			Reason:     safeErr,
		})
		b.emit(Event{
			Kind:     "tool_result",
			Author:   "hive",
			Label:    "Hive Residente",
			ToolName: call.Name,
			Detail:   "erro: " + RedactTarget(safeErr),
		})
		return "", fmt.Errorf("%s", safeErr)
	}

	fenced := WrapUntrustedData("tool", target, rawOut)
	_ = b.cfg.Audit.RecordOutcome(AuditEntry{
		AccountID:  b.cfg.AccountID,
		DeviceID:   b.cfg.DeviceID,
		Origin:     call.Origin,
		Workstream: call.Workstream,
		Tool:       strings.ToLower(call.Name),
		Tier:       tier,
		Decision:   "allow",
		ApprovalID: callID,
		Sandbox:    "not_required",
		Target:     target,
		Outcome:    "ok",
	})
	b.emit(Event{
		Kind:     "tool_result",
		Author:   "hive",
		Label:    "Hive Residente",
		ToolName: call.Name,
		Detail:   RedactTarget(target),
	})
	return fenced, nil
}

func (b *Broker) recordDenyBestEffort(callID string, call Call, tier Tier, target, reason string) {
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
		Sandbox:    "not_required",
		Target:     target,
		Outcome:    "denied",
		ErrorClass: "policy_denied",
		Reason:     reason,
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
	default:
		return nil, false
	}
}

func (b *Broker) requireWorkspace() (string, error) {
	ws := strings.TrimSpace(b.cfg.Workspace)
	if ws == "" {
		return "", fmt.Errorf("nenhum workspace aberto para leitura")
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
		return "", fmt.Errorf("caminho sensível recusado na fase 1: %s", RedactTarget(rel))
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
		return "", fmt.Errorf("caminho sensível recusado na fase 1: %s", RedactTarget(rel))
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
		return "", fmt.Errorf("caminho sensível recusado na fase 1: %s", RedactTarget(sub))
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
		return "", fmt.Errorf("caminho sensível recusado na fase 1: %s", RedactTarget(sub))
	}
	return tools.FindFilesReadOnly(ctx, ws, strArg(args, "pattern"), sub, intArg(args, "max_results"))
}

func (b *Broker) handleListProjects(_ context.Context, _ map[string]any) (string, error) {
	var projects []ProjectRef
	if b.cfg.ListProjects != nil {
		projects = b.cfg.ListProjects()
	}
	if len(projects) == 0 && strings.TrimSpace(b.cfg.Workspace) != "" {
		projects = []ProjectRef{{Name: b.cfg.Workspace, Root: b.cfg.Workspace, Active: true}}
	}
	if len(projects) == 0 {
		return "Nenhum projeto aberto ou recente registrado.", nil
	}
	raw, err := json.MarshalIndent(projects, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (b *Broker) handleMemorySearch(_ context.Context, args map[string]any) (string, error) {
	if b.cfg.Memory == nil {
		return "Memória pessoal local vazia.", nil
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
			fmt.Fprintf(&sb, "- [%s] %s\n", f.Workstream, f.Text)
		}
	}
	if len(res.Matches) > 0 {
		sb.WriteString("# Fatos relevantes\n")
		for _, f := range res.Matches {
			fmt.Fprintf(&sb, "- [%s] %s\n", f.Workstream, f.Text)
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

func extractTarget(toolName string, args map[string]any) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read_file", "list_dir":
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
	case "list_projects":
		return "workspaces"
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
	low := strings.ToLower(err.Error())
	switch {
	case strings.Contains(low, "fora do workspace"):
		return "path_escape"
	case strings.Contains(low, ".git"), strings.Contains(low, ".hivemind"), strings.Contains(low, "sensível"):
		return "forbidden_path"
	case strings.Contains(low, "not exist"), strings.Contains(low, "no such file"), strings.Contains(low, "cannot find"):
		return "not_found"
	case strings.Contains(low, "context canceled"), strings.Contains(low, "deadline exceeded"):
		return "canceled"
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
