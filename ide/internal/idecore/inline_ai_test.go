package idecore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestExpandAtFileMentionsLineRanges(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 1; i <= 40; i++ {
		lines = append(lines, fmt.Sprintf("line_content_%02d", i))
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Explicit range @main.go:L10-L14 injects only lines 10..14.
	out := expandAtFileMentions(dir, "Analise @main.go:L10-L14 por favor")
	if !strings.Contains(out, "[Trecho anexado @main.go:L10-L14 (de 40 linhas)]") {
		t.Fatalf("expected range header, got:\n%s", out)
	}
	if !strings.Contains(out, "L10: line_content_10") || !strings.Contains(out, "L14: line_content_14") {
		t.Fatalf("missing expected lines 10..14:\n%s", out)
	}
	if strings.Contains(out, "L9:") || strings.Contains(out, "L15:") {
		t.Fatalf("unexpected lines outside range:\n%s", out)
	}

	// 2. Single line @main.go:L25 expands +/-10 context window (L15..L35).
	outSingle := expandAtFileMentions(dir, "Veja @main.go:L25")
	if !strings.Contains(outSingle, "L15: line_content_15") || !strings.Contains(outSingle, "L25: line_content_25") || !strings.Contains(outSingle, "L35: line_content_35") {
		t.Fatalf("expected +/-10 context around L25, got:\n%s", outSingle)
	}

	// 3. Ctrl+L already pasted a fenced snippet right after @main.go:L10-L14 -> do not duplicate.
	outCtrlL := expandAtFileMentions(dir, "@main.go:L10-L14\n```go\nline_content_10\n```\nExplique")
	if outCtrlL != "" {
		t.Fatalf("expected Ctrl+L fenced mention not to be duplicated, got:\n%s", outCtrlL)
	}
}

func TestCompleteInlineCodeAndClean(t *testing.T) {
	origFactory := inlineModelFactory
	t.Cleanup(func() { inlineModelFactory = origFactory })

	inlineModelFactory = func(_ context.Context, _, _ string) (model.LLM, error) {
		return &modelx.Scripted{
			Fn: func(_ *model.LLMRequest, _ int) *model.LLMResponse {
				return &model.LLMResponse{
					Content: &genai.Content{
						Role:  "model",
						Parts: []*genai.Part{{Text: "```go\nreturn a + b\n```"}},
					},
					UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
						PromptTokenCount:     42,
						CandidatesTokenCount: 8,
					},
				}
			},
		}, nil
	}

	res, err := CompleteInlineCode(context.Background(), InlineCompleteRequest{
		Workspace: t.TempDir(),
		FilePath:  "math.go",
		Prefix:    "func add(a, b int) int {\n\t",
		Suffix:    "\n}",
		Model:     "opencode-go:glm-5.3-flash",
	})
	if err != nil {
		t.Fatalf("CompleteInlineCode failed: %v", err)
	}
	if res.Completion != "return a + b" {
		t.Fatalf("expected cleaned completion 'return a + b', got %q", res.Completion)
	}
	if res.InputTokens != 42 || res.OutputTokens != 8 {
		t.Fatalf("unexpected token counts: %+v", res)
	}

	// Prefix/suffix echo stripping check.
	got := cleanInlineCompletion("const x = 42;", "const x = ", ";")
	if got != "42" {
		t.Fatalf("expected overlap trimmed to '42', got %q", got)
	}
}

func TestGenerateInlineEditAndComputeLineDiff(t *testing.T) {
	origFactory := inlineModelFactory
	t.Cleanup(func() { inlineModelFactory = origFactory })

	inlineModelFactory = func(_ context.Context, _, _ string) (model.LLM, error) {
		return &modelx.Scripted{
			Fn: func(_ *model.LLMRequest, _ int) *model.LLMResponse {
				return &model.LLMResponse{
					Content: &genai.Content{
						Role:  "model",
						Parts: []*genai.Part{{Text: "```go\nfunc sum(a, b int) int {\n\tif a < 0 || b < 0 {\n\t\treturn 0\n\t}\n\treturn a + b\n}\n```"}},
					},
				}
			},
		}, nil
	}

	selected := "func sum(a, b int) int {\n\treturn a + b\n}"
	res, err := GenerateInlineEdit(context.Background(), InlineEditRequest{
		Workspace:    t.TempDir(),
		FilePath:     "math.go",
		Instruction:  "retorne 0 se algum parâmetro for negativo",
		SelectedText: selected,
		StartLine:    10,
		EndLine:      12,
		Model:        "opencode-go:glm-5.3",
	})
	if err != nil {
		t.Fatalf("GenerateInlineEdit failed: %v", err)
	}
	if !strings.Contains(res.Replacement, "if a < 0 || b < 0") {
		t.Fatalf("unexpected replacement:\n%s", res.Replacement)
	}
	if res.Added != 3 || res.Removed != 0 {
		t.Fatalf("expected +3 -0 diff, got +%d -%d (lines=%+v)", res.Added, res.Removed, res.DiffLines)
	}
}

