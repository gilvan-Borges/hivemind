package orb

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// OrbNotice represents one recent alert/activity item shown in the floating Orb.
type OrbNotice struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
	Mood      string `json:"mood"`              // idle | thinking | working | success | error | attention
	Surface   string `json:"surface,omitempty"` // team | resident | github
	ProjectID string `json:"projectId,omitempty"`
	LaneID    string `json:"laneId,omitempty"`
	MessageID string `json:"messageId,omitempty"`
	CreatedAt string `json:"createdAt"`
}

// OrbSnapshot is the full normalized state streamed to the floating Orb window.
type OrbSnapshot struct {
	Mood         string      `json:"mood"`
	IDEVisible   bool        `json:"ideVisible"`
	ProjectName  string      `json:"projectName,omitempty"`
	PendingCount int         `json:"pendingCount"`
	Items        []OrbNotice `json:"items"`
	UpdatedAt    string      `json:"updatedAt"`
}

// OrbAction is a command sent from the floating Orb back to the main IDE process.
type OrbAction struct {
	Action   string `json:"action"` // show_ide | open_notice | dismiss_notice | clear_notices | quit_all
	NoticeID string `json:"noticeId,omitempty"`
}

const maxOrbItems = 20

// Hub serves live IDE status and notifications over a local-only loopback HTTP/SSE bridge
// consumed by the independent floating Orb window (`hivemind-ide --orb`).
type Hub struct {
	mu          sync.RWMutex
	mood        string
	ideVisible  bool
	projectName string
	items       []OrbNotice
	updatedAt   time.Time
	token       string
	onAction    func(OrbAction)

	subMu sync.Mutex
	subs  map[chan []byte]struct{}

	ln     net.Listener
	server *http.Server
}

// NewHub constructs an unstarted Orb Hub.
func NewHub(onAction func(OrbAction)) *Hub {
	var raw [24]byte
	_, _ = rand.Read(raw[:])
	return &Hub{
		mood:       "idle",
		ideVisible: true,
		updatedAt:  time.Now().UTC(),
		token:      hex.EncodeToString(raw[:]),
		onAction:   onAction,
		subs:       make(map[chan []byte]struct{}),
	}
}

// Start binds the hub on 127.0.0.1:0 (ephemeral loopback port).
func (h *Hub) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/state", h.handleState)
	mux.HandleFunc("/events", h.handleEvents)
	mux.HandleFunc("/action", h.handleAction)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	h.mu.Lock()
	h.ln = ln
	h.server = srv
	h.mu.Unlock()

	go func() {
		_ = srv.Serve(ln)
	}()
	return nil
}

// Addr returns the bound loopback host:port (e.g. "127.0.0.1:54321").
func (h *Hub) Addr() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.ln == nil {
		return ""
	}
	return h.ln.Addr().String()
}

// Token returns the per-session bearer token required by all endpoints.
func (h *Hub) Token() string {
	return h.token
}

// Close shuts down the HTTP server and closes all active SSE subscribers.
func (h *Hub) Close() {
	h.mu.Lock()
	srv := h.server
	h.server = nil
	h.ln = nil
	h.mu.Unlock()

	h.subMu.Lock()
	for ch := range h.subs {
		close(ch)
		delete(h.subs, ch)
	}
	h.subMu.Unlock()

	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// Snapshot returns a thread-safe copy of the current Orb state.
func (h *Hub) Snapshot() OrbSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	items := make([]OrbNotice, len(h.items))
	copy(items, h.items)
	return OrbSnapshot{
		Mood:         h.mood,
		IDEVisible:   h.ideVisible,
		ProjectName:  h.projectName,
		PendingCount: len(items),
		Items:        items,
		UpdatedAt:    h.updatedAt.UTC().Format(time.RFC3339),
	}
}

// SetIDEVisible updates whether the main IDE window is currently visible.
func (h *Hub) SetIDEVisible(visible bool) {
	h.mu.Lock()
	if h.ideVisible == visible {
		h.mu.Unlock()
		return
	}
	h.ideVisible = visible
	h.updatedAt = time.Now().UTC()
	h.mu.Unlock()
	h.broadcast()
}

// SetProject updates the active project name displayed by the Orb.
func (h *Hub) SetProject(name string) {
	clean := strings.TrimSpace(name)
	h.mu.Lock()
	if h.projectName == clean {
		h.mu.Unlock()
		return
	}
	h.projectName = clean
	h.updatedAt = time.Now().UTC()
	h.mu.Unlock()
	h.broadcast()
}

