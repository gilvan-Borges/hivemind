package skillx

import (
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/version"
)

func TestBuiltinTopicsListAndGet(t *testing.T) {
	topics := ListBuiltinTopics()
	if len(topics) < 6 {
		t.Fatalf("expected at least 6 builtin topics, got %d", len(topics))
	}
	for _, tp := range topics {
		if tp.Version != version.Version {
			t.Fatalf("topic %q version mismatch: got %q want %q", tp.Name, tp.Version, version.Version)
		}
		if strings.TrimSpace(tp.Body) == "" || strings.TrimSpace(tp.Title) == "" {
			t.Fatalf("topic %q empty title/body", tp.Name)
		}
	}

	got, ok := GetBuiltinTopic("CLI.md")
	if !ok || got.Name != "cli" {
		t.Fatalf("expected case-insensitive lookup for cli, got ok=%v %#v", ok, got)
	}
	if _, ok := GetBuiltinTopic("inexistente"); ok {
		t.Fatalf("expected missing topic lookup to return false")
	}
}
