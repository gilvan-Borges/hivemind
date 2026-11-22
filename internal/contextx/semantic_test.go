package contextx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
)

func semanticReq(contents int, inflateTextBytes int) *model.LLMRequest {
	req := &model.LLMRequest{Contents: []*genai.Content{}}
	big := strings.Repeat("x", inflateTextBytes)
	for i := 0; i < contents; i++ {
		role := genai.RoleUser
		if i%2 == 1 {
			role = genai.RoleModel
		}
		req.Contents = append(req.Contents, &genai.Content{
			Role:  role,
			Parts: []*genai.Part{{Text: fmt.Sprintf("c%d %s", i, big)}},
		})
	}
	return req
}

// pairReq builds a request with a call/response pair in the old head and a
// verbatim tail, mimicking a mid-run projection.
func pairReq() *model.LLMRequest {
	req := &model.LLMRequest{Contents: []*genai.Content{}}
	add := func(role string, parts ...*genai.Part) {
		req.Contents = append(req.Contents, &genai.Content{Role: role, Parts: parts})
	}
	big := strings.Repeat("y", 4096)
	add(genai.RoleUser, &genai.Part{Text: "objetivo inicial " + big})
	add(genai.RoleModel, &genai.Part{FunctionCall: &genai.FunctionCall{ID: "call-1", Name: "read_file", Args: map[string]any{"path": "a.go"}}})
	add(genai.RoleUser, &genai.Part{FunctionResponse: &genai.FunctionResponse{ID: "call-1", Name: "read_file", Response: map[string]any{"text": big}}})
	for i := 0; i < semanticTailContents; i++ {
		add(genai.RoleUser, &genai.Part{Text: fmt.Sprintf("tail %d %s", i, big)})
	}
	return req
}

func TestSummarizeOldTurnsBelowThresholdSkipped(t *testing.T) {
	req := semanticReq(12, 1024)
	b := Budget{
		ContextWindow: 1_000_000,
		Summarizer: func(context.Context, string) (string, error) {
			t.Fatalf("summarizer must not run below the threshold")
			return "", nil
		},
	}
	if _, applied := SummarizeOldTurns(context.Background(), req, b); applied {
		t.Fatalf("semantic pass applied below threshold")
	}
}

func TestSummarizeOldTurnsReplacesHeadWithCarrier(t *testing.T) {
	req := pairReq()
	tailBefore := len(req.Contents) - semanticTailContents
	b := Budget{
		ContextWindow: 8 * 1024, // tiny window → ratio far above 75%
		Summarizer: func(_ context.Context, transcript string) (string, error) {
			if !strings.Contains(transcript, "read_file") {
				t.Fatalf("transcript missing tool exchange: %q", transcript[:120])
			}
			return "RESUMO SINTÉTICO: objetivo concluído em a.go", nil
		},
	}
	st, applied := SummarizeOldTurns(context.Background(), req, b)
	if !applied {
		t.Fatalf("semantic pass not applied above threshold")
	}
	if !st.Semantic || st.BytesSaved <= 0 {
		t.Fatalf("expected semantic stats, got %+v", st)
	}
	if len(req.Contents) != 1+tailBefore+0 && len(req.Contents) > 1+semanticTailContents+1 {
		t.Fatalf("unexpected content count: %d", len(req.Contents))
	}
	first := req.Contents[0]
	if first.Role != genai.RoleUser || first.Parts[0].Text == "" {
		t.Fatalf("missing summary carrier")
	}
	if !strings.Contains(first.Parts[0].Text, SummarizedConversationTag) {
		t.Fatalf("carrier missing tag")
	}
	if !strings.Contains(first.Parts[0].Text, "RESUMO SINTÉTICO") {
		t.Fatalf("carrier missing summary body")
	}
	// No orphaned calls: every FunctionCall left must have its response.
	calls := map[string]bool{}
	for _, c := range req.Contents {
		for _, p := range c.Parts {
			if p.FunctionCall != nil {
				calls[p.FunctionCall.ID] = true
			}
		}
	}
	for _, c := range req.Contents {
		for _, p := range c.Parts {
			if p.FunctionResponse != nil {
				delete(calls, p.FunctionResponse.ID)
			}
		}
	}
	// The head pair was removed whole; the tail has no calls.
	for id := range calls {
		if id == "call-1" {
			t.Fatalf("call-1 kept while its response was summarized away")
		}
	}
}