// SetMood updates the live mascot mood (idle, thinking, working, success, error, attention).
func (h *Hub) SetMood(mood string) {
	m := strings.TrimSpace(mood)
	if m == "" {
		m = "idle"
	}
	h.mu.Lock()
	if h.mood == m {
		h.mu.Unlock()
		return
	}
	h.mood = m
	h.updatedAt = time.Now().UTC()
	h.mu.Unlock()
	h.broadcast()
}

// PushNotice prepends a new notification item and broadcasts the updated snapshot.
func (h *Hub) PushNotice(n OrbNotice) {
	n.Title = strings.TrimSpace(n.Title)
	if n.Title == "" {
		return
	}
	if strings.TrimSpace(n.ID) == "" {
		n.ID = fmt.Sprintf("orb-%d", time.Now().UnixNano())
	}
	if strings.TrimSpace(n.Mood) == "" {
		n.Mood = "attention"
	}
	if strings.TrimSpace(n.CreatedAt) == "" {
		n.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	h.mu.Lock()
	next := make([]OrbNotice, 0, len(h.items)+1)
	next = append(next, n)
	for _, existing := range h.items {
		if existing.ID == n.ID {
			continue
		}
		next = append(next, existing)
		if len(next) >= maxOrbItems {
			break
		}
	}
	h.items = next
	h.mood = n.Mood
	h.updatedAt = time.Now().UTC()
	h.mu.Unlock()
	h.broadcast()
}

// DismissNotice removes one item by ID.
func (h *Hub) DismissNotice(id string) {
	target := strings.TrimSpace(id)
	if target == "" {
		return
	}
	h.mu.Lock()
	changed := false
	next := h.items[:0]
	for _, it := range h.items {
		if it.ID == target {
			changed = true
			continue
		}
		next = append(next, it)
	}
	if changed {
		h.items = append([]OrbNotice(nil), next...)
		if len(h.items) == 0 && (h.mood == "attention" || h.mood == "error" || h.mood == "success") {
			h.mood = "idle"
		}
		h.updatedAt = time.Now().UTC()
	}
	h.mu.Unlock()
	if changed {
		h.broadcast()
	}
}

// ClearNotices clears all pending items and returns mood to idle if it was an alert mood.
func (h *Hub) ClearNotices() {
	h.mu.Lock()
	h.items = nil
	if h.mood == "attention" || h.mood == "error" || h.mood == "success" {
		h.mood = "idle"
	}
	h.updatedAt = time.Now().UTC()
	h.mu.Unlock()
	h.broadcast()
}

func (h *Hub) broadcast() {
	data, err := json.Marshal(h.Snapshot())
	if err != nil {
		return
	}
	h.subMu.Lock()
	for ch := range h.subs {
		select {
		case ch <- data:
		default:
			// Drop slow frame; subscriber will get the next one.
		}
	}
	h.subMu.Unlock()
}

func (h *Hub) authorized(r *http.Request) bool {
	tok := ""
	if auth := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(auth, "Bearer ") {
		tok = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	if tok == "" {
		tok = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if tok == "" || len(tok) != len(h.token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(tok), []byte(h.token)) == 1
}

func (h *Hub) handleState(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.Snapshot())
}

func (h *Hub) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan []byte, 8)
	h.subMu.Lock()
	h.subs[ch] = struct{}{}
	h.subMu.Unlock()
	defer func() {
		h.subMu.Lock()
		if _, exists := h.subs[ch]; exists {
			delete(h.subs, ch)
			close(ch)
		}
		h.subMu.Unlock()
	}()

	// Send initial snapshot immediately.
	if initBytes, err := json.Marshal(h.Snapshot()); err == nil {
		_, _ = fmt.Fprintf(w, "data: %s\n\n", initBytes)
		flusher.Flush()
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case payload, ok := <-ch:
			if !ok {
				return
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		case <-ticker.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (h *Hub) handleAction(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var act OrbAction
	if err := json.NewDecoder(r.Body).Decode(&act); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	act.Action = strings.TrimSpace(act.Action)
	switch act.Action {
	case "dismiss_notice":
		h.DismissNotice(act.NoticeID)
	case "clear_notices":
		h.ClearNotices()
	case "open_notice":
		h.DismissNotice(act.NoticeID)
	}
	if h.onAction != nil {
		h.onAction(act)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
