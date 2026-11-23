package main

import (
	"context"
	"log"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/internal/remote"
)

// mobileBridge bundles the Remote Bridge pieces owned by the App
// (Marco 1 da SPEC_APP_MOBILE_NATIVO.md): live Hub, HTTP+SSE server and the
// persisted pairing config.
type mobileBridge struct {
	hub     *remote.Hub
	adapter *idecore.Adapter
	server  *remote.Server
	cfg     remote.Config
}

func newMobileBridge(sessions *idecore.SessionManager, residentMgr *idecore.ResidentManager, autoReview *idecore.AutoReviewManager, hub *remote.Hub) *mobileBridge {
	cfg, err := remote.LoadConfig()
	if err != nil {
		log.Printf("aviso: remote bridge: %v", err)
		cfg = remote.Config{Port: remote.DefaultPort}
	}
	adapter := &idecore.Adapter{Sessions: sessions, Resident: residentMgr, AutoReview: autoReview, Hub: hub}
	server := remote.NewServer(hub, adapter, cfg)
	return &mobileBridge{hub: hub, adapter: adapter, server: server, cfg: cfg}
}

// SetComposerMode publishes the IDE composer's run mode to paired phones.
func (a *App) SetComposerMode(workspace, mode string) {
	if a == nil || a.mobile == nil || a.mobile.hub == nil {
		return
	}
	a.mobile.hub.SetComposerMode(workspace, mode)
}

// start brings the bridge up when the user enabled it previously.
func (b *mobileBridge) start() {
	if !b.cfg.Enabled {
		return
	}
	if err := b.server.Start(); err != nil {
		log.Printf("aviso: remote bridge: %v", err)
	}
}

func (b *mobileBridge) stop() {
	_ = b.server.Stop(context.Background())
}

// MobileBridgeStatus returns the bridge state for the IDE status bar chip.
func (a *App) MobileBridgeStatus() remote.StatusInfo {
	return a.mobile.server.Status()
}

// SetMobileBridgeEnabled turns the bridge on/off and persists the choice.
func (a *App) SetMobileBridgeEnabled(on bool) (remote.StatusInfo, error) {
	cfg, err := a.mobile.server.SetEnabled(on)
	if err != nil {
		return a.mobile.server.Status(), err
	}
	a.mobile.cfg = cfg
	if on {
		if err := a.mobile.server.Start(); err != nil {
			return a.mobile.server.Status(), err
		}
	} else {
		_ = a.mobile.server.Stop(context.Background())
	}
	st := a.mobile.server.Status()
	st.Enabled = on
	return st, nil
}

// RotateMobileToken drops every paired device ("Rotacionar Token / Desconectar Celulares").
func (a *App) RotateMobileToken() (remote.StatusInfo, error) {
	cfg, err := a.mobile.server.RotateSession()
	if err != nil {
		return a.mobile.server.Status(), err
	}
	a.mobile.cfg = cfg
	if cfg.Enabled {
		_ = a.mobile.server.Stop(context.Background())
		if err := a.mobile.server.Start(); err != nil {
			return a.mobile.server.Status(), err
		}
	}
	st := a.mobile.server.Status()
	st.Enabled = cfg.Enabled
	return st, nil
}

// MobilePairing returns the QR pairing payload for the "App Mobile" modal.
func (a *App) MobilePairing() (remote.PairingInfo, error) {
	return a.mobile.server.Pairing()
}
