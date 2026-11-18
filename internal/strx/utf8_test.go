package strx

import (
	"testing"
	"unicode/utf8"
)

func TestTruncateBytesPreservesValidUTF8(t *testing.T) {
	input := "configuração de integração automática"
	// "configuraç" -> 'ç' is 2 bytes at index 9..10; slicing at 10 would split 'ç'.
	got := TruncateBytes(input, 10, "…")
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid UTF-8, got %q", got)
	}
	if got != "configura…" {
		t.Fatalf("got %q, want %q", got, "configura…")
	}
	if unchanged := TruncateBytes("curto", 20, "…"); unchanged != "curto" {
		t.Fatalf("unexpected truncation: %q", unchanged)
	}
}

func TestTruncateRunes(t *testing.T) {
	input := "olá mundo"
	got := TruncateRunes(input, 3, "…")
	if got != "olá…" {
		t.Fatalf("got %q, want %q", got, "olá…")
	}
}
