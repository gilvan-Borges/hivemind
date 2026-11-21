package remote

import (
	"net/http"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/resident"
)

// The resident-over-the-bridge integration with the IDE services (ResidentManager)
// lives in ide/internal/idecore (remote_resident_test.go); here we pin the
// HTTP contract only: unknown runModes are rejected and "resident" is
// normalized to the fixed resident lane (§1.10, §15).
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
