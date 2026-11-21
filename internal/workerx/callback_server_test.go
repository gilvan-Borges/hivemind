package workerx

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestCallbackServer_HeartbeatAskAndIdempotentDone(t *testing.T) {
	reg := NewDispatchRegistry()
	var heartbeats []HeartbeatEvent
	srv, err := StartCallbackServer(context.Background(), "disp-1", "task-1", reg, Callbacks{
		OnHeartbeat: func(hb HeartbeatEvent) { heartbeats = append(heartbeats, hb) },
		OnAsk: func(_ context.Context, req AskRequest) (AskResponse, error) {
			return AskResponse{Approved: true, Answer: "prossiga com API v2"}, nil
		},
	})
	if err != nil {
		t.Fatalf("StartCallbackServer: %v", err)
	}
	defer srv.Close()

	post := func(path string, payload any, auth bool) *http.Response {
		t.Helper()
		b, _ := json.Marshal(payload)
		req, err := http.NewRequest(http.MethodPost, srv.URL()+path, bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if auth {
			req.Header.Set("Authorization", "Bearer "+srv.Token())
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// Unauthenticated request rejected.
	rUnauth := post("/heartbeat", HeartbeatEvent{Message: "oi"}, false)
	rUnauth.Body.Close()
	if rUnauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rUnauth.StatusCode)
	}

	// Heartbeat succeeds.
	rHB := post("/heartbeat", HeartbeatEvent{Message: "compilando"}, true)
	rHB.Body.Close()
	if rHB.StatusCode != http.StatusOK || len(heartbeats) != 1 || heartbeats[0].Message != "compilando" {
		t.Fatalf("unexpected heartbeat result status=%d hbs=%#v", rHB.StatusCode, heartbeats)
	}

	// Blocking ask returns human answer.
	rAsk := post("/ask", AskRequest{Question: "Usar v2?"}, true)
	defer rAsk.Body.Close()
	var askResp AskResponse
	_ = json.NewDecoder(rAsk.Body).Decode(&askResp)
	if !askResp.Approved || askResp.Answer != "prossiga com API v2" {
		t.Fatalf("unexpected AskResponse: %+v", askResp)
	}

	// First /done accepted, duplicate /done rejected with 409 Conflict.
	rDone1 := post("/done", DoneEvent{Outcome: "success", FilesModified: []string{"api.go"}}, true)
	rDone1.Body.Close()
	if rDone1.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on first /done, got %d", rDone1.StatusCode)
	}
	rDone2 := post("/done", DoneEvent{Outcome: "failed", FilesModified: []string{"other.go"}}, true)
	rDone2.Body.Close()
	if rDone2.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on duplicate /done, got %d", rDone2.StatusCode)
	}
	stored, ok := reg.Get("disp-1")
	if !ok || stored.Outcome != "success" {
		t.Fatalf("expected stored first DoneEvent intact, got %+v ok=%v", stored, ok)
	}
}
