package remote

import (
	"net"
	"net/http"
	"strings"
	"time"
)

func (s *Server) controls() (ProjectControls, bool) {
	if s == nil || s.deps == nil {
		return nil, false
	}
	c, ok := s.deps.(ProjectControls)
	return c, ok
}

func (s *Server) handleApprovals(w http.ResponseWriter, r *http.Request) {
	ws := r.URL.Query().Get("workspace")
	items := []ApprovalInfo{}
	if s.hub != nil {
		if got := s.hub.PendingApprovals(ws); got != nil {
			items = got
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": items})
}

func (s *Server) handleSessionRead(w http.ResponseWriter, r *http.Request) {
	c, ok := s.controls()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "leitura de conversa indisponível neste host")
		return
	}
	var req struct {
		Workspace string `json:"workspace"`
		SessionID string `json:"sessionId"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Workspace) == "" || strings.TrimSpace(req.SessionID) == "" {
		writeErr(w, http.StatusBadRequest, "workspace e sessionId obrigatórios")
		return
	}
	if err := c.MarkSessionRead(r.Context(), req.Workspace, req.SessionID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"read": true})
}

func (s *Server) handleProjectPermission(w http.ResponseWriter, r *http.Request) {
	c, ok := s.controls()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "permissão por projeto indisponível neste host")
		return
	}
	var req struct {
		Workspace      string `json:"workspace"`
		PermissionMode string `json:"permissionMode"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Workspace) == "" || strings.TrimSpace(req.PermissionMode) == "" {
		writeErr(w, http.StatusBadRequest, "workspace e permissionMode obrigatórios")
		return
	}
	if err := c.SetPermissionMode(req.Workspace, req.PermissionMode); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissionMode": req.PermissionMode})
}

func (s *Server) handleGitCommit(w http.ResponseWriter, r *http.Request) {
	c, ok := s.controls()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "commit indisponível neste host")
		return
	}
	var req struct {
		Workspace string `json:"workspace"`
		Message   string `json:"message"`
		Push      bool   `json:"push"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Workspace) == "" || strings.TrimSpace(req.Message) == "" {
		writeErr(w, http.StatusBadRequest, "workspace e message obrigatórios")
		return
	}
	out, err := c.Commit(r.Context(), req.Workspace, req.Message, req.Push)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"committed": true, "summary": out})
}

func (s *Server) handleGitPR(w http.ResponseWriter, r *http.Request) {
	c, ok := s.controls()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "pull request indisponível neste host")
		return
	}
	var req struct {
		Workspace  string `json:"workspace"`
		Title      string `json:"title"`
		Body       string `json:"body"`
		BaseBranch string `json:"baseBranch"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Workspace) == "" || strings.TrimSpace(req.Title) == "" {
		writeErr(w, http.StatusBadRequest, "workspace e title obrigatórios")
		return
	}
	url, err := c.OpenPullRequest(r.Context(), req.Workspace, req.Title, req.Body, req.BaseBranch)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"opened": true, "url": url})
}

func (s *Server) handleRoutines(w http.ResponseWriter, r *http.Request) {
	c, ok := s.controls()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"routines": []RoutineInfo{}})
		return
	}
	items, err := c.ListRoutines()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []RoutineInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"routines": items})
}

func (s *Server) handleRoutineEnabled(w http.ResponseWriter, r *http.Request) {
	c, ok := s.controls()
	if !ok {
		writeErr(w, http.StatusNotImplemented, "rotinas indisponíveis neste host")
		return
	}
	var req struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeErr(w, http.StatusBadRequest, "id obrigatório")
		return
	}
	if err := c.SetRoutineEnabled(req.ID, req.Enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": req.ID, "enabled": req.Enabled})
}

func (s *Server) healthBody(r *http.Request) map[string]any {
	body := map[string]any{
		"ok":        true,
		"online":    true,
		"hostName":  s.host,
		"version":   s.version,
		"time":      s.clock().UTC().Format(time.RFC3339),
		"transport": requestTransport(r),
	}
	if s.hub != nil {
		if p, ok := s.hub.FocusProject(); ok {
			body["openProject"] = map[string]string{
				"workspace": p.Workspace,
				"name":      p.Name,
			}
		}
	}
	return body
}

func requestTransport(r *http.Request) string {
	if r == nil {
		return "lan"
	}
	if _, ok := r.Context().Value(localKey{}).(*localCap); ok {
		return "relay"
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && isTailscale(ip) {
		return "tailscale"
	}
	return "lan"
}

func activityPhase(tool, detail string) (phase, target string) {
	name := strings.ToLower(strings.TrimSpace(tool))
	target = strings.TrimSpace(detail)
	if target == "" {
		target = strings.TrimSpace(tool)
	}
	low := strings.ToLower(detail)
	switch {
	case strings.Contains(name, "test") || strings.Contains(low, "go test") || strings.Contains(low, "npm test"):
		return "test", target
	case name == "exec_command" || name == "run_shell" || name == "run_command" || strings.Contains(name, "shell"):
		return "command", target
	case strings.Contains(name, "file") || name == "read_file" || name == "write_file" || name == "edit_file" || name == "delete_file":
		return "file", target
	default:
		return "other", target
	}
}
