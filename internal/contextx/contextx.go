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
// escalates to the aggressive cut at SummaryThresholdPct.
func NewCompactionCallback(b Budget) llmagent.BeforeModelCallback {
	return func(_ agent.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
		CompactRequest(req, b)
		return nil, nil
	}
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
		// Nowhere near the window: no compaction cost at all.
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
	if neverCompactInTurnTools[name] || !compactableInTurnTools[name] {
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
	preview := strx.TruncateBytes(strings.TrimSpace(raw), previewBytes, "…")
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
