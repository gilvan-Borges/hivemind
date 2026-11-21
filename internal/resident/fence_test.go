package resident

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestFenceTruncationIsUTF8SafeAtBoundary pins the Spec 02 acceptance
// criterion for fence.go: labels with accents/emojis cut at the boundary
// never produce invalid UTF-8 (which providers reject with HTTP 400).
func TestFenceTruncationIsUTF8SafeAtBoundary(t *testing.T) {
	// 6 bytes/rune × N makes the 120-byte cut land inside a multi-byte rune.
	long := strings.Repeat("ação e emoji 🚀 no rótulo ", 20)

	if got := sanitizeHeaderToken(long); !utf8.ValidString(got) {
		t.Fatalf("sanitizeHeaderToken produced invalid UTF-8: %q", got)
	}
	if got := sanitizeHeaderToken(strings.Repeat("ção🚀", 200)); !utf8.ValidString(got) {
		t.Fatalf("sanitizeHeaderToken emoji boundary invalid: %q", got)
	}
	if got := RedactTarget(long); !utf8.ValidString(got) || len(got) > 161 {
		t.Fatalf("RedactTarget produced invalid/oversized label: len=%d valid=%v", len(got), utf8.ValidString(got))
	}
	if got := RedactTarget(strings.Repeat("x", 500)); len(got) > 161 {
		t.Fatalf("RedactTarget exceeded 160+ellipsis: %d", len(got))
	}
}
