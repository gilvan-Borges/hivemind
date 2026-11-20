package remote

import (
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
)

// StreamEvent is one SSE message mirrored to mobile clients. Types:
// "lane_event", "autoreview_status", "approval_resolved", "ping".
type StreamEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// ApprovalInfo is a pending approval gate visible to the mobile app.
type ApprovalInfo struct {
	ApprovalID string    `json:"approvalId"`
	LaneID     string    `json:"laneId"`
	Workspace  string    `json:"workspace"`
	Prompt     string    `json:"prompt"`
	Label      string    `json:"label,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// LaneEventItem is a capped transcript entry of a lane's active turn.
type LaneEventItem struct {
	Kind              string   `json:"kind"`
	Stage             string   `json:"stage,omitempty"`
	Author            string   `json:"author,omitempty"`
	Label             string   `json:"label,omitempty"`
	Text              string   `json:"text,omitempty"`
	ToolName          string   `json:"toolName,omitempty"`
	Tokens            int64    `json:"tokens,omitempty"`
	Provider          string   `json:"provider,omitempty"`
	Model             string   `json:"model,omitempty"`
	Tier              string   `json:"tier,omitempty"`
	Reasons           []string `json:"reasons,omitempty"`
	EstimatedMicroUSD int64    `json:"estimatedMicroUsd,omitempty"`
	DecisionMicros    int64    `json:"decisionMicros,omitempty"`
	At                string   `json:"at,omitempty"`
}

// LaneSnapshot is the live state of one (workspace, laneId) pair.
type LaneSnapshot struct {
	Workspace        string          `json:"workspace"`
	LaneID           string          `json:"laneId"`
	TeamName         string          `json:"teamName,omitempty"`
	SessionID        string          `json:"sessionId,omitempty"`
	RunMode          string          `json:"runMode,omitempty"`
	Objective        string          `json:"objective,omitempty"`
	Running          bool            `json:"running"`
	Stage            string          `json:"stage,omitempty"`
	LastError        string          `json:"lastError,omitempty"`
	StartedAt        string          `json:"startedAt,omitempty"`
	TurnTokens       int64           `json:"turnTokens"`
	RecentEvents     []LaneEventItem `json:"recentEvents"`
	PendingApprovals []ApprovalInfo  `json:"pendingApprovals"`
}

// ProjectSummary describes one open/recent workspace for GET /api/v1/projects.
type ProjectSummary struct {
	Workspace        string `json:"workspace"`
	Name             string `json:"name"`
	Branch           string `json:"branch,omitempty"`
	Stack            string `json:"stack,omitempty"`
	ActiveTeam       string `json:"activeTeam,omitempty"`
	RunningLanes     int    `json:"runningLanes"`
	PendingApprovals int    `json:"pendingApprovals"`
}

const (
	maxRecentEvents    = 60
	maxApprovalsPerLane = 16
)

type laneState struct {
	snapshot  LaneSnapshot
	approvals map[string]ApprovalInfo
}

func keyFor(workspace, laneID string) string {
	return workspace + "\x00" + laneID
}

// Hub keeps the live (workspace, laneId) buffer consumed by the HTTP API and
// fans events out to SSE subscribers.
type Hub struct {
	mu          sync.Mutex
	lanes       map[string]*laneState
	laneWks     map[string]string // laneID -> workspace (events carry no workspace)
	autoreview  map[string]idecore.AutoReviewStatus
	projects    map[string]ProjectSummary
	subscribers map[chan StreamEvent]struct{}
}

// NewHub creates an empty Hub.
func NewHub() *Hub {
	return &Hub{
		lanes:       make(map[string]*laneState),
		laneWks:     make(map[string]string),
		autoreview:  make(map[string]idecore.AutoReviewStatus),
		projects:    make(map[string]ProjectSummary),
		subscribers: make(map[chan StreamEvent]struct{}),
	}
}

func (h *Hub) stateForLocked(workspace, laneID string) *laneState {
	st, ok := h.lanes[keyFor(workspace, laneID)]
	if !ok {
		st = &laneState{
			snapshot:  LaneSnapshot{Workspace: workspace, LaneID: laneID},
			approvals: make(map[string]ApprovalInfo),
		}
		h.lanes[keyFor(workspace, laneID)] = st
	}
	return st
}

// RegisterRun records a run about to start (called for both IDE- and
// mobile-initiated turns) so the Hub can attribute subsequent LaneEvents to
// the right workspace.
func (h *Hub) RegisterRun(laneID, workspace, teamName, objective, sessionID, runMode string) {
	h.mu.Lock()
	h.laneWks[laneID] = workspace
	st := h.stateForLocked(workspace, laneID)
	s := &st.snapshot
	s.TeamName = teamName
	s.SessionID = sessionID
	s.RunMode = runMode
	s.Objective = objective
	s.Running = true
	s.Stage = ""
	s.LastError = ""
	s.StartedAt = time.Now().UTC().Format(time.RFC3339)
	s.TurnTokens = 0
	s.RecentEvents = nil
	s.PendingApprovals = nil
	st.approvals = make(map[string]ApprovalInfo)
	h.mu.Unlock()
}

// HandleLaneEvent consumes every idecore.LaneEvent emitted by the
// SessionManager, updating the live snapshot and broadcasting to SSE clients.
func (h *Hub) HandleLaneEvent(ev idecore.LaneEvent) {
	e := ev.Event
	at := time.Now().UTC().Format(time.RFC3339)

	h.mu.Lock()
	ws := h.laneWks[ev.LaneID]
	st := h.stateForLocked(ws, ev.LaneID)
	s := &st.snapshot
	switch e.Kind {
	case "stage":
		s.Stage = e.Stage
		if !s.Running {
			s.Running = true
			s.StartedAt = at
		}
	case "usage":
		if e.Tokens > 0 {
			s.TurnTokens += int64(e.Tokens)
		} else {
			s.TurnTokens += int64(e.InputTokens) + int64(e.OutputTokens)
		}
	case "approval_required":
		if _, dup := st.approvals[e.ApprovalID]; !dup && len(st.approvals) < maxApprovalsPerLane {
			st.approvals[e.ApprovalID] = ApprovalInfo{
				ApprovalID: e.ApprovalID,
				LaneID:     ev.LaneID,
				Workspace:  ws,
				Prompt:     e.Text,
				Label:      e.Label,
				CreatedAt:  time.Now().UTC(),
			}
		}
	case "finish":
		s.Running = false
		s.Stage = "done"
		st.approvals = make(map[string]ApprovalInfo)
	case "error":
		s.Running = false
		s.LastError = e.Text
		st.approvals = make(map[string]ApprovalInfo)
	}
	if e.Kind != "usage" {
		item := LaneEventItem{
			Kind:              e.Kind,
			Stage:             e.Stage,
			Author:            e.Author,
			Label:             e.Label,
			Text:              e.Text,
			ToolName:          e.ToolName,
			Tokens:            int64(e.Tokens),
			Provider:          e.Provider,
			Model:             e.Model,
			Tier:              e.Tier,
			Reasons:           append([]string(nil), e.Reasons...),
			EstimatedMicroUSD: e.EstimatedMicroUSD,
			DecisionMicros:    e.DecisionMicros,
			At:                at,
		}
		s.RecentEvents = append(s.RecentEvents, item)
		if len(s.RecentEvents) > maxRecentEvents {
			s.RecentEvents = s.RecentEvents[len(s.RecentEvents)-maxRecentEvents:]
		}
	}
	s.PendingApprovals = approvalsSlice(st.approvals)
	raw, _ := json.Marshal(ev)
	h.mu.Unlock()

	h.broadcast(StreamEvent{Type: "lane_event", Data: raw})
}

// HandleRunError marks a lane whose turn failed to start (e.g. no team
// configured) so mobile clients see the error even without any event.
func (h *Hub) HandleRunError(laneID, workspace string, err error) {
	h.mu.Lock()
	h.laneWks[laneID] = workspace
	st := h.stateForLocked(workspace, laneID)
	st.snapshot.Running = false
	st.snapshot.LastError = err.Error()
	item := LaneEventItem{Kind: "error", Author: "sistema", Label: "Erro", Text: err.Error(), At: time.Now().UTC().Format(time.RFC3339)}
	st.snapshot.RecentEvents = append(st.snapshot.RecentEvents, item)
	if len(st.snapshot.RecentEvents) > maxRecentEvents {
		st.snapshot.RecentEvents = st.snapshot.RecentEvents[len(st.snapshot.RecentEvents)-maxRecentEvents:]
	}
	st.snapshot.PendingApprovals = approvalsSlice(st.approvals)
	h.mu.Unlock()

	ev := idecore.LaneEvent{
		LaneID: laneID,
		Event:  newErrorEvent(err.Error()),
	}
	raw, _ := json.Marshal(ev)
	h.broadcast(StreamEvent{Type: "lane_event", Data: raw})
}

// MarkCancel records an explicit cancel of a lane.
func (h *Hub) MarkCancel(laneID string) {
	h.mu.Lock()
	ws := h.laneWks[laneID]
	st := h.stateForLocked(ws, laneID)
	st.snapshot.Running = false
	st.approvals = make(map[string]ApprovalInfo)
	st.snapshot.PendingApprovals = nil
	h.mu.Unlock()
}

// MarkApprovalResolved removes a resolved approval from every lane and
// broadcasts an approval_resolved event to all SSE clients.
func (h *Hub) MarkApprovalResolved(approvalID string, approved bool) {
	h.mu.Lock()
	var found *ApprovalInfo
	for _, st := range h.lanes {
		if ai, ok := st.approvals[approvalID]; ok {
			cp := ai
			found = &cp
			delete(st.approvals, approvalID)
			st.snapshot.PendingApprovals = approvalsSlice(st.approvals)
		}
	}
	h.mu.Unlock()

	payload := struct {
		ApprovalID string `json:"approvalId"`
		Approved   bool   `json:"approved"`
	}{ApprovalID: approvalID, Approved: approved}
	raw, _ := json.Marshal(payload)
	h.broadcast(StreamEvent{Type: "approval_resolved", Data: raw})

	// The resolved approval must disappear on the other device too: re-emit an
	// approval_resolved lane event so open chats (IDE and mobile) refresh.
	if found != nil {
		ev := idecore.LaneEvent{
			LaneID: found.LaneID,
			Team:   "",
			Event:  approvalResolvedEvent(approvalID, approved),
		}
		rawEv, _ := json.Marshal(ev)
		h.broadcast(StreamEvent{Type: "lane_event", Data: rawEv})
	}
}

// HandleAutoReviewStatus records the auto-review snapshot for a workspace and
// keeps the dedicated lane-attribution for lane-autoreview events.
func (h *Hub) HandleAutoReviewStatus(st idecore.AutoReviewStatus) {
	h.mu.Lock()
	h.autoreview[st.Workspace] = st
	if st.Workspace != "" {
		h.laneWks["lane-autoreview"] = st.Workspace
	}
	raw, _ := json.Marshal(st)
	h.mu.Unlock()
	h.broadcast(StreamEvent{Type: "autoreview_status", Data: raw})
}

// RegisterProject records metadata about an open workspace (branch, stack,
// active team) for GET /api/v1/projects.
func (h *Hub) RegisterProject(workspace, name, branch, stack, activeTeam string) {
	h.mu.Lock()
	p := h.projects[workspace]
	p.Workspace = workspace
	p.Name = name
	if branch != "" {
		p.Branch = branch
	}
	if stack != "" {
		p.Stack = stack
	}
	if activeTeam != "" {
		p.ActiveTeam = activeTeam
	}
	h.projects[workspace] = p
	h.mu.Unlock()
}

// Projects returns the registered projects with live lane/approval counters.
func (h *Hub) Projects() []ProjectSummary {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ProjectSummary, 0, len(h.projects))
	for ws, p := range h.projects {
		cp := p
		cp.RunningLanes = 0
		cp.PendingApprovals = 0
		for _, st := range h.lanes {
			if st.snapshot.Workspace != ws {
				continue
			}
			if st.snapshot.Running {
				cp.RunningLanes++
			}
			cp.PendingApprovals += len(st.approvals)
		}
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Snapshots returns the live lane snapshots, optionally filtered by workspace.
func (h *Hub) Snapshots(workspace string) []LaneSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]LaneSnapshot, 0, len(h.lanes))
	for _, st := range h.lanes {
		s := st.snapshot
		s.PendingApprovals = approvalsSlice(st.approvals)
		if s.RecentEvents == nil {
			s.RecentEvents = []LaneEventItem{}
		}
		if s.PendingApprovals == nil {
			s.PendingApprovals = []ApprovalInfo{}
		}
		if workspace != "" && s.Workspace != workspace {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Workspace != out[j].Workspace {
			return out[i].Workspace < out[j].Workspace
		}
		return out[i].LaneID < out[j].LaneID
	})
	return out
}

// AutoReviewSnapshot returns the last known auto-review status for a workspace.
func (h *Hub) AutoReviewSnapshot(workspace string) (idecore.AutoReviewStatus, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st, ok := h.autoreview[workspace]
	return st, ok
}

// Subscribe registers an SSE subscriber. The returned cancel function
// unregisters and closes the channel.
func (h *Hub) Subscribe() (<-chan StreamEvent, func()) {
	ch := make(chan StreamEvent, 64)
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subscribers, ch)
		h.mu.Unlock()
		close(ch)
	}
}

func (h *Hub) broadcast(ev StreamEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subscribers {
		select {
		case ch <- ev:
		default:
			// Slow consumer: drop the event rather than blocking the emitter.
		}
	}
}

func approvalsSlice(m map[string]ApprovalInfo) []ApprovalInfo {
	out := make([]ApprovalInfo, 0, len(m))
	for _, ai := range m {
		out = append(out, ai)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}
