// Summary tier: at the aggressive structural cut (SummaryThresholdPct) the
// payloads being replaced by previews are first distilled into a running
// summary by an optional auxiliary model call (cheap route via Jev). The
// deterministic compaction stays the source of truth; the summary only rides
// the tail as a <system-reminder> block so semantics lost to previews stay
// available to the agent for the rest of the run.
//
// Anti-thrash: the projection is rebuilt from the same session history on
// every model call, so the pass would re-summarize the same payloads forever.
// SummaryState tracks which FunctionResponse IDs are already covered by the
// running summary — only the delta is summarized (iterative update), and a
// failure ladder (60s/300s/900s) backs off a broken summarizer instead of
// paying for it on every call.
package contextx

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

const (
	// summaryEntryHeadBytes and summaryEntryTailBytes bound each entry fed to
	// the summarizer: long tool outputs keep their leading context and their
	// trailing diagnostics without paying full length.
	summaryEntryHeadBytes = 4_000
	summaryEntryTailBytes = 1_500

	// summaryBatchMaxChars bounds one summarizer request even when many old
	// payloads age out of the keep-recent window at once.
	summaryBatchMaxChars = 160_000

	// summaryMaxChars bounds the running summary itself: it re-enters the
	// request tail on every call after the first compaction.
	summaryMaxChars = 16_000
)

// cooldownLadder backs off summarizer failures: one cheap-model outage must
// not become a per-call tax.
var cooldownLadder = [...]time.Duration{60 * time.Second, 300 * time.Second, 900 * time.Second}

// CompactedEntry is one tool payload about to be replaced by a compact
// preview, with the original bytes needed to distill it.
type CompactedEntry struct {
	// ID is the FunctionResponse ID (delta tracking key).
	ID string
	// Tool is the lowercased tool name.
	Tool string
	// Label is the one-line call target (e.g. read_file("main.go:40-80")).
	Label string
	// Text is the original payload, capped per entry before handoff.
	Text string
}

// Summarizer distills delta into an updated running summary. prev is the
// current running summary ("" on the first pass): implementations must
// preserve it and only add the delta (iterative update, never a rewrite).
type Summarizer func(ctx context.Context, prev string, delta []CompactedEntry) (string, error)

// SummaryState carries the running summary, the IDs it already covers and
// the failure cooldown for one agent run. Not safe to share across agents.
type SummaryState struct {
	mu           sync.Mutex
	summarized   map[string]bool
	summary      string
	failures     int
	cooldownEnds time.Time
}

// NewSummaryState returns an empty per-run state.
func NewSummaryState() *SummaryState {
	return &SummaryState{summarized: make(map[string]bool)}
}

// current returns the running summary ("" before the first successful pass).
func (s *SummaryState) current() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summary
}

// pending filters entries whose ID is not covered by the running summary yet.
func (s *SummaryState) pending(entries []CompactedEntry) []CompactedEntry {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []CompactedEntry
	for _, e := range entries {
		if !s.summarized[e.ID] {
			out = append(out, e)
		}
	}
	return out
}

// ready reports whether a new summarizer call is allowed right now.
func (s *SummaryState) ready(now time.Time) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return now.After(s.cooldownEnds)
}

// fail arms the cooldown ladder (60s → 300s → 900s).
func (s *SummaryState) fail(now time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures++
	idx := s.failures - 1
	if idx >= len(cooldownLadder) {
		idx = len(cooldownLadder) - 1
	}
	s.cooldownEnds = now.Add(cooldownLadder[idx])
}

// commit stores the updated summary and marks the covered IDs.
func (s *SummaryState) commit(summary string, covered []CompactedEntry) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.summarized == nil {
		s.summarized = make(map[string]bool)
	}
	for _, e := range covered {
		if e.ID != "" {
			s.summarized[e.ID] = true
		}
	}
	s.summary = capSummaryText(summary)
	s.failures = 0
	s.cooldownEnds = time.Time{}
}

