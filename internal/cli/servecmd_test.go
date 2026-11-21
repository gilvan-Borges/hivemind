package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/remote"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestServeCommandRegistered(t *testing.T) {
	root := newRoot()
	cmd, _, err := root.Find([]string{"serve"})
	if err != nil {
		t.Fatalf("subcomando serve deveria existir: %v", err)
	}
	if cmd.Use == "" || !strings.HasPrefix(cmd.Use, "serve") {
		t.Fatalf("Use errado: %s", cmd.Use)
	}
}

func TestServeCommandAcceptsOptionalWorkspaceArg(t *testing.T) {
	root := newRoot()
	if err := root.Flags().Parse(nil); err != nil {
		t.Fatal(err)
	}
	cmd, _, err := root.Find([]string{"serve", "some-dir"})
	if err != nil {
		t.Fatalf("serve [workspace] deveria ser aceito: %v", err)
	}
	if cmd.Args == nil {
		t.Fatal("serve deveria validar seus argumentos")
	}
}

func TestServeHostRunStartRequiresConfiguredTeam(t *testing.T) {
	hub := remote.NewHub()
	host := newServeHost(hub)
	err := host.RunStart(context.Background(), "lane-1", t.TempDir(), "", "objetivo", "", "", "squad", nil)
	if err == nil {
		t.Fatal("RunStart sem team.yaml configurado deveria falhar com orientação")
	}
	if !strings.Contains(err.Error(), "time") {
		t.Fatalf("erro deveria orientar sobre o time: %v", err)
	}
}

func TestServeHostRunStartRegistersLaneAndCancelStopsIt(t *testing.T) {
	ws := t.TempDir()
	if err := team.Save(ws, team.File{
		Version:  1,
		Active:   "padrao",
		Settings: team.DefaultSettings(),
		Teams: []team.Team{{
			Name: "padrao",
			Agents: []team.Agent{
				{Name: "chefe", Role: team.RoleProjectManager, Provider: "gemini", Model: "gemini-flash-latest"},
				{Name: "dev", Role: team.RoleDeveloper, Provider: "gemini", Model: "gemini-flash-latest"},
				{Name: "qa", Role: team.RoleQA, Provider: "gemini", Model: "gemini-flash-latest"},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	hub := remote.NewHub()
	host := newServeHost(hub)
	if err := host.RunStart(context.Background(), "lane-x", ws, "padrao", "objetivo de teste", "", "", "squad", nil); err != nil {
		t.Fatalf("RunStart com time válido: %v", err)
	}

	snaps := hub.Snapshots(ws)
	if len(snaps) == 0 {
		t.Fatal("lane não registrada no hub")
	}
	if snaps[0].LaneID != "lane-x" || !snaps[0].Running {
		t.Fatalf("snapshot inesperado: %+v", snaps[0])
	}

	if !host.RunCancel("lane-x") {
		t.Fatal("RunCancel deveria parar a lane ativa")
	}
	// Idempotent: second cancel reports false.
	if host.RunCancel("lane-x") {
		t.Fatal("segundo RunCancel não deveria achar cancel ativo")
	}
}

func TestServeHostApprovalGateResolvesFromDeps(t *testing.T) {
	hub := remote.NewHub()
	host := newServeHost(hub)

	type result struct {
		ok  bool
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		resCh <- result{ok: host.requestApproval("lane-g", "padrao", "shell", "rm -rf teste")}
	}()

	deadline := time.Now().Add(5 * time.Second)
	var approved bool
	for time.Now().Before(deadline) {
		if host.approvalCount() > 0 {
			approved = host.ApprovalResolve("cli-1", true)
			if approved {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !approved {
		t.Fatal("gate pendente deveria ser resolvível via ApprovalResolve")
	}
	select {
	case res := <-resCh:
		if !res.ok {
			t.Fatal("requestApproval deveria receber a aprovação")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("requestApproval não retornou após a resolução")
	}

	if host.ApprovalResolve("cli-inexistente", true) {
		t.Fatal("resolver gate inexistente deveria retornar false")
	}
}

func TestServeHostUnimplementedRoutesFailClosedWithGuidance(t *testing.T) {
	hub := remote.NewHub()
	host := newServeHost(hub)
	ws := t.TempDir()

	if _, err := host.Diff(context.Background(), ws); err == nil {
		t.Fatal("Diff deveria falhar fechado com orientação até o W3-04")
	}
	if _, err := host.ListPRs(context.Background(), ws); err == nil {
		t.Fatal("ListPRs deveria falhar fechado com orientação até o W3-04")
	}
	if _, err := host.ListIssues(context.Background(), ws); err == nil {
		t.Fatal("ListIssues deveria falhar fechado com orientação até o W3-04")
	}
	if st := host.AutoReviewStatus(ws); st.State != "idle" {
		t.Fatalf("AutoReviewStatus deveria reportar idle, got %q", st.State)
	}
}

func TestServeHostSessionsUseSharedStore(t *testing.T) {
	hub := remote.NewHub()
	host := newServeHost(hub)
	ws := filepath.Join(t.TempDir(), "proj")
	if err := team.Save(ws, team.File{Version: 1, Active: "t", Settings: team.DefaultSettings(), Teams: []team.Team{{Name: "t", Agents: []team.Agent{{Name: "dev", Role: team.RoleDeveloper, Provider: "gemini", Model: "m"}}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := host.ListSessions(context.Background(), ws); err != nil {
		t.Fatalf("ListSessions deveria ler o store compartilhado: %v", err)
	}
}

func (s *serveHost) approvalCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.approvals)
}
