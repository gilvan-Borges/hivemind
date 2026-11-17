package modelx

import (
	"context"
	"testing"
)

func TestMissingCredentials(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_GENAI_USE_VERTEXAI", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_BACKEND", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	if _, err := New(context.Background(), "gemini", "gemini-flash-latest"); err == nil {
		t.Fatal("gemini sem chave deveria falhar")
	}
	if _, err := New(context.Background(), "anthropic", "claude-sonnet-4-5"); err == nil {
		t.Fatal("anthropic sem chave deveria falhar")
	}
	if _, err := New(context.Background(), "openai", "gpt-4.1"); err == nil {
		t.Fatal("openai sem chave deveria falhar")
	}
	if _, err := New(context.Background(), "desconhecido", "x"); err == nil {
		t.Fatal("provedor desconhecido deveria falhar")
	}
}
