package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/secretx"
	"github.com/gilvan-Borges/HiveMind/relaywire"
)

// maxBodyBytes caps request bodies (base64 image attachments can be large).
const maxBodyBytes = 32 << 20 // 32 MiB

// Server is the authenticated HTTP + SSE bridge (see
// docs/SPEC_APP_MOBILE_NATIVO.md §5 for the API contract).
type Server struct {
	hub  *Hub
	deps Deps

	mu            sync.Mutex
	boxMu         sync.Mutex
	cfg           Config
	srv           *http.Server
	port          int
	urls          []string
	host          string
	version       string
	configPath    string
	secrets       secretx.Vault
	box           *relaywire.KeyPair
	offer         *pendingOffer
	redeems       *redeemLimiter
	local         *localCap
	now           func() time.Time
	githubWebhook http.Handler
	slackWebhook  http.Handler
}

// SetWebhookHandlers attaches optional event-driven webhook/Slack handlers
// mounted under /api/v1/webhooks/* (authenticated via HMAC signatures).
func (s *Server) SetWebhookHandlers(githubWebhook, slackWebhook http.Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.githubWebhook = githubWebhook
	s.slackWebhook = slackWebhook
}

// NewServer builds the bridge (not yet listening).
func NewServer(hub *Hub, deps Deps, cfg Config) *Server {
	host, _ := os.Hostname()
	cfg.Token = ""
	return &Server{
		hub:        hub,
		deps:       deps,
		cfg:        cfg,
		host:       host,
		version:    BridgeVersion,
		configPath: configPathFn(),
		redeems:    &redeemLimiter{by: map[string]*redeemFail{}},
		local:      &localCap{},
	}
}

// Start binds 0.0.0.0:<cfg.Port> and serves in the background.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.srv != nil {
		return nil
	}
	port := s.cfg.Port
	if port <= 0 || port > 65535 {
		port = DefaultPort
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return fmt.Errorf("iniciando o bridge na porta %d: %w", port, err)
	}
	s.port = ln.Addr().(*net.TCPAddr).Port
	s.urls = joinURLs(DetectAddrs(), s.port)
	srv := &http.Server{
		Handler:           s.handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	s.srv = srv
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("remote bridge: %v", err)
		}
	}()
	return nil
}

// Stop shuts the server down and drops all SSE clients.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	srv := s.srv
	s.srv = nil
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// SnapshotConfig returns the persisted view. The raw session token is never included.
func (s *Server) SnapshotConfig() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.cfg
	cfg.Token = ""
	return cfg
}

// SetEnabled persists the opt-in flag without rewriting the session digest from a stale copy.
func (s *Server) SetEnabled(on bool) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Enabled = on
	if err := saveConfig(s.configPath, s.cfg); err != nil {
		return Config{}, err
	}
	cfg := s.cfg
	cfg.Token = ""
	return cfg, nil
}

// RotateSession invalidates every paired device and any outstanding offer.
func (s *Server) RotateSession() (Config, error) {
	s.mu.Lock()
	path := s.configPath
	s.mu.Unlock()
	cfg, err := rotateAt(path)
	if err != nil {
		return Config{}, err
	}
	s.mu.Lock()
	s.cfg = cfg
	s.offer = nil
	s.mu.Unlock()
	return cfg, nil
}

// StatusInfo describes the bridge for the IDE status bar and modal.
type StatusInfo struct {
	Enabled bool     `json:"enabled"`
	Running bool     `json:"running"`
	Port    int      `json:"port"`
	URLs    []string `json:"urls"`
	Host    string   `json:"host"`
	Version string   `json:"version"`
}

