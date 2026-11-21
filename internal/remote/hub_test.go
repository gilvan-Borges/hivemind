package remote

import (
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
)

func TestHubRunLifecycle(t *testing.T) {
	hub := NewHub()
	hub.RegisterRun("l1", "C:/w", "principal", "objetivo", "sess-1", "plan")

	snaps := hub.Snapshots("")
	if len(snaps) != 1 {
		t.Fatalf("esperava 1 lane, veio %d", len(snaps))
	}
	s := snaps[0]
	if !s.Running || s.SessionID != "sess-1" || s.RunMode != "plan" || s.Objective != "objetivo" || s.Workspace != "C:/w" {
		t.Fatalf("snapshot inicial errado: %+v", s)
	}

	hub.HandleLaneEvent(LaneEvent{LaneID: "l1", Team: "principal", Event: orchestrator.Event{Kind: "stage", Stage: "developing"}})
	hub.HandleLaneEvent(LaneEvent{LaneID: "l1", Team: "principal", Event: orchestrator.Event{Kind: "usage", InputTokens: 100, OutputTokens: 20}})
	hub.HandleLaneEvent(LaneEvent{LaneID: "l1", Team: "principal", Event: orchestrator.Event{Kind: "approval_required", ApprovalID: "l1:appr:1", Text: "posso commitar?", Label: "Permissão Necessária"}})

	snaps = hub.Snapshots("C:/w")
	s = snaps[0]
	if !s.Running || s.Stage != "developing" {
		t.Fatalf("estado após stage errado: %+v", s)
	}
	if s.TurnTokens != 120 {
		t.Fatalf("tokens deveriam ser 120, veio %d", s.TurnTokens)
	}
	if len(s.PendingApprovals) != 1 || s.PendingApprovals[0].ApprovalID != "l1:appr:1" || s.PendingApprovals[0].Prompt != "posso commitar?" {
		t.Fatalf("aprovação pendente errada: %+v", s.PendingApprovals)
	}

	hub.MarkApprovalResolved("l1:appr:1", true)
	snaps = hub.Snapshots("C:/w")
	if len(snaps[0].PendingApprovals) != 0 {
		t.Fatalf("aprovação deveria sumir após resolução: %+v", snaps[0].PendingApprovals)
	}

	hub.HandleLaneEvent(LaneEvent{LaneID: "l1", Team: "principal", Event: orchestrator.Event{Kind: "finish", Text: "pronto"}})
	snaps = hub.Snapshots("C:/w")
	if snaps[0].Running || snaps[0].Stage != "done" {
		t.Fatalf("estado após finish errado: %+v", snaps[0])
	}
}

func TestHubWorkspaceAttribution(t *testing.T) {
	hub := NewHub()
	// Same laneID used in two workspaces must not collide in the Hub index.
	hub.RegisterRun("lane-x", "C:/repo1", "t1", "obj1", "", "auto")
	hub.RegisterRun("lane-x", "C:/repo2", "t2", "obj2", "", "auto")

	hub.HandleLaneEvent(LaneEvent{LaneID: "lane-x", Team: "t2", Event: orchestrator.Event{Kind: "error", Text: "falhou"}})

	snaps := hub.Snapshots("C:/repo2")
	if len(snaps) != 1 {
		t.Fatalf("esperava 1 lane no repo2, veio %+v", snaps)
	}
	if snaps[0].LastError != "falhou" || snaps[0].Workspace != "C:/repo2" {
		t.Fatalf("atribuição de workspace errada: %+v", snaps[0])
	}
	// Events carry no workspace; the hub maps via the last registered lane.
	// repo1's lane-x snapshot must be untouched by the repo2 event apart from
	// the shared recent-events buffer behavior (still running).
	snaps = hub.Snapshots("C:/repo1")
	if len(snaps) != 1 || !snaps[0].Running {
		t.Fatalf("repo1 deveria continuar rodando: %+v", snaps)
	}
}

