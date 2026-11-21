package orchestrator

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/contextx"
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

	compacted, saved := contextx.CompactRequest(req, contextx.Budget{ContextWindow: 15_000, KeepRecent: 4})
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
	compacted, saved := contextx.CompactRequest(req, contextx.Budget{ContextWindow: 6_000, KeepRecent: 1})
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

	// Window small enough for the 55% gate to pass, so the assertion actually
	// exercises the "never compact the latest parallel batch" guard.
	compacted, saved := contextx.CompactRequest(req, contextx.Budget{ContextWindow: 3_500, KeepRecent: 4})
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

func TestRepeatReadGuardBlocksShiftedRangesAndRepeatedPRDiff(t *testing.T) {
	cb := newRepeatReadGuardCallback()
	readTool := stubNamedTool{name: "read_file"}
	diffTool := stubNamedTool{name: "github_pr_diff"}

	// Shifting line ranges on the same file (1-40, 2-33, 5-33, 10-40) must trigger anti-loop on 5th read.
	for i := 1; i <= 4; i++ {
		res, err := cb(nil, readTool, map[string]any{
			"path":       "specs/018-provedor-asaas/plan.md",
			"start_line": 250 + i,
			"end_line":   290 + i,
		})
		if err != nil || res != nil {
			t.Fatalf("shifted read #%d should pass, got res=%v err=%v", i, res, err)
		}
	}
	res5, err := cb(nil, readTool, map[string]any{
		"path":       "specs/018-provedor-asaas/plan.md",
		"start_line": 266,
		"end_line":   276,
	})
	if err != nil || res5 == nil || !strings.Contains(fmt.Sprint(res5["text"]), "AVISO ANTI-LOOP") {
		t.Fatalf("5th shifted read on same file must trigger anti-loop, got res=%v err=%v", res5, err)
	}

	// Calling github_pr_diff twice for the same PR and file must trigger anti-loop on the 2nd call.
	diffArgs := map[string]any{"number": 730, "file": "specs/018-provedor-asaas/spec.md"}
	if res, err := cb(nil, diffTool, diffArgs); err != nil || res != nil {
		t.Fatalf("1st github_pr_diff should pass, got res=%v err=%v", res, err)
	}
	resDiff2, err := cb(nil, diffTool, diffArgs)
	if err != nil || resDiff2 == nil || !strings.Contains(fmt.Sprint(resDiff2["text"]), "AVISO ANTI-LOOP") {
		t.Fatalf("2nd identical github_pr_diff should trigger anti-loop, got res=%v err=%v", resDiff2, err)
	}
}

func TestPRReviewGuardCallbacksAndExtractPRNumber(t *testing.T) {
	if got := extractReviewPRNumber("Revisar PR #730 de @joaomarqu3ss — docs(spec-018)"); got != 730 {
		t.Fatalf("extractReviewPRNumber = %d, want 730", got)
	}
	if !isGeneratedOrLockfile("frontend/package-lock.json") || isGeneratedOrLockfile("specs/018/spec.md") {
		t.Fatal("unexpected isGeneratedOrLockfile classification")
	}

	before, after := newPRReviewGuardCallbacks(730, true, 3)
	getPRTool := stubNamedTool{name: "github_get_pr"}
	diffTool := stubNamedTool{name: "github_pr_diff"}
	readTool := stubNamedTool{name: "read_file"}
	reviewTool := stubNamedTool{name: "github_review_pr"}

	// Preloaded PR metadata and full diff are short-circuited immediately.
	if res, _ := before(nil, getPRTool, map[string]any{"number": 730}); res == nil || !strings.Contains(fmt.Sprint(res["text"]), "CACHE DO DOSSIÊ") {
		t.Fatalf("expected github_get_pr cache hit, got %v", res)
	}
	if res, _ := before(nil, diffTool, map[string]any{"number": 730}); res == nil || !strings.Contains(fmt.Sprint(res["text"]), "CACHE DO DOSSIÊ") {
		t.Fatalf("expected github_pr_diff cache hit when allPatchesComplete=true, got %v", res)
	}

	// Up to maxExploreCalls=3 repository reads pass; 4th is blocked.
	for i := 1; i <= 3; i++ {
		if res, _ := before(nil, readTool, map[string]any{"path": fmt.Sprintf("a%d.java", i)}); res != nil {
			t.Fatalf("explore read #%d should pass, got %v", i, res)
		}
	}
	if res, _ := before(nil, readTool, map[string]any{"path": "a4.java"}); res == nil || !strings.Contains(fmt.Sprint(res["text"]), "ORÇAMENTO DE VERIFICAÇÃO") {
		t.Fatalf("4th explore read should be blocked by review budget, got %v", res)
	}

	// Once github_review_pr succeeds, any further tool call is blocked.
	_, _ = after(nil, reviewTool, nil, map[string]any{"text": "ok"}, nil)
	if res, _ := before(nil, readTool, map[string]any{"path": "a1.java"}); res == nil || !strings.Contains(fmt.Sprint(res["text"]), "já foi enviada") {
		t.Fatalf("expected post-review tool block, got %v", res)
	}
}

