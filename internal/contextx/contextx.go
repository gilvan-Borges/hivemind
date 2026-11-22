// Package contextx provides the single token-aware, non-destructive in-turn
// context compaction shared by the orchestrator and the resident
// (Spec 02 §3.3: unified context_guard.go + resident/prompt.go).
//
// Compaction is a read-projection applied to the LLMRequest right before it is
// sent: Content/Part/FunctionResponse structs are cloned, never mutated in
// place, so sessionx history on disk stays byte-identical (raw transcript is
// the source of truth; a compacted view never overwrites it).
package contextx

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

const (
	// DefaultContextWindow is the conservative window used when the routed
	// model is unknown to the catalog.
	DefaultContextWindow = 128_000

	// MicroThresholdPct of the context window above which older large tool
	// outputs are micro-compacted (Spec 02: 55%).
	MicroThresholdPct = 0.55

	// SummaryThresholdPct of the context window above which the aggressive
	// structural cut kicks in (Spec 02: 75%).
	SummaryThresholdPct = 0.75

	defaultKeepToolResults  = 8
	microCompactThreshold   = 600  // bytes; payloads at or below this stay verbatim
	summaryCompactThreshold = 300  // bytes for the aggressive pass
	microPreviewBytes       = 420
	summaryPreviewBytes     = 240
)

// compactableInTurnTools lists read/search/diff tools whose old outputs are
// safe to replace with a compact preview + conclusion note.
var compactableInTurnTools = map[string]bool{
	"read_file":       true,
	"read_skill":      true,
	"list_dir":        true,
	"search_text":     true,
	"find_files":      true,
	"github_pr_diff":  true,
	"github_pr_files": true,
	"git_diff":        true,
	"git_status":      true,
	"run_shell":       true,
}

// neverCompactInTurnTools keeps governance-critical outputs verbatim.
var neverCompactInTurnTools = map[string]bool{
	"github_get_pr":      true,
	"github_ci_status":   true,
	"delegate_to_squad":  true,
	"qa_approve":         true,
	"qa_request_changes": true,
	"sdd_docs_ready":     true,
	// The enable flow is the audit trail of which MCP server the user
	// approved for this task; a preview would hide that record.
	"mcp_enable_server": true,
}

// compactableToolName reports whether one older FunctionResponse payload may
// be replaced by a compact preview. Besides the fixed read/search/diff set,
// every MCP tool response qualifies: mcp_call and the direct wrappers are
// named mcp_<server>_<tool>, and remote payloads are re-fetchable by calling
// the tool again, so stale copies only burn request tokens.
func compactableToolName(name string) bool {
	return compactableInTurnTools[name] || strings.HasPrefix(name, "mcp_")
}

// Stats reports what one compaction pass saved, so the orchestrator can emit
// usage events (compacted payloads / tokens saved): without it, token savings
// are invisible.
type Stats struct {
	// Compacted is how many payloads were replaced with compact previews.
	Compacted int
	// BytesSaved is how many request bytes the pass removed.
	BytesSaved int
	// TokensSaved is the conservative token estimate of BytesSaved using
	// the same bytes/4 heuristic as EstimateRequestTokens.
	TokensSaved int
	// Semantic marks a pass that replaced the old conversation head with an
	// LLM-generated summary carrier (SummarizeOldTurns) instead of previews.
	Semantic bool
}

// Budget controls one compaction pass.
type Budget struct {
	// ContextWindow is the model's token window. Zero falls back to
	// DefaultContextWindow (unless ContextWindowFn returns > 0).
	ContextWindow int
	// ContextWindowFn optionally resolves the active model's token window
	// dynamically per call (e.g. after Jev routing or mid-run failover).
	ContextWindowFn func() int
	// KeepRecent is the minimum number of recent tool results kept verbatim.
	// Zero falls back to defaultKeepToolResults.
	KeepRecent int
	// OnCompact, when set, receives one Stats per pass that actually trimmed
	// payloads. No-op passes stay silent to avoid event spam.
	OnCompact func(Stats)
	// MaxToolEntryBytes optionally caps individual older (non-latest-turn) tool
	// outputs via head-and-tail truncation even before the global 55% compaction
	// threshold fires (Codex subagent_default_context_limits pattern). Zero disables
	// the proactive per-entry cap.
	MaxToolEntryBytes int
	// Summarizer, when set, is called once the request crosses the summary
	// tier (75%) with a rendered transcript of the old conversation head; the
	// returned summary replaces that head in the request projection. The
	// persisted session history is never touched: the summary is a projection
	// carrier, and any summarizer failure falls back to the heuristic cut.
	Summarizer func(ctx context.Context, transcript string) (string, error)
}

