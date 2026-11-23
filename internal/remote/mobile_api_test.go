package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
)

type stubControls struct {
	fakeDeps
	mode     string
	commit   string
	prURL    string
	routines []RoutineInfo
	readID   string
	loops    []LoopInfo
	stopped  string
}

func (s *stubControls) SetPermissionMode(_ string, mode string) error {
	s.mode = mode
	return nil
}
func (s *stubControls) PermissionMode(string) string { return s.mode }
func (s *stubControls) Commit(context.Context, string, string, bool) (string, error) {
	return s.commit, nil
}
func (s *stubControls) OpenPullRequest(context.Context, string, string, string, string) (string, error) {
	return s.prURL, nil
}
func (s *stubControls) ListRoutines() ([]RoutineInfo, error) { return s.routines, nil }
func (s *stubControls) SetRoutineEnabled(id string, enabled bool) error {
	for i := range s.routines {
		if s.routines[i].ID == id {
			s.routines[i].Enabled = enabled
		}
	}
	return nil
}
func (s *stubControls) ListLoops(string) ([]LoopInfo, error) { return s.loops, nil }
func (s *stubControls) GetLoop(_ string, id string) (LoopInfo, error) {
	for _, item := range s.loops {
		if item.ID == id {
			return item, nil
		}
	}
	return LoopInfo{}, fmt.Errorf("loop não encontrado")
}
func (s *stubControls) StopLoop(_ string, id string) error {
	s.stopped = id
	return nil
}
func (s *stubControls) PauseLoop(string, string) error  { return nil }
func (s *stubControls) ResumeLoop(string, string) error { return nil }

func (s *stubControls) MarkSessionRead(_ context.Context, _ string, id string) error {
	s.readID = id
	return nil
}

func TestHealthReportsTransportAndOpenProject(t *testing.T) {
	hub := NewHub()
	hub.RegisterProject("/tmp/acme", "acme", "main", "go", "principal")
	srv := NewServer(hub, &fakeDeps{}, Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.RemoteAddr = "100.64.1.5:4242"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["online"] != true || body["transport"] != "tailscale" {
		t.Fatalf("health = %s", rec.Body.String())
	}
	open, _ := body["openProject"].(map[string]any)
	if open["name"] != "acme" {
		t.Fatalf("projeto = %v", body["openProject"])
	}

	relayReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	relayReq = srv.AttachLocal(relayReq)
	relayRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(relayRec, relayReq)
	if !strings.Contains(relayRec.Body.String(), `"transport":"relay"`) {
		t.Fatalf("relay = %s", relayRec.Body.String())
	}
}

func TestActivityEventAndMobileRoutes(t *testing.T) {
	hub := NewHub()
	controls := &stubControls{commit: "ok", prURL: "https://example.com/pull/1", routines: []RoutineInfo{{ID: "r1", Expr: "0 9 * * *", Text: "bom dia", Enabled: true}}}
	srv := NewServer(hub, controls, Config{})
	ch, unsub := hub.Subscribe()
	defer unsub()
	hub.HandleLaneEvent(LaneEvent{LaneID: "lane-1", Team: "principal", Event: orchestrator.Event{Kind: "tool_call", ToolName: "edit_file", Detail: "main.go", Author: "dev"}})
	got := <-ch
	if got.Type != "lane_event" {
		t.Fatalf("primeiro = %s", got.Type)
	}
	got = <-ch
	if got.Type != "activity" || !strings.Contains(string(got.Data), `"phase":"file"`) {
		t.Fatalf("activity = %s %s", got.Type, got.Data)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/project/permission", strings.NewReader(`{"workspace":"/tmp/acme","permissionMode":"full"}`))
	req = srv.AttachLocal(req)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || controls.mode != "full" {
		t.Fatalf("permission %d %s mode=%s", rec.Code, rec.Body.String(), controls.mode)
	}

	bare := NewServer(NewHub(), &fakeDeps{}, Config{})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/git/commit", strings.NewReader(`{"workspace":"/tmp/acme","message":"wip"}`))
	req = bare.AttachLocal(req)
	bare.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("sem IDE = %d", rec.Code)
	}
}

func TestLoopRoutesListAndStop(t *testing.T) {
	controls := &stubControls{loops: []LoopInfo{{ID: "fix-auth", Status: "running", Iteration: 2, LastVerdict: "continue"}}}
	srv := NewServer(NewHub(), controls, Config{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/loops?workspace=/tmp/acme", nil)
	req = srv.AttachLocal(req)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"fix-auth"`) {
		t.Fatalf("lista %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/loops?workspace=/tmp/acme&id=fix-auth", nil)
	req = srv.AttachLocal(req)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"iteration":2`) {
		t.Fatalf("item %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/loops/stop", strings.NewReader(`{"workspace":"/tmp/acme","id":"fix-auth"}`))
	req = srv.AttachLocal(req)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || controls.stopped != "fix-auth" {
		t.Fatalf("stop %d %s stopped=%s", rec.Code, rec.Body.String(), controls.stopped)
	}
}

func TestRequestTransportTailscale(t *testing.T) {
	if got := requestTransport(nil); got != "lan" {
		t.Fatal(got)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.9:9"
	if requestTransport(req) != "lan" {
		t.Fatal(requestTransport(req))
	}
}
