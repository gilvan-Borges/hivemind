package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/ide/internal/orb"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// idecoreEventUserMessage builds the lane event that echoes an Orb-originated
// message into the IDE Agent Hive tab with the correct "Você" authoring.
func idecoreEventUserMessage(text string) orchestrator.Event {
	return orchestrator.Event{
		Kind:   "user_message",
		Author: "orb",
		Origin: "orb",
		Label:  "Você",
		Text:   text,
	}
}

type orbHostState struct {
	mu        sync.Mutex
	hub       *orb.Hub
	cmd       *exec.Cmd
	running   bool
	forceQuit bool
}

func (a *App) startOrbHost() {
	if a.smokeCheck || a.ctx == nil {
		return
	}
	hub := orb.NewHub(a.handleOrbAction)
	if err := hub.Start(); err != nil {
		log.Printf("aviso: orb host não iniciou: %v", err)
		return
	}
	a.orb.mu.Lock()
	a.orb.hub = hub
	a.orb.mu.Unlock()
	a.hookResidentLaneEvents()
	if a.resident == nil || a.resident.IsOrbEnabled() {
		if err := a.launchOrbWindow(); err != nil {
			log.Printf("aviso: janela do Orb flutuante não iniciou: %v", err)
		}
	}
	go a.orbSyncResidentSnapshot()
}

// hookResidentLaneEvents mirrors every idecore LaneEvent of lane "resident"
// into the Orb Hub so the floating Agent Hive chats live with the very same
// session used by the IDE tab (Spec 007 H003). The wrap happens once during
// Wails startup, before any binding or scheduler is able to fire a resident
// turn, so the swap of SessionManager.Emit is race-free in practice.
func (a *App) hookResidentLaneEvents() {
	if a.sessions == nil {
		return
	}
	prev := a.sessions.Emit
	a.sessions.Emit = func(ev idecore.LaneEvent) {
		if prev != nil {
			prev(ev)
		}
		a.orbMirrorResidentEvent(ev)
	}
}

// orbMirrorResidentEvent converts one resident LaneEvent into the neutral
// OrbResidentEvent shape applied on top of the mirrored Agent Hive state.
func (a *App) orbMirrorResidentEvent(ev idecore.LaneEvent) {
	if ev.LaneID != "resident" {
		return
	}
	a.orb.mu.Lock()
	hub := a.orb.hub
	a.orb.mu.Unlock()
	if hub == nil {
		return
	}
	e := ev.Event
	orbEv := orb.OrbResidentEvent{
		Kind:       e.Kind,
		Text:       e.Text,
		Partial:    e.Partial,
		Label:      e.Label,
		Role:       e.Role,
		MessageID:  e.MessageID,
		ToolName:   e.ToolName,
		ToolDetail: e.Detail,
		RiskTier:   e.Gate,
		ApprovalID: e.ApprovalID,
	}
	switch e.Kind {
	case "approval_resolved":
		low := strings.ToLower(e.Text)
		orbEv.Approved = !strings.Contains(low, "recus") && !strings.Contains(low, "negad")
		hub.ApplyResidentEvent(orbEv)
	case "user_message", "text", "tool_call", "tool_result", "approval_required", "stage", "error", "finish", "shared_message":
		hub.ApplyResidentEvent(orbEv)
	}
}

// orbActiveWorkspace resolves the workspace the Agent Hive should operate on
// when a turn is started from the floating Orb. It matches the active project
// name (already pushed to the Orb by OpenProject via orbSetProject) against
// the projects registered in the mobile bridge hub.
func (a *App) orbActiveWorkspace() string {
	var name string
	if hub := a.orbHub(); hub != nil {
		name = strings.TrimSpace(hub.Snapshot().ProjectName)
	}
	if a.mobile != nil && a.mobile.hub != nil {
		projects := a.mobile.hub.Projects()
		if name != "" {
			for _, p := range projects {
				if p.Name == name {
					return p.Workspace
				}
			}
		}
		if len(projects) == 1 {
			return projects[0].Workspace
		}
	}
	if strings.TrimSpace(a.initialDir) != "" {
		return a.initialDir
	}
	return ""
}

func (a *App) orbHub() *orb.Hub {
	a.orb.mu.Lock()
	defer a.orb.mu.Unlock()
	return a.orb.hub
}

