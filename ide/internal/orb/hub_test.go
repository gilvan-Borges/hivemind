package orb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestHubAuthStateAndActions(t *testing.T) {
	actions := make(chan OrbAction, 4)
	hub := NewHub(func(act OrbAction) {
		actions <- act
	})
	if err := hub.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer hub.Close()

	base := "http://" + hub.Addr()

	// Unauthorized request must be rejected.
	resp, err := http.Get(base + "/state")
	if err != nil {
		t.Fatalf("GET /state: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	hub.SetProject("hivemind")
	hub.SetIDEVisible(false)
	hub.PushNotice(OrbNotice{
		ID:      "n-1",
		Title:   "@marcus marcou você no PR #42",
		Body:    "fix: valida token",
		Mood:    "attention",
		Surface: "github",
	})

	req, _ := http.NewRequest(http.MethodGet, base+"/state", nil)
	req.Header.Set("Authorization", "Bearer "+hub.Token())
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("authorized GET /state: %v", err)
	}
	defer resp.Body.Close()
	var snap OrbSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snap.IDEVisible || snap.ProjectName != "hivemind" || snap.PendingCount != 1 || snap.Mood != "attention" {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}

	// Send open_notice action -> removes item and invokes callback.
	body, _ := json.Marshal(OrbAction{Action: "open_notice", NoticeID: "n-1"})
	postReq, _ := http.NewRequest(http.MethodPost, base+"/action", bytes.NewReader(body))
	postReq.Header.Set("Authorization", "Bearer "+hub.Token())
	postReq.Header.Set("Content-Type", "application/json")
	postResp, err := http.DefaultClient.Do(postReq)
	if err != nil {
		t.Fatalf("POST /action: %v", err)
	}
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", postResp.StatusCode)
	}

	select {
	case got := <-actions:
		if got.Action != "open_notice" || got.NoticeID != "n-1" {
			t.Fatalf("unexpected action: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for action callback")
	}

	after := hub.Snapshot()
	if after.PendingCount != 0 || after.Mood != "idle" {
		t.Fatalf("expected cleared state after open_notice, got %+v", after)
	}
}

func TestHubMaxItemsCap(t *testing.T) {
	hub := NewHub(nil)
	for i := 0; i < maxOrbItems+5; i++ {
		hub.PushNotice(OrbNotice{
			ID:    fmt.Sprintf("id-%d", i),
			Title: fmt.Sprintf("Evento %d", i),
			Mood:  "attention",
		})
	}
	snap := hub.Snapshot()
	if len(snap.Items) != maxOrbItems {
		t.Fatalf("expected %d items, got %d", maxOrbItems, len(snap.Items))
	}
	if snap.Items[0].ID != fmt.Sprintf("id-%d", maxOrbItems+4) {
		t.Fatalf("expected newest item first, got %s", snap.Items[0].ID)
	}
}

func TestHubResidentSnapshotAndStreaming(t *testing.T) {
	hub := NewHub(nil)

	// Initial snapshot sync (H001/H003 shape).
	hub.SetResidentState(OrbResidentState{
		State:            "ocioso",
		Workstream:       "geral",
		Workspace:        "C:\\hivemind",
		Provider:         "openrouter",
		Model:            "auto",
		SandboxAvailable: true,
		SandboxMechanism: "appcontainer",
		Messages:         []OrbResidentMessage{{ID: "old-1", Kind: "user", Text: "hi"}},
	})
	snap := hub.Snapshot()
	if snap.Resident.State != "ocioso" || snap.Resident.Workstream != "geral" || snap.Resident.Workspace != "C:\\hivemind" {
		t.Fatalf("unexpected resident snapshot: %+v", snap.Resident)
	}
	if len(snap.Resident.Messages) != 1 || snap.Resident.Messages[0].ID != "old-1" {
		t.Fatalf("expected seeded message, got %+v", snap.Resident.Messages)
	}

	// Live streaming: user message + partial chief text chunks must merge into ONE bubble.
	hub.ApplyResidentEvent(OrbResidentEvent{Kind: "user_message", Text: "lista os últimos commits"})
	hub.ApplyResidentEvent(OrbResidentEvent{Kind: "stage", Text: "pensando"})
	hub.ApplyResidentEvent(OrbResidentEvent{Kind: "text", Text: "Vou ver o ", Partial: true})
	hub.ApplyResidentEvent(OrbResidentEvent{Kind: "text", Text: "git log.", Partial: true})
	st := hub.Resident()
	if !st.Running || st.State != "pensando" {
		t.Fatalf("expected running/pensando, got %+v", st)
	}
	chief := st.Messages[len(st.Messages)-1]
	if chief.Kind != "chief" || chief.Text != "Vou ver o git log." {
		t.Fatalf("expected merged streaming chief message, got %+v", chief)
	}

	// Tool call + result lifecycle.
	hub.ApplyResidentEvent(OrbResidentEvent{Kind: "tool_call", ToolName: "read_file", ToolDetail: "README.md", RiskTier: "read"})
	hub.ApplyResidentEvent(OrbResidentEvent{Kind: "tool_result"})
	st = hub.Resident()
	tool := st.Messages[len(st.Messages)-1]
	if tool.Kind != "tool" || tool.ToolName != "read_file" || tool.Active {
		t.Fatalf("expected closed tool card, got %+v", tool)
	}

	// Approval flow: attention mood, pending card, then approved resolution.
	hub.ApplyResidentEvent(OrbResidentEvent{
		Kind:       "approval_required",
		Label:      "Escrever arquivo",
		Text:       "Posso gravar o relatório?",
		ToolName:   "write_file",
		RiskTier:   "write",
		ApprovalID: "resident:appr:9",
	})
	st = hub.Resident()
	if hub.Snapshot().Mood != "attention" || st.State != "aguardando você" {
		t.Fatalf("expected attention/aguardando, got mood=%s state=%s", hub.Snapshot().Mood, st.State)
	}
	appr := st.Messages[len(st.Messages)-1]
	if appr.Kind != "approval" || appr.ApprovalID != "resident:appr:9" || appr.Resolved != "" {
		t.Fatalf("expected pending approval, got %+v", appr)
	}
	hub.ApplyResidentEvent(OrbResidentEvent{Kind: "approval_resolved", ApprovalID: "resident:appr:9", Approved: true})
	st = hub.Resident()
	if st.Messages[len(st.Messages)-1].Resolved != "approved" {
		t.Fatalf("expected resolved=approved, got %+v", st.Messages[len(st.Messages)-1])
	}
	if hub.Snapshot().Mood != "idle" {
		t.Fatalf("expected mood back to idle, got %s", hub.Snapshot().Mood)
	}

	// Finish must clear running state.
	hub.ApplyResidentEvent(OrbResidentEvent{Kind: "stage"})
	hub.ApplyResidentEvent(OrbResidentEvent{Kind: "finish"})
	if hub.Resident().Running || hub.Resident().State != "ocioso" {
		t.Fatalf("expected idle resident after finish, got %+v", hub.Resident())
	}
}

func TestHubResidentMessagesCap(t *testing.T) {
	hub := NewHub(nil)
	for i := 0; i < maxOrbResidentMessages+10; i++ {
		hub.ApplyResidentEvent(OrbResidentEvent{Kind: "user_message", Text: fmt.Sprintf("msg %d", i)})
	}
	msgs := hub.Resident().Messages
	if len(msgs) != maxOrbResidentMessages {
		t.Fatalf("expected %d messages, got %d", maxOrbResidentMessages, len(msgs))
	}
	if msgs[0].Text != fmt.Sprintf("msg %d", 10) {
		t.Fatalf("expected oldest surviving message first, got %q", msgs[0].Text)
	}
}

func TestHubResidentActions(t *testing.T) {
	actions := make(chan OrbAction, 6)
	hub := NewHub(func(act OrbAction) {
		actions <- act
	})
	if err := hub.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer hub.Close()
	base := "http://" + hub.Addr()

	post := func(body map[string]any) *http.Response {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, base+"/action", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+hub.Token())
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /action: %v", err)
		}
		return resp
	}

	// resident_send requires non-empty text.
	if resp := post(map[string]any{"action": "resident_send", "text": "  "}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty resident_send, got %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	// resident_approve requires approvalId.
	if resp := post(map[string]any{"action": "resident_approve", "approved": true}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for approval without id, got %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	cases := []map[string]any{
		{"action": "resident_send", "text": "roda os testes", "workstream": "geral"},
		{"action": "resident_cancel"},
		{"action": "resident_approve", "approvalId": "resident:appr:1", "approved": true},
		{"action": "resident_clear", "workstream": "geral"},
		{"action": "open_resident"},
	}
	for _, body := range cases {
		resp := post(body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 for %v, got %d", body["action"], resp.StatusCode)
		}
		select {
		case got := <-actions:
			if got.Action != body["action"] {
				t.Fatalf("expected action %q, got %q", body["action"], got.Action)
			}
			if body["action"] == "resident_send" && (got.Text != "roda os testes" || got.Workstream != "geral") {
				t.Fatalf("unexpected resident_send payload: %+v", got)
			}
			if body["action"] == "resident_approve" && (got.ApprovalID != "resident:appr:1" || !got.Approved) {
				t.Fatalf("unexpected resident_approve payload: %+v", got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for action %q", body["action"])
		}
	}
}