// Status snapshots the current bridge state.
func (s *Server) Status() StatusInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	running := s.srv != nil
	port := s.cfg.Port
	if port <= 0 || port > 65535 {
		port = DefaultPort
	}
	urls := s.urls
	if running {
		urls = joinURLs(DetectAddrs(), s.port)
	}
	return StatusInfo{
		Enabled: s.cfg.Enabled,
		Running: running,
		Port:    port,
		URLs:    urls,
		Host:    s.host,
		Version: s.version,
	}
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/projects", s.handleProjects)
	mux.HandleFunc("GET /api/v1/lanes", s.handleLanes)
	mux.HandleFunc("GET /api/v1/sessions", s.handleSessions)
	mux.HandleFunc("GET /api/v1/session", s.handleSession)
	mux.HandleFunc("POST /api/v1/session/read", s.handleSessionRead)
	mux.HandleFunc("POST /api/v1/session/delete", s.handleSessionDelete)
	mux.HandleFunc("GET /api/v1/approvals", s.handleApprovals)
	mux.HandleFunc("POST /api/v1/project/permission", s.handleProjectPermission)
	mux.HandleFunc("POST /api/v1/runs/start", s.handleRunStart)
	mux.HandleFunc("POST /api/v1/runs/cancel", s.handleRunCancel)
	mux.HandleFunc("POST /api/v1/runs/redirect", s.handleRunRedirect)
	mux.HandleFunc("POST /api/v1/runs/steer", s.handleRunSteer)
	mux.HandleFunc("POST /api/v1/approvals/resolve", s.handleApprovalResolve)
	mux.HandleFunc("GET /api/v1/git/diffs", s.handleDiffs)
	mux.HandleFunc("POST /api/v1/git/commit", s.handleGitCommit)
	mux.HandleFunc("POST /api/v1/git/pr", s.handleGitPR)
	mux.HandleFunc("GET /api/v1/routines", s.handleRoutines)
	mux.HandleFunc("POST /api/v1/routines/enabled", s.handleRoutineEnabled)
	mux.HandleFunc("GET /api/v1/loops", s.handleLoops)
	mux.HandleFunc("POST /api/v1/loops/stop", s.handleLoopStop)
	mux.HandleFunc("POST /api/v1/loops/pause", s.handleLoopPause)
	mux.HandleFunc("POST /api/v1/loops/resume", s.handleLoopResume)
	mux.HandleFunc("GET /api/v1/git/prs", s.handlePRs)
	mux.HandleFunc("GET /api/v1/git/issues", s.handleIssues)
	mux.HandleFunc("GET /api/v1/autoreview/status", s.handleAutoReviewStatus)
	mux.HandleFunc("POST /api/v1/autoreview/poll", s.handleAutoReviewPoll)
	mux.HandleFunc("GET /api/v1/events", s.handleEvents)
	mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusBadRequest, "rota não permitida")
	})
	outer := http.NewServeMux()
	outer.HandleFunc("POST /api/v1/webhooks/github", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		h := s.githubWebhook
		s.mu.Unlock()
		if h == nil {
			writeErr(w, http.StatusServiceUnavailable, "webhook GitHub não configurado")
			return
		}
		h.ServeHTTP(w, r)
	})
	outer.HandleFunc("POST /api/v1/webhooks/slack", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		h := s.slackWebhook
		s.mu.Unlock()
		if h == nil {
			writeErr(w, http.StatusServiceUnavailable, "webhook Slack não configurado")
			return
		}
		h.ServeHTTP(w, r)
	})
	outer.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.authorize(w, r, mux)
	})
	return outer
}

// Handler exposes the composed http.Handler (tests use httptest).
func (s *Server) Handler() http.Handler {
	return s.handler()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "corpo JSON inválido: "+err.Error())
		return false
	}
	return true
}