func (b Budget) window() int {
	if b.ContextWindowFn != nil {
		if w := b.ContextWindowFn(); w > 0 {
			return w
		}
	}
	if b.ContextWindow > 0 {
		return b.ContextWindow
	}
	return DefaultContextWindow
}

func (b Budget) keepRecent() int {
	if b.KeepRecent > 0 {
		return b.KeepRecent
	}
	return defaultKeepToolResults
}

// EstimateRequestTokens returns a fast conservative token estimate for req:
// visible text, serialized function arguments, and the system instruction,
// divided by 4 bytes/token (the canonical ASCII-ish approximation).
func EstimateRequestTokens(req *model.LLMRequest) int {
	if req == nil {
		return 0
	}
	totalBytes := 0
	if req.Config != nil && req.Config.SystemInstruction != nil {
		for _, p := range req.Config.SystemInstruction.Parts {
			if p != nil {
				totalBytes += len(p.Text)
			}
		}
	}
	if req.Config != nil {
		for _, tl := range req.Config.Tools {
			if tl == nil {
				continue
			}
			for _, decl := range tl.FunctionDeclarations {
				if decl == nil {
					continue
				}
				totalBytes += len(decl.Name) + len(decl.Description)
			}
		}
	}
	for _, c := range req.Contents {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			if p == nil {
				continue
			}
			totalBytes += len(p.Text)
			if p.FunctionCall != nil {
				totalBytes += estimateArgsBytes(p.FunctionCall.Args)
			}
			if p.FunctionResponse != nil {
				totalBytes += estimateArgsBytes(p.FunctionResponse.Response)
			}
			if p.InlineData != nil {
				// Images are charged per tile by most providers; one
				// conservative tile is the honest floor.
				totalBytes += 1024
			}
		}
	}
	if totalBytes <= 0 {
		return 0
	}
	return (totalBytes + 3) / 4
}

func estimateArgsBytes(args map[string]any) int {
	if len(args) == 0 {
		return 0
	}
	var b strings.Builder
	appendArgValue(&b, args)
	return b.Len()
}

func appendArgValue(b *strings.Builder, v any) {
	switch t := v.(type) {
	case string:
		b.WriteString(t)
	case map[string]any:
		for k, val := range t {
			b.WriteString(k)
			b.WriteByte(':')
			appendArgValue(b, val)
		}
	case []any:
		for _, val := range t {
			appendArgValue(b, val)
		}
	default:
		if v != nil {
			fmt.Fprintf(b, "%v", v)
		}
	}
}

// NewCompactionCallback returns an ADK BeforeModelCallback that trims older,
// large read/search/diff tool outputs inside the active turn without mutating
// the persisted session history. The pass only runs once the request is over
// MicroThresholdPct of the model window (token-aware, Spec 02 §3.3), and
// escalates to the aggressive cut at SummaryThresholdPct. When Budget carries
// a Summarizer, the summary tier first attempts one semantic pass that
// replaces the old conversation head with a summary carrier.
func NewCompactionCallback(b Budget) llmagent.BeforeModelCallback {
	return func(actx agent.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
		if st, applied := SummarizeOldTurns(actx, req, b); applied {
			if b.OnCompact != nil {
				b.OnCompact(st)
			}
		}
		compacted, bytesSaved := CompactRequest(req, b)
		if compacted > 0 && b.OnCompact != nil {
			b.OnCompact(Stats{
				Compacted:   compacted,
				BytesSaved:  bytesSaved,
				TokensSaved: (bytesSaved + 3) / 4,
			})
		}
		return nil, nil
	}
}

// SummarizedConversationTag opens the synthetic carrier that replaces the old
// conversation head. A request whose leading content already carries the tag
// is skipped by SummarizeOldTurns, so the pass is self-limiting.
const SummarizedConversationTag = "<conversa_anterior_resumida>"

