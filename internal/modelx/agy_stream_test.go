package modelx

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
)

func contentsFromText(text string) []*genai.Content {
	return []*genai.Content{{Role: genai.RoleUser, Parts: []*genai.Part{{Text: text}}}}
}

// Spec 06 §3.1: Agy.GenerateContent(..., stream=true) emite deltas
// incrementais antes do resultado final.
func TestAgyGenerateContentStreamsDeltas(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeAgy(t, dir)
	t.Setenv("AGY_FAKE_MODE", "stream")
	llm := &Agy{Bin: bin, Dir: dir, Timeout: 5 * time.Second}

	var partials []string
	var final *model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: contentsFromText("diga olá"),
	}, true) {		if err != nil {
			t.Fatalf("stream erro inesperado: %v", err)
		}
		if resp == nil {
			continue
		}
		if resp.Partial {
			for _, p := range resp.Content.Parts {
				if p != nil && p.Text != "" {
					partials = append(partials, p.Text)
				}
			}
			continue
		}
		final = resp
	}
	if len(partials) == 0 {
		t.Fatal("nenhum delta Partial foi emitido no modo stream")
	}
	joined := strings.Join(partials, "")
	if !strings.Contains(joined, "Olá") {
		t.Fatalf("deltas não carregam o texto: %q", joined)
	}
	if !strings.Contains(joined, "[[hmtool:") {
		t.Fatalf("deltas não carregam o marcador de tool: %q", joined)
	}
	if final == nil {
		t.Fatal("resposta final ausente")
	}
	if !final.TurnComplete {
		t.Fatal("resposta final deve ser TurnComplete")
	}
	var finalText strings.Builder
	for _, p := range final.Content.Parts {
		if p != nil && p.Text != "" {
			finalText.WriteString(p.Text)
		}
	}
	if !strings.Contains(finalText.String(), "Olá") {
		t.Fatalf("resposta final sem texto do envelope: %q", finalText.String())
	}

	// O argv do stream usa o formato certo.
	args := readFile(t, filepath.Join(dir, "args"))
	if !strings.Contains(args, "stream-json") {
		t.Fatalf("argv sem --output-format stream-json:\n%s", args)
	}
	assertPromptOnlyOnStdin(t, args, readFile(t, filepath.Join(dir, "stdin")), "diga olá")
}
