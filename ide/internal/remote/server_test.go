package remote

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/secretx"
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

func newTestServer(t *testing.T) (*Server, *Hub, *fakeDeps, *lanSession) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "remote-bridge.json")
	cfg, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	hub := NewHub()
	deps := &fakeDeps{cancelResult: true, resolveOK: true}
	srv := NewServer(hub, deps, cfg)
	srv.configPath = path
	srv.secrets = secretx.NewMemoryVault()
	srv.urls = []string{"http://127.0.0.1:47890"}
	return srv, hub, deps, pairDevice(t, srv)
}

func doJSON(t *testing.T, srv *Server, method, url string, sess *lanSession, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if sess != nil {
		rd = bytes.NewReader(sess.seal(t, body))
	} else if body != nil {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
		rd = bytes.NewReader(buf.Bytes())
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, url, rd)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if sess != nil {
		unwrapLAN(sess, rec)
	}
	return rec
}

func TestPolicyRouteIsRejected(t *testing.T) {
	srv, _, _, sess := newTestServer(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/jev/policy", sess, map[string]any{
		"dailyUsd": map[string]float64{"openrouter": 1000},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST de policy deveria ser 400, veio %d (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "policy.json") {
		t.Fatalf("resposta não deve apontar o arquivo de policy: %s", rec.Body.String())
	}
}

func TestHealthIsPublic(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	rec := doJSON(t, srv, http.MethodGet, "/api/v1/health", nil, nil)
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
	srv, _, _, sess := newTestServer(t)
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
		rec := doJSON(t, srv, c.method, c.path, nil, c.body)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s sem quadro deveria ser 401, veio %d", c.method, c.path, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.Header.Set("Authorization", "Bearer "+sess.token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Bearer claro deveria ser 401, veio %d", rec.Code)
	}

	if _, err := srv.RotateSession(); err != nil {
		t.Fatal(err)
	}
	if rec := doJSON(t, srv, http.MethodGet, "/api/v1/projects", sess, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("sessão antiga após rotação deveria ser 401, veio %d", rec.Code)
	}
}

func TestRunStartValidation(t *testing.T) {
	srv, _, deps, sess := newTestServer(t)

	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/runs/start", sess, map[string]any{"workspace": "w", "objective": "x"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("sem laneId deveria ser 400, veio %d", rec.Code)
	}
	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/runs/start", sess, map[string]any{"laneId": "l", "objective": "x"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("sem workspace deveria ser 400, veio %d", rec.Code)
	}
	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/runs/start", sess, map[string]any{"laneId": "l", "workspace": "w"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("sem objective deveria ser 400, veio %d", rec.Code)
	}

	rec := doJSON(t, srv, http.MethodPost, "/api/v1/runs/start", sess, map[string]any{
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
	srv, _, deps, sess := newTestServer(t)

	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/runs/cancel", sess, map[string]any{"laneId": "l9"}); rec.Code != http.StatusOK {
		t.Fatalf("cancel deveria ser 200, veio %d", rec.Code)
	}
	if len(deps.cancelled) != 1 || deps.cancelled[0] != "l9" {
		t.Fatalf("cancel mal registrado: %+v", deps.cancelled)
	}

	if rec := doJSON(t, srv, http.MethodPost, "/api/v1/runs/redirect", sess, map[string]any{"laneId": "l9", "workspace": "w", "objective": "nova"}); rec.Code != http.StatusAccepted {
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
	srv, _, deps, sess := newTestServer(t)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/approvals/resolve", sess, map[string]any{"approvalId": "lane1:appr:2", "approved": true})
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
	srv, hub, _, sess := newTestServer(t)
	hub.RegisterProject("C:/a", "ProjA", "main", "Go", "squad-a")
	hub.RegisterProject("C:/b", "ProjB", "feat/x", "Java", "squad-b")
	hub.RegisterRun("l1", "C:/a", "squad-a", "obj", "s1", "auto")
	hub.RegisterRun("l2", "C:/b", "squad-b", "obj", "s2", "auto")

	hub.HandleLaneEvent(idecore.LaneEvent{LaneID: "l1", Team: "squad-a", Event: orchestrator.Event{Kind: "stage", Stage: "developing"}})

	rec := doJSON(t, srv, http.MethodGet, "/api/v1/projects", sess, nil)
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

	rec = doJSON(t, srv, http.MethodGet, "/api/v1/lanes?workspace=C:/a", sess, nil)
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
	srv, hub, _, sess := newTestServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/events", bytes.NewReader(sess.seal(t, nil)))
	if err != nil {
		t.Fatal(err)
	}
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
	nextEvent := func(timeout time.Duration) StreamEvent {
		t.Helper()
		deadline := time.Now().Add(timeout)
		var dataLine string
		for time.Now().Before(deadline) {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("lendo SSE: %v", err)
			}
			if strings.Contains(line, "sk-shouldnotleak123") {
				t.Fatalf("segredo em claro no SSE: %q", line)
			}
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "data: ") {
				dataLine = strings.TrimPrefix(line, "data: ")
				break
			}
		}
		if dataLine == "" {
			t.Fatal("timeout esperando data SSE")
		}
		plain, err := sess.app.Open(sess.pcPub, mustBox(t, []byte(dataLine)))
		if err != nil {
			t.Fatalf("abrindo SSE: %v (%s)", err, dataLine)
		}
		var ev StreamEvent
		if err := json.Unmarshal(plain, &ev); err != nil {
			t.Fatalf("evento SSE: %v", err)
		}
		return ev
	}

	if ev := nextEvent(3 * time.Second); ev.Type != "connected" {
		t.Fatalf("primeiro evento: %+v", ev)
	}

	hub.RegisterRun("l1", "C:/w", "t", "obj", "", "auto")
	hub.HandleLaneEvent(idecore.LaneEvent{LaneID: "l1", Team: "t", Event: orchestrator.Event{Kind: "stage", Stage: "chief"}})

	if ev := nextEvent(3 * time.Second); ev.Type != "lane_event" {
		t.Fatalf("tipo SSE errado: %+v", ev)
	}

	sent := hub.HandleProviderHealth([]jev.ProviderHealthView{
		{Provider: "openrouter", State: string(jev.CircuitOpen), Reason: "auth sk-shouldnotleak123", Until: "2026-11-20T03:00:00Z"},
	}, true)
	if !sent {
		t.Fatal("expected forced provider_health emit")
	}
	ev := nextEvent(3 * time.Second)
	if ev.Type != "provider_health" {
		t.Fatalf("tipo inesperado: %+v", ev)
	}
	if strings.Contains(string(ev.Data), "sk-shouldnotleak123") {
		t.Fatalf("segredo no plaintext do SSE: %s", ev.Data)
	}
	if !strings.Contains(string(ev.Data), `"provider":"openrouter"`) || !strings.Contains(string(ev.Data), `"state":"open"`) {
		t.Fatalf("payload inesperado: %s", ev.Data)
	}

	if hub.HandleProviderHealth([]jev.ProviderHealthView{{Provider: "openrouter", State: string(jev.CircuitClosed)}}, false) {
		t.Fatal("expected non-forced provider_health within 60s to be rate-limited")
	}
}
