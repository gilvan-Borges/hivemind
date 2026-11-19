package remote

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
)

// fakeDeps records calls and returns canned values.
type fakeDeps struct {
	started   []startCall
	cancelled []string
	resolved  []resolveCall
	polled    []string

	startErr     error
	cancelResult bool
	resolveOK    bool
	sessions     idecore.WorkspaceSessionsSummary
	session      idecore.SessionDetail
	files        []idecore.FileDiff
	prs          []idecore.ProjectPullRequest
	issues       []idecore.ProjectIssue
	arStatus     idecore.AutoReviewStatus
}

type startCall struct {
	LaneID, Workspace, Team, Objective, JiraKey, SessionID, RunMode string
	Images                                                          []idecore.ImageInput
}

type resolveCall struct {
	ID       string
	Approved bool
}

func (f *fakeDeps) RunStart(_ context.Context, laneID, workspace, teamName, objective, jiraKey, sessionID, runMode string, images []idecore.ImageInput) error {
	f.started = append(f.started, startCall{laneID, workspace, teamName, objective, jiraKey, sessionID, runMode, images})
	return f.startErr
}

func (f *fakeDeps) RunCancel(laneID string) bool {
	f.cancelled = append(f.cancelled, laneID)
	return f.cancelResult
}

func (f *fakeDeps) ApprovalResolve(id string, approved bool) bool {
	f.resolved = append(f.resolved, resolveCall{id, approved})
	return f.resolveOK
}

func (f *fakeDeps) ListSessions(_ context.Context, _ string) (idecore.WorkspaceSessionsSummary, error) {
	return f.sessions, nil
}

func (f *fakeDeps) LoadSession(_ context.Context, _, _ string) (idecore.SessionDetail, error) {
	return f.session, nil
}

func (f *fakeDeps) Diff(_ context.Context, _ string) ([]idecore.FileDiff, error) {
	return f.files, nil
}

func (f *fakeDeps) ListPRs(_ context.Context, _ string) ([]idecore.ProjectPullRequest, error) {
	return f.prs, nil
}

func (f *fakeDeps) ListIssues(_ context.Context, _ string) ([]idecore.ProjectIssue, error) {
	return f.issues, nil
}

func (f *fakeDeps) AutoReviewStatus(workspace string) idecore.AutoReviewStatus {
	return f.arStatus
}

func (f *fakeDeps) AutoReviewPoll(workspace string) idecore.AutoReviewStatus {
	f.polled = append(f.polled, workspace)
	return f.arStatus
}

func newTestServer(t *testing.T) (*Server, *Hub, *fakeDeps, string) {
	t.Helper()
	cfg, err := LoadConfigFrom(t.TempDir() + "/remote-bridge.json")
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	hub := NewHub()
	deps := &fakeDeps{cancelResult: true, resolveOK: true}
	srv := NewServer(hub, deps, cfg)
	return srv, hub, deps, cfg.Token
}

