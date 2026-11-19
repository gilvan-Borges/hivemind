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
	if got := NormalizeProvider("", "claude-sonnet-4-5"); got != "anthropic" {
		t.Fatalf("plain claude id must infer anthropic, got %q", got)
	}
}
