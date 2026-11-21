package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/gilvan-Borges/HiveMind/ide/internal/orb"
	"github.com/gilvan-Borges/HiveMind/internal/procx"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

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
	if err := a.launchOrbWindow(); err != nil {
		log.Printf("aviso: janela do Orb flutuante não iniciou: %v", err)
	}
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
	cmd := exec.Command(exe, "--orb", "--orb-addr", addr, "--orb-token", token)
	procx.ConfigureContext(cmd)
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
	}
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
	if force || !running || a.ctx == nil {
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