const (
	// semanticTailContents is the minimum verbatim tail kept by the semantic
	// pass: the model must still see the freshest tool exchanges and the
	// current turn to keep working.
	semanticTailContents = 6
	// semanticTranscriptMaxBytes bounds the transcript handed to the
	// summarizer (~24k tokens): enough signal, one bounded call.
	semanticTranscriptMaxBytes = 96 * 1024
	// semanticSummaryMaxBytes caps the returned summary defensively so a
	// misbehaving summarizer cannot inflate the request instead of shrinking it.
	semanticSummaryMaxBytes = 12 * 1024
	// semanticPartPreviewBytes bounds each single part inside the transcript.
	semanticPartPreviewBytes = 600
)

// SummarizeOldTurns replaces the old conversation head of the request
// projection with one synthetic user content carrying an LLM-generated
// summary, keeping a verbatim tail of the most recent contents. It is the
// semantic tier of compaction: previews keep byte fragments, the summary keeps
// meaning. The persisted session transcript is never modified; a summarizer
// failure simply returns applied=false so the heuristic structural cut in
// CompactRequest still bounds the request.
func SummarizeOldTurns(ctx context.Context, req *model.LLMRequest, b Budget) (Stats, bool) {
	if req == nil || len(req.Contents) < semanticTailContents+2 || b.Summarizer == nil {
		return Stats{}, false
	}
	if ContextUsageRatio(req, b) < SummaryThresholdPct {
		return Stats{}, false
	}
	if head := firstContentText(req); strings.Contains(head, SummarizedConversationTag) {
		// Already summarized once: a second pass would summarize the summary.
		return Stats{}, false
	}

	cut := summarizeCutIndex(req)
	if cut < 2 {
		return Stats{}, false
	}
	transcript := renderConversationTranscript(req.Contents[:cut])
	if strings.TrimSpace(transcript) == "" {
		return Stats{}, false
	}
	summary, err := b.Summarizer(ctx, transcript)
	if err != nil {
		// Fail-open: the heuristic cut below still bounds the request.
		return Stats{}, false
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return Stats{}, false
	}
	summary = strx.TruncateBytes(summary, semanticSummaryMaxBytes, "\n[resumo truncado pelo teto defensivo]")

	savedBytes := contentsTextBytes(req.Contents[:cut])
	block := SummarizedConversationTag + "\n" +
		"Resumo da conversa anterior desta sessão. Os turnos antigos foram compactados semanticamente na visão enviada ao modelo; o transcript completo permanece registrado na sessão. Use este resumo como estado consolidado do que já foi feito:\n\n" +
		summary + "\n\n" +
		"Orientação: não refaça trabalho já concluído; se precisar do conteúdo integral de um ponto citado, releia pontualmente com read_file em vez de varrer o repositório.\n" +
		"</conversa_anterior_resumida>"
	carrier := &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{Text: block}},
	}
	next := make([]*genai.Content, 0, 1+len(req.Contents)-cut)
	next = append(next, carrier)
	next = append(next, req.Contents[cut:]...)
	req.Contents = next
	return Stats{
		Compacted:   cut,
		BytesSaved:  savedBytes,
		TokensSaved: (savedBytes + 3) / 4,
		Semantic:    true,
	}, true
}

// summarizeCutIndex picks the head/tail boundary for the semantic pass. The
// tail keeps the most recent semanticTailContents contents, moved back when
// needed so no FunctionCall ends up in the head while its FunctionResponse
// stays in the tail (orphaned calls break strict providers).
func summarizeCutIndex(req *model.LLMRequest) int {
	cut := len(req.Contents) - semanticTailContents
	if cut < 1 {
		return 0
	}
	for {
		respondedInHead := make(map[string]bool)
		for _, c := range req.Contents[:cut] {
			if c == nil {
				continue
			}
			for _, p := range c.Parts {
				if p != nil && p.FunctionResponse != nil && p.FunctionResponse.ID != "" {
					respondedInHead[p.FunctionResponse.ID] = true
				}
			}
		}
		splitAt := -1
		for i := cut - 1; i >= 0; i-- {
			c := req.Contents[i]
			if c == nil {
				continue
			}
			for _, p := range c.Parts {
				if p != nil && p.FunctionCall != nil && p.FunctionCall.ID != "" && !respondedInHead[p.FunctionCall.ID] {
					splitAt = i
					break
				}
			}
			if splitAt >= 0 {
				break
			}
		}
		if splitAt < 1 {
			return cut
		}
		if splitAt <= 1 {
			// The conversation is one long tool exchange: no safe head to
			// summarize.
			return 0
		}
		cut = splitAt
	}
}