// --- Handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.healthBody(r))
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects := []ProjectSummary{}
	if s.hub != nil {
		projects = s.hub.Projects()
	}
	if c, ok := s.controls(); ok {
		for i := range projects {
			if mode := strings.TrimSpace(c.PermissionMode(projects[i].Workspace)); mode != "" {
				projects[i].PermissionMode = mode
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (s *Server) handleLanes(w http.ResponseWriter, r *http.Request) {
	ws := r.URL.Query().Get("workspace")
	if s.deps != nil {
		targets := []string{ws}
		if ws == "" {
			seen := map[string]bool{}
			targets = nil
			for _, snap := range s.hub.Snapshots("") {
				if snap.Workspace != "" && !seen[snap.Workspace] {
					seen[snap.Workspace] = true
					targets = append(targets, snap.Workspace)
				}
			}
		}
		for _, targetWS := range targets {
			if targetWS == "" {
				continue
			}
			if sum, err := s.deps.ListSessions(r.Context(), targetWS); err == nil {
				active := make(map[string]bool, len(sum.Sessions))
				for _, sess := range sum.Sessions {
					if sess.ID != "" {
						active[sess.ID] = true
					}
				}
				s.hub.PruneDeletedSessions(targetWS, active)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"lanes": s.hub.Snapshots(ws)})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	ws := r.URL.Query().Get("workspace")
	if ws == "" {
		writeErr(w, http.StatusBadRequest, "parâmetro ?workspace= obrigatório")
		return
	}
	res, err := s.deps.ListSessions(r.Context(), ws)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	ws := r.URL.Query().Get("workspace")
	id := r.URL.Query().Get("id")
	if ws == "" || id == "" {
		writeErr(w, http.StatusBadRequest, "parâmetros ?workspace= e ?id= obrigatórios")
		return
	}
	res, err := s.deps.LoadSession(r.Context(), ws, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// runStartRequest is the POST /api/v1/runs/start and /runs/redirect payload.
type runStartRequest struct {
	LaneID    string       `json:"laneId"`
	Workspace string       `json:"workspace"`
	Team      string       `json:"team,omitempty"`
	Objective string       `json:"objective,omitempty"`
	JiraKey   string       `json:"jiraKey,omitempty"`
	SessionID string       `json:"sessionId,omitempty"`
	RunMode   string       `json:"runMode,omitempty"`
	Images    []ImageInput `json:"images,omitempty"`
}

func isAllowedRunMode(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto", "direct", "plan", "plan:sdd", "plan:sdd-jira", "squad", "resident":
		return true
	default:
		return false
	}
}

func (req *runStartRequest) validate() string {
	if !isAllowedRunMode(req.RunMode) {
		return fmt.Sprintf("runMode inválido: %q", req.RunMode)
	}
	isResident := strings.EqualFold(strings.TrimSpace(req.RunMode), "resident")
	if isResident {
		// Resident runs always use fixed lane "resident" (§1.10, §10).
		req.LaneID = "resident"
		req.RunMode = "resident"
	} else {
		if strings.TrimSpace(req.LaneID) == "" {
			return "laneId obrigatório"
		}
		if strings.TrimSpace(req.Workspace) == "" {
			return "workspace obrigatório"
		}
	}
	if strings.TrimSpace(req.Objective) == "" && len(req.Images) == 0 {
		return "objective (ou images) obrigatório"
	}
	for _, img := range req.Images {
		if strings.TrimSpace(img.DataBase64) == "" {
			return "cada image precisa de dataBase64"
		}
	}
	return ""
}

func (s *Server) handleRunStart(w http.ResponseWriter, r *http.Request) {
	var req runStartRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if msg := req.validate(); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.deps.RunStart(r.Context(), req.LaneID, req.Workspace, req.Team, req.Objective, req.JiraKey, req.SessionID, req.RunMode, req.Images); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":  true,
		"laneId":    req.LaneID,
		"workspace": req.Workspace,
	})
}

func (s *Server) handleRunCancel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LaneID string `json:"laneId"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.LaneID) == "" {
		writeErr(w, http.StatusBadRequest, "laneId obrigatório")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": s.deps.RunCancel(req.LaneID)})
}

func (s *Server) handleRunRedirect(w http.ResponseWriter, r *http.Request) {
	var req runStartRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if msg := req.validate(); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	// A message sent while the squad is running used to cancel the turn and
	// start over, paying for the same plan twice and dropping the worktree
	// state. Steer keeps the run and hands the note to the live roundtable.
	// Images still restart: the steer path is text-only.
	if len(req.Images) == 0 && s.deps.RunSteer(req.LaneID, req.Objective) {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"accepted": true,
			"steered":  true,
			"laneId":   req.LaneID,
		})
		return
	}
	s.deps.RunCancel(req.LaneID)
	if err := s.deps.RunStart(r.Context(), req.LaneID, req.Workspace, req.Team, req.Objective, req.JiraKey, req.SessionID, req.RunMode, req.Images); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":  true,
		"laneId":    req.LaneID,
		"workspace": req.Workspace,
	})
}

func (s *Server) handleRunSteer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LaneID  string `json:"laneId"`
		Message string `json:"message"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.LaneID) == "" || strings.TrimSpace(req.Message) == "" {
		writeErr(w, http.StatusBadRequest, "laneId e message obrigatórios")
		return
	}
	if !s.deps.RunSteer(req.LaneID, req.Message) {
		writeErr(w, http.StatusConflict, "nenhuma execução ativa nesta lane")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"steered": true, "laneId": req.LaneID})
}

