package notify

import (
	"errors"
	"testing"
)

type fakeSender struct {
	got    []Notice
	err    error
	closed bool
}

func (f *fakeSender) Send(n Notice) error {
	f.got = append(f.got, n)
	return f.err
}

func (f *fakeSender) Close() error {
	f.closed = true
	return nil
}

func TestHubSendUsesSender(t *testing.T) {
	fake := &fakeSender{}
	hub := NewHub(fake, nil, nil)
	err := hub.Send(Notice{ID: "n1", Title: "  Plano pronto para aprovar  ", Body: "revise", Sound: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.got) != 1 {
		t.Fatalf("envios: %d", len(fake.got))
	}
	if fake.got[0].ID != "n1" || fake.got[0].Title != "Plano pronto para aprovar" || !fake.got[0].Sound {
		t.Fatalf("aviso: %+v", fake.got[0])
	}
}

func TestHubSendRejectsEmpty(t *testing.T) {
	fake := &fakeSender{}
	hub := NewHub(fake, nil, nil)
	if err := hub.Send(Notice{Title: "oi"}); err == nil {
		t.Fatal("esperava erro sem id")
	}
	if len(fake.got) != 0 {
		t.Fatal("sender não deveria ser chamado")
	}
}

func TestHubSendReturnsSenderError(t *testing.T) {
	fake := &fakeSender{err: errors.New("runtime indisponível")}
	hub := NewHub(fake, nil, nil)
	err := hub.Send(Notice{ID: "n1", Title: "Erro no QA"})
	if err == nil || err.Error() != "runtime indisponível" {
		t.Fatalf("erro: %v", err)
	}
}

func TestHubActivateFocusThenOpen(t *testing.T) {
	var order []string
	hub := NewHub(nil, func() { order = append(order, "focus") }, func(id string) {
		order = append(order, "open:"+id)
	})
	hub.Activate("  card-1 ")
	if len(order) != 2 || order[0] != "focus" || order[1] != "open:card-1" {
		t.Fatalf("ordem: %#v", order)
	}
	hub.Activate("   ")
	if len(order) != 2 {
		t.Fatal("id vazio não deve ativar")
	}
}

func TestHubClose(t *testing.T) {
	fake := &fakeSender{}
	hub := NewHub(fake, nil, nil)
	if err := hub.Close(); err != nil || !fake.closed {
		t.Fatalf("close: %v %v", err, fake.closed)
	}
}

func TestClip(t *testing.T) {
	if Clip("  a   b  ", 10) != "a b" {
		t.Fatal(Clip("  a   b  ", 10))
	}
	got := Clip("abcdefghijklmnopqrstuvwxyz", 8)
	if got != "abcdefg…" {
		t.Fatalf("corte: %q", got)
	}
}