// renderConversationTranscript flattens contents into a bounded, role-tagged
// transcript for the summarizer call. Function arguments and tool results get
// short previews: the summary needs the gist, not the bytes.
func renderConversationTranscript(contents []*genai.Content) string {
	var b strings.Builder
	for _, c := range contents {
		if c == nil {
			continue
		}
		role := "user"
		if c.Role != "" {
			role = c.Role
		}
		for _, p := range c.Parts {
			if p == nil {
				continue
			}
			switch {
			case p.FunctionCall != nil:
				fc := p.FunctionCall
				fmt.Fprintf(&b, "tool_call %s", fc.Name)
				if len(fc.Args) > 0 {
					args := fmt.Sprint(fc.Args)
					fmt.Fprintf(&b, " %s", strx.TruncateBytes(args, 240, "…"))
				}
				b.WriteString("\n")
			case p.FunctionResponse != nil:
				fr := p.FunctionResponse
				text := fmt.Sprint(fr.Response)
				fmt.Fprintf(&b, "tool_result %s: %s\n", fr.Name, strx.TruncateBytes(text, semanticPartPreviewBytes, "…"))
			case p.Text != "":
				fmt.Fprintf(&b, "%s: %s\n", role, strx.TruncateBytes(p.Text, 4*semanticPartPreviewBytes, "…"))
			}
		}
	}
	out := b.String()
	if len(out) > semanticTranscriptMaxBytes {
		// Keep the most recent slice: it carries the state closest to now.
		tail := out[len(out)-semanticTranscriptMaxBytes:]
		for len(tail) > 0 && (tail[0]&0xC0) == 0x80 {
			tail = tail[1:]
		}
		return "[início do transcript omitido pelo teto]\n" + tail
	}
	return out
}

