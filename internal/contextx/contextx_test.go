package contextx

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func bigPayload(tag string) string {
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&b, "%s line %02d: payload content repeated to exceed threshold\n", tag, i)
	}
	return b.String()
}

// buildTurnRequest assembles a user message followed by N read_file call/response
// pairs with large payloads, mirroring a real multi-read turn.
func buildTurnRequest(n int) (*model.LLMRequest, []string) {
	contents := []*genai.Content{
		{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "Revise o PR #716 verificando rotas e DTOs."}}},
	}
	texts := make([]string, n)
	for i := 0; i < n; i++ {
		callID := fmt.Sprintf("call-read-%02d", i)
		contents = append(contents, &genai.Content{
			Role: genai.RoleModel,
			Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: callID, Name: "read_file", Args: map[string]any{"path": fmt.Sprintf("src/module_%02d.ts", i)}}}},
		})
		texts[i] = bigPayload(fmt.Sprintf("FILE-%02d", i))
		contents = append(contents, &genai.Content{
			Role: genai.RoleUser,
			Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: callID, Name: "read_file", Response: map[string]any{"text": texts[i]}}}},
		})
	}
	return &model.LLMRequest{Contents: contents}, texts
}

func TestCompactRequestBelowMicroThresholdIsNoOp(t *testing.T) {
	req, _ := buildTurnRequest(12)
	// Default 128k window: this small request sits far below 55%, so nothing
	// may be touched (token-aware gate, Spec 02 §3.3).
	compacted, saved := CompactRequest(req, Budget{ContextWindow: DefaultContextWindow, KeepRecent: 4})
	if compacted != 0 || saved != 0 {
		t.Fatalf("expected no compaction under 55%% of window, got compacted=%d saved=%d", compacted, saved)
	}
}

func TestCompactRequestMicroPassKeepsRecentAndNeverMutatesHistory(t *testing.T) {
	req, texts := buildTurnRequest(12)
	orig := append([]*genai.Content(nil), req.Contents...)

	compacted, saved := CompactRequest(req, Budget{ContextWindow: 12_000, KeepRecent: 4})
	if compacted != 8 {
		t.Fatalf("expected 8 older read_file responses compacted, got %d", compacted)
	}
	if saved <= 0 {
		t.Fatalf("expected positive bytesSaved, got %d", saved)
	}
	// Last 4 stay verbatim.
	for i := 8; i < 12; i++ {
		got := req.Contents[2+2*i].Parts[0].FunctionResponse.Response["text"].(string)
		if got != texts[i] {
			t.Fatalf("recent read #%d must stay verbatim", i)
		}
	}
	// Older ones carry the target + notice.
	for i := 0; i < 8; i++ {
		got := req.Contents[2+2*i].Parts[0].FunctionResponse.Response["text"].(string)
		if !strings.Contains(got, fmt.Sprintf("read_file(%q)", fmt.Sprintf("src/module_%02d.ts", i))) {
			t.Fatalf("compacted read #%d missing file target:\n%s", i, got)
		}
		if len(got) >= len(texts[i]) {
			t.Fatalf("read #%d not compacted", i)
		}
	}
	// Raw history preserved (zero in-place mutation): every original Content
	// snapshot still carries the full payload it was built with.
	for i := 0; i < 8; i++ {
		raw := orig[2+2*i].Parts[0].FunctionResponse.Response["text"].(string)
		if raw != texts[i] {
			t.Fatalf("original session content at read #%d was mutated in place", i)
		}
	}
}

func TestCompactRequestSummaryTierIsMoreAggressive(t *testing.T) {
	req, _ := buildTurnRequest(12)
	// Tiny window: estimate lands above 75%, triggering the structural cut.
	compacted, _ := CompactRequest(req, Budget{ContextWindow: 1_000, KeepRecent: 8})
	if compacted < 8 {
		t.Fatalf("summary tier must compact at least the 8 oldest, got %d", compacted)
	}
	// In the summary tier only the 2 most recent results stay verbatim.
	kept := 0
	for i := 0; i < 12; i++ {
		txt := req.Contents[2+2*i].Parts[0].FunctionResponse.Response["text"].(string)
		if !strings.Contains(txt, "compactada:") {
			kept++
		}
	}
	if kept > 2 {
		t.Fatalf("summary tier must keep at most 2 verbatim results, kept %d", kept)
	}
}

