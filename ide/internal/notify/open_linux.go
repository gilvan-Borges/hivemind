//go:build linux

package notify

import (
	"fmt"

	"github.com/godbus/dbus/v5"
)

type linuxSender struct {
	conn *dbus.Conn
	book *tokenBook
	on   func(string)
	done chan struct{}
}

// Open connects to the session bus and listens for the notification click.
// shortcut: sem barramento de sessão o Open falha e o host usa o fallback da janela.
func Open(onActivate func(string)) (Sender, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("barramento de sessão: %w", err)
	}
	s := &linuxSender{
		conn: conn,
		book: newTokenBook(),
		on:   onActivate,
		done: make(chan struct{}),
	}
	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.Notifications"),
		dbus.WithMatchMember("ActionInvoked"),
	); err != nil {
		_ = conn.Close()
		return nil, err
	}
	go s.loop()
	return s, nil
}

func (s *linuxSender) Send(n Notice) error {
	if err := Validate(n); err != nil {
		return err
	}
	hints := map[string]dbus.Variant{}
	if !n.Sound {
		hints["suppress-sound"] = dbus.MakeVariant(true)
	}
	obj := s.conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.Call(
		"org.freedesktop.Notifications.Notify",
		0,
		"HiveMind IDE",
		uint32(0),
		"",
		n.Title,
		n.Body,
		[]string{"default", "Abrir"},
		hints,
		int32(7000),
	)
	if call.Err != nil {
		return call.Err
	}
	var id uint32
	if err := call.Store(&id); err != nil {
		return err
	}
	s.book.Put(id, n.ID)
	return nil
}

func (s *linuxSender) loop() {
	ch := make(chan *dbus.Signal, 8)
	s.conn.Signal(ch)
	defer s.conn.RemoveSignal(ch)
	for {
		select {
		case <-s.done:
			return
		case sig, ok := <-ch:
			if !ok || sig == nil {
				return
			}
			if sig.Name != "org.freedesktop.Notifications.ActionInvoked" || len(sig.Body) < 1 {
				continue
			}
			id, ok := sig.Body[0].(uint32)
			if !ok {
				continue
			}
			token, found := s.book.Take(id)
			if found && s.on != nil {
				s.on(token)
			}
		}
	}
}

func (s *linuxSender) Close() error {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}
