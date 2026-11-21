package resident

import (
	"strings"
	"testing"
)

func TestSoulLoadSaveAndBound(t *testing.T) {
	baseDir := t.TempDir()

	got, err := LoadSoul(baseDir)
	if err != nil || got != "" {
		t.Fatalf("expected empty soul on missing file, got %q err=%v", got, err)
	}

	// Secret values must be redacted when loaded into prompt context, and long content bounded.
	longText := "Prefiro respostas diretas em PT-BR.\nToken acidental: sk-test-secret-1234567890abcdef\n" + strings.Repeat("a", MaxSoulBytes+500)
	if err := SaveSoul(baseDir, longText); err != nil {
		t.Fatalf("SaveSoul failed: %v", err)
	}

	loaded, err := LoadSoul(baseDir)
	if err != nil {
		t.Fatalf("LoadSoul failed: %v", err)
	}
	if len(loaded) > MaxSoulBytes+8 {
		t.Fatalf("expected bounded soul <= ~%d bytes, got %d", MaxSoulBytes, len(loaded))
	}
	if strings.Contains(loaded, "sk-test-secret-1234567890abcdef") {
		t.Fatal("expected secret token redacted when loading SOUL.md")
	}

	prompt, err := BuildUserPrompt(TurnRequest{
		Workstream: "geral",
		Message:    "resuma o status",
		Soul:       loaded,
	}, nil)
	if err != nil {
		t.Fatalf("BuildUserPrompt failed: %v", err)
	}
	if !strings.Contains(prompt, "Preferências do Usuário (SOUL.md)") || !strings.Contains(prompt, "Prefiro respostas diretas") {
		t.Fatalf("expected SOUL.md injected cleanly into user prompt, got:\n%s", prompt)
	}
}