func doJSON(t *testing.T, srv *Server, method, url, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Buffer
	if body != nil {
		rd = &bytes.Buffer{}
		if err := json.NewEncoder(rd).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	} else {
		rd = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, url, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestHealthIsPublic(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	rec := doJSON(t, srv, http.MethodGet, "/api/v1/health", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("health sem token deveria ser 200, veio %d", rec.Code)
	}
	var body struct {
		OK       bool   `json:"ok"`
		HostName string `json:"hostName"`
		Version  string `json:"version"`
		Time     string `json:"time"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("health body: %v", err)
	}
	if !body.OK || body.Version == "" || body.Time == "" {
		t.Fatalf("health incompleto: %+v", body)
	}
}

func TestAuthRequired(t *testing.T) {
	srv, _, _, token := newTestServer(t)
	cases := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/projects", nil},
		{http.MethodGet, "/api/v1/lanes?workspace=w", nil},
		{http.MethodGet, "/api/v1/sessions?workspace=w", nil},
		{http.MethodGet, "/api/v1/session?workspace=w&id=s", nil},
		{http.MethodPost, "/api/v1/runs/start", map[string]any{"laneId": "l", "workspace": "w", "objective": "x"}},
		{http.MethodPost, "/api/v1/runs/cancel", map[string]any{"laneId": "l"}},
		{http.MethodPost, "/api/v1/runs/redirect", map[string]any{"laneId": "l", "workspace": "w", "objective": "x"}},
		{http.MethodPost, "/api/v1/approvals/resolve", map[string]any{"approvalId": "a", "approved": true}},
		{http.MethodGet, "/api/v1/git/diffs?workspace=w", nil},
		{http.MethodGet, "/api/v1/git/prs?workspace=w", nil},
		{http.MethodGet, "/api/v1/git/issues?workspace=w", nil},
		{http.MethodGet, "/api/v1/autoreview/status?workspace=w", nil},
		{http.MethodPost, "/api/v1/autoreview/poll?workspace=w", nil},
	}
	for _, c := range cases {
		rec := doJSON(t, srv, c.method, c.path, "", c.body)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s sem token deveria ser 401, veio %d", c.method, c.path, rec.Code)
		}
	}

	// Wrong token must also be rejected.
	if rec := doJSON(t, srv, http.MethodGet, "/api/v1/projects", strings.Repeat("0", 64), nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token errado deveria ser 401, veio %d", rec.Code)
	}

	// Token rotation invalidates previous tokens immediately.
	srv.SetToken(strings.Repeat("f", 64))
	if rec := doJSON(t, srv, http.MethodGet, "/api/v1/projects", token, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token antigo após rotação deveria ser 401, veio %d", rec.Code)
	}
}

func TestRunStartValidation(t *testing.T) {
	srv, _, deps, token := newTestServer(t)

	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/runs/start", token, map[string]any{"workspace": "w", "objective": "x"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("sem laneId deveria ser 400, veio %d", rec.Code)
	}
	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/runs/start", token, map[string]any{"laneId": "l", "objective": "x"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("sem workspace deveria ser 400, veio %d", rec.Code)
	}
	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/runs/start", token, map[string]any{"laneId": "l", "workspace": "w"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("sem objective deveria ser 400, veio %d", rec.Code)
	}

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/runs/start", token, map[string]any{
		"laneId": "l1", "workspace": "C:/repo", "team": "principal",
		"objective": "corrija o bug", "jiraKey": "PROJ-1", "sessionId": "s1", "runMode": "plan",
		"images": []map[string]string{{"name": "foto.png", "mimeType": "image/png", "dataBase64": "aW1n"}},
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start válido deveria ser 202, veio %d: %s", rec.Code, rec.Body.String())
	}
	if len(deps.started) != 1 {
		t.Fatalf("esperava 1 start, veio %d", len(deps.started))
	}
	c := deps.started[0]
	if c.LaneID != "l1" || c.Workspace != "C:/repo" || c.Team != "principal" || c.Objective != "corrija o bug" ||
		c.JiraKey != "PROJ-1" || c.SessionID != "s1" || c.RunMode != "plan" || len(c.Images) != 1 {
		t.Fatalf("start mal registrado: %+v", c)
	}
}

func TestRunCancelAndRedirect(t *testing.T) {
	srv, _, deps, token := newTestServer(t)

	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/runs/cancel", token, map[string]any{"laneId": "l9"}); rec.Code != http.StatusOK {
		t.Fatalf("cancel deveria ser 200, veio %d", rec.Code)
	}
	if len(deps.cancelled) != 1 || deps.cancelled[0] != "l9" {
		t.Fatalf("cancel mal registrado: %+v", deps.cancelled)
	}

	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/runs/redirect", token, map[string]any{"laneId": "l9", "workspace": "w", "objective": "nova"}); rec.Code != http.StatusAccepted {
		t.Fatalf("redirect deveria ser 202, veio %d", rec.Code)
	}
	if len(deps.cancelled) != 2 {
		t.Fatalf("redirect deveria cancelar antes de iniciar, cancels=%+v", deps.cancelled)
	}
	if len(deps.started) != 1 || deps.started[0].Objective != "nova" {
		t.Fatalf("redirect deveria iniciar nova instrução: %+v", deps.started)
	}
}

func TestApprovalResolve(t *testing.T) {
	srv, _, deps, token := newTestServer(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/approvals/resolve", token, map[string]any{"approvalId": "lane1:appr:2", "approved": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve deveria ser 200, veio %d", rec.Code)
	}
	if len(deps.resolved) != 1 || deps.resolved[0].ID != "lane1:appr:2" || !deps.resolved[0].Approved {
		t.Fatalf("resolve mal registrado: %+v", deps.resolved)
	}
	var body map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body["resolved"] {
		t.Fatalf("resolve deveria reportar resolved=true: %s", rec.Body.String())
	}
}

func TestProjectsAndLanesIsolation(t *testing.T) {
	srv, hub, _, token := newTestServer(t)
	hub.RegisterProject("C:/a", "ProjA", "main", "Go", "squad-a")
	hub.RegisterProject("C:/b", "ProjB", "feat/x", "Java", "squad-b")
	hub.RegisterRun("l1", "C:/a", "squad-a", "obj", "s1", "auto")
	hub.RegisterRun("l2", "C:/b", "squad-b", "obj", "s2", "auto")

	hub.HandleLaneEvent(idecore.LaneEvent{LaneID: "l1", Team: "squad-a", Event: orchestrator.Event{Kind: "stage", Stage: "developing"}})

	rec := doJSON(t, srv, http.MethodGet, "/api/v1/projects", token, nil)
	var projs struct {
		Projects []ProjectSummary `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &projs); err != nil {
		t.Fatalf("projects: %v", err)
	}
	if len(projs.Projects) != 2 {
		t.Fatalf("esperava 2 projetos, veio %+v", projs.Projects)
	}
	var a, b ProjectSummary
	for _, p := range projs.Projects {
		switch p.Name {
		case "ProjA":
			a = p
		case "ProjB":
			b = p
		}
	}
	if a.RunningLanes != 1 || a.PendingApprovals != 0 {
		t.Fatalf("ProjA deveria ter 1 lane rodando, veio %+v", a)
	}
	if b.RunningLanes != 1 {
		t.Fatalf("ProjB deveria ter 1 lane rodando, veio %+v", b)
	}

	rec = doJSON(t, srv, http.MethodGet, "/api/v1/lanes?workspace=C:/a", token, nil)
	var lanes struct {
		Lanes []LaneSnapshot `json:"lanes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lanes); err != nil {
		t.Fatalf("lanes: %v", err)
	}
	if len(lanes.Lanes) != 1 || lanes.Lanes[0].LaneID != "l1" || lanes.Lanes[0].Workspace != "C:/a" {
		t.Fatalf("isolamento de workspace falhou: %+v", lanes.Lanes)
	}
}

func TestSSEStreaming(t *testing.T) {
	srv, hub, _, token := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events deveria ser 200, veio %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type errado: %s", ct)
	}

	reader := bufio.NewReader(resp.Body)
	readUntil := func(want string, timeout time.Duration) string {
		deadline := time.Now().Add(timeout)
		var sb strings.Builder
		for time.Now().Before(deadline) {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("lendo SSE: %v (até agora: %q)", err, sb.String())
			}
			sb.WriteString(line)
			if strings.Contains(sb.String(), want) {
				return sb.String()
			}
		}
		t.Fatalf("timeout esperando %q no stream (veio: %q)", want, sb.String())
		return ""
	}

	readUntil("event: connected", 3*time.Second)

	hub.RegisterRun("l1", "C:/w", "t", "obj", "", "auto")
	hub.HandleLaneEvent(idecore.LaneEvent{LaneID: "l1", Team: "t", Event: orchestrator.Event{Kind: "stage", Stage: "chief"}})

	got := readUntil("event: lane_event\ndata: ", 3*time.Second)
	var data map[string]any
	marker := "event: lane_event\ndata: "
	idx := strings.Index(got, marker)
	if idx < 0 {
		t.Fatalf("sem data no SSE: %q", got)
	}
	payload := got[idx+len(marker):]
	if nl := strings.IndexByte(payload, '\n'); nl >= 0 {
		payload = payload[:nl]
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &data); err != nil {
		t.Fatalf("data SSE inválida: %v (%q)", err, payload)
	}
	if data["type"] != "lane_event" {
		t.Fatalf("tipo SSE errado: %+v", data)
	}
}
