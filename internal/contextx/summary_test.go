package contextx

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSummaryStateCooldownLadder(t *testing.T) {
	st := NewSummaryState()
	now := time.Now()
	if !st.ready(now) {
		t.Fatal("fresh state must be ready")
	}
	st.fail(now)
	if st.ready(now.Add(30 * time.Second)) {
		t.Fatal("first failure must arm the 60s cooldown")
	}
	if !st.ready(now.Add(61 * time.Second)) {
		t.Fatal("cooldown must release after 60s")
	}
	st.fail(now.Add(2 * time.Minute))
	if st.ready(now.Add(2*time.Minute + 200*time.Second)) {
		t.Fatal("second failure must arm the 300s cooldown")
	}
	st.fail(now.Add(10 * time.Minute))
	if st.ready(now.Add(10*time.Minute + 800*time.Second)) {
		t.Fatal("third failure must arm the 900s cooldown")
	}
}

func TestSummaryStatePendingFiltersCoveredIDs(t *testing.T) {
	st := NewSummaryState()
	entries := []CompactedEntry{
		{ID: "call-1", Tool: "read_file", Text: "a"},
		{ID: "call-2", Tool: "read_file", Text: "b"},
	}
	if got := st.pending(entries); len(got) != 2 {
		t.Fatalf("fresh state must want everything, got %d", len(got))
	}
	st.commit("resumo v1", entries)
	if got := st.pending(entries); len(got) != 0 {
		t.Fatalf("covered IDs must not re-enter the delta, got %d", len(got))
	}
	if st.current() != "resumo v1" {
		t.Fatalf("running summary = %q", st.current())
	}
	// Iterative: a new payload ages out and only it is summarized next.
	next := append(entries, CompactedEntry{ID: "call-3", Tool: "read_file", Text: "c"})
	if got := st.pending(next); len(got) != 1 || got[0].ID != "call-3" {
		t.Fatalf("delta must contain only call-3, got %+v", got)
	}
}

func TestCapEntryTextBoundedWithTail(t *testing.T) {
	text := strings.Repeat("HEAD\n", 1000) + strings.Repeat("TAIL-MARKER\n", 100)
	capped := capEntryText(text)
	if len(capped) >= len(text) {
		t.Fatalf("entry must be capped, got %d of %d", len(capped), len(text))
	}
	if !strings.Contains(capped, "HEAD") || !strings.Contains(capped, "TAIL-MARKER") {
		t.Fatal("capped entry must keep head and tail")
	}
}

func TestRenderSummaryBlockFormat(t *testing.T) {
	block := RenderSummaryBlock("  ação concluída  ")
	if !strings.HasPrefix(block, "<system-reminder>\n<context_summary>") {
		t.Fatalf("unexpected block start: %q", block)
	}
	if !strings.Contains(block, "ação concluída") || !strings.HasSuffix(block, "</context_summary>\n</system-reminder>") {
		t.Fatalf("unexpected block: %q", block)
	}
}

func TestCompactionCallbackSummarizesDeltaAndInjectsReminder(t *testing.T) {
	req, _ := buildTurnRequest(12)
	st := NewSummaryState()

	calls := 0
	b := Budget{
		ContextWindow: 1_000, // tiny window: the pass lands in the summary tier
		KeepRecent:    8,
		SummaryState:  st,
		Summarizer: func(_ context.Context, prev string, delta []CompactedEntry) (string, error) {
			calls++
			if calls == 1 && prev != "" {
				t.Fatal("first pass must receive an empty running summary")
			}
			if len(delta) != 10 {
				t.Fatalf("delta must carry the 10 payloads outside keepRecent=2, got %d", len(delta))
			}
			return "RESUMO:" + strings.Repeat("x", 10), nil
		},
	}
	cb := NewCompactionCallback(b)
	if _, err := cb(nil, req); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly one summarizer call, got %d", calls)
	}

	// The running summary rides the tail as a reminder block, cloned onto
	// the last content — never into the raw history.
	last := req.Contents[len(req.Contents)-1]
	found := false
	for _, p := range last.Parts {
		if p != nil && strings.Contains(p.Text, "<context_summary>") && strings.Contains(p.Text, "RESUMO:") {
			found = true
		}
	}
	if !found {
		t.Fatal("summary reminder must be appended to the last content")
	}

	// Second call over the same projection: everything is already covered, so
	// no new model call — but the reminder must keep riding the tail.
	if _, err := cb(nil, req); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("delta-only summarization violated: %d calls", calls)
	}
}

func TestCompactionCallbackSummarizerFailureIsFailOpenAndCooledDown(t *testing.T) {
	req, _ := buildTurnRequest(12)
	st := NewSummaryState()
	calls := 0
	b := Budget{
		ContextWindow: 1_000,
		KeepRecent:    8,
		SummaryState:  st,
		Summarizer: func(_ context.Context, _ string, _ []CompactedEntry) (string, error) {
			calls++
			return "", errBoom
		},
	}
	cb := NewCompactionCallback(b)
	if _, err := cb(nil, req); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("first attempt must reach the summarizer, got %d", calls)
	}
	// Deterministic compaction still happened (fail-open).
	found := false
	for _, c := range req.Contents {
		for _, p := range c.Parts {
			if p != nil && p.FunctionResponse != nil {
				if txt, ok := p.FunctionResponse.Response["text"].(string); ok && strings.Contains(txt, "compactada:") {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("deterministic compaction must stay authoritative on summarizer failure")
	}
	// No reminder was injected (no summary exists).
	for _, c := range req.Contents {
		for _, p := range c.Parts {
			if p != nil && strings.Contains(p.Text, "<context_summary>") {
				t.Fatal("failed summarizer must not inject anything")
			}
		}
	}
	// Cooldown: a fresh request in the same run must not pay for the outage.
	req2, _ := buildTurnRequest(12)
	if _, err := cb(nil, req2); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("cooldown must suppress the second call, got %d", calls)
	}
}

var errBoom = boomErr{}

type boomErr struct{}

func (boomErr) Error() string { return "boom" }