func TestChiefTriageBudgetCallbacksAndJiraKeyExtract(t *testing.T) {
	if got := extractJiraKeyFromObjective("Resolver issue IPG-874 — Front F3 (RN-018-15, D-179, FT-232)"); got != "IPG-874" {
		t.Fatalf("extractJiraKeyFromObjective = %q, want IPG-874", got)
	}

	before, after := newChiefTriageBudgetCallbacks("IPG-874", 3)
	jiraGetTool := stubNamedTool{name: "jira_get"}
	readTool := stubNamedTool{name: "read_file"}
	delegateTool := stubNamedTool{name: "delegate_to_squad"}

	// Preloaded Jira key is short-circuited immediately.
	if res, _ := before(nil, jiraGetTool, map[string]any{"key": "ipg-874"}); res == nil || !strings.Contains(fmt.Sprint(res["text"]), "CACHE DO DOSSIÊ JIRA") {
		t.Fatalf("expected jira_get cache hit, got %v", res)
	}

	// Up to maxExploreCalls=3 reads pass; 4th forces immediate delegate_to_squad.
	for i := 1; i <= 3; i++ {
		if res, _ := before(nil, readTool, map[string]any{"path": fmt.Sprintf("f%d.ts", i)}); res != nil {
			t.Fatalf("triage read #%d should pass, got %v", i, res)
		}
	}
	if res, _ := before(nil, readTool, map[string]any{"path": "f4.ts"}); res == nil || !strings.Contains(fmt.Sprint(res["text"]), "ORÇAMENTO DE TRIAGEM DO CHEFE ATINGIDO") {
		t.Fatalf("4th triage read must force delegate_to_squad, got %v", res)
	}

	// Once delegate_to_squad succeeds, any further read is blocked.
	_, _ = after(nil, delegateTool, nil, map[string]any{"status": "ok"}, nil)
	if res, _ := before(nil, readTool, map[string]any{"path": "f1.ts"}); res == nil || !strings.Contains(fmt.Sprint(res["text"]), "delegate_to_squad já foi registrado") {
		t.Fatalf("expected post-delegate tool block, got %v", res)
	}
}

// fakeReminderState is a minimal session.ReadonlyState for callback tests.
type fakeReminderState struct {
	m map[string]any
}

func (s fakeReminderState) Get(k string) (any, error) {
	if v, ok := s.m[k]; ok {
		return v, nil
	}
	return nil, session.ErrStateKeyNotExist
}

func (s fakeReminderState) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		for k, v := range s.m {
			if !yield(k, v) {
				return
			}
		}
	}
}

// reminderCtxFake answers only ReadonlyState, the single method the dynamic
// reminder callback touches.
type reminderCtxFake struct {
	agent.StrictContextMock
	state fakeReminderState
}

func (f *reminderCtxFake) ReadonlyState() session.ReadonlyState { return f.state }