// orbSyncResidentSnapshot pushes a full snapshot of the Agent Hive state
// (status, active workstream, recent session messages and resolved workspace)
// to the floating Orb (Spec 007 H003).
func (a *App) orbSyncResidentSnapshot() {
	hub := a.orbHub()
	if hub == nil || a.resident == nil {
		return
	}
	st := a.resident.Status()
	msgs, err := a.resident.SessionMessages(st.Workstream)
	if err != nil {
		msgs = nil
	}
	out := orb.OrbResidentState{
		State:            st.State,
		Workstream:       st.Workstream,
		Workspace:        a.orbActiveWorkspace(),
		ConversationID:   st.ConversationID,
		SessionID:        st.SharedSessionID,
		Provider:         st.Provider,
		Model:            st.Model,
		SandboxAvailable: st.SandboxAvailable,
		SandboxMechanism: st.SandboxMechanism,
		Running:          st.State == "pensando",
		Messages:         make([]orb.OrbResidentMessage, 0, len(msgs)),
	}
	for i := range msgs {
		kind := "chief"
		label := "Agent Hive"
		if msgs[i].Role == "user" {
			kind = "user"
			label = "Você"
		}
		id := msgs[i].ID
		if id == "" {
			id = fmt.Sprintf("hist-%d", i)
		}
		out.Messages = append(out.Messages, orb.OrbResidentMessage{
			ID:    id,
			Kind:  kind,
			Label: label,
			Text:  msgs[i].Text,
		})
	}
	hub.SetResidentState(out)
}

func (a *App) launchOrbWindow() error {
	a.orb.mu.Lock()
	if a.orb.running || a.orb.hub == nil {
		a.orb.mu.Unlock()
		return nil
	}
	addr := a.orb.hub.Addr()
	token := a.orb.hub.Token()
	a.orb.mu.Unlock()

	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		return err
	}
	// Do NOT apply procx.HideWindow here: on Windows HideWindow=true sets SW_HIDE
	// in STARTUPINFO and hides the floating Orb GUI window itself.
	cmd := exec.Command(exe, "--orb", "--orb-addr", addr, "--orb-token", token)
	if err := cmd.Start(); err != nil {
		return err
	}

	a.orb.mu.Lock()
	a.orb.cmd = cmd
	a.orb.running = true
	a.orb.mu.Unlock()

	go func(c *exec.Cmd) {
		_ = c.Wait()
		a.orb.mu.Lock()
		if a.orb.cmd == c {
			a.orb.cmd = nil
			a.orb.running = false
		}
		a.orb.mu.Unlock()
	}(cmd)
	return nil
}

func (a *App) isOrbRunning() bool {
	a.orb.mu.Lock()
	defer a.orb.mu.Unlock()
	return a.orb.running
}