// firstContentText returns the concatenated text parts of the first content.
func firstContentText(req *model.LLMRequest) string {
	if req == nil || len(req.Contents) == 0 || req.Contents[0] == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range req.Contents[0].Parts {
		if p != nil && p.Text != "" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// contentsTextBytes sums the text bytes of the contents being replaced.
func contentsTextBytes(contents []*genai.Content) int {
	total := 0
	for _, c := range contents {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			if p != nil {
				total += len(p.Text)
			}
		}
	}
	return total
}

// ContextUsageRatio returns the estimated fraction (0.0..1.0+) of the model's
// context window currently occupied by req.
func ContextUsageRatio(req *model.LLMRequest, b Budget) float64 {
	if req == nil || len(req.Contents) == 0 {
		return 0
	}
	window := b.window()
	if window <= 0 {
		return 0
	}
	est := EstimateRequestTokens(req)
	if est <= 0 {
		return 0
	}
	return float64(est) / float64(window)
}

// ContextWindowGuidance returns a concise <context_window_guidance> block when
// req exceeds MicroThresholdPct (55%) of the active model window, helping the
// agent conserve tokens and wrap up cleanly without mutating SystemInstruction/Tools.
func ContextWindowGuidance(req *model.LLMRequest, b Budget) string {
	ratio := ContextUsageRatio(req, b)
	if ratio < MicroThresholdPct {
		return ""
	}
	pct := int(ratio * 100)
	if pct > 99 {
		pct = 99
	}
	if ratio >= SummaryThresholdPct {
		return fmt.Sprintf("<context_window_guidance>\nUso estimado da janela de contexto: ~%d%% (alto). NÃO faça novas leituras exploratórias redundantes — finalize as edições essenciais e entregue o resumo agora.\n</context_window_guidance>", pct)
	}
	return fmt.Sprintf("<context_window_guidance>\nUso estimado da janela de contexto: ~%d%%. Evite releituras de arquivos já vistos, prefira trechos pontuais e conclua a fatia com economia de tokens.\n</context_window_guidance>", pct)
}

// CompactRequest applies one non-destructive compaction pass over req and
// reports how many payloads were compacted and how many bytes were saved.
func CompactRequest(req *model.LLMRequest, b Budget) (compactedCount, bytesSaved int) {
	if req == nil || len(req.Contents) == 0 {
		return 0, 0
	}
	window := b.window()
	est := EstimateRequestTokens(req)
	if est <= 0 {
		return 0, 0
	}
	ratio := float64(est) / float64(window)
	if ratio < MicroThresholdPct {
		// Even below 55%, enforce proactive per-entry head-and-tail caps for
		// older oversized tool outputs when MaxToolEntryBytes is configured
		// (subagent_default_context_limits).
		if b.MaxToolEntryBytes > 0 {
			return capOversizedOlderToolOutputs(req, b.MaxToolEntryBytes)
		}
		return 0, 0
	}

	keepRecent := b.keepRecent()
	compactThreshold := microCompactThreshold
	previewBytes := microPreviewBytes
	if ratio >= SummaryThresholdPct {
		// Structural cut: keep only the 2 most recent tool results verbatim
		// and squeeze harder (Spec 02: 75% summary tier).
		if keepRecent > 2 {
			keepRecent = 2
		}
		compactThreshold = summaryCompactThreshold
		previewBytes = summaryPreviewBytes
	}
	return compactLLMRequestInTurn(req, keepRecent, compactThreshold, previewBytes)
}

// capOversizedOlderToolOutputs applies head-and-tail truncation to older (non-latest-turn)
// FunctionResponse entries exceeding maxEntryBytes, preserving both leading headers and
// trailing error/test summaries while keeping subagent input tokens lean.
func capOversizedOlderToolOutputs(req *model.LLMRequest, maxEntryBytes int) (compactedCount, bytesSaved int) {
	if req == nil || maxEntryBytes <= 0 {
		return 0, 0
	}
	lastCallContentIdx := -1
	lastRespContentIdx := -1
	for ci, content := range req.Contents {
		if content == nil {
			continue
		}
		for _, part := range content.Parts {
			if part == nil {
				continue
			}
			if part.FunctionCall != nil {
				lastCallContentIdx = ci
			}
			if part.FunctionResponse != nil {
				lastRespContentIdx = ci
			}
		}
	}
	clonedSlice := false
	for ci, origContent := range req.Contents {
		if origContent == nil {
			continue
		}
		// Never truncate the latest tool-response turn so the model sees fresh outputs in full.
		if ci == lastRespContentIdx || (lastCallContentIdx >= 0 && ci > lastCallContentIdx) {
			continue
		}
		var newParts []*genai.Part
		contentChanged := false
		for pi, origPart := range origContent.Parts {
			if origPart != nil && origPart.FunctionResponse != nil {
				fr := origPart.FunctionResponse
				name := strings.ToLower(strings.TrimSpace(fr.Name))
				if !neverCompactInTurnTools[name] && compactableToolName(name) {
					if rawText, ok := fr.Response["text"].(string); ok && len(rawText) > maxEntryBytes {
						trimmed := truncateHeadTailTokenAware(rawText, maxEntryBytes)
						if len(trimmed) < len(rawText) {
							if !contentChanged {
								newParts = make([]*genai.Part, 0, len(origContent.Parts))
								newParts = append(newParts, origContent.Parts[:pi]...)
								contentChanged = true
							}
							partCopy := *origPart
							frCopy := *fr
							respCopy := make(map[string]any, len(fr.Response))
							for k, v := range fr.Response {
								respCopy[k] = v
							}
							respCopy["text"] = trimmed
							frCopy.Response = respCopy
							partCopy.FunctionResponse = &frCopy
							newParts = append(newParts, &partCopy)
							compactedCount++
							bytesSaved += len(rawText) - len(trimmed)
							continue
						}
					}
				}
			}
			if contentChanged {
				newParts = append(newParts, origPart)
			}
		}
		if contentChanged {
			if !clonedSlice {
				next := make([]*genai.Content, len(req.Contents))
				copy(next, req.Contents)
				req.Contents = next
				clonedSlice = true
			}
			contentCopy := *origContent
			contentCopy.Parts = newParts
			req.Contents[ci] = &contentCopy
		}
	}
	return compactedCount, bytesSaved
}

func truncateHeadTailTokenAware(raw string, maxBytes int) string {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) <= maxBytes || maxBytes < 64 {
		return trimmed
	}
	headBytes := (maxBytes * 45) / 100
	tailBytes := (maxBytes * 45) / 100
	omittedBytes := len(trimmed) - (headBytes + tailBytes)
	if omittedBytes <= 40 {
		return trimmed
	}
	approxTokens := (omittedBytes + 3) / 4
	head := strx.TruncateBytes(trimmed, headBytes, "")
	tailRaw := trimmed[len(trimmed)-tailBytes:]
	// Advance tailRaw to a valid UTF-8 rune boundary if needed.
	for len(tailRaw) > 0 && (tailRaw[0]&0xC0) == 0x80 {
		tailRaw = tailRaw[1:]
	}
	return fmt.Sprintf("%s\n<truncated omitted_approx_tokens=\"%d\" />\n%s", head, approxTokens, tailRaw)
}

