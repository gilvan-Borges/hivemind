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


func TestInTurnCompactionTrimsOldWriteAndEditArgs(t *testing.T) {
	bigBody := strings.Repeat("linha de código relevante\n", 120)
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{
				Role: genai.RoleModel,
				Parts: []*genai.Part{
					{FunctionCall: &genai.FunctionCall{ID: "c1", Name: "write_file", Args: map[string]any{"path": "a.go", "content": bigBody}}},
				},
			},
			{
				Role: genai.RoleUser,
				Parts: []*genai.Part{
					{FunctionResponse: &genai.FunctionResponse{ID: "c1", Name: "write_file", Response: map[string]any{"text": "arquivo gravado: a.go"}}},
				},
			},
			{
				Role: genai.RoleModel,
				Parts: []*genai.Part{
					{FunctionCall: &genai.FunctionCall{ID: "c2", Name: "edit_file", Args: map[string]any{"path": "b.go", "old_text": bigBody, "new_text": bigBody}}},
				},
			},
			{
				Role: genai.RoleUser,
				Parts: []*genai.Part{
					{FunctionResponse: &genai.FunctionResponse{ID: "c2", Name: "edit_file", Response: map[string]any{"text": "arquivo editado cirurgicamente: b.go"}}},
				},
			},
			{
				Role: genai.RoleModel,
				Parts: []*genai.Part{
					{FunctionCall: &genai.FunctionCall{ID: "c3", Name: "edit_file", Args: map[string]any{"path": "c.go", "old_text": bigBody, "new_text": bigBody}}},
				},
			},
			{
				Role: genai.RoleUser,
				Parts: []*genai.Part{
					{FunctionResponse: &genai.FunctionResponse{ID: "c3", Name: "edit_file", Response: map[string]any{"text": "arquivo editado cirurgicamente: c.go"}}},
				},
			},
		},
	}

	origWriteContent := req.Contents[0].Parts[0].FunctionCall.Args["content"].(string)
	compacted, saved := compactLLMRequestInTurn(req, 1)
	if compacted != 2 || saved <= 0 {
		t.Fatalf("expected 2 old call args compacted, got compacted=%d saved=%d", compacted, saved)
	}
	gotWrite := req.Contents[0].Parts[0].FunctionCall.Args["content"].(string)
	if !strings.Contains(gotWrite, "compactado") || len(gotWrite) >= len(origWriteContent) {
		t.Fatalf("old write_file content was not compacted: %q", gotWrite)
	}
	gotOldEdit := req.Contents[2].Parts[0].FunctionCall.Args["old_text"].(string)
	if !strings.Contains(gotOldEdit, "compactado") {
		t.Fatalf("old edit_file old_text was not compacted: %q", gotOldEdit)
	}
	gotRecentEdit := req.Contents[4].Parts[0].FunctionCall.Args["old_text"].(string)
	if gotRecentEdit != bigBody {
		t.Fatalf("most recent edit_file call should remain intact")
	}
}


type stubNamedTool struct{ name string }

func (s stubNamedTool) Name() string        { return s.name }
func (s stubNamedTool) Description() string { return s.name }
func (s stubNamedTool) IsLongRunning() bool { return false }

func TestInTurnCompactionNeverCompactsLatestParallelBatch(t *testing.T) {
	// Reproduz exatamente o bug da T010: o Dev chama 8 read_file em paralelo no mesmo turno
	// com keepRecent=4; TODAS as 8 respostas da última mensagem de ferramenta devem ficar
	// 100% íntegras para o modelo realmente ler o que acabou de pedir!
	parts := make([]*genai.Part, 0, 8)
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
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "Implemente a task"}}},
			{Role: genai.RoleUser, Parts: parts},
		},
	}

	compacted, saved := compactLLMRequestInTurn(req, 4)
	if compacted != 0 || saved != 0 {
		t.Fatalf("latest parallel batch must NEVER be compacted, got compacted=%d saved=%d", compacted, saved)
	}
	for i := 0; i < 8; i++ {
		got := req.Contents[1].Parts[i].FunctionResponse.Response["text"].(string)
		if got != bodies[i] {
			t.Fatalf("parallel read #%d was corrupted/compacted", i)
		}
	}
}

func TestRepeatReadGuardBlocksThirdIdenticalReadAndResetsOnEdit(t *testing.T) {
	cb := newRepeatReadGuardCallback()
	readTool := stubNamedTool{name: "read_file"}
	editTool := stubNamedTool{name: "edit_file"}
	args := map[string]any{"path": "specs/017/spec.md", "start_line": 1, "end_line": 100}

	// 1st and 2nd reads pass through (nil override).
	if res, err := cb(nil, readTool, args); err != nil || res != nil {
		t.Fatalf("1st read should pass, got res=%v err=%v", res, err)
	}
	if res, err := cb(nil, readTool, args); err != nil || res != nil {
		t.Fatalf("2nd read should pass, got res=%v err=%v", res, err)
	}
	// 3rd identical read triggers anti-loop short-circuit!
	res, err := cb(nil, readTool, args)
	if err != nil || res == nil || !strings.Contains(fmt.Sprint(res["text"]), "AVISO ANTI-LOOP") {
		t.Fatalf("3rd identical read should trigger anti-loop notice, got res=%v err=%v", res, err)
	}
	// Editing that file resets the counter so subsequent read works again.
	if _, err := cb(nil, editTool, map[string]any{"path": "specs/017/spec.md"}); err != nil {
		t.Fatalf("edit_file reset failed: %v", err)
	}
	if res2, err := cb(nil, readTool, args); err != nil || res2 != nil {
		t.Fatalf("read after edit_file should pass again, got res=%v err=%v", res2, err)
	}
}
