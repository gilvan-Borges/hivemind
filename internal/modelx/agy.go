package modelx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/procx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

const (
	// DefaultAgyTimeout is the headless wait when the caller sets no deadline.
	DefaultAgyTimeout = 10 * time.Minute
)

// The prompt is never an argv element. Windows quoting splits newlines and
// quotes inside `agy -p`, and agy then reports an unexpected argument.
// Headless mode reads the plain UTF-8 prompt from stdin with --input-format text.

// Agy is a model.LLM that calls the Google Antigravity CLI (`agy`).
// Each GenerateContent is one headless process. HiveMind tools are not
// executed by agy: the process runs outside the repository and must answer
// with a JSON decision. ADK runs the real tools after that.
type Agy struct {
	Bin     string
	Model   string
	Effort  string
	Timeout time.Duration
	// Dir is the process working directory. Empty means a fresh temp dir,
	// never the project, so agy's own tools cannot write the repo.
	Dir string
	// Env carries optional per-profile environment overrides (e.g. AGY_ADC_AUTH,
	// CLOUDSDK_CONFIG, GOOGLE_CLOUD_PROJECT) isolated to this child process.
	Env map[string]string
	// IsolateEnv strips inherited API keys and CLOUDSDK_CONFIG before applying Env,
	// so an org-scoped or ADC account does not see another org's credentials.
	IsolateEnv bool
	// ProfileID identifies which isolated AGY OAuth vault session to activate before running.
	ProfileID string
}

// AgyStatus is what `hivemind doctor` prints about the CLI. It never includes
// secrets or the model list.
type AgyStatus struct {
	Found    bool
	Path     string
	LoggedIn bool
	Detail   string
}

// LookAgy resolves the Antigravity CLI. The order is HIVEMIND_AGY_PATH,
// then PATH with PATHEXT, then %LOCALAPPDATA%\agy\bin\agy.exe.
// A missing binary is the only "not found" result. A later execution
// failure keeps the real exit code.
func LookAgy() (string, error) {
	if p := strings.TrimSpace(os.Getenv("HIVEMIND_AGY_PATH")); p != "" && fileExists(p) {
		return p, nil
	}
	if p := findAgyOnPath(); p != "" {
		return p, nil
	}
	if p := localAppDataAgy(); p != "" && fileExists(p) {
		return p, nil
	}
	return "", fmt.Errorf("não encontrei o agy. Defina HIVEMIND_AGY_PATH, coloque agy no PATH ou instale em %%LOCALAPPDATA%%\\agy\\bin\\agy.exe e entre com a conta Google Pro")
}

// AgyAvailable reports whether the CLI is on PATH.
func AgyAvailable() bool {
	_, err := LookAgy()
	return err == nil
}

// HasGoogleAPIKey reports whether Gemini can be called without Vertex.
func HasGoogleAPIKey() bool {
	return firstEnv("GOOGLE_API_KEY", "GEMINI_API_KEY") != ""
}

// DefaultProviderModel is the team default. agy wins when it is installed and
// no Gemini API key is set.
func DefaultProviderModel() (provider, modelName string) {
	if HasGoogleAPIKey() {
		return "gemini", "gemini-flash-latest"
	}
	if AgyAvailable() {
		return "agy", "agy"
	}
	return "gemini", "gemini-flash-latest"
}

// ProbeAgy runs `agy models` with a short timeout. Exit 0 means a session
// exists. The output of the command is discarded.
func ProbeAgy(ctx context.Context) AgyStatus {
	path, err := LookAgy()
	if err != nil {
		return AgyStatus{Detail: "ausente do PATH. Instale o Antigravity CLI e entre com `agy`"}
	}
	status := AgyStatus{Found: true, Path: path}
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := agyCommand(probeCtx, path, []string{"models"})
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		status.LoggedIn = true
		status.Detail = "autenticado (`agy models` ok)"
		return status
	}
	if probeCtx.Err() != nil {
		status.Detail = "encontrado, mas `agy models` excedeu o tempo"
		return status
	}
	if looksLikeAuth(stderr.String()) {
		status.Detail = "encontrado, sem login. Rode `agy` e entre com a conta Google Pro"
		return status
	}
	status.Detail = "encontrado, mas `agy models` falhou"
	return status
}

