package orchestrator

import (
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

const (
	defaultInTurnKeepToolResults = 6
	inTurnCompactThresholdBytes  = 600
	inTurnPreviewBytes           = 240
)

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

var neverCompactInTurnTools = map[string]bool{
	"github_get_pr":      true,
	"github_ci_status":   true,
	"delegate_to_squad":  true,
	"qa_approve":         true,
	"qa_request_changes": true,
	"sdd_docs_ready":     true,
}

// newInTurnCompactionCallback returns an ADK BeforeModelCallback that trims older,
// large read/search/diff tool outputs inside the active turn without mutating the
// persisted session history. User prompts, agent reasoning/notes, recent tool
// results, errors, and critical governance outputs stay verbatim.
func newInTurnCompactionCallback(keepRecent int) llmagent.BeforeModelCallback {
	if keepRecent <= 0 {
		keepRecent = defaultInTurnKeepToolResults
	}
	return func(_ agent.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
		compactLLMRequestInTurn(req, keepRecent)
		return nil, nil
	}
}

type toolRespLoc struct {
	contentIdx int
	partIdx    int
}

// compactLLMRequestInTurn replaces older, large FunctionResponse payloads in
// req.Contents with compact previews + metadata notes, cloning only the modified
// Content/Part/FunctionResponse structs so the underlying session history is untouched.
func compactLLMRequestInTurn(req *model.LLMRequest, keepRecent int) (compactedCount int, bytesSaved int) {
	if req == nil || len(req.Contents) == 0 {
		return 0, 0
	}
	if keepRecent <= 0 {
		keepRecent = defaultInTurnKeepToolResults
	}

	callTargets := make(map[string]string)
	var responses []toolRespLoc
	var calls []toolRespLoc
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
			}
			if part.FunctionResponse != nil {
				responses = append(responses, toolRespLoc{contentIdx: ci, partIdx: pi})
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
				newResp, saved, ok := compactFunctionResponseMap(fr, callTargets[fr.ID])
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
				newArgs, saved, ok := compactFunctionCallArgs(fc)
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

func compactFunctionCallArgs(fc *genai.FunctionCall) (map[string]any, int, bool) {
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
		if !ok || len(raw) <= inTurnCompactThresholdBytes {
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

func compactFunctionResponseMap(fr *genai.FunctionResponse, callTarget string) (map[string]any, int, bool) {
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
		if len(rawText) <= inTurnCompactThresholdBytes {
			return nil, 0, false
		}
		compacted := buildCompactNotice(name, label, rawText)
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
		if len(serialized) <= inTurnCompactThresholdBytes {
			return nil, 0, false
		}
		compacted := buildCompactNotice(name, label, serialized)
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

func buildCompactNotice(toolName, label, raw string) string {
	lines := strings.Count(raw, "\n") + 1
	sizeLabel := formatCompactSize(len(raw))
	preview := strx.TruncateBytes(strings.TrimSpace(raw), inTurnPreviewBytes, "…")
	hint := "chame novamente se precisar reler"
	if toolName == "read_file" {
		hint = "chame novamente com start_line/end_line se precisar reler"
	} else if toolName == "github_pr_diff" {
		hint = "chame novamente com o parâmetro file se precisar reler um arquivo específico"
	}
	return fmt.Sprintf("%s\n[saída anterior de %s compactada: %d linhas / %s — já analisada neste turno; %s]", preview, label, lines, sizeLabel, hint)
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
