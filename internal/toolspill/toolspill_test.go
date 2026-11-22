package toolspill

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

type fakeTool struct{ name string }

func (f fakeTool) Name() string        { return f.name }
func (f fakeTool) Description() string { return "fake" }
func (f fakeTool) IsLongRunning() bool { return false }

func largeText(n int) string {
	chunk := strings.Repeat("x", 99) + "\n"
	var b strings.Builder
	for b.Len() < n {
		b.WriteString(chunk)
	}
	return b.String()
}

func TestStoreSaveVerifiesBytes(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	rel, err := store.Save("run_shell", "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rel, SpillRelDir+"/") || !strings.HasSuffix(rel, ".txt") {
		t.Fatalf("unexpected rel path: %s", rel)
	}
	// The pointer must resolve inside the workspace jail with the scoped
	// carve-out and deliver the exact bytes back.
	if !isSpillReadable(t, root, rel, "hello world") {
		t.Fatal("spilled file not readable via jail carve-out")
	}
}

func isSpillReadable(t *testing.T, root, rel, want string) bool {
	t.Helper()
	resolved, err := workspace.ResolveWithOptions(root, rel, workspace.Options{AllowSpilloverRead: true})
	if err != nil {
		return false
	}
	data, err := os.ReadFile(resolved)
	return err == nil && string(data) == want
}

func TestStoreSaveFailsWithoutRoot(t *testing.T) {
	store := NewStore("")
	if _, err := store.Save("run_shell", "x"); err == nil {
		t.Fatal("expected error without root")
	}
}

func TestStoreSaveRedactsSecrets(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	payload := "log line\nsk-abcdefghijklmnopqrstuvwx error\nmore"
	rel, err := store.Save("run_shell", payload)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := workspace.ResolveWithOptions(root, rel, workspace.Options{AllowSpilloverRead: true})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-abcdefghijklmnopqrstuvwx") {
		t.Fatal("secret shape must be redacted on disk")
	}
	if !strings.Contains(string(data), "[REDACTED]") {
		t.Fatal("expected redaction marker")
	}
}

func TestHeadTailPreviewKeepsEnds(t *testing.T) {
	text := "HEAD\n" + largeText(100_000) + "\nTAIL-MARKER"
	preview := HeadTailPreview(text, 1_500)
	if !strings.HasPrefix(preview, "HEAD") {
		t.Fatal("preview must keep the head")
	}
	if !strings.Contains(preview, "TAIL-MARKER") {
		t.Fatal("preview must keep the tail (diagnostics sit at the end)")
	}
	if !strings.Contains(preview, "omitidos no meio") {
		t.Fatal("preview must announce the elided middle")
	}
	if len(preview) > 4_000 {
		t.Fatalf("preview too large: %d", len(preview))
	}
}

func TestHeadTailPreviewSmallTextVerbatim(t *testing.T) {
	text := "curto"
	if got := HeadTailPreview(text, 1_500); got != text {
		t.Fatalf("small text must stay verbatim, got %q", got)
	}
}

func TestSpillTextUnderThresholdUnchanged(t *testing.T) {
	store := NewStore(t.TempDir())
	got, spilled := SpillText("run_shell", "pequeno", store, 100_000, 1_500)
	if spilled || got != "pequeno" {
		t.Fatalf("unexpected spill: %q %v", got, spilled)
	}
}

func TestSpillTextOverThresholdSpills(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	text := largeText(150_000)
	got, spilled := SpillText("run_shell", text, store, 100_000, 1_500)
	if !spilled {
		t.Fatal("expected spill")
	}
	if len(got) >= len(text) {
		t.Fatalf("replacement must be smaller: %d", len(got))
	}
	if !strings.Contains(got, "read_file") {
		t.Fatal("replacement must carry read_file recovery pointer")
	}
	idx := strings.Index(got, "arquivo: ")
	rel := got[idx+len("arquivo: "):]
	if end := strings.Index(rel, " —"); end >= 0 {
		rel = rel[:end]
	}
	if !isSpillReadable(t, root, rel, RedactSecrets(text)) {
		t.Fatal("pointer must resolve to the exact archived bytes")
	}
}