func (a *App) hideOrbWindow() {
	a.orb.mu.Lock()
	cmd := a.orb.cmd
	a.orb.cmd = nil
	a.orb.running = false
	a.orb.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (a *App) stopOrbHost() {
	a.orb.mu.Lock()
	hub := a.orb.hub
	cmd := a.orb.cmd
	a.orb.hub = nil
	a.orb.cmd = nil
	a.orb.running = false
	a.orb.mu.Unlock()

	if hub != nil {
		hub.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (a *App) handleOrbAction(act orb.OrbAction) {
	switch act.Action {
	case "show_ide":
		a.bringIDEToFront()
		a.orbSetIDEVisible(true)
	case "open_notice":
		a.bringIDEToFront()
		a.orbSetIDEVisible(true)
		if a.ctx != nil && strings.TrimSpace(act.NoticeID) != "" {
			wailsrt.EventsEmit(a.ctx, "hivemind:notice-open", act.NoticeID)
		}
	case "quit_all":
		a.QuitAll()
	case "resident_send":
		a.orbHandleResidentSend(act)
	case "resident_cancel":
		a.ResidentCancel()
	case "resident_approve":
		a.RespondApproval(act.ApprovalID, act.Approved)
	case "resident_clear":
		if err := a.ResidentClearSession(act.Workstream); err != nil {
			log.Printf("aviso: orb resident_clear: %v", err)
		}
		go a.orbSyncResidentSnapshot()
	case "open_resident":
		a.bringIDEToFront()
		a.orbSetIDEVisible(true)
		if a.ctx != nil {
			wailsrt.EventsEmit(a.ctx, "hivemind:open-resident")
		}
	}
}

// orbHandleResidentSend runs one Agent Hive turn started from the floating
// Orb. It first echoes the user message on lane "resident" so both the Orb
// timeline and the IDE Agent Hive tab show it immediately, then executes the
// same sandboxed ResidentStartTurn path used by the IDE (no shortcuts).
func (a *App) orbHandleResidentSend(act orb.OrbAction) {
	text := strings.TrimSpace(act.Text)
	if text == "" {
		return
	}
	if a.sessions != nil && a.sessions.Emit != nil {
		a.sessions.Emit(idecore.LaneEvent{
			LaneID: "resident",
			Event:  idecoreEventUserMessage(text),
		})
	}
	go func() {
		ws := strings.TrimSpace(act.Workstream)
		if ws == "" {
			if a.resident != nil {
				ws = a.resident.Status().Workstream
			}
			if ws == "" {
				ws = "geral"
			}
		}
		sessionID := ""
		if hub := a.orbHub(); hub != nil {
			sessionID = hub.Resident().SessionID
		}
		if _, err := a.ResidentStartTurnIn(a.orbActiveWorkspace(), ws, text, sessionID); err != nil {
			log.Printf("aviso: turno do Agent Hive via Orb: %v", err)
		}
		a.orbSyncResidentSnapshot()
	}()
}

func (a *App) orbSetIDEVisible(visible bool) {
	a.orb.mu.Lock()
	hub := a.orb.hub
	a.orb.mu.Unlock()
	if hub != nil {
		hub.SetIDEVisible(visible)
	}
}

func (a *App) orbSetProject(name string) {
	a.orb.mu.Lock()
	hub := a.orb.hub
	a.orb.mu.Unlock()
	if hub != nil {
		hub.SetProject(name)
	}
}

// beforeClose intercepts closing the main IDE window so that when the floating Orb
// is active, the IDE hides into the background instead of terminating background monitors.
func (a *App) beforeClose(_ context.Context) bool {
	a.orb.mu.Lock()
	force := a.orb.forceQuit
	running := a.orb.running
	hub := a.orb.hub
	a.orb.mu.Unlock()
	if force || a.ctx == nil {
		return false
	}
	if !running && !a.tray.isActive() {
		return false
	}
	wailsrt.WindowHide(a.ctx)
	if hub != nil {
		hub.SetIDEVisible(false)
	}
	return true
}

// QuitAll closes both the main IDE and the floating Orb cleanly.
func (a *App) QuitAll() {
	a.orb.mu.Lock()
	a.orb.forceQuit = true
	a.orb.mu.Unlock()
	if a.ctx != nil {
		wailsrt.Quit(a.ctx)
	}
}

// OrbShow launches the floating Orb window if it is not currently running.
func (a *App) OrbShow() error {
	return a.launchOrbWindow()
}

// OrbHide stops the floating Orb window process while keeping the host hub ready.
func (a *App) OrbHide() error {
	a.hideOrbWindow()
	return nil
}

// OrbRunning reports whether the independent floating Orb window is active.
func (a *App) OrbRunning() bool {
	return a.isOrbRunning()
}

// OrbSetMood updates the floating Orb mascot animation state.
func (a *App) OrbSetMood(mood string) {
	a.orb.mu.Lock()
	hub := a.orb.hub
	a.orb.mu.Unlock()
	if hub != nil {
		hub.SetMood(mood)
	}
}

// OrbPushNotice forwards a notification/activity item to the floating Orb window.
func (a *App) OrbPushNotice(id, title, body, mood, surface, projectId, laneId, messageId string) {
	a.orb.mu.Lock()
	hub := a.orb.hub
	a.orb.mu.Unlock()
	if hub != nil {
		hub.PushNotice(orb.OrbNotice{
			ID:        id,
			Title:     title,
			Body:      body,
			Mood:      mood,
			Surface:   surface,
			ProjectID: projectId,
			LaneID:    laneId,
			MessageID: messageId,
		})
	}
}

// OrbDismissNotice removes a dismissed notification from the floating Orb window.
func (a *App) OrbDismissNotice(id string) {
	a.orb.mu.Lock()
	hub := a.orb.hub
	a.orb.mu.Unlock()
	if hub != nil {
		hub.DismissNotice(id)
	}
}
