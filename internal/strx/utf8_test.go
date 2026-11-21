package strx

import (
	"strings"
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

func TestTruncateHeadTailBytesPreservesTailAndUTF8(t *testing.T) {
	head := "INÍCIO-CONFIGURAÇÃO-" + strings.Repeat("a", 64)
	tail := strings.Repeat("b", 64) + "-ERRO_FINAL_COMPILAÇÃO"
	got := TruncateHeadTailBytes(head+tail, 24, 30)
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid UTF-8 output, got %q", got)
	}
	if !strings.HasPrefix(got, "INÍCIO") {
		t.Fatalf("expected head preserved, got %q", got)
	}
	if !strings.HasSuffix(got, "ERRO_FINAL_COMPILAÇÃO") {
		t.Fatalf("expected tail error preserved, got %q", got)
	}
	if !strings.Contains(got, "bytes omitidos") {
		t.Fatalf("expected omission marker, got %q", got)
	}
}