// TestSystemInstructionAndToolsStayInvariantAcrossTurns is the Spec 02
// acceptance criterion: over 10 tool turns — with sibling state deltas and
// TeamBoard notes landing mid-run — hash(SystemInstruction + Tools) must stay
// byte-identical from turn 1 to turn 10, and the dynamic deltas must ride a
// trailing <system-reminder> block.
func TestSystemInstructionAndToolsStayInvariantAcrossTurns(t *testing.T) {
	r := &Runner{sharedBoard: newTeamBoard()}
	r.sharedBoard.RegisterOwner("dev_1", []string{"src/a.go"})
	devAgent := team.Agent{Role: team.RoleDeveloper, Name: "dev_1", Provider: "agy", Model: "agy"}
	keys := []string{"objective", "chief_plan", "dev_dev_2_report"}

	state := fakeReminderState{m: map[string]any{
		"objective":   "Implementar a tarefa 42",
		"chief_plan":  "plano estável do chefe",
	}}
	ctxFake := &reminderCtxFake{state: state}

	sysInstruction := &genai.Content{
		Role:  genai.RoleUser,
		Parts: []*genai.Part{{Text: "Instrução estável do Desenvolvedor HiveMind."}},
	}
	toolsDecl := []*genai.Tool{{
		FunctionDeclarations: []*genai.FunctionDeclaration{
			{Name: "read_file", Description: "Lê arquivo"},
			{Name: "delegate_to_squad", Description: "Delega ao squad"},
		},
	}}

	hashPrefix := func(req *model.LLMRequest) string {
		h := sha256.New()
		for _, p := range req.Config.SystemInstruction.Parts {
			h.Write([]byte(p.Text))
		}
		for _, tl := range req.Config.Tools {
			for _, d := range tl.FunctionDeclarations {
				h.Write([]byte(d.Name))
			}
		}
		return hex.EncodeToString(h.Sum(nil))
	}

	compaction := contextx.NewCompactionCallback(contextx.Budget{ContextWindow: 128_000, KeepRecent: 8})
	reminder := r.newDynamicContextReminderCallback("dev_1", devAgent, false, "dev_dev_1_report", keys)

	var firstHash string
	for turn := 1; turn <= 10; turn++ {
		// Sibling report appears mid-run (turn 5) and a board note lands at
		// turn 7: exactly the events that used to rewrite the SystemInstruction.
		if turn == 5 {
			state.m["dev_dev_2_report"] = "Dev 2 terminou a fatia de autenticação"
		}
		if turn == 7 {
			r.sharedBoard.PublishNote("dev_1", "contrato API", "use POST /v1/items")
		}

		contents := []*genai.Content{
			{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "Implemente X"}}},
		}
		for i := 1; i <= 3; i++ {
			contents = append(contents,
				&genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{
					ID: fmt.Sprintf("t%d-c%d", turn, i), Name: "read_file", Args: map[string]any{"path": fmt.Sprintf("f%d.go", i)},
				}}}},
				&genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
					ID: fmt.Sprintf("t%d-c%d", turn, i), Name: "read_file", Response: map[string]any{"text": "conteúdo do arquivo " + fmt.Sprint(turn)},
				}}}},
			)
		}
		req := &model.LLMRequest{
			Config: &genai.GenerateContentConfig{
				SystemInstruction: sysInstruction,
				Tools:             toolsDecl,
			},
			Contents: contents,
		}

		if _, err := compaction(ctxFake, req); err != nil {
			t.Fatalf("turn %d: compaction error %v", turn, err)
		}
		if _, err := reminder(ctxFake, req); err != nil {
			t.Fatalf("turn %d: reminder error %v", turn, err)
		}

		hash := hashPrefix(req)
		if turn == 1 {
			firstHash = hash
		} else if hash != firstHash {
			t.Fatalf("turn %d: KV-cache prefix hash changed (turn1=%s turn%d=%s)", turn, firstHash, turn, hash)
		}
		if req.Config.SystemInstruction != sysInstruction {
			t.Fatalf("turn %d: SystemInstruction struct was replaced", turn)
		}
		if len(req.Config.Tools) != 1 || req.Config.Tools[0] != toolsDecl[0] {
			t.Fatalf("turn %d: Tools struct was replaced", turn)
		}
		lastParts := req.Contents[len(req.Contents)-1].Parts
		lastText := lastParts[len(lastParts)-1].Text
		if !strings.Contains(lastText, "<system-reminder>") {
			t.Fatalf("turn %d: expected trailing <system-reminder>, got %q", turn, lastText)
		}
		if turn >= 5 && !strings.Contains(lastText, "dev_dev_2_report") {
			t.Fatalf("turn %d: sibling delta missing from reminder", turn)
		}
		if turn >= 7 && !strings.Contains(lastText, "use POST /v1/items") {
			t.Fatalf("turn %d: board note missing from reminder", turn)
		}
	}
}
