package idecore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/remote"
	"github.com/gilvan-Borges/HiveMind/internal/resident"
)

// TestAdapterResidentRunRemoteRequiresApproval pins the resident-over-bridge
// contract end to end on the IDE services: a remote-initiated resident turn
// (Adapter.RunStart with runMode "resident") routes to the ResidentManager,
// every gated tool call surfaces as a pending approval on the hub, and the
// turn only finishes after ApprovalResolve. (The LAN/HTTP wire side of this
// flow is covered by internal/remote tests; this one exercises the IDE
// services the wire drives.)
func TestAdapterResidentRunRemoteRequiresApproval(t *testing.T) {
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

	hub := remote.NewHub()
	sessions := NewSessionManager(hub.HandleLaneEvent)
	resMgr := NewResidentManager(ResidentManagerOptions{
		BaseDir:   baseDir,
		Sessions:  sessions,
		TestModel: scripted,
	})
	t.Cleanup(resMgr.Close)
	adapter := &Adapter{Sessions: sessions, Resident: resMgr, Hub: hub}

	if err := adapter.RunStart(context.Background(), "celular", ws, "", "o que diz o README?", "", "", "resident", nil); err != nil {
		t.Fatalf("RunStart resident: %v", err)
	}

	// The gated read must surface a pending approval on the hub.
	approvalID := ""
	deadline := time.Now().Add(8 * time.Second)
	for approvalID == "" && time.Now().Before(deadline) {
		for _, snap := range hub.Snapshots(ws) {
			if snap.LaneID == resident.LaneID && len(snap.PendingApprovals) > 0 {
				approvalID = snap.PendingApprovals[0].ApprovalID
				break
			}
		}
		if approvalID == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if approvalID == "" {
		t.Fatal("timeout aguardando approval pendente para leitura remota do residente")
	}

	if !adapter.ApprovalResolve(approvalID, true) {
		t.Fatal("ApprovalResolve deveria aceitar o gate pendente")
	}

	// The turn finishes with the scripted read result.
	finished := false
	deadline = time.Now().Add(8 * time.Second)
	for !finished && time.Now().Before(deadline) {
		for _, snap := range hub.Snapshots(ws) {
			if snap.LaneID != resident.LaneID {
				continue
			}
			if !snap.Running && snap.Stage == "done" {
				for _, ev := range snap.RecentEvents {
					if ev.Kind == "finish" && strings.Contains(ev.Text, "Projeto Alpha") {
						finished = true
					}
				}
			}
		}
		if !finished {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !finished {
		t.Fatal("timeout aguardando finish do turno residente após aprovação")
	}
}

// TestAdapterResidentRunCancel forwards remote cancels to the ResidentManager.
func TestAdapterResidentRunCancel(t *testing.T) {
	hub := remote.NewHub()
	sessions := NewSessionManager(hub.HandleLaneEvent)
	resMgr := NewResidentManager(ResidentManagerOptions{
		BaseDir:  t.TempDir(),
		Sessions: sessions,
	})
	t.Cleanup(resMgr.Close)
	adapter := &Adapter{Sessions: sessions, Resident: resMgr, Hub: hub}

	if adapter.RunCancel(resident.LaneID) {
		t.Fatal("RunCancel sem turno ativo deveria ser false (fail-closed)")
	}
}