type toolRespLoc struct {
	contentIdx int
	partIdx    int
}

// compactLLMRequestInTurn replaces older, large FunctionResponse payloads in
// req.Contents with compact previews + metadata notes, cloning only the
// modified Content/Part/FunctionResponse structs so the underlying session
// history is untouched.
func compactLLMRequestInTurn(req *model.LLMRequest, keepRecent, compactThreshold, previewBytes int) (compactedCount int, bytesSaved int) {
	callTargets := make(map[string]string)
	var responses []toolRespLoc
	var calls []toolRespLoc
	lastRespContentIdx := -1
	lastCallContentIdx := -1
	for ci, content := range req.Contents {
		if content == nil {
			continue
		}
		for pi, part := range content.Parts {
			if part == nil {
				continue
			}
			if part.FunctionCall != nil {
				if part.FunctionCall.ID != "" {
					callTargets[part.FunctionCall.ID] = formatCompactTarget(part.FunctionCall.Name, part.FunctionCall.Args)
				}
				calls = append(calls, toolRespLoc{contentIdx: ci, partIdx: pi})
				lastCallContentIdx = ci
			}
			if part.FunctionResponse != nil {
				responses = append(responses, toolRespLoc{contentIdx: ci, partIdx: pi})
				lastRespContentIdx = ci
			}
		}
	}
	if len(responses) <= keepRecent && len(calls) <= keepRecent {
		return 0, 0
	}

	toCompactResp := make(map[int]map[int]bool)
	if len(responses) > keepRecent {
		cutoff := len(responses) - keepRecent
		for _, loc := range responses[:cutoff] {
			// NEVER compact any response from the latest tool-response turn
			// (whether grouped in lastRespContentIdx or split across Contents
			// after lastCallContentIdx): a parallel batch of 7-10 tool calls
			// must be seen in full by the model on the very next turn, or it
			// enters a re-read loop.
			if loc.contentIdx == lastRespContentIdx || (lastCallContentIdx >= 0 && loc.contentIdx > lastCallContentIdx) {
				continue
			}
			if toCompactResp[loc.contentIdx] == nil {
				toCompactResp[loc.contentIdx] = make(map[int]bool)
			}
			toCompactResp[loc.contentIdx][loc.partIdx] = true
		}
	}
	toCompactCall := make(map[int]map[int]bool)
	if len(calls) > keepRecent {
		cutoff := len(calls) - keepRecent
		for _, loc := range calls[:cutoff] {
			if loc.contentIdx == lastCallContentIdx {
				continue
			}
			if toCompactCall[loc.contentIdx] == nil {
				toCompactCall[loc.contentIdx] = make(map[int]bool)
			}
			toCompactCall[loc.contentIdx][loc.partIdx] = true
		}
	}

	affectedContents := make(map[int]bool, len(toCompactResp)+len(toCompactCall))
	for ci := range toCompactResp {
		affectedContents[ci] = true
	}
	for ci := range toCompactCall {
		affectedContents[ci] = true
	}

	clonedSlice := false
	for ci := range affectedContents {
		origContent := req.Contents[ci]
		if origContent == nil {
			continue
		}
		respSet := toCompactResp[ci]
		callSet := toCompactCall[ci]
		var newParts []*genai.Part
		contentChanged := false

		for pi, origPart := range origContent.Parts {
			if origPart == nil {
				if contentChanged {
					newParts = append(newParts, origPart)
				}
				continue
			}
			if respSet[pi] && origPart.FunctionResponse != nil {
				fr := origPart.FunctionResponse
				newResp, saved, ok := compactFunctionResponseMap(fr, callTargets[fr.ID], compactThreshold, previewBytes)
				if ok {
					if !contentChanged {
						newParts = make([]*genai.Part, 0, len(origContent.Parts))
						newParts = append(newParts, origContent.Parts[:pi]...)
						contentChanged = true
					}
					partCopy := *origPart
					frCopy := *fr
					frCopy.Response = newResp
					partCopy.FunctionResponse = &frCopy
					newParts = append(newParts, &partCopy)
					compactedCount++
					bytesSaved += saved
					continue
				}
			}
			if callSet[pi] && origPart.FunctionCall != nil {
				fc := origPart.FunctionCall
				newArgs, saved, ok := compactFunctionCallArgs(fc, compactThreshold)
				if ok {
					if !contentChanged {
						newParts = make([]*genai.Part, 0, len(origContent.Parts))
						newParts = append(newParts, origContent.Parts[:pi]...)
						contentChanged = true
					}
					partCopy := *origPart
					fcCopy := *fc
					fcCopy.Args = newArgs
					partCopy.FunctionCall = &fcCopy
					newParts = append(newParts, &partCopy)
					compactedCount++
					bytesSaved += saved
					continue
				}
			}
			if contentChanged {
				newParts = append(newParts, origPart)
			}
		}

		if contentChanged {
			if !clonedSlice {
				next := make([]*genai.Content, len(req.Contents))
				copy(next, req.Contents)
				req.Contents = next
				clonedSlice = true
			}
			contentCopy := *origContent
			contentCopy.Parts = newParts
			req.Contents[ci] = &contentCopy
		}
	}

	return compactedCount, bytesSaved
}