// capEntryText bounds one payload before it is handed to the summarizer:
// head + elided marker + tail (trailing diagnostics survive).
func capEntryText(text string) string {
	if len(text) <= summaryEntryHeadBytes+summaryEntryTailBytes {
		return text
	}
	head := text[:summaryEntryHeadBytes]
	tail := text[len(text)-summaryEntryTailBytes:]
	return head + "\n[… " + fmt.Sprintf("%d", len(text)-summaryEntryHeadBytes-summaryEntryTailBytes) + " caracteres omitidos no meio …]\n" + tail
}

func capSummaryText(summary string) string {
	return strx.TruncateBytes(strings.TrimSpace(summary), summaryMaxChars, "\n…(resumo truncado)")
}

// capBatch bounds one summarizer request; entries beyond the cap are noted.
func capBatch(entries []CompactedEntry) (kept []CompactedEntry, omitted int) {
	total := 0
	for i, e := range entries {
		if total+len(e.Text) > summaryBatchMaxChars && i > 0 {
			return entries[:i], len(entries) - i
		}
		total += len(e.Text)
	}
	return entries, 0
}

// RenderDelta formats the delta entries for the summarizer prompt.
func RenderDelta(entries []CompactedEntry, omitted int) string {
	var b strings.Builder
	for _, e := range entries {
		label := e.Label
		if label == "" {
			label = e.Tool
		}
		fmt.Fprintf(&b, "### %s\n%s\n\n", label, e.Text)
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "(mais %d saídas antigas omitidas por limite de lote)\n", omitted)
	}
	return b.String()
}

// RenderSummaryBlock wraps the running summary as a tail reminder block.
func RenderSummaryBlock(summary string) string {
	return "<system-reminder>\n<context_summary>\n" + strings.TrimSpace(summary) + "\n</context_summary>\n</system-reminder>"
}

// AppendSummaryReminder clones the last Content and appends block as a
// trailing text part — the same non-destructive projection semantics as the
// compaction itself (session history on disk stays untouched).
func AppendSummaryReminder(req *model.LLMRequest, block string) {
	if req == nil || strings.TrimSpace(block) == "" || len(req.Contents) == 0 {
		return
	}
	lastIdx := len(req.Contents) - 1
	orig := req.Contents[lastIdx]
	if orig == nil {
		req.Contents[lastIdx] = &genai.Content{
			Role:  genai.RoleUser,
			Parts: []*genai.Part{{Text: block}},
		}
		return
	}
	contentCopy := *orig
	contentCopy.Parts = make([]*genai.Part, len(orig.Parts), len(orig.Parts)+1)
	copy(contentCopy.Parts, orig.Parts)
	contentCopy.Parts = append(contentCopy.Parts, &genai.Part{Text: block})
	sliceCopy := make([]*genai.Content, len(req.Contents))
	copy(sliceCopy, req.Contents)
	sliceCopy[lastIdx] = &contentCopy
	req.Contents = sliceCopy
}

// summarizePending runs one delta pass: it distills the not-yet-covered
// entries into the running summary under the cooldown ladder.
func summarizePending(ctx context.Context, b Budget, state *SummaryState, entries []CompactedEntry, now time.Time) {
	if b.Summarizer == nil || state == nil || len(entries) == 0 {
		return
	}
	delta := state.pending(entries)
	if len(delta) == 0 {
		return
	}
	if !state.ready(now) {
		return
	}
	delta, _ = capBatch(delta)
	summary, err := b.Summarizer(ctx, state.current(), delta)
	if err != nil || strings.TrimSpace(summary) == "" {
		state.fail(now)
		return
	}
	state.commit(summary, delta)
}

// injectSummary appends the running summary to the request tail once one
// exists. The block is small (<=16 KiB) and only appears after the aggressive
// cut has already fired.
func injectSummary(req *model.LLMRequest, state *SummaryState) {
	if req == nil {
		return
	}
	summary := state.current()
	if summary == "" {
		return
	}
	AppendSummaryReminder(req, RenderSummaryBlock(summary))
}