// ParseAgySpec splits model strings such as "agy", "agy:gemini-3.8-flash-medium"
// and "agy:gemini-3.8-flash-medium:high".
func ParseAgySpec(modelName string) (slug, effort string) {
	modelName = strings.TrimSpace(modelName)
	lower := strings.ToLower(modelName)
	if modelName == "" || lower == "agy" || lower == "default" {
		return "", ""
	}
	if len(modelName) > 4 && strings.EqualFold(modelName[:4], "agy:") {
		modelName = modelName[4:]
	}
	if modelName == "" {
		return "", ""
	}
	if i := strings.LastIndex(modelName, ":"); i >= 0 {
		tail := strings.ToLower(modelName[i+1:])
		if isAgyEffort(tail) {
			return modelName[:i], tail
		}
	}
	if isAgyEffort(strings.ToLower(modelName)) {
		return "", strings.ToLower(modelName)
	}
	return modelName, ""
}

// NewAgy builds a model that shells out to agy. Bin may be empty; then PATH is searched.
func NewAgy(bin, modelName string) (*Agy, error) {
	return NewAgyForContext(context.Background(), bin, modelName)
}

// NewAgyForContext resolves any workspace/org-scoped AGY profile from ctx and builds
// an Agy adapter with isolated per-process environment variables.
func NewAgyForContext(ctx context.Context, bin, modelName string) (*Agy, error) {
	prof, mode, _ := ResolveProfile(ctx, "agy")
	if mode == "isolated" {
		return nil, fmt.Errorf("nenhuma conta AGY vinculada a esta organização")
	}
	var envMap map[string]string
	var profileID string
	isolate := false
	if prof != nil {
		profileID = prof.ID
		isolate = len(prof.OrgPatterns) > 0 || strings.EqualFold(prof.AuthMode, "adc")
		if len(prof.Env) > 0 {
			envMap = make(map[string]string, len(prof.Env))
			for k, v := range prof.Env {
				envMap[k] = v
			}
		}
		if bin == "" && strings.TrimSpace(prof.Command) != "" {
			bin = strings.TrimSpace(prof.Command)
		}
	}

	slug, effort := ParseAgySpec(modelName)
	if slug == "" {
		slug = strings.TrimSpace(envMap["AGY_MODEL"])
	}
	if slug == "" && !isolate {
		slug = strings.TrimSpace(os.Getenv("AGY_MODEL"))
	}
	if effort == "" {
		effort = strings.ToLower(strings.TrimSpace(envMap["AGY_EFFORT"]))
		if effort == "" && !isolate {
			effort = strings.ToLower(strings.TrimSpace(os.Getenv("AGY_EFFORT")))
		}
		if effort != "" && !isAgyEffort(effort) {
			return nil, fmt.Errorf("AGY_EFFORT %q é inválido; use low, medium, high, xhigh ou max", effort)
		}
	}
	if bin == "" {
		found, err := LookAgy()
		if err != nil {
			return nil, err
		}
		bin = found
	}
	return &Agy{Bin: bin, Model: slug, Effort: effort, Timeout: DefaultAgyTimeout, Env: envMap, IsolateEnv: isolate, ProfileID: profileID}, nil
}

// Name implements model.LLM.
func (a *Agy) Name() string {
	if a.Model != "" {
		return "agy:" + a.Model
	}
	return "agy"
}

// GenerateContent implements model.LLM.
func (a *Agy) GenerateContent(ctx context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		resp, err := a.generate(ctx, req)
		yield(resp, err)
	}
}