func compactFunctionCallArgs(fc *genai.FunctionCall, compactThreshold int) (map[string]any, int, bool) {
	if fc == nil || len(fc.Args) == 0 {
		return nil, 0, false
	}
	var fields []string
	switch strings.ToLower(strings.TrimSpace(fc.Name)) {
	case "write_file", "write_doc":
		fields = []string{"content"}
	case "edit_file":
		fields = []string{"old_text", "new_text"}
	default:
		return nil, 0, false
	}
	var cloned map[string]any
	saved := 0
	for _, field := range fields {
		raw, ok := fc.Args[field].(string)
		if !ok || len(raw) <= compactThreshold {
			continue
		}
		note := fmt.Sprintf("[argumento %s de %s compactado: %s já aplicado no arquivo]", field, fc.Name, formatCompactSize(len(raw)))
		if len(note) >= len(raw) {
			continue
		}
		if cloned == nil {
			cloned = make(map[string]any, len(fc.Args))
			for k, v := range fc.Args {
				cloned[k] = v
			}
		}
		cloned[field] = note
		saved += len(raw) - len(note)
	}
	if cloned == nil {
		return nil, 0, false
	}
	return cloned, saved, true
}

func compactFunctionResponseMap(fr *genai.FunctionResponse, callTarget string, compactThreshold, previewBytes int) (map[string]any, int, bool) {
	if fr == nil || len(fr.Response) == 0 {
		return nil, 0, false
	}
	name := strings.ToLower(strings.TrimSpace(fr.Name))
	if neverCompactInTurnTools[name] || !compactableToolName(name) {
		return nil, 0, false
	}
	// Never compact error responses so the model can still diagnose failures.
	if errVal, hasErr := fr.Response["error"]; hasErr && strings.TrimSpace(fmt.Sprint(errVal)) != "" {
		return nil, 0, false
	}

	label := callTarget
	if label == "" {
		label = name
	}

	if rawText, ok := fr.Response["text"].(string); ok {
		if len(rawText) <= compactThreshold {
			return nil, 0, false
		}
		compacted := buildCompactNotice(name, label, rawText, previewBytes)
		if len(compacted) >= len(rawText) {
			return nil, 0, false
		}
		cloned := make(map[string]any, len(fr.Response))
		for k, v := range fr.Response {
			cloned[k] = v
		}
		cloned["text"] = compacted
		return cloned, len(rawText) - len(compacted), true
	}

	if entriesRaw, ok := fr.Response["entries"]; ok && entriesRaw != nil {
		serialized := fmt.Sprint(entriesRaw)
		if len(serialized) <= compactThreshold {
			return nil, 0, false
		}
		compacted := buildCompactNotice(name, label, serialized, previewBytes)
		if len(compacted) >= len(serialized) {
			return nil, 0, false
		}
		cloned := make(map[string]any, len(fr.Response))
		for k, v := range fr.Response {
			cloned[k] = v
		}
		cloned["entries"] = []string{compacted}
		return cloned, len(serialized) - len(compacted), true
	}

	return nil, 0, false
}

