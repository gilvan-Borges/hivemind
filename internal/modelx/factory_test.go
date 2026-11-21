package modelx

import (
	"context"
	"testing"
)

func TestWireModelID(t *testing.T) {
	cases := []struct{ provider, model, want string }{
		{"openrouter", "deepseek/deepseek-v4.1-flash", "deepseek/deepseek-v4.1-flash"},
		{"openrouter", "openrouter:deepseek/deepseek-v4.1-flash", "deepseek/deepseek-v4.1-flash"},
		{"openrouter", "openrouter/deepseek/deepseek-v4.1-flash", "deepseek/deepseek-v4.1-flash"},
		{"openrouter", "openrouter/auto", "openrouter/auto"},
		{"openrouter", "particle-ai/particle-1:free", "particle-ai/particle-1:free"},
		{"opencode", "opencode-go:deepseek-v4.1-flash", "deepseek-v4.1-flash"},
		{"cursor", "cursor:auto", "auto"},
		{"gemini", "gemini:gemini-2.5-flash", "gemini-2.5-flash"},
		{"anthropic", "claude-sonnet-4-5", "claude-sonnet-4-5"},
	}
	for _, tc := range cases {
		if got := WireModelID(tc.provider, tc.model); got != tc.want {
			t.Errorf("WireModelID(%q, %q) = %q, want %q", tc.provider, tc.model, got, tc.want)
		}
	}
}

func TestMissingCredentials(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_BACKEND", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("OPENROUTER_BASE_URL", "")
	prevPath := keysFilePathFn
	keysFilePathFn = func() (string, error) { return t.TempDir() + "/api-keys.json", nil }
	t.Cleanup(func() { keysFilePathFn = prevPath })
	if _, err := New(context.Background(), "gemini", "gemini-flash-latest"); err == nil {
		t.Fatal("gemini sem chave deveria falhar")
	}
	if _, err := New(context.Background(), "anthropic", "claude-sonnet-4-5"); err == nil {
		t.Fatal("anthropic sem chave deveria falhar")
	}
	if _, err := New(context.Background(), "openai", "gpt-4.1"); err == nil {
		t.Fatal("openai sem chave deveria falhar")
	}
	if _, err := New(context.Background(), "openrouter", "openai/gpt-4.1"); err == nil {
		t.Fatal("openrouter sem chave deveria falhar")
	}
	if _, err := New(context.Background(), "desconhecido", "x"); err == nil {
		t.Fatal("provedor desconhecido deveria falhar")
	}
}