func (a *Agy) generate(ctx context.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
	if a.Bin == "" {
		return nil, fmt.Errorf("não encontrei o comando agy no PATH. Instale o Google Antigravity CLI e entre com `agy`")
	}
	decls := toolDecls(req)
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = DefaultAgyTimeout
	}
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dir := a.Dir
	if dir == "" {
		fresh, err := os.MkdirTemp("", "hivemind-agy-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(fresh)
		dir = fresh
	}

	images, _ := SaveRequestImages(dir, req)
	prompt := BuildAgyPromptWithImages(req, decls, images)

	args := []string{
		"--output-format", "json",
		"--input-format", "text",
		"--print-timeout", formatAgyTimeout(timeout),
	}
	if len(images) > 0 {
		args = append(args, "--add-dir", dir)
	}
	if a.Model != "" {
		args = append(args, "--model", a.Model)
	}
	if a.Effort != "" {
		args = append(args, "--effort", a.Effort)
	}
	schema, err := writeDecisionSchema()
	if err != nil {
		return nil, err
	}
	defer os.Remove(schema)
	args = append(args, "--json-schema", schema)

	var (
		stdout string
		stderr string
		code   int
	)
	leaseErr := WithAgyVaultLease(a.ProfileID, func() error {
		stdout, stderr, code, err = invokeAgyWithEnv(runCtx, a.Bin, dir, args, prompt, a.Env, a.IsolateEnv)
		if (code != 0 || err != nil) && strings.Contains(strings.ToLower(stderr+errString(err)), "empty conversation id") {
			select {
			case <-runCtx.Done():
			case <-time.After(2 * time.Second):
				stdout, stderr, code, err = invokeAgyWithEnv(runCtx, a.Bin, dir, args, prompt, a.Env, a.IsolateEnv)
			}
		}
		// Transient provider failures (429/quota, 5xx, network blips): the AGY_ERROR
		// envelope printed by agy carries "retryable":true (dogfooding achado #12) —
		// back off and retry instead of killing the whole multi-agent run.
		for attempt := 0; attempt < len(agyRetryDelays); attempt++ {
			if runCtx.Err() != nil {
				break
			}
			transient := parseAgyError(stderr)
			if transient == nil || !transient.Retryable {
				break
			}
			select {
			case <-runCtx.Done():
			case <-time.After(agyRetryDelays[attempt]):
				stdout, stderr, code, err = invokeAgyWithEnv(runCtx, a.Bin, dir, args, prompt, a.Env, a.IsolateEnv)
			}
		}
		return nil
	})
	if leaseErr != nil && err == nil {
		err = leaseErr
	}
	if runCtx.Err() != nil && ctx.Err() == nil {
		return nil, fmt.Errorf("agy excedeu o tempo (%s). Confira o login com `agy` ou aumente o contexto", formatAgyTimeout(timeout))
	}
	env, parseErr := parseAgyEnvelope([]byte(stdout))
	if code != 0 || (err != nil && !isBrokenPipe(err)) {
		detail := strings.TrimSpace(stderr)
		if env.Error != "" {
			detail = env.Error
		}
		if looksLikeAuth(detail) || looksLikeAuth(stderr) {
			return nil, fmt.Errorf("agy encerrou com código %d e não está autenticado. Abra um terminal, rode `agy` e entre com a conta Google Pro. %s", code, truncateText(detail, 500))
		}
		if errors.Is(err, exec.ErrNotFound) || os.IsNotExist(err) {
			return nil, fmt.Errorf("não foi possível executar o agy em %s: %s", a.Bin, err)
		}
		if quota := parseAgyError(stderr); quota != nil && (quota.ErrorCode == 429 || strings.EqualFold(quota.Status, "RESOURCE_EXHAUSTED")) {
			return nil, fmt.Errorf(
				"agy esgotou a quota/créditos (429 %s). Aguarde a janela de uso reabrir, fixe um modelo mais leve (ex.: agy:gemini-3.8-flash-low) ou troque de provedor. %s",
				quota.Status, truncateText(firstNonEmpty(env.Error, quota.ShortError), 300))
		}
		return nil, fmt.Errorf("agy encerrou com código %d: %s", code, truncateText(firstNonEmpty(detail, errString(err)), 500))
	}
	if parseErr != nil {
		return nil, fmt.Errorf("não consegui ler a resposta do agy: %w", parseErr)
	}
	if !strings.EqualFold(env.Status, "SUCCESS") && env.Status != "" {
		msg := env.Error
		if msg == "" {
			msg = env.Status
		}
		if looksLikeAuth(msg) {
			return nil, fmt.Errorf("agy não está autenticado. Abra um terminal, rode `agy` e entre com a conta Google Pro")
		}
		return nil, fmt.Errorf("agy devolveu status %s: %s", env.Status, truncateText(msg, 500))
	}
	return env.toResponse(true)
}

