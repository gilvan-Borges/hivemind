package orchestrator

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/contextx"
	"github.com/gilvan-Borges/HiveMind/internal/skillx"
)

func TestCodexSkillsFrontmatterAndContextBudget(t *testing.T) {
	// Body paragraph remains primary when present (preserving HiveMind PT-BR body descriptions),
	// while frontmatter description serves as fallback when the body only has headings.
	mdWithBody := "---\nname: plan\ndescription: fallback\n---\n\n# Plan\n\nDescrição principal no corpo.\n"
	if got := skillx.ExtractDescription(mdWithBody); !strings.Contains(got, "Descrição principal no corpo") {
		t.Fatalf("expected body description when present, got %q", got)
	}
	mdFrontmatterOnly := "---\nname: plan\ndescription: >-\n  When the user asks to plan a task,\n  ask focused questions and capture decisions.\n---\n\n# Plan\n"
	got := skillx.ExtractDescription(mdFrontmatterOnly)
	if !strings.Contains(got, "When the user asks to plan a task, ask focused questions") {
		t.Fatalf("expected YAML frontmatter description fallback when body has no paragraph, got %q", got)
	}

	var many []skillx.Skill
	for i := 0; i < 40; i++ {
		many = append(many, skillx.Skill{
			Name:        fmt.Sprintf("skill-%02d", i),
			Description: strings.Repeat("diretriz detalhada de exemplo ", 6),
			Path:        fmt.Sprintf(".agents/skills/skill-%02d/SKILL.md", i),
			Source:      "project",
			Enabled:     true,
		})
	}
	budget := 900
	block := skillx.CatalogBlockWithBudget(many, nil, budget)
	if len(block) > budget+120 {
		t.Fatalf("expected catalog to respect budget=%d, got %d bytes", budget, len(block))
	}
	if !strings.Contains(block, "orçamento de contexto de skills") {
		t.Fatalf("expected explicit budget truncation notice when exceeding minimal budget, got %q", block)
	}
}

func TestCodexSubagentHeadTailCapAndContextWindowGuidance(t *testing.T) {
	hugeHead := "HEADER_START: mvn test\n" + strings.Repeat("INFO downloading dependency...\n", 400)
	hugeTail := "\n[ERROR] Compilation failure in AuthService.java:[42,10] cannot find symbol\n"
	bigOutput := hugeHead + hugeTail

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "Corrija o build"}}},
			{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: "c1", Name: "run_shell"}}}},
			{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
				ID:       "c1",
				Name:     "run_shell",
				Response: map[string]any{"text": bigOutput},
			}}}},
			{Role: genai.RoleModel, Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: "c2", Name: "read_file"}}}},
			{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{
				ID:       "c2",
				Name:     "read_file",
				Response: map[string]any{"text": "package auth;\npublic class AuthService {}"},
			}}}},
		},
	}

	// Below 55% of a 200k window, MaxToolEntryBytes proactively head-and-tail trims the older c1 output
	// while preserving BOTH the header AND the trailing compiler error!
	count, saved := contextx.CompactRequest(req, contextx.Budget{
		ContextWindow:     200_000,
		MaxToolEntryBytes: 2048,
	})
	if count != 1 || saved <= 0 {
		t.Fatalf("expected proactive head-and-tail cap on older oversized tool output, got count=%d saved=%d", count, saved)
	}
	trimmed := req.Contents[2].Parts[0].FunctionResponse.Response["text"].(string)
	if !strings.Contains(trimmed, "HEADER_START: mvn test") || !strings.Contains(trimmed, "AuthService.java:[42,10]") {
		t.Fatalf("head-and-tail truncation must keep both start and trailing error, got %q", trimmed)
	}
	if !strings.Contains(trimmed, "<truncated omitted_approx_tokens=") {
		t.Fatalf("expected Codex-style omitted_approx_tokens marker, got %q", trimmed)
	}

	// ContextWindowGuidance triggers above 55% of window without mutating SystemInstruction/Tools.
	guidance := contextx.ContextWindowGuidance(req, contextx.Budget{ContextWindow: 800})
	if !strings.Contains(guidance, "<context_window_guidance>") {
		t.Fatalf("expected <context_window_guidance> when usage > 55%%, got %q", guidance)
	}
}

func TestCodexMessageBoardUnreadNotificationDeliveredOnce(t *testing.T) {
	board := newTeamBoard()
	board.PublishDirective("chefe", "dev_1", "Edite src/auth/Usuario.java L628")

	first := board.UnreadNotificationFor("dev_1")
	if !strings.Contains(first, "<agent_message_board_notification>") || !strings.Contains(first, "Usuario.java L628") {
		t.Fatalf("expected unread notification for dev_1, got %q", first)
	}
	// Second read returns empty so stale notifications never bloat subsequent turns.
	if second := board.UnreadNotificationFor("dev_1"); second != "" {
		t.Fatalf("expected notification delivered once, got %q", second)
	}
}