func TestSpillTextFailClosedWithoutStore(t *testing.T) {
	got, spilled := SpillText("run_shell", largeText(150_000), NewStore(""), 100_000, 1_500)
	if spilled {
		t.Fatal("no spill without a store")
	}
	if len(got) >= 150_000 {
		t.Fatal("fail-closed replacement must still bound the payload")
	}
	if strings.Contains(got, "arquivo:") {
		t.Fatal("fail-closed replacement must not reference a lost archive")
	}
}

type ctxlessTool struct{ name string }

func TestAfterToolCallbackSpillsLargeResult(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	mon := &Monitor{}
	cb := NewAfterToolCallback(Config{PerResultChars: 1_000, PreviewChars: 400, EscalatedChars: 300, TurnBudgetChars: 10_000}, store, mon)
	large := largeText(5_000)
	result := map[string]any{"text": large}
	out, err := cb(nil, fakeTool{name: "search_text"}, nil, result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("expected rewritten result")
	}
	got, _ := out["text"].(string)
	if got == large {
		t.Fatal("expected spilled replacement")
	}
	if !strings.Contains(got, "arquivo: ") {
		t.Fatal("expected archive pointer")
	}
	if v, _ := result["text"].(string); v != large {
		t.Fatal("original result map must stay untouched (clone semantics)")
	}
}

func TestAfterToolCallbackPinnedToolsStayVerbatim(t *testing.T) {
	cb := NewAfterToolCallback(Config{PerResultChars: 100}, NewStore(t.TempDir()), &Monitor{})
	large := largeText(5_000)
	out, err := cb(nil, fakeTool{name: "read_file"}, nil, map[string]any{"text": large}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		t.Fatal("read_file result must never spill (persist→read→persist loop)")
	}
	for _, name := range []string{"qa_approve", "qa_request_changes", "delegate_to_squad", "github_get_pr"} {
		out, err := cb(nil, fakeTool{name: name}, nil, map[string]any{"text": large}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if out != nil {
			t.Fatalf("%s must stay verbatim", name)
		}
	}
}

func TestAfterToolCallbackErrorResultsUntouched(t *testing.T) {
	cb := NewAfterToolCallback(Config{PerResultChars: 100}, NewStore(t.TempDir()), &Monitor{})
	out, err := cb(nil, fakeTool{name: "search_text"}, nil, map[string]any{"text": largeText(5_000)}, errors.New("boom"))
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		t.Fatal("error results must stay verbatim for diagnosis")
	}
}

func TestMonitorEscalatesAfterTurnBudget(t *testing.T) {
	cfg := Config{PerResultChars: 100_000, TurnBudgetChars: 1_000, EscalatedChars: 8_000}
	mon := &Monitor{}
	if got := mon.Threshold(cfg); got != 100_000 {
		t.Fatalf("initial threshold = %d", got)
	}
	mon.Add(1_500)
	if got := mon.Threshold(cfg); got != 8_000 {
		t.Fatalf("escalated threshold = %d", got)
	}
}

func TestCleanupRemovesOnlyOldFiles(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	rel, err := store.Save("run_shell", "dados")
	if err != nil {
		t.Fatal(err)
	}
	// Backdate the spilled file beyond retention.
	resolved, err := workspace.ResolveWithOptions(root, rel, workspace.Options{AllowSpilloverRead: true})
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(resolved, past, past); err != nil {
		t.Fatal(err)
	}
	removed := store.Cleanup(24 * time.Hour)
	if removed != 1 {
		t.Fatalf("expected 1 removal, got %d", removed)
	}
	if _, err := os.Stat(resolved); !os.IsNotExist(err) {
		t.Fatal("old spill must be removed")
	}
}
