package team

import "testing"

func TestNormalizeProviderOpenRouter(t *testing.T) {
	if got := NormalizeProvider("openrouter", ""); got != "openrouter" {
		t.Fatalf("explicit openrouter provider, got %q", got)
	}
	if got := NormalizeProvider("", "anthropic/claude-sonnet-4-5"); got != "openrouter" {
		t.Fatalf("vendor/model id must infer openrouter, got %q", got)
	}
	if got := NormalizeProvider("", "opencode-go:glm-5.3"); got != "opencode" {
		t.Fatalf("opencode-go spec must keep opencode provider, got %q", got)
	}
	if got := NormalizeProvider("", "gpt-4.1"); got != "openai" {
		t.Fatalf("plain gpt id must infer openai, got %q", got)
	}
	if got := NormalizeProvider("", "cursor:auto"); got != "cursor" {
		t.Fatalf("cursor:auto deve ser o provedor cursor, veio %q", got)
	}
	if got := NormalizeProvider("cursor", "auto"); got != "cursor" {
		t.Fatalf("provedor explícito, veio %q", got)
	}
	if got := NormalizeProvider("", "claude-sonnet-4-5"); got != "anthropic" {
		t.Fatalf("plain claude id must infer anthropic, got %q", got)
	}
	if got := NormalizeProvider("jev", "auto"); got != "jev" {
		t.Fatalf("explicit jev provider must stay jev, got %q", got)
	}
	if got := NormalizeProvider("", "jev:auto"); got != "jev" {
		t.Fatalf("jev:auto must infer jev, got %q", got)
	}
	tErr := Team{
		Name: "x",
		Agents: []Agent{
			{Name: "jev", Role: RoleDeveloper, Provider: "gemini", Model: "gemini-flash-latest"},
		},
	}.Validate()
	if tErr == nil {
		t.Fatal("expected reserved agent name jev to fail validation")
	}
}