func TestResolveInlineModelPrefersHTTPForGhostText(t *testing.T) {
	dir := t.TempDir()
	_, err := BuildTeamFromWizard(dir, WizardInput{
		TeamName:     "principal",
		DevCount:     1,
		DefaultModel: "agy:claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Explicitly requesting agy for Ghost Text (requireHTTP=true) must be rejected cleanly.
	prov, mod, reason := ResolveInlineModel(dir, "agy:claude-sonnet-4-6", true)
	if prov != "" || mod != "" || reason == "" {
		t.Fatalf("expected agy to be rejected for Ghost Text, got prov=%q mod=%q reason=%q", prov, mod, reason)
	}

	// For Ctrl+K Inline Edit (requireHTTP=false), agy fallback works out-of-the-box.
	prov, mod, reason = ResolveInlineModel(dir, "agy:claude-sonnet-4-6", false)
	if prov != "agy" || mod != "agy:claude-sonnet-4-6" || reason != "" {
		t.Fatalf("expected agy allowed for Ctrl+K edit, got prov=%q mod=%q reason=%q", prov, mod, reason)
	}

	// Explicit inline_model in team.yaml wins for both.
	cfg, err := team.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Settings.InlineModel = "opencode-go:glm-5.3-flash"
	if err := team.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	prov, mod, _ = ResolveInlineModel(dir, "", true)
	if prov != "opencode" || mod != "opencode-go:glm-5.3-flash" {
		t.Fatalf("expected settings.inline_model to win, got prov=%q mod=%q", prov, mod)
	}
}

func TestStageAndRevertSingleDiffHunks(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := InitGitRepo(ctx, dir); err != nil {
		t.Fatal(err)
	}
	_, _ = runGit(ctx, dir, "config", "user.email", "test@hivemind.local")
	_, _ = runGit(ctx, dir, "config", "user.name", "HiveMind Test")

	var orig []string
	for i := 1; i <= 20; i++ {
		orig = append(orig, fmt.Sprintf("line %02d", i))
	}
	target := filepath.Join(dir, "app.txt")
	if err := os.WriteFile(target, []byte(strings.Join(orig, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = runGit(ctx, dir, "add", "app.txt")
	_, _ = runGit(ctx, dir, "commit", "-m", "initial")

	// Modify line 2 and line 19 to produce 2 separate hunks.
	mod := append([]string(nil), orig...)
	mod[1] = "line 02 ACCEPTED"
	mod[18] = "line 19 REJECTED"
	if err := os.WriteFile(target, []byte(strings.Join(mod, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fd, err := UnstagedFileDiff(ctx, dir, "app.txt")
	if err != nil || fd == nil {
		t.Fatalf("expected unstaged diff, got fd=%v err=%v", fd, err)
	}

	// Split parsed lines into hunks.
	type hunk struct {
		header string
		lines  []string
	}
	var hunks []hunk
	for _, ln := range fd.Lines {
		if ln.Kind == DiffHunk {
			hunks = append(hunks, hunk{header: ln.Text})
			continue
		}
		if len(hunks) > 0 && ln.Kind != DiffHeader {
			hunks[len(hunks)-1].lines = append(hunks[len(hunks)-1].lines, ln.Text)
		}
	}
	if len(hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d (lines=%+v)", len(hunks), fd.Lines)
	}

	// Accept (stage) hunk 1; reject (revert) hunk 2.
	if err := StageDiffHunk(ctx, dir, "app.txt", hunks[0].header, hunks[0].lines); err != nil {
		t.Fatalf("StageDiffHunk failed: %v", err)
	}
	if err := RevertDiffHunk(ctx, dir, "app.txt", hunks[1].header, hunks[1].lines); err != nil {
		t.Fatalf("RevertDiffHunk failed: %v", err)
	}

	afterDiff, err := UnstagedFileDiff(ctx, dir, "app.txt")
	if err != nil {
		t.Fatalf("UnstagedFileDiff after accept/reject failed: %v", err)
	}
	if afterDiff != nil {
		t.Fatalf("expected zero remaining unstaged hunks, got %+v", afterDiff)
	}

	finalData, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	finalStr := strings.ReplaceAll(string(finalData), "\r\n", "\n")
	if !strings.Contains(finalStr, "line 02 ACCEPTED") || !strings.Contains(finalStr, "line 19\n") || strings.Contains(finalStr, "REJECTED") {
		t.Fatalf("unexpected final file content:\n%s", finalStr)
	}
}
