package main

import (
	"context"
	"errors"
	"log"

	"github.com/gilvan-Borges/HiveMind/ide/internal/notify"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) startDesktopNotices() {
	if a.ctx == nil {
		return
	}
	var hub *notify.Hub
	activate := func(id string) {
		if hub != nil {
			hub.Activate(id)
		}
	}
	sender, err := notify.Open(activate)
	if err != nil {
		if !errors.Is(err, notify.ErrNeedsHost) {
			log.Printf("avisos do sistema: %v", err)
		}
		if initErr := wailsrt.InitializeNotifications(a.ctx); initErr != nil {
			log.Printf("avisos do sistema (janela): %v", initErr)
		}
		_, _ = wailsrt.RequestNotificationAuthorization(a.ctx)
		wailsrt.OnNotificationResponse(a.ctx, func(res wailsrt.NotificationResult) {
			if res.Error != nil || res.Response.ID == "" {
				return
			}
			activate(res.Response.ID)
		})
		// shortcut: neste fallback o som do toast segue o sistema; o sino do companion continua no ajuste.
		sender = wailsNoticeSender{ctx: a.ctx}
	}
	hub = notify.NewHub(sender, a.bringIDEToFront, func(id string) {
		wailsrt.EventsEmit(a.ctx, "hivemind:notice-open", id)
	})
	a.desk = hub
}

func (a *App) bringIDEToFront() {
	if a.ctx == nil {
		return
	}
	wailsrt.WindowUnminimise(a.ctx)
	wailsrt.WindowShow(a.ctx)
	notify.FocusMainWindow(notify.WindowTitle)
}

type wailsNoticeSender struct {
	ctx context.Context
}

func (s wailsNoticeSender) Send(n notify.Notice) error {
	if s.ctx == nil {
		return errors.New("janela indisponível")
	}
	return wailsrt.SendNotification(s.ctx, wailsrt.NotificationOptions{
		ID:    n.ID,
		Title: n.Title,
		Body:  n.Body,
	})
}

func (s wailsNoticeSender) Close() error { return nil }
