package modelx

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
)

func TestParseAgySpec(t *testing.T) {
	slug, effort := ParseAgySpec("agy")
	if slug != "" || effort != "" {
		t.Fatalf("agy: %q %q", slug, effort)
	}
	slug, effort = ParseAgySpec("agy:gemini-3.8-flash-medium")
	if slug != "gemini-3.8-flash-medium" || effort != "" {
		t.Fatalf("slug: %q %q", slug, effort)
	}
	slug, effort = ParseAgySpec("agy:gemini-3.8-flash-medium:high")
	if slug != "gemini-3.8-flash-medium" || effort != "high" {
		t.Fatalf("effort: %q %q", slug, effort)
	}
	slug, effort = ParseAgySpec("agy:high")
	if slug != "" || effort != "high" {
		t.Fatalf("effort only: %q %q", slug, effort)
	}
}

func TestAgyTextAndToolCall(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeAgy(t, dir)
	work := filepath.Join(dir, "empty")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AGY_FAKE_MODE", "text")
	llm := &Agy{Bin: bin, Dir: work, Timeout: 5 * time.Second}
	var textResp *model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "diga oi"}}}},
	}, false) {
		if err != nil {
			t.Fatal(err)
		}
		textResp = resp
	}
	if textResp == nil || textResp.Content.Parts[0].Text != "plano pronto" {
		t.Fatalf("text = %+v", textResp)
	}
	args := readFile(t, filepath.Join(dir, "args"))
	if strings.Contains(args, "--dangerously-skip-permissions") || strings.Contains(args, "--disable-slash-commands") {
		t.Fatalf("flags recusadas: %s", args)
	}
	if !strings.Contains(args, "--output-format") || !strings.Contains(args, "--input-format") {
		t.Fatalf("args = %s", args)
	}
	assertPromptOnlyOnStdin(t, args, readFile(t, filepath.Join(dir, "stdin")), "diga oi")
	assertSchemaFile(t, args, filepath.Join(dir, "schema-body"))
	if strings.TrimSpace(readFile(t, filepath.Join(dir, "pwd"))) != work {
		t.Fatalf("cwd = %q, want %s", readFile(t, filepath.Join(dir, "pwd")), work)
	}

	t.Setenv("AGY_FAKE_MODE", "tool")
	req := &model.LLMRequest{
		Config: &genai.GenerateContentConfig{Tools: []*genai.Tool{{
			FunctionDeclarations: []*genai.FunctionDeclaration{{
				Name:        "write_file",
				Description: "Grava um arquivo.",
				ParametersJsonSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string"},
					},
				},
			}},
		}}},
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "grave a.txt"}}}},
	}
	var toolResp *model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatal(err)
		}
		toolResp = resp
	}
	call := toolResp.Content.Parts[0].FunctionCall
	if call == nil || call.Name != "write_file" || call.Args["path"] != "a.txt" {
		t.Fatalf("call = %+v", toolResp.Content.Parts)
	}
	toolArgs := readFile(t, filepath.Join(dir, "args"))
	prompt := readFile(t, filepath.Join(dir, "stdin"))
	assertPromptOnlyOnStdin(t, toolArgs, prompt, "grave a.txt")
	if !strings.Contains(prompt, "write_file") {
		t.Fatalf("stdin sem a ferramenta:\n%s", prompt)
	}
	assertSchemaFile(t, toolArgs, filepath.Join(dir, "schema-body"))
}

func TestPromptNeverInArgv(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeAgy(t, dir)
	t.Setenv("AGY_FAKE_MODE", "text")
	llm := &Agy{Bin: bin, Dir: dir, Timeout: 5 * time.Second}
	marker := "linha um\n\"aspas\" e mais texto"
	for _, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: marker}}}},
	}, false) {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertPromptOnlyOnStdin(t, readFile(t, filepath.Join(dir, "args")), readFile(t, filepath.Join(dir, "stdin")), marker)
}

func TestAgyExitsBeforeReadingStdin(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeAgy(t, dir)
	t.Setenv("AGY_FAKE_MODE", "early")
	llm := &Agy{Bin: bin, Dir: dir, Timeout: 5 * time.Second}
	var got error
	for _, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "prompt longo o bastante para o stdin"}}}},
	}, false) {
		got = err
	}
	if got == nil {
		t.Fatal("esperava erro")
	}
	msg := got.Error()
	if strings.Contains(msg, "não encontrei") || strings.Contains(strings.ToLower(msg), "not found") {
		t.Fatalf("pipe quebrado foi tratado como ausência do agy: %s", msg)
	}
	if !strings.Contains(msg, "código 2") || !strings.Contains(msg, "opção recusada pelo agy") {
		t.Fatalf("erro = %s", msg)
	}
}

