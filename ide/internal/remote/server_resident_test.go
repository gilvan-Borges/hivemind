package remote

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/resident"
	"github.com/gilvan-Borges/HiveMind/internal/secretx"
)

func TestBridgeRejectsUnknownRunModeAndAcceptsResident(t *testing.T) {
	srv, _, deps, token := newTestServer(t)

	// Unknown runMode MUST be rejected with 400 Bad Request (§15).
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/runs/start", token, map[string]any{
		"laneId":    "lane-1",
		"workspace": "/tmp/proj",
		"objective": "oi",
		"runMode":   "modo-desconhecido",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("runMode desconhecido deveria devolver 400, veio %d (%s)", rec.Code, rec.Body.String())
	}

	// runMode: "resident" is accepted and normalized to fixed lane "resident" (§1.10).
	rec = doJSON(t, srv, http.MethodPost, "/api/v1/runs/start", token, map[string]any{
		"laneId":    "qualquer-coisa",
		"workspace": "",
		"objective": "resuma meus projetos",
		"runMode":   "resident",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("runMode resident deveria devolver 202, veio %d (%s)", rec.Code, rec.Body.String())
	}
	if len(deps.started) != 1 {
		t.Fatalf("esperado 1 chamada RunStart, veio %d", len(deps.started))
	}
	if deps.started[0].LaneID != resident.LaneID || deps.started[0].RunMode != resident.RunMode {
		t.Fatalf("esperado laneId=resident e runMode=resident, veio %+v", deps.started[0])
	}
}

func TestBridgeResidentRemoteRequiresApprovalOverExistingRoutes(t *testing.T) {
	ws := t.TempDir()
	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "README.md"), []byte("Projeto Alpha em Go"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	scripted := &modelx.Scripted{
		Fn: func(req *model.LLMRequest, call int) *model.LLMResponse {
			if call == 1 {
				return modelx.FunctionCall("read_file", "c1", map[string]any{"path": "README.md"})
			}
			return modelx.Text("Li o README.md: Projeto Alpha em Go.")
		},
	}

	hub := NewHub()
	sessions := idecore.NewSessionManager(hub.HandleLaneEvent)
	resMgr := idecore.NewResidentManager(idecore.ResidentManagerOptions{
		BaseDir:   baseDir,
		Sessions:  sessions,
		TestModel: scripted,
	})
	t.Cleanup(resMgr.Close)
	adapter := &Adapter{Sessions: sessions, Resident: resMgr, Hub: hub}
	path := filepath.Join(t.TempDir(), "remote-bridge.json")
	cfg, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatalf("LoadConfigFrom: %v", err)
	}
	srv := NewServer(hub, adapter, cfg)
	srv.configPath = path
	srv.secrets = secretx.NewMemoryVault()
	srv.urls = []string{"http://127.0.0.1:47890"}
	sess := pairDevice(t, srv)

	ch, unsub := hub.Subscribe()
	defer unsub()

	// Start remote resident turn via existing POST /api/v1/runs/start (no new route, no new port).
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/runs/start", sess, map[string]any{
		"laneId":    "resident",
		"workspace": ws,
		"objective": "o que diz o README?",
		"runMode":   "resident",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /api/v1/runs/start resident: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Remote read MUST trigger approval_required via existing SSE stream (§10, §12.4).
	var approvalID string
	deadline := time.After(5 * time.Second)
	for approvalID == "" {
		select {
		case ev := <-ch:
			if ev.Type != "lane_event" {
				continue
			}
			var le idecore.LaneEvent
			if err := json.Unmarshal(ev.Data, &le); err != nil {
				continue
			}
			if le.LaneID == resident.LaneID && le.Event.Kind == "approval_required" && le.Event.ApprovalID != "" {
				approvalID = le.Event.ApprovalID
			}
		case <-deadline:
			t.Fatal("timeout aguardando approval_required para leitura remota do residente")
		}
	}

	// Resolve approval via existing POST /api/v1/approvals/resolve.
	recAppr := doJSON(t, srv, http.MethodPost, "/api/v1/approvals/resolve", sess, map[string]any{
		"approvalId": approvalID,
		"approved":   true,
	})
	if recAppr.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/approvals/resolve: status=%d body=%s", recAppr.Code, recAppr.Body.String())
	}

	// Wait for finish event with the read result.
	finished := false
	deadline2 := time.After(5 * time.Second)
	for !finished {
		select {
		case ev := <-ch:
			if ev.Type != "lane_event" {
				continue
			}
			var le idecore.LaneEvent
			if err := json.Unmarshal(ev.Data, &le); err != nil {
				continue
			}
			if le.LaneID == resident.LaneID && le.Event.Kind == "finish" && strings.Contains(le.Event.Text, "Projeto Alpha") {
				finished = true
			}
		case <-deadline2:
			t.Fatal("timeout aguardando finish do turno residente após aprovação")
		}
	}
}
