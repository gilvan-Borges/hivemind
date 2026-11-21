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