func TestLookAgyOrder(t *testing.T) {
	root := t.TempDir()
	envBin := filepath.Join(root, "from-env")
	pathDir := filepath.Join(root, "path")
	localRoot := filepath.Join(root, "local")
	if err := os.MkdirAll(pathDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(localRoot, "agy", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{envBin, filepath.Join(pathDir, "agy.CMD"), filepath.Join(localRoot, "agy", "bin", "agy.exe")} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HIVEMIND_AGY_PATH", envBin)
	t.Setenv("PATH", pathDir)
	t.Setenv("PATHEXT", ".CMD")
	t.Setenv("LOCALAPPDATA", localRoot)
	got, err := LookAgy()
	if err != nil || got != envBin {
		t.Fatalf("env = %s %v", got, err)
	}
	t.Setenv("HIVEMIND_AGY_PATH", "")
	got, err = LookAgy()
	if err != nil || got != filepath.Join(pathDir, "agy.CMD") {
		t.Fatalf("path = %s %v", got, err)
	}
	t.Setenv("PATH", filepath.Join(root, "vazio"))
	if err := os.MkdirAll(os.Getenv("PATH"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = LookAgy()
	if err != nil || got != filepath.Join(localRoot, "agy", "bin", "agy.exe") {
		t.Fatalf("localappdata = %s %v", got, err)
	}
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "sem-agy"))
	if _, err := LookAgy(); err == nil || !strings.Contains(err.Error(), "HIVEMIND_AGY_PATH") {
		t.Fatalf("ausente = %v", err)
	}
}

func TestAgyAuthAndMissingBinary(t *testing.T) {
	dir := t.TempDir()
	bin := writeFakeAgy(t, dir)
	t.Setenv("AGY_FAKE_MODE", "auth")
	llm := &Agy{Bin: bin, Dir: dir, Timeout: 5 * time.Second}
	for _, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "oi"}}}},
	}, false) {
		if err == nil || !strings.Contains(err.Error(), "Google Pro") {
			t.Fatalf("erro = %v", err)
		}
	}
	missing := &Agy{Bin: filepath.Join(dir, "nao-existe"), Timeout: time.Second}
	for _, err := range missing.GenerateContent(context.Background(), &model.LLMRequest{}, false) {
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "agy") {
			t.Fatalf("missing = %v", err)
		}
	}
}

func TestAgyCmdWrapper(t *testing.T) {
	cmd := agyCommand(context.Background(), `C:\Tools\agy.cmd`, []string{"-p", "oi"})
	if cmd.Path != "cmd.exe" && !strings.HasSuffix(strings.ToLower(cmd.Path), "cmd.exe") {
		t.Fatalf("path = %s", cmd.Path)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "/d /c") || !strings.Contains(joined, "agy.cmd") {
		t.Fatalf("args = %#v", cmd.Args)
	}
}

func TestDefaultProviderModel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("PATH", dir)
	t.Setenv("HIVEMIND_AGY_PATH", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(dir, "sem-local"))
	provider, modelName := DefaultProviderModel()
	if provider != "gemini" || modelName != "gemini-flash-latest" {
		t.Fatalf("sem agy: %s %s", provider, modelName)
	}
	writeFakeAgy(t, dir)
	provider, modelName = DefaultProviderModel()
	if provider != "agy" || modelName != "agy" {
		t.Fatalf("com agy: %s %s", provider, modelName)
	}
	t.Setenv("GOOGLE_API_KEY", "presente")
	provider, modelName = DefaultProviderModel()
	if provider != "gemini" {
		t.Fatalf("com chave: %s %s", provider, modelName)
	}
	_, err := New(context.Background(), "agy", "agy")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	if _, err := New(context.Background(), "agy", "agy"); err == nil || !strings.Contains(err.Error(), "HIVEMIND_AGY_PATH") {
		t.Fatalf("new sem agy: %v", err)
	}
}

func TestProbeAgyPrompt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("HIVEMIND_AGY_PATH", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(dir, "sem-local"))
	writeFakeAgy(t, dir)
	t.Setenv("AGY_FAKE_MODE", "probe")
	text, err := ProbeAgyPrompt(context.Background())
	if err != nil || !strings.Contains(text, "OK") {
		t.Fatalf("probe = %q %v", text, err)
	}
	args := readFile(t, filepath.Join(dir, "args"))
	stdin := readFile(t, filepath.Join(dir, "stdin"))
	assertPromptOnlyOnStdin(t, args, stdin, "responda apenas OK")
	assertSchemaFile(t, args, filepath.Join(dir, "schema-body"))
	t.Setenv("AGY_FAKE_MODE", "early")
	_, err = ProbeAgyPrompt(context.Background())
	if err == nil || !strings.Contains(err.Error(), "código 2") || strings.Contains(err.Error(), "não encontrei") {
		t.Fatalf("probe early = %v", err)
	}
}