type agyEnvelope struct {
	Status     string          `json:"status"`
	Response   string          `json:"response"`
	Error      string          `json:"error"`
	Structured json.RawMessage `json:"structured_output"`
	Usage      struct {
		Input  int32 `json:"input_tokens"`
		Output int32 `json:"output_tokens"`
		Total  int32 `json:"total_tokens"`
	} `json:"usage"`
}

// agyRetryDelays backs off transient agy failures (429/quota, 5xx) that the
// AGY_ERROR envelope marks retryable. Package var so tests can speed it up.
var agyRetryDelays = []time.Duration{30 * time.Second, 60 * time.Second}

// agyErrorEnvelope is the JSON agy prints after the AGY_ERROR: marker on
// stderr when a provider call fails, e.g. quota exhaustion:
//
//	AGY_ERROR: {"short_error":"RESOURCE_EXHAUSTED (code 429): ...","status":"RESOURCE_EXHAUSTED","error_code":429,"code_kind":"http","retryable":true,"error_id":"..."}
type agyErrorEnvelope struct {
	ShortError string `json:"short_error"`
	Status     string `json:"status"`
	ErrorCode  int    `json:"error_code"`
	CodeKind   string `json:"code_kind"`
	Retryable  bool   `json:"retryable"`
	ErrorID    string `json:"error_id"`
}

// parseAgyError extracts the AGY_ERROR envelope from agy stderr, or nil.
func parseAgyError(stderr string) *agyErrorEnvelope {
	const marker = "AGY_ERROR:"
	idx := strings.Index(stderr, marker)
	if idx < 0 {
		return nil
	}
	raw := strings.TrimSpace(stderr[idx+len(marker):])
	if raw == "" {
		return nil
	}
	var env agyErrorEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err == nil {
		return &env
	}
	// The envelope may be followed by other log lines; retry with the first line only.
	if nl := strings.IndexByte(raw, '\n'); nl >= 0 {
		first := strings.TrimSpace(raw[:nl])
		if first != "" && json.Unmarshal([]byte(first), &env) == nil {
			return &env
		}
	}
	return nil
}

type agyDecision struct {
	Text      string        `json:"text"`
	ToolCalls []agyToolCall `json:"tool_calls"`
}

type agyToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments any    `json:"arguments"`
}

var agyCallSeq uint64

func (e agyEnvelope) toResponse(structured bool) (*model.LLMResponse, error) {
	text := strings.TrimSpace(e.Response)
	var calls []agyToolCall
	if structured {
		decision, err := decodeDecision(e.Structured, e.Response)
		if err != nil {
			return nil, fmt.Errorf("agy não devolveu a decisão JSON das ferramentas: %w", err)
		}
		text = strings.TrimSpace(decision.Text)
		calls = decision.ToolCalls
	}
	parts := make([]*genai.Part, 0, len(calls)+1)
	turnSeq := atomic.AddUint64(&agyCallSeq, 1)
	for i, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		// Always assign a globally unique FunctionCall.ID per turn and per call index.
		// Never reuse "agy-1" across turns or trust LLM-provided IDs: ADK v2 indexes
		// FunctionResponses by ID across the entire session history, so duplicate IDs
		// cause ADK to overwrite/swap tool outputs between turns and collapse parallel calls.
		id := fmt.Sprintf("agy-%d-%d", turnSeq, i+1)
		args, err := call.argumentMap()
		if err != nil {
			return nil, fmt.Errorf("argumentos da ferramenta %s: %w", name, err)
		}
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{
			ID:   id,
			Name: name,
			Args: args,
		}})
	}
	if text != "" || len(parts) == 0 {
		parts = append(parts, &genai.Part{Text: text})
	}
	if len(calls) == 0 && text == "" {
		return nil, fmt.Errorf("agy devolveu uma resposta vazia")
	}
	resp := &model.LLMResponse{
		Content:      &genai.Content{Role: "model", Parts: parts},
		TurnComplete: true,
	}
	if e.Usage.Total > 0 || e.Usage.Input > 0 || e.Usage.Output > 0 {
		resp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     e.Usage.Input,
			CandidatesTokenCount: e.Usage.Output,
			TotalTokenCount:      e.Usage.Total,
		}
	}
	return resp, nil
}