func (s *Server) handleApprovalResolve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ApprovalID string `json:"approvalId"`
		Approved   bool   `json:"approved"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ApprovalID) == "" {
		writeErr(w, http.StatusBadRequest, "approvalId obrigatório")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resolved": s.deps.ApprovalResolve(req.ApprovalID, req.Approved)})
}

func (s *Server) handleDiffs(w http.ResponseWriter, r *http.Request) {
	ws := r.URL.Query().Get("workspace")
	if ws == "" {
		writeErr(w, http.StatusBadRequest, "parâmetro ?workspace= obrigatório")
		return
	}
	files, err := s.deps.Diff(r.Context(), ws)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (s *Server) handlePRs(w http.ResponseWriter, r *http.Request) {
	ws := r.URL.Query().Get("workspace")
	if ws == "" {
		writeErr(w, http.StatusBadRequest, "parâmetro ?workspace= obrigatório")
		return
	}
	prs, err := s.deps.ListPRs(r.Context(), ws)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prs": prs})
}

func (s *Server) handleIssues(w http.ResponseWriter, r *http.Request) {
	ws := r.URL.Query().Get("workspace")
	if ws == "" {
		writeErr(w, http.StatusBadRequest, "parâmetro ?workspace= obrigatório")
		return
	}
	issues, err := s.deps.ListIssues(r.Context(), ws)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": issues})
}

func (s *Server) handleAutoReviewStatus(w http.ResponseWriter, r *http.Request) {
	ws := r.URL.Query().Get("workspace")
	if ws == "" {
		writeErr(w, http.StatusBadRequest, "parâmetro ?workspace= obrigatório")
		return
	}
	writeJSON(w, http.StatusOK, s.deps.AutoReviewStatus(ws))
}

func (s *Server) handleAutoReviewPoll(w http.ResponseWriter, r *http.Request) {
	ws := r.URL.Query().Get("workspace")
	if ws == "" {
		var req struct {
			Workspace string `json:"workspace"`
		}
		if !decodeBody(w, r, &req) {
			return
		}
		ws = req.Workspace
	}
	if ws == "" {
		writeErr(w, http.StatusBadRequest, "parâmetro ?workspace= obrigatório")
		return
	}
	writeJSON(w, http.StatusOK, s.deps.AutoReviewPoll(ws))
}

// handleEvents streams SSE. On the LAN each data line is a naclbox; the event
// type sits inside the plaintext. In-process callers still see the clear frames.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if _, encrypted := r.Context().Value(peerKey{}).([32]byte); encrypted {
		if _, err := s.ensureBox(); err != nil {
			writeAPIError(w, http.StatusServiceUnavailable, "internal", "cofre indisponível")
			return
		}
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming não suportado")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := s.hub.Subscribe()
	defer unsub()

	connected, _ := json.Marshal(map[string]bool{"ok": true})
	s.writeStream(w, fl, r, StreamEvent{Type: "connected", Data: connected})

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			s.writeStream(w, fl, r, ev)
		case <-ticker.C:
			s.writeStream(w, fl, r, StreamEvent{Type: "ping"})
		}
	}
}
