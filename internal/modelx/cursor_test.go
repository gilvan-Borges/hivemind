package modelx

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
)

func TestParseCursorStreamSkipsDuplicateFlushes(t *testing.T) {
	delta := `{"type":"assistant","timestamp_ms":10,"message":{"role":"assistant","content":[{"type":"text","text":"vou ler"}]}}`
	flush := `{"type":"assistant","timestamp_ms":11,"model_call_id":"call-1","message":{"role":"assistant","content":[{"type":"text","text":"vou ler"}]}}`
	endFlush := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"vou ler"}]}}`
	tool := `{"type":"tool_call","subtype":"started","tool_call":{"readToolCall":{"args":{"path":"README.md","fileText":"segredo que não deve aparecer"}}}}`
	doneTool := `{"type":"tool_call","subtype":"completed","tool_call":{"readToolCall":{"result":{"success":{"content":"arquivo inteiro"}}}}}`
	result := `{"type":"result","subtype":"success","is_error":false,"result":"pronto"}`
	system := `{"type":"system","subtype":"init","apiKeySource":"env","model":"auto"}`

	n, ok := parseCursorStreamLine(delta)
	if !ok || n.Text != "vou ler" || !n.Partial {
		t.Fatalf("delta: %+v ok=%v", n, ok)
	}
	if _, ok := parseCursorStreamLine(flush); ok {
		t.Fatal("flush antes da ferramenta não pode repetir o texto")
	}
	if _, ok := parseCursorStreamLine(endFlush); ok {
		t.Fatal("flush final não pode repetir o texto")
	}
	toolNotice, ok := parseCursorStreamLine(tool)
	if !ok || !strings.Contains(toolNotice.Text, "read") || !strings.Contains(toolNotice.Text, "README.md") {
		t.Fatalf("ferramenta: %+v", toolNotice)
	}
	if strings.Contains(toolNotice.Text, "segredo") {
		t.Fatal("aviso de ferramenta não pode incluir o corpo do arquivo")
	}
	if _, ok := parseCursorStreamLine(doneTool); ok {
		t.Fatal("conclusão da ferramenta não entra na UI")
	}
	final, ok := parseCursorStreamLine(result)
	if !ok || !final.Final || final.Text != "pronto" {
		t.Fatalf("resultado: %+v", final)
	}
	if _, ok := parseCursorStreamLine(system); ok {
		t.Fatal("evento de sistema não deve aparecer")
	}
}

func TestNewCursorRejectsForeignModelBeforeCLI(t *testing.T) {
	for _, id := range []string{
		"openrouter/particle-ai/particle-1:free",
		"deepseek/deepseek-v4.1-flash",
		"particle-ai/particle-1:free",
	} {
		_, err := NewCursor(context.Background(), id)
		if err == nil {
			t.Fatalf("%s deveria falhar antes de procurar o CLI", id)
		}
		if !strings.Contains(err.Error(), id) || !strings.Contains(err.Error(), "não é enviado ao CLI") {
			t.Fatalf("%s: %v", id, err)
		}
		if !strings.Contains(err.Error(), "cursor/"+id) {
			t.Fatalf("erro sem provedor/modelo: %v", err)
		}
	}
	if foreignCursorModel("auto") || foreignCursorModel("composer-2.5") || foreignCursorModel("sonnet-4.5") {
		t.Fatal("slug do Cursor foi tratado como id de outro provedor")
	}
}

func TestCursorArgsOmitAutoAndNeverCarryTheKey(t *testing.T) {
	secret := "crsr_supersecretvalue"
	args := cursorArgs("auto", `C:\projetos\meu app`)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--model") {
		t.Fatalf("auto não deve fixar --model: %s", joined)
	}
	if strings.Contains(joined, secret) {
		t.Fatal("argumentos não podem carregar a chave")
	}
	if !strings.Contains(joined, "stream-json") || !strings.Contains(joined, "--trust") {
		t.Fatalf("args: %s", joined)
	}
	pinned := cursorArgs("composer-2.5", "/tmp/proj")
	if pinned[0] != "--model" || pinned[1] != "composer-2.5" {
		t.Fatalf("modelo explícito: %v", pinned[:4])
	}
	env := cursorChildEnv([]string{"CURSOR_API_KEY=old", "PATH=/bin"}, secret)
	found := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, "CURSOR_API_KEY=") {
			found++
			if entry != "CURSOR_API_KEY="+secret {
				t.Fatalf("env da chave: %s", entry)
			}
		}
	}
	if found != 1 {
		t.Fatalf("chave no ambiente %d vezes", found)
	}
}

func TestCursorModelID(t *testing.T) {
	if got := cursorModelID("cursor:auto"); got != "auto" {
		t.Fatal(got)
	}
	if got := cursorModelID("Composer-2.5"); got != "Composer-2.5" {
		t.Fatal(got)
	}
	if got := cursorModelID(""); got != "auto" {
		t.Fatal(got)
	}
}

func TestLimitWriterReportsFullLength(t *testing.T) {
	var buf strings.Builder
	w := &limitWriter{w: &buf, n: 4}
	n, err := w.Write([]byte("abcdefghij"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 10 {
		t.Fatalf("escreveu %d, o contrato exige o tamanho original", n)
	}
	if buf.String() != "abcd" {
		t.Fatalf("guardou %q", buf.String())
	}
	n, err = w.Write([]byte("zzzz"))
	if err != nil || n != 4 {
		t.Fatalf("descarte n=%d err=%v", n, err)
	}
	if buf.String() != "abcd" {
		t.Fatalf("passou do limite: %q", buf.String())
	}
}

func TestReadCursorStreamSkipsOversizedLine(t *testing.T) {
	first := "{\"type\":\"assistant\",\"timestamp_ms\":1,\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"oi\"}]}}\n"
	huge := strings.Repeat("y", 70*1024) + "\n"
	last := "{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"fim\"}\n"
	var got []string
	err := readCursorStream(strings.NewReader(first+huge+last), 1024, func(line string) bool {
		got = append(got, strings.TrimSpace(line))
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.Contains(got[0], "oi") || !strings.Contains(got[1], "fim") {
		t.Fatalf("linhas: %d %v", len(got), got)
	}
}

func TestBuildCursorPromptIncludesImagePath(t *testing.T) {
	dir := t.TempDir()
	req := &model.LLMRequest{Contents: []*genai.Content{{
		Role: "user",
		Parts: []*genai.Part{
			{Text: "o que tem nesta tela?"},
			{InlineData: &genai.Blob{MIMEType: "image/png", Data: []byte{0x89, 0x50, 0x4e, 0x47}}},
		},
	}}}
	images, err := SaveRequestImages(dir, req)
	if err != nil {
		t.Fatal(err)
	}
	prompt := buildCursorPrompt(req, images)
	if strings.Contains(prompt, "omitido") || !strings.Contains(prompt, images[0].Path) {
		t.Fatalf("prompt:\n%s", prompt)
	}
}

func TestRedactSecret(t *testing.T) {
	if got := redactSecret("falha crsr_abc no stderr", "crsr_abc"); strings.Contains(got, "crsr_abc") {
		t.Fatal(got)
	}
}

func TestParseCursorModelList(t *testing.T) {
	got := parseCursorModelList("Available models\nmodel id\nauto\ncomposer-2.5\ncomposer-2.5\n")
	if len(got) != 2 || got[0] != "auto" || got[1] != "composer-2.5" {
		t.Fatalf("%v", got)
	}
	fromJSON := parseCursorModelList(`[{"id":"auto"},{"id":"gpt-5"}]`)
	if len(fromJSON) != 2 || fromJSON[1] != "gpt-5" {
		t.Fatalf("%v", fromJSON)
	}
}

func TestNewCursorMissingKey(t *testing.T) {
	t.Setenv("CURSOR_API_KEY", "")
	tmp := t.TempDir()
	prev := keysFilePathFn
	keysFilePathFn = func() (string, error) { return filepath.Join(tmp, "api-keys.json"), nil }
	t.Cleanup(func() { keysFilePathFn = prev })
	prevProf := profilesFilePathFn
	profilesFilePathFn = func() (string, error) { return filepath.Join(tmp, "provider-profiles.json"), nil }
	t.Cleanup(func() { profilesFilePathFn = prevProf })
	_, err := New(context.Background(), "cursor", "auto")
	if err == nil || !strings.Contains(err.Error(), "CURSOR_API_KEY") {
		t.Fatalf("erro: %v", err)
	}
}

func TestNewCursorMissingCLIDoesNotLeakKey(t *testing.T) {
	secret := "crsr_supersecretvalue"
	t.Setenv("CURSOR_API_KEY", secret)
	missing := filepath.Join(t.TempDir(), "no-agent")
	t.Setenv("HIVEMIND_CURSOR_AGENT_PATH", missing)
	_, err := New(context.Background(), "cursor", "cursor:auto")
	if err == nil {
		t.Fatal("esperava CLI ausente")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("a chave vazou no erro: %s", err)
	}
	if !strings.Contains(err.Error(), "cursor.com/install") {
		t.Fatalf("faltou a instrução de instalação: %s", err)
	}
}

func TestGenerateContentReadsStreamJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o falso CLI deste teste é um script shell")
	}
	secret := "crsr_supersecretvalue"
	t.Setenv("CURSOR_API_KEY", secret)
	dir := t.TempDir()
	script := filepath.Join(dir, "agent")
	body := "#!/bin/sh\nprintf '%s\\n' '" +
		`{"type":"assistant","timestamp_ms":1,"message":{"content":[{"type":"text","text":"olá"}]}}` + "'\n" +
		"printf '%s\\n' '" +
		`{"type":"tool_call","subtype":"started","tool_call":{"writeToolCall":{"args":{"path":"a.go"}}}}` + "'\n" +
		"printf '%s\\n' '" +
		`{"type":"result","subtype":"success","is_error":false,"result":"feito"}` + "'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIVEMIND_CURSOR_AGENT_PATH", script)
	prev := keysFilePathFn
	keysFilePathFn = func() (string, error) { return filepath.Join(dir, "keys.json"), nil }
	t.Cleanup(func() { keysFilePathFn = prev })

	llm, err := New(WithAgentDir(context.Background(), dir), "cursor", "auto")
	if err != nil {
		t.Fatal(err)
	}
	var partials []string
	var final string
	for resp, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Config:   &genai.GenerateContentConfig{SystemInstruction: genai.NewContentFromText("não grave segredos", genai.RoleUser)},
		Contents: []*genai.Content{genai.NewContentFromText("adicione um healthcheck", genai.RoleUser)},
	}, true) {
		if err != nil {
			t.Fatal(err)
		}
		if resp == nil || resp.Content == nil || len(resp.Content.Parts) == 0 {
			continue
		}
		text := resp.Content.Parts[0].Text
		if strings.Contains(text, secret) {
			t.Fatalf("texto com chave: %s", text)
		}
		if resp.Partial {
			partials = append(partials, text)
			continue
		}
		final = text
	}
	if final != "feito" {
		t.Fatalf("final %q parciais %v", final, partials)
	}
	if len(partials) < 2 {
		t.Fatalf("parciais: %v", partials)
	}
	if _, err := os.Stat(filepath.Join(dir, ".hivemind", "cursor-task.md")); !os.IsNotExist(err) {
		t.Fatal("o prompt temporário deveria ser apagado")
	}
}
