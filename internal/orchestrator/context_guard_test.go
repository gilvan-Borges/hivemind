package orchestrator

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestInTurnCompactionTrimsOldReadsKeepsRecent(t *testing.T) {
	bigPayload := func(tag string) string {
		var b strings.Builder
		for i := 1; i <= 40; i++ {
			fmt.Fprintf(&b, "%s line %02d: payload content repeated to exceed threshold\n", tag, i)
		}
		return b.String()
	}

	userMsg := &genai.Content{
		Role:  genai.RoleUser,
		Parts: []*genai.Part{{Text: "Revise o PR #716 verificando rotas e DTOs."}},
	}
	prGetCall := &genai.Content{
		Role: "model",
		Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{ID: "call-pr", Name: "github_get_pr", Args: map[string]any{"number": 716}}},
		},
	}
	prGetRespOrig := bigPayload("PR-716-DETAIL")
	prGetResp := &genai.Content{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{ID: "call-pr", Name: "github_get_pr", Response: map[string]any{"text": prGetRespOrig}}},
		},
	}
	errCall := &genai.Content{
		Role: "model",
		Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{ID: "call-err", Name: "read_file", Args: map[string]any{"path": "missing.ts"}}},
		},
	}
	errPayload := strings.Repeat("detailed error trace ", 60)
	errResp := &genai.Content{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{ID: "call-err", Name: "read_file", Response: map[string]any{"error": errPayload}}},
		},
	}

	origContents := []*genai.Content{userMsg, prGetCall, prGetResp, errCall, errResp}
	readOrigTexts := make([]string, 12)
	readRespIndices := make([]int, 12)

	for i := 0; i < 12; i++ {
		callID := fmt.Sprintf("call-read-%02d", i)
		filePath := fmt.Sprintf("src/module_%02d.ts", i)
		noteText := fmt.Sprintf("Conclusão parcial %02d confirmada.", i)
		origContents = append(origContents, &genai.Content{
			Role: "model",
			Parts: []*genai.Part{
				{Text: noteText},
				{FunctionCall: &genai.FunctionCall{ID: callID, Name: "read_file", Args: map[string]any{"path": filePath}}},
			},
		})
		txt := bigPayload(fmt.Sprintf("FILE-%02d", i))
		readOrigTexts[i] = txt
		readRespIndices[i] = len(origContents)
		origContents = append(origContents, &genai.Content{
			Role: genai.RoleUser,
			Parts: []*genai.Part{
				{FunctionResponse: &genai.FunctionResponse{ID: callID, Name: "read_file", Response: map[string]any{"text": txt}}},
			},
		})
	}

	// Keep original pointer snapshot to verify zero in-place mutation.
	origSnapshot := append([]*genai.Content(nil), origContents...)
	req := &model.LLMRequest{Contents: origContents}

	compacted, saved := compactLLMRequestInTurn(req, 4)
	if compacted != 8 {
		t.Fatalf("expected 8 older read_file responses compacted, got %d", compacted)
	}
	if saved <= 0 {
		t.Fatalf("expected positive bytesSaved, got %d", saved)
	}

	// 1. User prompt intact.
	if req.Contents[0].Parts[0].Text != "Revise o PR #716 verificando rotas e DTOs." {
		t.Fatal("user message was altered")
	}
	// 2. github_get_pr never compacted.
	if got := req.Contents[2].Parts[0].FunctionResponse.Response["text"].(string); got != prGetRespOrig {
		t.Fatal("github_get_pr response must never be compacted")
	}
	// 3. Error response never compacted.
	if got := req.Contents[4].Parts[0].FunctionResponse.Response["error"].(string); got != errPayload {
		t.Fatal("error response must never be compacted")
	}
	// 4. First 8 read_file outputs compacted with target path preserved; agent notes intact.
	for i := 0; i < 8; i++ {
		idx := readRespIndices[i]
		got := req.Contents[idx].Parts[0].FunctionResponse.Response["text"].(string)
		if !strings.Contains(got, fmt.Sprintf("read_file(%q)", fmt.Sprintf("src/module_%02d.ts", i))) {
			t.Fatalf("compacted read #%d missing file target, got:\n%s", i, got)
		}
		if !strings.Contains(got, "compactada:") || len(got) >= len(readOrigTexts[i]) {
			t.Fatalf("read #%d was not properly compacted (len=%d orig=%d)", i, len(got), len(readOrigTexts[i]))
		}
		// Original session content must remain unmutated.
		origGot := origSnapshot[idx].Parts[0].FunctionResponse.Response["text"].(string)
		if origGot != readOrigTexts[i] {
			t.Fatalf("original session content at idx %d was mutated in place", idx)
		}
		// Agent text note in the preceding model message stays intact.
		noteGot := req.Contents[idx-1].Parts[0].Text
		if noteGot != fmt.Sprintf("Conclusão parcial %02d confirmada.", i) {
			t.Fatalf("agent note before read #%d was altered: %q", i, noteGot)
		}
	}
	// 5. Last 4 read_file outputs remain verbatim.
	for i := 8; i < 12; i++ {
		idx := readRespIndices[i]
		got := req.Contents[idx].Parts[0].FunctionResponse.Response["text"].(string)
		if got != readOrigTexts[i] {
			t.Fatalf("recent read #%d should stay verbatim, got len=%d want=%d", i, len(got), len(readOrigTexts[i]))
		}
	}
}

func TestReviewModelOverrideUsedOnlyOnPRReview(t *testing.T) {
	baseTeam := team.Team{
		Name: "principal",
		Agents: []team.Agent{
			{Name: "chefe", Role: team.RoleArchitect, Provider: "agy", Model: "agy:claude-opus-4-6"},
			{Name: "dev", Role: team.RoleDeveloper, Provider: "agy", Model: "agy:claude-sonnet-4-6"},
			{Name: "qa", Role: team.RoleQA, Provider: "agy", Model: "agy:claude-sonnet-4-6"},
		},
	}
	settings := team.DefaultSettings()
	settings.ReviewModel = "opencode-go:glm-5.3"

	// Normal objective (not a PR review): keeps default chief model.
	rNormal := &Runner{
		Project:         team.File{Settings: settings},
		Team:            baseTeam,
		reviewRequested: false,
	}
	gotNormal := rNormal.resolveTriageChief()
	if gotNormal.Model != "agy:claude-opus-4-6" || gotNormal.Provider != "agy" {
		t.Fatalf("non-review turn must keep default chief model, got %+v", gotNormal)
	}

	// PR review objective: switches chief to settings.review_model + inferred provider.
	rReview := &Runner{
		Project:         team.File{Settings: settings},
		Team:            baseTeam,
		reviewRequested: true,
	}
	gotReview := rReview.resolveTriageChief()
	if gotReview.Model != "opencode-go:glm-5.3" || gotReview.Provider != "opencode" {
		t.Fatalf("PR review turn must apply review_model override, got %+v", gotReview)
	}

	// Auto-review mode prefers auto_review.model over review_model when configured.
	settings.AutoReview.Model = "opencode-go:glm-5.3-flash"
	rAuto := &Runner{
		Project:         team.File{Settings: settings},
		Team:            baseTeam,
		reviewRequested: true,
		autoReviewMode:  true,
	}
	gotAuto := rAuto.resolveTriageChief()
	if gotAuto.Model != "opencode-go:glm-5.3-flash" || gotAuto.Provider != "opencode" {
		t.Fatalf("auto-review turn must prefer auto_review.model, got %+v", gotAuto)
	}
}