func TestHubRunErrorAndCancel(t *testing.T) {
	hub := NewHub()
	hub.RegisterRun("l1", "C:/w", "t", "obj", "", "auto")
	hub.HandleRunError("l1", "C:/w", errTest("sem equipe"))

	snaps := hub.Snapshots("")
	if snaps[0].Running || snaps[0].LastError != "sem equipe" {
		t.Fatalf("erro de run não refletido: %+v", snaps[0])
	}

	hub.RegisterRun("l2", "C:/w", "t", "obj", "", "auto")
	hub.MarkCancel("l2")
	if hub.Snapshots("")[0].Running && hub.Snapshots("")[0].LaneID == "l2" {
		t.Fatalf("cancel deveria parar a lane l2: %+v", hub.Snapshots(""))
	}
}

func TestHubAutoReviewRegistration(t *testing.T) {
	hub := NewHub()
	hub.HandleAutoReviewStatus(AutoReviewStatus{Workspace: "C:/w", Enabled: true, State: "polling"})
	st, ok := hub.AutoReviewSnapshot("C:/w")
	if !ok || !st.Enabled || st.State != "polling" {
		t.Fatalf("autoreview snapshot errado: %+v (ok=%v)", st, ok)
	}
	// lane-autoreview events must be attributed to the workspace.
	hub.HandleLaneEvent(LaneEvent{LaneID: "lane-autoreview", Team: "t", Event: orchestrator.Event{Kind: "stage", Stage: "reviewing"}})
	snaps := hub.Snapshots("C:/w")
	found := false
	for _, s := range snaps {
		if s.LaneID == "lane-autoreview" && s.Workspace == "C:/w" && s.Stage == "reviewing" {
			found = true
		}
	}
	if !found {
		t.Fatalf("lane-autoreview deveria pertencer a C:/w: %+v", snaps)
	}
}

func TestHubSubscribeAndBroadcast(t *testing.T) {
	hub := NewHub()
	ch, unsub := hub.Subscribe()

	hub.RegisterRun("l1", "C:/w", "t", "obj", "", "auto")
	hub.HandleLaneEvent(LaneEvent{LaneID: "l1", Team: "t", Event: orchestrator.Event{Kind: "stage", Stage: "chief"}})

	ev := <-ch
	if ev.Type != "lane_event" {
		t.Fatalf("evento errado: %+v", ev)
	}
	hub.HandleLaneEvent(LaneEvent{
		LaneID: "l1",
		Team:   "t",
		Event: orchestrator.Event{
			Kind:              "model_route",
			Stage:             "dev",
			Author:            "dev",
			Provider:          "openrouter",
			Model:             "deepseek/deepseek-chat",
			Tier:              "cheap",
			Reasons:           []string{"cheap_default"},
			EstimatedMicroUSD: 120,
			DecisionMicros:    250,
			Text:              "Jev escolheu openrouter/deepseek/deepseek-chat (faixa cheap · cheap_default)",
		},
	})
	routeEv := <-ch
	if routeEv.Type != "lane_event" || !strings.Contains(string(routeEv.Data), `"kind":"model_route"`) || !strings.Contains(string(routeEv.Data), `"provider":"openrouter"`) {
		t.Fatalf("evento model_route inválido no SSE: %s", string(routeEv.Data))
	}
	snaps := hub.Snapshots("C:/w")
	if len(snaps) != 1 || len(snaps[0].RecentEvents) == 0 {
		t.Fatalf("recentEvents não registrou model_route: %+v", snaps)
	}
	last := snaps[0].RecentEvents[len(snaps[0].RecentEvents)-1]
	if last.Kind != "model_route" || last.Provider != "openrouter" || last.Model != "deepseek/deepseek-chat" {
		t.Fatalf("item model_route errado no snapshot: %+v", last)
	}
	unsub()
	_, ok := <-ch
	if ok {
		t.Fatal("canal deveria estar fechado após unsubscribe")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