func buildCompactNotice(toolName, label, raw string, previewBytes int) string {
	lines := strings.Count(raw, "\n") + 1
	sizeLabel := formatCompactSize(len(raw))
	preview := truncateHeadTailTokenAware(raw, previewBytes)
	hint := "NÃO releia sem necessidade; use as conclusões já obtidas"
	if toolName == "read_file" {
		hint = "já lido neste turno; NÃO releia o mesmo trecho — avance na tarefa ou leia outra faixa start_line/end_line só se realmente faltar um detalhe"
	} else if toolName == "github_pr_diff" {
		hint = "diff já lido em turno anterior; NÃO chame github_pr_diff novamente para o mesmo arquivo — use as informações já analisadas"
	}
	return fmt.Sprintf("%s\n[saída anterior de %s compactada: %d linhas / %s — %s]", preview, label, lines, sizeLabel, hint)
}

func formatCompactSize(bytesLen int) string {
	if bytesLen < 1024 {
		return fmt.Sprintf("%d B", bytesLen)
	}
	return fmt.Sprintf("%.1f KiB", float64(bytesLen)/1024.0)
}

func formatCompactTarget(name string, args map[string]any) string {
	if len(args) == 0 {
		return name
	}
	detail := summarizeToolArgs(name, args)
	if detail == "" {
		return name
	}
	return fmt.Sprintf("%s(%q)", name, detail)
}

// summarizeToolArgs renders a one-line label of the most relevant call argument
// so the compact notice can point back to the exact file/query/PR it replaces.
func summarizeToolArgs(name string, args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	switch name {
	case "read_file":
		if p, ok := args["path"]; ok && p != nil {
			s := strings.TrimSpace(fmt.Sprint(p))
			if s != "" && s != "<nil>" {
				start := parseIntArg(args["start_line"])
				end := parseIntArg(args["end_line"])
				if start > 0 && end > 0 {
					s = fmt.Sprintf("%s:%d-%d", s, start, end)
				} else if start > 0 {
					s = fmt.Sprintf("%s:%d", s, start)
				}
				return strx.TruncateRunes(strings.ReplaceAll(s, "\n", " "), 120, "…")
			}
		}
	case "search_text":
		q := strings.TrimSpace(fmt.Sprint(args["query"]))
		if q != "" && q != "<nil>" {
			if g := strings.TrimSpace(fmt.Sprint(args["glob"])); g != "" && g != "<nil>" {
				q += " (" + g + ")"
			} else if p := strings.TrimSpace(fmt.Sprint(args["path"])); p != "" && p != "<nil>" {
				q += " em " + p
			}
			return strx.TruncateRunes(strings.ReplaceAll(q, "\n", " "), 120, "…")
		}
	case "find_files":
		pat := strings.TrimSpace(fmt.Sprint(args["pattern"]))
		if pat != "" && pat != "<nil>" {
			if p := strings.TrimSpace(fmt.Sprint(args["path"])); p != "" && p != "<nil>" {
				pat += " em " + p
			}
			return strx.TruncateRunes(strings.ReplaceAll(pat, "\n", " "), 120, "…")
		}
	}
	if strings.HasPrefix(name, "github_") {
		if num, ok := args["number"]; ok && num != nil {
			n := strings.TrimSpace(fmt.Sprint(num))
			if n != "" && n != "<nil>" {
				if f, ok := args["file"]; ok && f != nil && strings.TrimSpace(fmt.Sprint(f)) != "" {
					return strx.TruncateRunes(fmt.Sprintf("#%s · %s", n, strings.TrimSpace(fmt.Sprint(f))), 120, "…")
				}
				return "#" + n
			}
		}
	}
	for _, key := range []string{"path", "query", "pattern", "command", "message", "title", "number", "key", "jql", "ref", "note", "notes"} {
		if v, ok := args[key]; ok && v != nil {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" && s != "<nil>" {
				return strx.TruncateRunes(strings.ReplaceAll(s, "\n", " "), 120, "…")
			}
		}
	}
	return ""
}

func parseIntArg(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	default:
		return 0
	}
}
