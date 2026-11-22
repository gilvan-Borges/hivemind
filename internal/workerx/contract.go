package workerx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// HandoffBrief carries the sanitized chief handoff passed to an external CLI worker.
type HandoffBrief struct {
	RunID        string   `json:"runId"`
	Objective    string   `json:"objective"`
	ContextBrief string   `json:"contextBrief,omitempty"`
	Slice        string   `json:"slice,omitempty"`
	OwnedPaths   []string `json:"ownedPaths,omitempty"`
	Facts        []string `json:"facts,omitempty"`
}

// DispatchRequest configures one external CLI worker execution inside an isolated worktree.
type DispatchRequest struct {
	DispatchID     string            `json:"dispatchId"`
	TaskID         string            `json:"taskId"`
	Provider       string            `json:"provider"`
	Model          string            `json:"model,omitempty"`
	AgentName      string            `json:"agentName"`
	WorktreeDir    string            `json:"worktreeDir"`
	Branch         string            `json:"branch,omitempty"`
	PermissionMode string            `json:"permissionMode"`
	Timeout        time.Duration     `json:"timeout,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Handoff        HandoffBrief      `json:"handoff"`
}

// HeartbeatEvent signals liveness during long external worker runs.
type HeartbeatEvent struct {
	DispatchID string    `json:"dispatchId"`
	TaskID     string    `json:"taskId,omitempty"`
	Message    string    `json:"message,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// AskRequest is a blocking human-in-the-loop question raised by an external worker.
type AskRequest struct {
	DispatchID string   `json:"dispatchId"`
	TaskID     string   `json:"taskId,omitempty"`
	Question   string   `json:"question"`
	Options    []string `json:"options,omitempty"`
}

// AskResponse returns the human/controller answer to an AskRequest.
type AskResponse struct {
	Approved bool   `json:"approved"`
	Answer   string `json:"answer,omitempty"`
}

// UsageReport carries the external worker's own token accounting into the
// HiveMind ledger. Without it, a CLI worker burns quota invisibly: the run
// shows zero cost while the worker's provider bills the real usage.
type UsageReport struct {
	Model           string `json:"model,omitempty"`
	InputTokens     int64  `json:"inputTokens,omitempty"`
	OutputTokens    int64  `json:"outputTokens,omitempty"`
	CacheReadTokens int64  `json:"cacheReadTokens,omitempty"`
	CostMicroUSD    int64  `json:"costMicroUsd,omitempty"`
}

// DoneEvent is the idempotent terminal report emitted once per dispatchId.
type DoneEvent struct {
	DispatchID    string       `json:"dispatchId"`
	TaskID        string       `json:"taskId"`
	Outcome       string       `json:"outcome"` // "success" | "failed" | "aborted"
	Summary       string       `json:"summary,omitempty"`
	FilesModified []string     `json:"filesModified,omitempty"`
	Usage         *UsageReport `json:"usage,omitempty"`
}

// Callbacks connects worker protocol events to the orchestrator / IDE / Mobile / Orb.
type Callbacks struct {
	OnHeartbeat func(HeartbeatEvent)
	OnAsk       func(ctx context.Context, req AskRequest) (AskResponse, error)
	OnOutput    func(line string)
	OnTool      func(name, path string)
}

// ExternalWorkerAdapter runs a governed external CLI agent under the Worker Contract.
type ExternalWorkerAdapter interface {
	Provider() string
	Execute(ctx context.Context, req DispatchRequest, cb Callbacks) (DoneEvent, error)
}

// AdapterRegistry maps provider names (e.g. "cursor") to external worker implementations.
type AdapterRegistry struct {
	mu       sync.RWMutex
	adapters map[string]ExternalWorkerAdapter
}

// NewAdapterRegistry creates an empty registry.
func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{adapters: make(map[string]ExternalWorkerAdapter)}
}

// NewDefaultRegistry returns a registry pre-populated with built-in CLI worker adapters.
func NewDefaultRegistry() *AdapterRegistry {
	reg := NewAdapterRegistry()
	reg.Register(NewCursorWorkerAdapter())
	return reg
}

// Register adds or replaces an ExternalWorkerAdapter by its normalized provider name.
func (r *AdapterRegistry) Register(adapter ExternalWorkerAdapter) {
	if r == nil || adapter == nil {
		return
	}
	key := strings.ToLower(strings.TrimSpace(adapter.Provider()))
	if key == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.adapters == nil {
		r.adapters = make(map[string]ExternalWorkerAdapter)
	}
	r.adapters[key] = adapter
}

// Get returns the registered adapter for provider if present.
func (r *AdapterRegistry) Get(provider string) (ExternalWorkerAdapter, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[strings.ToLower(strings.TrimSpace(provider))]
	return a, ok
}

// List returns the registered provider identifiers sorted alphabetically.
func (r *AdapterRegistry) List() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.adapters))
	for k := range r.adapters {
		out = append(out, k)
	}
	return out
}

// DispatchRegistry enforces at-most-once terminal completion per dispatchId.
type DispatchRegistry struct {
	mu   sync.Mutex
	done map[string]DoneEvent
}

// NewDispatchRegistry constructs an empty DispatchRegistry.
func NewDispatchRegistry() *DispatchRegistry {
	return &DispatchRegistry{done: make(map[string]DoneEvent)}
}

