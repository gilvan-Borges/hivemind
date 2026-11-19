package main

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/gilvan-Borges/HiveMind/ide/internal/relayclient"
)

func newRelayClient(a *App) *relayclient.Client {
	if a == nil || a.mobile == nil || a.mobile.server == nil || a.mobile.hub == nil {
		return nil
	}
	client, err := relayclient.Load(relayclient.Options{
		Handler:   a.mobile.server.Handler(),
		Bearer:    a.mobile.server.Bearer,
		Subscribe: a.mobile.hub.Subscribe,
	})
	if err != nil {
		log.Printf("aviso: relay: %v", err)
		return nil
	}
	return client
}

func (a *App) relayCall(fn func(context.Context) (relayclient.Status, error)) (relayclient.Status, error) {
	if a == nil || a.relay == nil {
		return relayclient.Status{}, errors.New("relay indisponível")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return fn(ctx)
}

// RelayStatus reports the account, link and tunnel state. It never includes tokens.
func (a *App) RelayStatus() relayclient.Status {
	if a == nil || a.relay == nil {
		return relayclient.Status{}
	}
	return a.relay.Status()
}

// RelayLogin signs the IDE into the relay account. The password is not stored.
func (a *App) RelayLogin(baseURL, email, password string) (relayclient.Status, error) {
	return a.relayCall(func(ctx context.Context) (relayclient.Status, error) {
		return a.relay.Login(ctx, baseURL, email, password)
	})
}

// RelayLogout revokes the user session and turns the tunnel off.
func (a *App) RelayLogout() (relayclient.Status, error) {
	return a.relayCall(func(ctx context.Context) (relayclient.Status, error) {
		return a.relay.Logout(ctx)
	})
}

// RelaySetEnabled turns remote access through the relay on or off.
// The LAN bridge is independent and keeps working either way.
func (a *App) RelaySetEnabled(on bool) (relayclient.Status, error) {
	return a.relayCall(func(ctx context.Context) (relayclient.Status, error) {
		return a.relay.SetEnabled(ctx, on)
	})
}

// RelayCreateLinkCode returns a code and QR for the phone to confirm.
func (a *App) RelayCreateLinkCode() (relayclient.LinkCode, error) {
	if a == nil || a.relay == nil {
		return relayclient.LinkCode{}, errors.New("relay indisponível")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return a.relay.CreateLinkCode(ctx)
}

// RelayClaimLinkCode confirms a code generated on the phone.
func (a *App) RelayClaimLinkCode(code string) (relayclient.Status, error) {
	return a.relayCall(func(ctx context.Context) (relayclient.Status, error) {
		return a.relay.ClaimLinkCode(ctx, code)
	})
}

// RelayUnlink revokes this IDE on the relay.
func (a *App) RelayUnlink() (relayclient.Status, error) {
	return a.relayCall(func(ctx context.Context) (relayclient.Status, error) {
		return a.relay.Unlink(ctx)
	})
}
