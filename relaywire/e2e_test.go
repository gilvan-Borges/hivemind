package relaywire

import (
	"bytes"
	"testing"
)

func TestBoxRoundTrip(t *testing.T) {
	alice, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	bob, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"method":"GET","path":"/api/v1/projects","marker":"SEGREDO"}`)
	sealed, err := alice.Seal(bob.Public, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(sealed.Ciphertext), []byte("SEGREDO")) {
		t.Fatal("ciphertext contém o plaintext")
	}
	got, err := bob.Open(alice.Public, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("plaintext = %s", got)
	}
	eve, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eve.Open(alice.Public, sealed); err == nil {
		t.Fatal("um terceiro abriu a caixa")
	}
}

func TestParseKeyAcceptsStdAndRaw(t *testing.T) {
	k, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	raw := k.PublicB64()
	got, err := ParseKey(raw)
	if err != nil || got != k.Public {
		t.Fatalf("raw: %v %x", err, got[:])
	}
	std := "not-used"
	_ = std
}

func TestLinkURI(t *testing.T) {
	uri := LinkURI("https://relay.example.com/", "abcd-efgh-23")
	base, code, ok := ParseLinkURI(uri)
	if !ok || base != "https://relay.example.com" || code != "ABCDEFGH23" {
		t.Fatalf("parse %q -> %q %q %v", uri, base, code, ok)
	}
}

func TestAllowedRPC(t *testing.T) {
	if !AllowedRPC("post", "/api/v1/approvals/resolve") {
		t.Fatal("resolve deveria ser permitido")
	}
	if !AllowedRPC("POST", "/api/v1/runs/steer") {
		t.Fatal("steer")
	}
	if !AllowedRPC("POST", "/api/v1/git/commit") || !AllowedRPC("GET", "/api/v1/routines") || !AllowedRPC("POST", "/api/v1/project/permission") || !AllowedRPC("POST", "/api/v1/session/delete") {
		t.Fatal("rotas novas do app precisam passar pelo relay")
	}
	if !AllowedRPC("GET", "/api/v1/loops") || !AllowedRPC("POST", "/api/v1/loops/stop") {
		t.Fatal("loops do app precisam passar pelo relay")
	}
	if AllowedRPC("POST", "/api/v1/loops/full") {
		t.Fatal("o app não grava allow_full_unattended")
	}
	if !AllowedRPC("POST", "/api/v1/runs/steer") {
		t.Fatal("steer deveria ser permitido")
	}
	if AllowedRPC("GET", "/api/v1/events") {
		t.Fatal("SSE não é RPC")
	}
	if AllowedRPC("GET", "/api/v1/../secret") {
		t.Fatal("path traversal")
	}
	if AllowedRPC("POST", "http://127.0.0.1/api/v1/projects") {
		t.Fatal("URL absoluta")
	}
}