func (c agyToolCall) argumentMap() (map[string]any, error) {
	switch v := c.Arguments.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return v, nil
	case string:
		if strings.TrimSpace(v) == "" {
			return map[string]any{}, nil
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}

func decodeDecision(structured json.RawMessage, response string) (agyDecision, error) {
	var decision agyDecision
	if len(bytes.TrimSpace(structured)) > 0 && string(structured) != "null" {
		if err := json.Unmarshal(structured, &decision); err != nil {
			return agyDecision{}, err
		}
		return decision, nil
	}
	body := strings.TrimSpace(response)
	if body == "" {
		return agyDecision{}, fmt.Errorf("structured_output ausente")
	}
	if err := json.Unmarshal([]byte(body), &decision); err != nil {
		return agyDecision{}, err
	}
	return decision, nil
}

func parseAgyEnvelope(stdout []byte) (agyEnvelope, error) {
	raw := bytes.TrimSpace(stdout)
	if len(raw) == 0 {
		return agyEnvelope{}, fmt.Errorf("stdout vazio")
	}
	var env agyEnvelope
	if err := json.Unmarshal(raw, &env); err == nil && (env.Status != "" || env.Response != "" || len(env.Structured) > 0) {
		return env, nil
	}
	lines := bytes.Split(raw, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		if err := json.Unmarshal(line, &env); err == nil {
			return env, nil
		}
	}
	return agyEnvelope{}, fmt.Errorf("stdout não é o JSON do agy")
}

func writeDecisionSchema() (string, error) {
	schema := `{
  "type": "object",
  "properties": {
    "text": {"type": "string"},
    "tool_calls": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "name": {"type": "string"},
          "arguments": {"type": "object"}
        },
        "required": ["name"]
      }
    }
  },
  "required": ["text", "tool_calls"]
}`
	f, err := os.CreateTemp("", "hivemind-agy-schema-*.json")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(schema); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

type toolDecl struct {
	Name        string
	Description string
	Parameters  string
}

func toolDecls(req *model.LLMRequest) []toolDecl {
	if req == nil || req.Config == nil {
		return nil
	}
	var out []toolDecl
	for _, tool := range req.Config.Tools {
		if tool == nil {
			continue
		}
		for _, decl := range tool.FunctionDeclarations {
			if decl == nil || decl.Name == "" {
				continue
			}
			params := ""
			switch {
			case decl.ParametersJsonSchema != nil:
				if raw, err := json.Marshal(decl.ParametersJsonSchema); err == nil {
					params = string(raw)
				}
			case decl.Parameters != nil:
				if raw, err := json.Marshal(decl.Parameters); err == nil {
					params = string(raw)
				}
			}
			out = append(out, toolDecl{Name: decl.Name, Description: decl.Description, Parameters: params})
		}
	}
	return out
}

// SavedImage describes an inline image written to disk for multimodal inspection by agy.
type SavedImage struct {
	Path     string
	MIMEType string
}

// SaveRequestImages extracts image InlineData blobs from req and writes them into dir.
func SaveRequestImages(dir string, req *model.LLMRequest) ([]SavedImage, error) {
	if req == nil || dir == "" {
		return nil, nil
	}
	var saved []SavedImage
	seq := 0
	for _, c := range req.Contents {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			if p == nil || p.InlineData == nil || len(p.InlineData.Data) == 0 {
				continue
			}
			mime := strings.ToLower(strings.TrimSpace(p.InlineData.MIMEType))
			if !strings.HasPrefix(mime, "image/") {
				continue
			}
			seq++
			ext := ".png"
			switch mime {
			case "image/jpeg", "image/jpg":
				ext = ".jpg"
			case "image/webp":
				ext = ".webp"
			case "image/gif":
				ext = ".gif"
			}
			full := filepath.Join(dir, fmt.Sprintf("imagem_%d%s", seq, ext))
			if err := os.WriteFile(full, p.InlineData.Data, 0o600); err != nil {
				return saved, err
			}
			saved = append(saved, SavedImage{Path: full, MIMEType: mime})
		}
	}
	return saved, nil
}

func buildAgyPrompt(req *model.LLMRequest, decls []toolDecl) string {
	return BuildAgyPromptWithImages(req, decls, nil)
}

// BuildAgyPromptWithImages formats the headless prompt, referencing any saved image files.
func BuildAgyPromptWithImages(req *model.LLMRequest, decls []toolDecl, images []SavedImage) string {
	var b strings.Builder
	b.WriteString("Você é o modelo de um agente do HiveMind. Responda em português quando o pedido estiver em português.\n")
	if len(images) > 0 {
		b.WriteString("O usuário anexou imagem(ns)/print(s) nesta conversa. Você PODE usar apenas a ferramenta de leitura/visualização de arquivo nas imagens listadas abaixo para enxergar o conteúdo visual delas; não use write_to_file, run_command nem altere arquivos por fora.\n")
		for i, img := range images {
			fmt.Fprintf(&b, "- Imagem %d (%s): %s\n", i+1, img.MIMEType, img.Path)
		}
	} else {
		b.WriteString("Não use as ferramentas internas do Antigravity (write_to_file, run_command e as demais). ")
		b.WriteString("Elas não têm acesso ao projeto: este processo roda fora do repositório de propósito.\n")
	}
	if len(decls) > 0 {
		b.WriteString("A única forma de agir no projeto é devolver o JSON do schema, com tool_calls para as ferramentas do HiveMind listadas abaixo. ")
		b.WriteString("O HiveMind executa essas chamadas e devolve o resultado na próxima mensagem. ")
		b.WriteString("Se não precisar de ferramenta do HiveMind, deixe tool_calls vazio e escreva a resposta final em text.\n")
		b.WriteString("Não invente que uma ferramenta foi executada.\n\nFerramentas do HiveMind:\n")
		for _, decl := range decls {
			fmt.Fprintf(&b, "- %s: %s\n", decl.Name, strings.TrimSpace(decl.Description))
			if decl.Parameters != "" {
				fmt.Fprintf(&b, "  parâmetros: %s\n", truncateText(decl.Parameters, 4000))
			}
		}
	} else {
		b.WriteString("Não há ferramentas do HiveMind nesta etapa. Responda só com o texto final.\n")
	}
	if req != nil && req.Config != nil && req.Config.SystemInstruction != nil {
		if text := contentTextWithImages(req.Config.SystemInstruction, nil); text != "" {
			b.WriteString("\n[instrução do sistema]\n")
			b.WriteString(text)
			b.WriteByte('\n')
		}
	}
	b.WriteString("\n[conversa]\n")
	imgIdx := 0
	if req != nil {
		for _, content := range req.Contents {
			if content == nil {
				continue
			}
			role := content.Role
			if role == "" {
				role = "user"
			}
			fmt.Fprintf(&b, "(%s)\n%s\n", role, contentTextWithImageCursor(content, images, &imgIdx))
		}
	}
	return b.String()
}

func contentText(content *genai.Content) string {
	idx := 0
	return contentTextWithImageCursor(content, nil, &idx)
}

func contentTextWithImages(content *genai.Content, images []SavedImage) string {
	idx := 0
	return contentTextWithImageCursor(content, images, &idx)
}

func contentTextWithImageCursor(content *genai.Content, images []SavedImage, imgIdx *int) string {
	if content == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range content.Parts {
		if part == nil {
			continue
		}
		switch {
		case part.Text != "":
			b.WriteString(truncateText(part.Text, 32*1024))
			b.WriteByte('\n')
		case part.FunctionCall != nil:
			raw, _ := json.Marshal(part.FunctionCall.Args)
			fmt.Fprintf(&b, "chamada %s %s %s\n", part.FunctionCall.ID, part.FunctionCall.Name, truncateText(string(raw), 4000))
		case part.FunctionResponse != nil:
			raw, _ := json.Marshal(part.FunctionResponse.Response)
			fmt.Fprintf(&b, "resultado %s %s %s\n", part.FunctionResponse.ID, part.FunctionResponse.Name, truncateText(string(raw), 8000))
		case part.InlineData != nil:
			if imgIdx != nil && *imgIdx < len(images) {
				img := images[*imgIdx]
				*imgIdx++
				fmt.Fprintf(&b, "[imagem anexada pelo usuário: %s (%s) — inspecione esta imagem antes de responder]\n", img.Path, img.MIMEType)
			} else {
				b.WriteString("[conteúdo binário omitido]\n")
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func findAgyOnPath() string {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		for _, ext := range pathextList() {
			candidate := filepath.Join(dir, "agy"+ext)
			if fileExists(candidate) {
				return candidate
			}
		}
	}
	return ""
}

func pathextList() []string {
	raw := os.Getenv("PATHEXT")
	if raw == "" {
		if runtime.GOOS != "windows" {
			return []string{""}
		}
		raw = ".COM;.EXE;.BAT;.CMD"
	}
	out := []string{""}
	seen := map[string]bool{"": true}
	for _, ext := range strings.Split(raw, ";") {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		key := strings.ToLower(ext)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ext)
	}
	return out
}

func localAppDataAgy() string {
	root := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if root == "" {
		return ""
	}
	return filepath.Join(root, "agy", "bin", "agy.exe")
}

// invokeAgy starts agy and drains stdout and stderr before any stdin write.
// A broken pipe on that write is ignored: the process often exits first when
// it rejects the command, and the real exit code plus stderr are what matter.
func invokeAgy(ctx context.Context, bin, dir string, args []string, stdinText string) (string, string, int, error) {
	return invokeAgyWithEnv(ctx, bin, dir, args, stdinText, nil, false)
}

func invokeAgyWithEnv(ctx context.Context, bin, dir string, args []string, stdinText string, extraEnv map[string]string, isolate bool) (string, string, int, error) {
	cmd := agyCommand(ctx, bin, args)
	if dir != "" {
		cmd.Dir = dir
	}
	if isolate || len(extraEnv) > 0 {
		base := os.Environ()
		if isolate {
			base = stripInheritedSecrets(base)
		}
		cmd.Env = mergeEnv(base, extraEnv)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	var stdinPipe io.WriteCloser
	if stdinText != "" {
		var err error
		stdinPipe, err = cmd.StdinPipe()
		if err != nil {
			return "", "", -1, err
		}
	}
	if err := cmd.Start(); err != nil {
		return "", "", -1, err
	}
	if stdinPipe != nil {
		_, werr := io.Copy(stdinPipe, strings.NewReader(stdinText))
		cerr := stdinPipe.Close()
		if werr != nil && !isBrokenPipe(werr) {
			_ = cmd.Process.Kill()
		} else if cerr != nil && !isBrokenPipe(cerr) && werr == nil {
			_ = cmd.Process.Kill()
		}
	}
	waitErr := cmd.Wait()
	code := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			code = exitErr.ExitCode()
		} else if isBrokenPipe(waitErr) {
			code = 0
			waitErr = nil
		} else {
			code = -1
		}
	}
	return stdout.String(), stderr.String(), code, waitErr
}

// ProbeAgyPrompt runs `hivemind doctor --probe` through the same headless
// path as a real turn: UTF-8 prompt on stdin and --json-schema as a file.
// It honors AGY_MODEL/AGY_EFFORT (dogfooding achado #1: probing the agy
// default model produced a false "sem créditos" while slugs worked).
func ProbeAgyPrompt(ctx context.Context) (string, error) {
	bin, err := LookAgy()
	if err != nil {
		return "", err
	}
	// Doctor probes the CLI installed on the machine. It must not load an
	// org profile or that profile's CLOUDSDK_CONFIG.
	a := &Agy{Bin: bin, Timeout: 45 * time.Second}
	if slug := strings.TrimSpace(os.Getenv("AGY_MODEL")); slug != "" {
		a.Model = slug
	}
	if effort := strings.ToLower(strings.TrimSpace(os.Getenv("AGY_EFFORT"))); effort != "" {
		if !isAgyEffort(effort) {
			return "", fmt.Errorf("AGY_EFFORT %q é inválido; use low, medium, high, xhigh ou max", effort)
		}
		a.Effort = effort
	}
	probeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	resp, err := a.generate(probeCtx, &model.LLMRequest{
		Contents: []*genai.Content{{
			Role:  "user",
			Parts: []*genai.Part{{Text: "responda apenas OK"}},
		}},
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if resp != nil && resp.Content != nil {
		for _, part := range resp.Content.Parts {
			if part != nil && part.Text != "" {
				b.WriteString(part.Text)
			}
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func isBrokenPipe(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "broken pipe") || strings.Contains(msg, "pipe has been ended") || strings.Contains(msg, "pipe is being closed")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func agyCommand(ctx context.Context, bin string, args []string) *exec.Cmd {
	ext := strings.ToLower(filepath.Ext(bin))
	var cmd *exec.Cmd
	if ext == ".cmd" || ext == ".bat" {
		wrapped := make([]string, 0, len(args)+3)
		wrapped = append(wrapped, "/d", "/c", bin)
		wrapped = append(wrapped, args...)
		cmd = exec.CommandContext(ctx, "cmd.exe", wrapped...)
	} else {
		cmd = exec.CommandContext(ctx, bin, args...)
	}
	procx.ConfigureContext(cmd)
	return cmd
}

func formatAgyTimeout(d time.Duration) string {
	if d <= 0 {
		return "10m"
	}
	if d < time.Minute {
		seconds := int(d.Round(time.Second) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := int((d + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	return fmt.Sprintf("%dm", minutes)
}

func isAgyEffort(v string) bool {
	switch v {
	case "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func looksLikeAuth(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "auth") || strings.Contains(l, "login") || strings.Contains(l, "sign in") || strings.Contains(l, "credential") || strings.Contains(l, "not logged")
}

func truncateText(s string, n int) string {
	return strx.TruncateBytes(s, n, "…")
}

// AgyModel represents one model entry returned by `agy models`.
type AgyModel struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// ListAgyModels runs `agy models` and returns the parsed model catalog.
func ListAgyModels(ctx context.Context) ([]AgyModel, error) {
	bin, err := LookAgy()
	if err != nil {
		return nil, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := agyCommand(probeCtx, bin, []string{"models"})
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("agy models falhou: %s", detail)
	}
	return ParseAgyModelsOutput(stdout.String()), nil
}

// ParseAgyModelsOutput parses tab-separated `slug\tName` lines from `agy models`.
func ParseAgyModelsOutput(raw string) []AgyModel {
	var models []AgyModel
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(strings.ToLower(line), "fetching") {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		slug := strings.TrimSpace(parts[0])
		if slug == "" || strings.Contains(slug, " ") {
			continue
		}
		name := slug
		if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
			name = strings.TrimSpace(parts[1])
		}
		models = append(models, AgyModel{Slug: slug, Name: name})
	}
	return models
}

// ProbeAndListAgy runs `agy models` once (15s timeout) and returns both AgyStatus and parsed models.
func ProbeAndListAgy(ctx context.Context) (AgyStatus, []AgyModel) {
	path, err := LookAgy()
	if err != nil {
		return AgyStatus{Detail: "ausente do PATH. Instale o Antigravity CLI e entre com `agy`"}, nil
	}
	status := AgyStatus{Found: true, Path: path}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := agyCommand(probeCtx, path, []string{"models"})
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		status.LoggedIn = true
		status.Detail = "autenticado (`agy models` ok)"
		return status, ParseAgyModelsOutput(stdout.String())
	}
	if probeCtx.Err() != nil {
		status.Detail = "encontrado, mas `agy models` excedeu o tempo"
		return status, nil
	}
	if looksLikeAuth(stderr.String()) {
		status.Detail = "encontrado, sem login. Rode `agy` e entre com a conta Google Pro"
		return status, nil
	}
	status.Detail = "encontrado, mas `agy models` falhou"
	return status, nil
}
