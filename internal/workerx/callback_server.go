package workerx

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const maxCallbackBodyBytes = 256 << 10

// CallbackServer exposes localhost-only authenticated Worker Contract endpoints
// (/heartbeat, /ask, /done) for the duration of a single external worker dispatch.
type CallbackServer struct {
	dispatchID string
	taskID     string
	token      string
	registry   *DispatchRegistry
	cb         Callbacks
	ln         net.Listener
	srv        *http.Server
	url        string
}

// StartCallbackServer binds a loopback ephemeral port and serves /heartbeat, /ask, and /done.
func StartCallbackServer(ctx context.Context, dispatchID, taskID string, reg *DispatchRegistry, cb Callbacks) (*CallbackServer, error) {
	if reg == nil {
		reg = NewDispatchRegistry()
	}
	tokBytes := make([]byte, 32)
	if _, err := rand.Read(tokBytes); err != nil {
		return nil, fmt.Errorf("gerar token do worker: %w", err)
	}
	token := hex.EncodeToString(tokBytes)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("abrir callback server loopback: %w", err)
	}

	s := &CallbackServer{
		dispatchID: strings.TrimSpace(dispatchID),
		taskID:     strings.TrimSpace(taskID),
		token:      token,
		registry:   reg,
		cb:         cb,
		ln:         ln,
		url:        "http://" + ln.Addr().String(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /heartbeat", s.handleHeartbeat)
	mux.HandleFunc("POST /ask", s.handleAsk)
	mux.HandleFunc("POST /done", s.handleDone)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	s.srv = srv
	go func() {
		_ = srv.Serve(ln)
	}()
	return s, nil
}

// URL returns the base loopback URL (e.g. http://127.0.0.1:54321).
func (s *CallbackServer) URL() string { return s.url }

// Token returns the per-dispatch bearer token injected as HIVEMIND_WORKER_TOKEN.
func (s *CallbackServer) Token() string { return s.token }

// Close gracefully shuts down the callback HTTP server.
func (s *CallbackServer) Close() error {
	if s == nil || s.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := s.srv.Shutdown(ctx)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *CallbackServer) authorize(w http.ResponseWriter, r *http.Request) bool {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		http.Error(w, "token ausente", http.StatusUnauthorized)
		return false
	}
	got := strings.TrimSpace(auth[len(prefix):])
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
		http.Error(w, "token inválido", http.StatusUnauthorized)
		return false
	}
	return true
}

func (s *CallbackServer) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	var hb HeartbeatEvent
	if err := decodeJSONLimited(w, r, &hb); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(hb.DispatchID) == "" {
		hb.DispatchID = s.dispatchID
	}
	if hb.DispatchID != s.dispatchID {
		http.Error(w, "dispatchId divergente", http.StatusBadRequest)
		return
	}
	if hb.TaskID == "" {
		hb.TaskID = s.taskID
	}
	if hb.Timestamp.IsZero() {
		hb.Timestamp = time.Now().UTC()
	}
	if s.cb.OnHeartbeat != nil {
		s.cb.OnHeartbeat(hb)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *CallbackServer) handleAsk(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	var req AskRequest
	if err := decodeJSONLimited(w, r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.DispatchID) == "" {
		req.DispatchID = s.dispatchID
	}
	if req.DispatchID != s.dispatchID {
		http.Error(w, "dispatchId divergente", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Question) == "" {
		http.Error(w, "question obrigatória", http.StatusBadRequest)
		return
	}
	if req.TaskID == "" {
		req.TaskID = s.taskID
	}
	if s.cb.OnAsk == nil {
		writeJSON(w, http.StatusOK, AskResponse{Approved: false, Answer: "aprovação interativa indisponível"})
		return
	}
	resp, err := s.cb.OnAsk(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusGatewayTimeout)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *CallbackServer) handleDone(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	var ev DoneEvent
	if err := decodeJSONLimited(w, r, &ev); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(ev.DispatchID) == "" {
		ev.DispatchID = s.dispatchID
	}
	if ev.DispatchID != s.dispatchID {
		http.Error(w, "dispatchId divergente", http.StatusBadRequest)
		return
	}
	if ev.TaskID == "" {
		ev.TaskID = s.taskID
	}
	if strings.TrimSpace(ev.Outcome) == "" {
		ev.Outcome = "success"
	}
	accepted, first := s.registry.AcceptDone(ev)
	if !accepted {
		writeJSON(w, http.StatusConflict, map[string]any{
			"accepted": false,
			"error":    "dispatchId já concluído (idempotência)",
			"first":    first,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "done": ev})
}

func decodeJSONLimited(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxCallbackBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("ler corpo: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return fmt.Errorf("corpo JSON vazio")
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("JSON inválido: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