func TestCompactionCallbackReportsStatsAndStaysSilentOnNoOp(t *testing.T) {
	var reports []Stats
	cb := NewCompactionCallback(Budget{
		ContextWindow: 12_000,
		KeepRecent:    4,
		OnCompact:     func(s Stats) { reports = append(reports, s) },
	})
	req, _ := buildTurnRequest(12)
	if _, err := cb(nil, req); err != nil {
		t.Fatalf("callback error: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("expected exactly one Stats report for a trimming pass, got %d", len(reports))
	}
	s := reports[0]
	if s.Compacted != 8 {
		t.Fatalf("expected 8 compacted payloads in stats, got %d", s.Compacted)
	}
	if s.BytesSaved <= 0 {
		t.Fatalf("expected positive BytesSaved, got %d", s.BytesSaved)
	}
	if s.TokensSaved != (s.BytesSaved+3)/4 {
		t.Fatalf("TokensSaved must follow the bytes/4 heuristic, got %+v", s)
	}

	// A pass below the compaction threshold must not emit anything.
	reports = nil
	noop := NewCompactionCallback(Budget{
		ContextWindow: DefaultContextWindow,
		OnCompact:     func(s Stats) { reports = append(reports, s) },
	})
	req2, _ := buildTurnRequest(12)
	if _, err := noop(nil, req2); err != nil {
		t.Fatalf("noop callback error: %v", err)
	}
	if len(reports) != 0 {
		t.Fatalf("no-op pass must stay silent, got %d reports", len(reports))
	}
}

func TestEstimateRequestTokensCountsSystemContentsArgsAndTools(t *testing.T) {
	req := &model.LLMRequest{
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: strings.Repeat("a", 400)}}},
			Tools: []*genai.Tool{{
				FunctionDeclarations: []*genai.FunctionDeclaration{
					{Name: "read_file", Description: strings.Repeat("d", 191)},
				},
			}},
		},
		Contents: []*genai.Content{
			{Role: genai.RoleUser, Parts: []*genai.Part{{Text: strings.Repeat("b", 400)}}},
			{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "write_file", Args: map[string]any{"content": strings.Repeat("c", 400)}}}}},
		},
	}
	est := EstimateRequestTokens(req)
	if est < 350 || est > 360 {
		t.Fatalf("unexpected token estimate %d", est)
	}
	if EstimateRequestTokens(nil) != 0 {
		t.Fatal("nil request must estimate 0")
	}
}

func TestCompactRequestNeverCompactsErrorsOrLatestParallelBatch(t *testing.T) {
	// Real shape of the T010 bug: 8 parallel read_file responses (one of them
	// an error) grouped in the latest tool-result content. None may be touched.
	parts := make([]*genai.Part, 0, 9)
	bodies := make([]string, 8)
	for i := 0; i < 8; i++ {
		bodies[i] = strings.Repeat(fmt.Sprintf("regra RN-%02d importante\n", i), 50)
		parts = append(parts, &genai.Part{
			FunctionResponse: &genai.FunctionResponse{
				ID:       fmt.Sprintf("p%d", i),
				Name:     "read_file",
				Response: map[string]any{"text": bodies[i]},
			},
		})
	}
	errPayload := strings.Repeat("detailed error trace ", 60)
	parts = append(parts, &genai.Part{
		FunctionResponse: &genai.FunctionResponse{
			ID:       "perr",
			Name:     "read_file",
			Response: map[string]any{"error": errPayload},
		},
	})
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "Implemente a task"}}},
			{Role: genai.RoleUser, Parts: parts},
		},
	}

	compacted, saved := CompactRequest(req, Budget{ContextWindow: 1_000, KeepRecent: 4})
	if compacted != 0 || saved != 0 {
		t.Fatalf("latest parallel batch must NEVER be compacted, got compacted=%d saved=%d", compacted, saved)
	}
	for i := 0; i < 8; i++ {
		if got := req.Contents[1].Parts[i].FunctionResponse.Response["text"].(string); got != bodies[i] {
			t.Fatalf("parallel read #%d was corrupted/compacted", i)
		}
	}
	if got := req.Contents[1].Parts[8].FunctionResponse.Response["error"].(string); got != errPayload {
		t.Fatal("error response must never be compacted")
	}
}