func TestSummarizeOldTurnsSelfLimiting(t *testing.T) {
	req := pairReq()
	calls := 0
	b := Budget{
		ContextWindow: 8 * 1024,
		Summarizer: func(context.Context, string) (string, error) {
			calls++
			return "RESUMO", nil
		},
	}
	if _, applied := SummarizeOldTurns(context.Background(), req, b); !applied {
		t.Fatalf("first pass must apply")
	}
	if _, applied := SummarizeOldTurns(context.Background(), req, b); applied {
		t.Fatalf("second pass must not summarize the summary")
	}
	if calls != 1 {
		t.Fatalf("summarizer ran %d times", calls)
	}
}

func TestSummarizeOldTurnsFailOpenOnError(t *testing.T) {
	req := pairReq()
	before := len(req.Contents)
	b := Budget{
		ContextWindow: 16 * 1024,
		Summarizer: func(context.Context, string) (string, error) {
			return "", errors.New("modelo indisponível")
		},
	}
	if _, applied := SummarizeOldTurns(context.Background(), req, b); applied {
		t.Fatalf("failed summarizer must not apply")
	}
	if len(req.Contents) != before {
		t.Fatalf("request mutated despite summarizer failure")
	}
}

func TestSummarizeOldTurnsSkippedWithoutSummarizer(t *testing.T) {
	req := pairReq()
	if _, applied := SummarizeOldTurns(context.Background(), req, Budget{ContextWindow: 16 * 1024}); applied {
		t.Fatalf("no summarizer configured, pass must not apply")
	}
}

func TestCompactRequestCompactsMCPToolOutputs(t *testing.T) {
	big := strings.Repeat("z", 4096)
	req := &model.LLMRequest{Contents: []*genai.Content{}}
	req.Contents = append(req.Contents,
		&genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: "m1", Name: "mcp_call", Args: map[string]any{"server": "playwright", "tool": "browser_snapshot"}}}}},
		&genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: "m1", Name: "mcp_call", Response: map[string]any{"text": big}}}}},
		&genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: "m2", Name: "mcp_enable_server", Args: map[string]any{"name": "playwright"}}}}},
		&genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: "m2", Name: "mcp_enable_server", Response: map[string]any{"text": big}}}}},
	)
	req.Contents = append(req.Contents, &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "segue o trabalho"}}})

	b := Budget{ContextWindow: 2048, KeepRecent: 1}
	compacted, _ := CompactRequest(req, b)
	if compacted == 0 {
		t.Fatalf("mcp_call output was not compacted")
	}
	// mcp_enable_server is pinned: its governance record must stay verbatim.
	for _, c := range req.Contents {
		for _, p := range c.Parts {
			if p.FunctionResponse != nil && p.FunctionResponse.Name == "mcp_enable_server" {
				if txt, _ := p.FunctionResponse.Response["text"].(string); txt != big {
					t.Fatalf("mcp_enable_server output was compacted despite the pin")
				}
			}
		}
	}
}

func TestCompactableToolNameMCPNames(t *testing.T) {
	for _, name := range []string{"mcp_call", "mcp_playwright_browser_snapshot", "mcp_github_create_issue"} {
		if !compactableToolName(name) {
			t.Fatalf("%s must be compactable", name)
		}
		if neverCompactInTurnTools[name] {
			t.Fatalf("%s must not be pinned", name)
		}
	}
	if !neverCompactInTurnTools["mcp_enable_server"] {
		t.Fatalf("mcp_enable_server must stay verbatim")
	}
}