func TestProbeAgy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("HIVEMIND_AGY_PATH", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(dir, "sem-local"))
	absent := ProbeAgy(context.Background())
	if absent.Found {
		t.Fatal("não deveria achar agy")
	}
	writeFakeAgy(t, dir)
	t.Setenv("AGY_FAKE_MODE", "models-ok")
	ok := ProbeAgy(context.Background())
	if !ok.Found || !ok.LoggedIn {
		t.Fatalf("probe = %+v", ok)
	}
	t.Setenv("AGY_FAKE_MODE", "auth")
	denied := ProbeAgy(context.Background())
	if !denied.Found || denied.LoggedIn || !strings.Contains(denied.Detail, "login") {
		t.Fatalf("denied = %+v", denied)
	}
}

func writeFakeAgy(t *testing.T, dir string) string {
	t.Helper()
	// O fake é um script /bin/sh: só executa no CI Linux. No Windows local os
	// testes pulam em vez de falhar (ruído documentado no DOGSFOODING).
	if runtime.GOOS == "windows" {
		t.Skip("fake agy é script sh; coberto no CI Linux")
	}
	path := filepath.Join(dir, "agy")
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + dir + `/args"
pwd > "` + dir + `/pwd"
mode="${AGY_FAKE_MODE:-text}"
if [ "$1" = "models" ]; then
  if [ "$mode" = "auth" ]; then
    echo "authentication required" >&2
    exit 1
  fi
  echo "gemini-3.8-flash-medium"
  exit 0
fi
if [ "$mode" = "early" ]; then
  echo "opção recusada pelo agy" >&2
  exit 2
fi
if [ "$mode" = "quota-once" ] || [ "$mode" = "quota-always" ]; then
  if [ "$mode" = "quota-always" ] || [ ! -f "$AGY_FAKE_DIR/quota-hit" ]; then
    touch "$AGY_FAKE_DIR/quota-hit"
    echo 'AGY_ERROR: {"short_error":"RESOURCE_EXHAUSTED (code 429): Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED","error_code":429,"code_kind":"http","retryable":true,"error_id":"t1"}' >&2
    printf '%s\n' '{"status":"ERROR","response":"","error":"Your AI credits balance is too low to continue."}'
    exit 3
  fi
  printf '%s\n' '{"status":"SUCCESS","response":"depois da quota","structured_output":{"text":"depois da quota","tool_calls":[]}}'
  exit 0
fi
/bin/cat > "` + dir + `/stdin"
prev=
for a in "$@"; do
  if [ "$prev" = "--json-schema" ]; then
    /bin/cp "$a" "` + dir + `/schema-body"
  fi
  prev=$a
done
case "$mode" in
  probe)
    printf '%s\n' '{"status":"SUCCESS","response":"OK","structured_output":{"text":"OK","tool_calls":[]}}'
    ;;
  tool)
    printf '%s\n' '{"status":"SUCCESS","response":"{\"text\":\"\",\"tool_calls\":[{\"id\":\"c1\",\"name\":\"write_file\",\"arguments\":{\"path\":\"a.txt\",\"content\":\"oi\"}}]}","structured_output":{"text":"","tool_calls":[{"id":"c1","name":"write_file","arguments":{"path":"a.txt","content":"oi"}}]},"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}'
    ;;
  auth)
    echo "authentication required" >&2
    printf '%s\n' '{"status":"ERROR","response":"","error":"authentication required"}'
    exit 1
    ;;
  *)
    printf '%s\n' '{"status":"SUCCESS","response":"plano pronto","structured_output":{"text":"plano pronto","tool_calls":[]}}'
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertPromptOnlyOnStdin(t *testing.T, args, stdin, marker string) {
	t.Helper()
	if !strings.Contains(stdin, marker) {
		t.Fatalf("stdin sem o prompt %q:\n%s", marker, stdin)
	}
	for _, arg := range strings.Split(args, "\n") {
		if strings.Contains(arg, marker) || arg == "-p" || arg == "--print" || arg == "--prompt" {
			t.Fatalf("argv contém o prompt ou -p: %q\nargs:\n%s", arg, args)
		}
	}
	if !strings.Contains(args, "--input-format\ntext") && !strings.Contains(args, "--input-format\r\ntext") {
		t.Fatalf("falta --input-format text:\n%s", args)
	}
}

func assertSchemaFile(t *testing.T, args, bodyPath string) {
	t.Helper()
	var schemaPath string
	var prev string
	for _, arg := range strings.Split(args, "\n") {
		if prev == "--json-schema" {
			schemaPath = arg
			break
		}
		prev = arg
	}
	if schemaPath == "" || strings.HasPrefix(strings.TrimSpace(schemaPath), "{") {
		t.Fatalf("json-schema deve ser um caminho, não JSON inline:\n%s", args)
	}
	body := readFile(t, bodyPath)
	if !strings.Contains(body, "tool_calls") || strings.Contains(schemaPath, "tool_calls") {
		t.Fatalf("schema path=%q body=%s", schemaPath, body)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
