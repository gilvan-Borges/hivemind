package notify

import "testing"

func TestTokenBookTakeOnce(t *testing.T) {
	b := newTokenBook()
	b.Put(7, "hm-rotina")
	got, ok := b.Take(7)
	if !ok || got != "hm-rotina" {
		t.Fatalf("take: %q %v", got, ok)
	}
	if _, ok := b.Take(7); ok {
		t.Fatal("o id do sistema só abre uma vez")
	}
	b.Put(0, "")
	if _, ok := b.Take(0); ok {
		t.Fatal("token vazio não entra")
	}
}