// AcceptDone records ev if its dispatchId has not yet completed and returns
// accepted=true. Duplicate terminal calls return accepted=false and the first event.
func (d *DispatchRegistry) AcceptDone(ev DoneEvent) (accepted bool, first DoneEvent) {
	id := strings.TrimSpace(ev.DispatchID)
	if id == "" {
		return false, DoneEvent{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if prev, exists := d.done[id]; exists {
		return false, prev
	}
	d.done[id] = ev
	return true, ev
}

// Get returns the recorded DoneEvent for dispatchId if completed.
func (d *DispatchRegistry) Get(dispatchID string) (DoneEvent, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	ev, ok := d.done[strings.TrimSpace(dispatchID)]
	return ev, ok
}

// BuildWorkerPreamble generates the task prompt delivered via dispatch to an external CLI worker.
func BuildWorkerPreamble(req DispatchRequest, callbackURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Contrato de Execução HiveMind Worker\n\n")
	fmt.Fprintf(&b, "- `taskId`: `%s`\n", req.TaskID)
	fmt.Fprintf(&b, "- `dispatchId`: `%s`\n", req.DispatchID)
	fmt.Fprintf(&b, "- `worktreeDir`: `%s`\n", req.WorktreeDir)
	if callbackURL != "" {
		fmt.Fprintf(&b, "- `callbackURL`: `%s` (use header `Authorization: Bearer $HIVEMIND_WORKER_TOKEN`)\n", callbackURL)
	}
	b.WriteString("\n## Regras Obrigatórias\n")
	b.WriteString("- Trabalhe apenas na worktree atribuída; não execute git commit, git push nem abra PR.\n")
	if len(req.Handoff.OwnedPaths) > 0 {
		fmt.Fprintf(&b, "- Escopo exclusivo permitido (`owned_paths`): `%s`. Edições fora serão rejeitadas pós-execução.\n", strings.Join(req.Handoff.OwnedPaths, "`, `"))
	}
	b.WriteString("- Em tarefas longas, sinalize progresso via `POST $HIVEMIND_WORKER_URL/heartbeat` ou linha `[[hm:heartbeat:{\"dispatchId\":\"...\"}]]`.\n")
	b.WriteString("- Se precisar de decisão humana bloqueante, consulte `POST $HIVEMIND_WORKER_URL/ask` (`{\"dispatchId\":\"...\",\"question\":\"...\"}`).\n")
	b.WriteString("- Ao concluir, envie `POST $HIVEMIND_WORKER_URL/done` (`{\"dispatchId\":\"...\",\"taskId\":\"...\",\"outcome\":\"success\",\"filesModified\":[...]}`) exatamente uma vez.\n\n")

	fmt.Fprintf(&b, "## Objetivo\n%s\n", strings.TrimSpace(req.Handoff.Objective))
	if strings.TrimSpace(req.Handoff.Slice) != "" {
		fmt.Fprintf(&b, "\n## Subtarefa Atribuída\n%s\n", strings.TrimSpace(req.Handoff.Slice))
	}
	if strings.TrimSpace(req.Handoff.ContextBrief) != "" {
		fmt.Fprintf(&b, "\n## Contexto do Chefe\n%s\n", strings.TrimSpace(req.Handoff.ContextBrief))
	}
	if len(req.Handoff.Facts) > 0 {
		b.WriteString("\n## Fatos Relevantes\n")
		for _, f := range req.Handoff.Facts {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(f))
		}
	}
	return b.String()
}

// ProtocolMarker represents an inline stdout fallback protocol marker emitted by a CLI worker.
type ProtocolMarker struct {
	Kind      string
	Heartbeat *HeartbeatEvent
	Ask       *AskRequest
	Done      *DoneEvent
}

// ParseProtocolMarker parses lines containing [[hm:heartbeat:{...}]],
// [[hm:ask:{...}]], or [[hm:done:{...}]].
func ParseProtocolMarker(line string) (ProtocolMarker, bool) {
	trimmed := strings.TrimSpace(line)
	for _, entry := range []struct {
		kind   string
		prefix string
	}{
		{"heartbeat", "[[hm:heartbeat:"},
		{"ask", "[[hm:ask:"},
		{"done", "[[hm:done:"},
	} {
		idx := strings.Index(trimmed, entry.prefix)
		if idx < 0 {
			continue
		}
		rest := trimmed[idx+len(entry.prefix):]
		end := strings.LastIndex(rest, "]]")
		if end <= 0 {
			continue
		}
		rawJSON := strings.TrimSpace(rest[:end])
		switch entry.kind {
		case "heartbeat":
			var hb HeartbeatEvent
			if err := json.Unmarshal([]byte(rawJSON), &hb); err == nil {
				if hb.Timestamp.IsZero() {
					hb.Timestamp = time.Now().UTC()
				}
				return ProtocolMarker{Kind: "heartbeat", Heartbeat: &hb}, true
			}
		case "ask":
			var ask AskRequest
			if err := json.Unmarshal([]byte(rawJSON), &ask); err == nil && strings.TrimSpace(ask.Question) != "" {
				return ProtocolMarker{Kind: "ask", Ask: &ask}, true
			}
		case "done":
			var done DoneEvent
			if err := json.Unmarshal([]byte(rawJSON), &done); err == nil && strings.TrimSpace(done.Outcome) != "" {
				return ProtocolMarker{Kind: "done", Done: &done}, true
			}
		}
	}
	return ProtocolMarker{}, false
}
