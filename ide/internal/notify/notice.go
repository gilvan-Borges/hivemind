// Package notify sends a short desktop notice and routes the click back to the IDE.
package notify

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// WindowTitle is the OS window title used to bring the IDE forward.
const WindowTitle = "HiveMind IDE"

// ErrNeedsHost means this OS delivers notices through the window host.
var ErrNeedsHost = errors.New("notify: esta plataforma usa o host da janela")

// Notice is one native notification.
type Notice struct {
	ID    string
	Title string
	Body  string
	Sound bool
}

// Sender delivers a notice. Tests substitute a fake.
type Sender interface {
	Send(n Notice) error
	Close() error
}

// Hub validates a notice, sends it, and on click focuses the window then opens the id.
type Hub struct {
	sender Sender
	focus  func()
	open   func(id string)
}

// NewHub wires a sender to focus and open callbacks. Either callback may be nil.
func NewHub(sender Sender, focus func(), open func(id string)) *Hub {
	return &Hub{sender: sender, focus: focus, open: open}
}

// Send clips the text and delivers the notice.
func (h *Hub) Send(n Notice) error {
	if h == nil || h.sender == nil {
		return errors.New("avisos do sistema indisponíveis")
	}
	n.ID = strings.TrimSpace(n.ID)
	n.Title = Clip(n.Title, 120)
	n.Body = Clip(n.Body, 240)
	if err := Validate(n); err != nil {
		return err
	}
	return h.sender.Send(n)
}

// Activate runs after the user clicks a notice. Empty ids are ignored.
func (h *Hub) Activate(id string) {
	if h == nil {
		return
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	if h.focus != nil {
		h.focus()
	}
	if h.open != nil {
		h.open(id)
	}
}

// Close releases the sender.
func (h *Hub) Close() error {
	if h == nil || h.sender == nil {
		return nil
	}
	return h.sender.Close()
}

// Validate rejects a notice that cannot be shown.
func Validate(n Notice) error {
	if strings.TrimSpace(n.ID) == "" || strings.TrimSpace(n.Title) == "" {
		return errors.New("aviso sem identificador ou título")
	}
	return nil
}

// Clip collapses whitespace and limits the string to max runes.
func Clip(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	if max == 1 {
		return "…"
	}
	return strings.TrimRight(string(runes[:max-1]), " ") + "…"
}

// Unavailable is a sender that always fails. Used when the platform cannot open.
type downSender struct{ err error }

func (d downSender) Send(Notice) error {
	if d.err != nil {
		return d.err
	}
	return errors.New("avisos do sistema indisponíveis")
}

func (d downSender) Close() error { return nil }

// Unavailable wraps err as a Sender.
func Unavailable(err error) Sender { return downSender{err: err} }
