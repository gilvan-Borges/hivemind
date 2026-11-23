package modelx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
)

const cursorInstallHint = "Instale o Cursor CLI oficial: no Windows, irm 'https://cursor.com/install?win32=true' | iex ; no Linux e no macOS, curl https://cursor.com/install -fsS | bash. Confirme com `agent --version`."

const defaultCursorTimeout = 15 * time.Minute

type agentDirKey struct{}

// WithAgentDir marks the workspace the Cursor CLI may edit for this call.
func WithAgentDir(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, agentDirKey{}, strings.TrimSpace(dir))
}

func agentDirFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	dir, _ := ctx.Value(agentDirKey{}).(string)
	return dir
}

// CursorWindowsSandboxWarning is shown in the IDE when the Cursor CLI has no
// sandbox on this OS. --trust is always passed: the current CLI refuses to start
// without it. --force stays off unless the mode is full_access.
const CursorWindowsSandboxWarning = "No Windows o Cursor CLI não tem sandbox. --trust vai em todo modo para o CLI aceitar a pasta. --force só entra em Full Acesso. Em Governado e Auto-Edição o CLI recusa edições que pediriam aprovação."

func cursorSandboxMode(goos string) string {
	if goos == "windows" {
		return "disabled"
	}
	return "enabled"
}

func cursorSandboxWarning(goos string) string {
	if goos == "windows" {
		return CursorWindowsSandboxWarning
	}
	return ""
}

// CursorStatus is safe to print. It never includes the API key.
type CursorStatus struct {
	Found          bool   `json:"found"`
	Path           string `json:"path"`
	HasKey         bool   `json:"hasKey"`
	Detail         string `json:"detail"`
	Sandbox        string `json:"sandbox,omitempty"`
	SandboxWarning string `json:"sandboxWarning,omitempty"`
}

func applyCursorSandboxStatus(st *CursorStatus) {
	if st == nil {
		return
	}
	st.Sandbox = cursorSandboxMode(runtime.GOOS)
	st.SandboxWarning = cursorSandboxWarning(runtime.GOOS)
	if st.SandboxWarning == "" {
		return
	}
	if strings.TrimSpace(st.Detail) == "" {
		st.Detail = st.SandboxWarning
		return
	}
	if !strings.Contains(st.Detail, st.SandboxWarning) {
		st.Detail = st.Detail + " " + st.SandboxWarning
	}
}

// Cursor is a model.LLM backed by the official Cursor CLI (`agent -p`).
// There is no chat-completions API. The CLI is a full agent: it edits the
// workspace itself. HiveMind only streams its text and tool notices. ADK does
// not re-run those tools.
type Cursor struct {
	Bin        string
	Model      string
	Dir        string
	Timeout    time.Duration
	Key        string
	Env        map[string]string
	IsolateEnv bool
	// PermMode is the team permission mode captured when the model was built.
	// full_access also passes --force. --trust is always added in BuildCursorArgs.
	PermMode string
}

// LookCursor finds the official CLI. HIVEMIND_CURSOR_AGENT_PATH, when set,
// is the only place searched, so a missing override does not fall through to
// whatever else is installed.
func LookCursor() (string, error) {
	if p := strings.TrimSpace(os.Getenv("HIVEMIND_CURSOR_AGENT_PATH")); p != "" {
		if fileExists(p) {
			return p, nil
		}
		return "", fmt.Errorf("HIVEMIND_CURSOR_AGENT_PATH não existe (%s). %s", p, cursorInstallHint)
	}
	for _, name := range []string{"agent", "cursor-agent"} {
		if p := findNamedOnPath(name); p != "" {
			return p, nil
		}
	}
	for _, p := range cursorWellKnown() {
		if fileExists(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("não encontrei o Cursor CLI (`agent`). %s", cursorInstallHint)
}

// ProbeCursor reports whether the CLI and a key are present. It does not run
// the agent and does not print the key.
func ProbeCursor() CursorStatus {
	key := strings.TrimSpace(resolveEnvForContext(context.Background(), "cursor", "CURSOR_API_KEY"))
	st := CursorStatus{HasKey: key != ""}
	path, err := LookCursor()
	if err != nil {
		st.Detail = err.Error()
		applyCursorSandboxStatus(&st)
		return st
	}
	st.Found = true
	st.Path = path
	if st.HasKey {
		st.Detail = "CLI encontrado e CURSOR_API_KEY definida (Pool de Contas, ambiente ou api-keys.json)"
	} else {
		st.Detail = "CLI encontrado, mas falta CURSOR_API_KEY. Cole a chave do painel Cursor em Chaves de API, ou exporte a variável. O modo headless não usa a sessão interativa do editor."
	}
	applyCursorSandboxStatus(&st)
	return st
}

// ResolveCursorAPIKey returns CURSOR_API_KEY from environment or saved profile.
func ResolveCursorAPIKey(ctx context.Context) string {
	return resolveEnvForContext(ctx, "cursor", "CURSOR_API_KEY")
}

// NewCursor builds the adapter. The key is read from the environment first,
// then from api-keys.json. The model id "auto" lets the Cursor CLI choose.
func NewCursor(ctx context.Context, modelName string) (model.LLM, error) {
	prof, mode, _ := ResolveProfile(ctx, "cursor")
	if mode == "isolated" {
		return nil, fmt.Errorf("nenhuma conta Cursor vinculada a esta organização")
	}
	modelID := cursorModelID(modelName)
	if err := rejectForeignCursorModel(modelID); err != nil {
		return nil, err
	}
	key := resolveEnvForContext(ctx, "cursor", "CURSOR_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("defina CURSOR_API_KEY para usar o Cursor (painel Cursor → API Keys). A variável de ambiente tem prioridade sobre a chave salva na IDE")
	}
	bin, err := LookCursor()
	if err != nil {
		return nil, err
	}
	var envMap map[string]string
	isolate := false
	if prof != nil {
		isolate = len(prof.OrgPatterns) > 0
		if len(prof.Env) > 0 {
			envMap = prof.Env
		}
	}
	return &Cursor{
		Bin:        bin,
		Model:      modelID,
		Dir:        agentDirFrom(ctx),
		Key:        key,
		Env:        envMap,
		IsolateEnv: isolate,
		PermMode:   permissionModeFrom(ctx),
	}, nil
}

func (c *Cursor) Name() string {
	modelID := c.Model
	if modelID == "" {
		modelID = "auto"
	}
	return "cursor:" + modelID
}

// GenerateContent runs one headless agent turn in the workspace.
func (c *Cursor) GenerateContent(ctx context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		resp, err := c.generate(ctx, req, func(text string) bool {
			return yield(&model.LLMResponse{
				Partial: true,
				Content: genai.NewContentFromText(text, genai.RoleModel),
			}, nil)
		})
		if err != nil {
			yield(nil, err)
			return
		}
		yield(resp, nil)
	}
}

func (c *Cursor) generate(ctx context.Context, req *model.LLMRequest, emit func(string) bool) (*model.LLMResponse, error) {
	if err := rejectForeignCursorModel(c.Model); err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.Key) == "" {
		return nil, fmt.Errorf("defina CURSOR_API_KEY para usar o Cursor")
	}
	bin := c.Bin
	if bin == "" {
		var err error
		bin, err = LookCursor()
		if err != nil {
			return nil, err
		}
	}
	dir := strings.TrimSpace(c.Dir)
	if dir == "" {
		dir = agentDirFrom(ctx)
	}
	if dir == "" {
		return nil, fmt.Errorf("o agente Cursor precisa de um diretório de trabalho")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultCursorTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	imgDir := filepath.Join(dir, ".hivemind", "cursor-images")
	if err := os.MkdirAll(imgDir, 0o755); err != nil {
		return nil, err
	}
	images, err := SaveRequestImages(imgDir, req)
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, img := range images {
			_ = os.Remove(img.Path)
		}
		_ = os.Remove(imgDir)
	}()
	prompt := buildCursorPrompt(req, images)
	prompt = redactSecret(prompt, c.Key)
	mode := permissionModeFrom(ctx)
	if mode == "" {
		mode = c.PermMode
	}
	if cursorSandboxMode(runtime.GOOS) == "disabled" && !cursorTrustForce(mode) {
		prompt += "\nNeste Windows o CLI está sem sandbox e sem --force. Uma edição que exigiria aprovação é recusada. Não contorne isso.\n"
	}
	taskPath, err := writeCursorTask(dir, prompt)
	if err != nil {
		return nil, err
	}
	defer os.Remove(taskPath)

	args := cursorArgs(c.Model, dir, mode)
	cmd := cliCommand(runCtx, bin, args)
	cmd.Dir = dir
	baseEnv := os.Environ()
	if c.IsolateEnv {
		baseEnv = stripInheritedSecrets(baseEnv)
	}
	cmd.Env = mergeEnv(cursorChildEnv(baseEnv, c.Key), c.Env)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &limitWriter{w: &stderr, n: 8 * 1024}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("não foi possível executar o Cursor CLI: %s", redactSecret(err.Error(), c.Key))
	}

	var final string
	var sawResult bool
	var failed bool
	var transcript cursorTranscript
	// Lê até o EOF, inclusive linhas maiores que o limite: um tool_call com o
	// corpo do arquivo pode passar de 4 MiB. Parar o scanner sem drenar enche
	// o pipe e o CLI trava até o timeout.
	readErr := readCLIStream(stdout, maxCursorLine, func(line string) bool {
		notice, ok := parseCursorStreamLine(line)
		if !ok {
			return true
		}
		notice.Text = redactSecret(notice.Text, c.Key)
		notice.ToolPath = redactSecret(notice.ToolPath, c.Key)
		if notice.Final {
			final = notice.Text
			sawResult = true
			failed = notice.Failed
			return true
		}
		chunk := transcript.absorb(notice, dir)
		if chunk != "" && !emit(chunk) {
			return false
		}
		return true
	})
	if readErr != nil {
		cancel()
		_ = cmd.Wait()
		if errors.Is(readErr, errStopCLIStream) {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("leitura do Cursor CLI: %s", redactSecret(readErr.Error(), c.Key))
	}
	// Wait só depois de esvaziar stdout. Chamar Wait em paralelo fecha o pipe
	// e, num processo rápido, perde a última linha (o evento result).
	err = cmd.Wait()
	if runCtx.Err() != nil && ctx.Err() == nil {
		return nil, fmt.Errorf("o Cursor CLI excedeu o tempo (%s)", timeout.Truncate(time.Second))
	}
	modelShown := c.Model
	if strings.TrimSpace(modelShown) == "" {
		modelShown = "auto"
	}
	if failed {
		detail := truncateText(redactSecret(firstNonEmpty(final, stderr.String()), c.Key), 500)
		return nil, fmt.Errorf("%s", FormatUpstreamError("cursor", modelShown, fmt.Errorf("o Cursor CLI terminou com erro: %s", detail), c.Key))
	}
	if err != nil {
		detail := redactSecret(strings.TrimSpace(stderr.String()), c.Key)
		return nil, fmt.Errorf("%s", FormatUpstreamError("cursor", modelShown, fmt.Errorf("o Cursor CLI falhou: %s", truncateText(firstNonEmpty(detail, redactSecret(err.Error(), c.Key)), 500)), c.Key))
	}
	if !sawResult {
		final = redactSecret(strings.TrimSpace(stderr.String()), c.Key)
	}
	body := transcript.finish(final)
	return &model.LLMResponse{
		Content:      genai.NewContentFromText(body, genai.RoleModel),
		TurnComplete: true,
	}, nil
}

func cursorModelID(modelName string) string {
	raw := strings.TrimSpace(modelName)
	low := strings.ToLower(raw)
	for _, prefix := range []string{"cursor:", "cursor/"} {
		if strings.HasPrefix(low, prefix) {
			raw = strings.TrimSpace(raw[len(prefix):])
			low = strings.ToLower(raw)
			break
		}
	}
	if raw == "" || low == "cursor" || low == "auto" {
		return "auto"
	}
	return raw
}

// foreignCursorModel reports an id that belongs to another provider. The Jev
// route text is "openrouter/<vendor>/<model>"; that string must not become
// `agent --model`. Cursor's API then answers with the OpenAI SDK summary
// "OpenAI API error: 400 Bad Request" and drops the body.
func foreignCursorModel(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	low := strings.ToLower(id)
	if low == "auto" || low == "cursor" {
		return false
	}
	return strings.Contains(id, "/") || strings.HasPrefix(low, "openrouter")
}

func rejectForeignCursorModel(id string) error {
	if !foreignCursorModel(id) {
		return nil
	}
	return fmt.Errorf("cursor/%s: modelo incompatível com o provedor Cursor (id de outro provedor não é enviado ao CLI): OpenAI API error: 400 Bad Request: model %q is not a Cursor model", id, id)
}

func cursorArgs(modelID, workspace, permissionMode string) []string {
	return BuildCursorArgs(modelID, workspace, permissionMode, runtime.GOOS)
}

// BuildCursorArgs builds the CLI argument list for Cursor `agent`.
// --trust is required by the current CLI before it will run in a folder.
// --force is only added in full_access.
func BuildCursorArgs(modelID, workspace, permissionMode, goos string) []string {
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--stream-partial-output",
		"--trust",
	}
	if CursorTrustForce(permissionMode) {
		args = append(args, "--force")
	}
	args = append(args,
		"--sandbox", cursorSandboxMode(goos),
		"--workspace", workspace,
		"Siga exatamente o arquivo .hivemind/cursor-task.md neste workspace. Resuma em português ao terminar.",
	)
	if modelID != "" && modelID != "auto" {
		args = append([]string{"--model", modelID}, args...)
	}
	return args
}

// CursorTrustForce reports whether permissionMode grants automatic trust/force flags.
func CursorTrustForce(permissionMode string) bool {
	return strings.EqualFold(strings.TrimSpace(permissionMode), "full_access")
}

func buildCursorArgs(modelID, workspace, permissionMode, goos string) []string {
	return BuildCursorArgs(modelID, workspace, permissionMode, goos)
}

func cursorTrustForce(permissionMode string) bool {
	return CursorTrustForce(permissionMode)
}

type permissionModeKey struct{}

// WithPermissionMode marks the team permission mode for this call.
// full_access is what adds --force. --trust is applied for every mode.
func WithPermissionMode(ctx context.Context, mode string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, permissionModeKey{}, strings.TrimSpace(mode))
}

func permissionModeFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	mode, _ := ctx.Value(permissionModeKey{}).(string)
	return strings.TrimSpace(mode)
}

// CursorChildEnv returns a child environment with CURSOR_API_KEY set if non-empty.
func CursorChildEnv(base []string, key string) []string {
	return cursorChildEnv(base, key)
}

func cursorChildEnv(base []string, key string) []string {
	out := make([]string, 0, len(base)+1)
	for _, entry := range base {
		name, _, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(name, "CURSOR_API_KEY") {
			continue
		}
		out = append(out, entry)
	}
	if strings.TrimSpace(key) == "" {
		return out
	}
	return append(out, "CURSOR_API_KEY="+key)
}

// WriteCursorTask writes prompt into <dir>/.hivemind/cursor-task.md with mode 0600.
func WriteCursorTask(dir, prompt string) (string, error) {
	return writeCursorTask(dir, prompt)
}

func writeCursorTask(dir, prompt string) (string, error) {
	folder := filepath.Join(dir, ".hivemind")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(folder, "cursor-task.md")
	if err := os.WriteFile(path, []byte(prompt), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func buildCursorPrompt(req *model.LLMRequest, images []SavedImage) string {
	var b strings.Builder
	b.WriteString("Você é o agente Cursor chamado pelo HiveMind, via CLI oficial em modo headless.\n")
	b.WriteString("Trabalhe só neste workspace.\n")
	b.WriteString("Se a instrução do time (Chefe/Arquiteto) pedir commit, push ou PR, faça. Dev e QA não devem commit/push/PR.\n")
	b.WriteString("Se a instrução disser que você não pode gravar código, não altere arquivos.\n")
	b.WriteString("O modo headless não pergunta nada: o que exigiria aprovação é recusado pelo CLI.\n")
	b.WriteString("Termine com um resumo curto em português do que fez ou do que ficou bloqueado.\n")
	if req != nil && req.Config != nil && req.Config.SystemInstruction != nil {
		if text := contentText(req.Config.SystemInstruction); text != "" {
			b.WriteString("\n[instrução do sistema]\n")
			b.WriteString(text)
			b.WriteByte('\n')
		}
	}
	if len(images) > 0 {
		b.WriteString("\nImagens anexadas pelo usuário. Leia estes arquivos antes de responder:\n")
		for _, img := range images {
			fmt.Fprintf(&b, "- %s (%s)\n", img.Path, img.MIMEType)
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

// ListCursorModels asks the CLI which models this account can use.
// "auto" is always first. A missing CLI returns only "auto" plus the install hint.
func ListCursorModels(ctx context.Context) ([]AgyModel, CursorStatus, error) {
	status := ProbeCursor()
	fallback := []AgyModel{{Slug: "auto", Name: "auto (a Cursor escolhe)"}}
	if !status.Found {
		return fallback, status, nil
	}
	if !status.HasKey {
		status.Detail = "CLI encontrado, sem CURSOR_API_KEY. A lista fica só em auto até a chave existir."
		return fallback, status, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cursorKey := resolveEnvForContext(ctx, "cursor", "CURSOR_API_KEY")
	cmd := cliCommand(probeCtx, status.Path, []string{"models"})
	cmd.Env = cursorChildEnv(os.Environ(), cursorKey)
	out, err := cmd.Output()
	if err != nil {
		status.Detail = "CLI encontrado, mas `agent models` falhou. Usando auto. " + truncateText(redactSecret(err.Error(), cursorKey), 200)
		return fallback, status, nil
	}
	ids := parseCursorModelList(string(out))
	if len(ids) == 0 {
		return fallback, status, nil
	}
	models := make([]AgyModel, 0, len(ids)+1)
	seen := map[string]bool{}
	models = append(models, fallback[0])
	seen["auto"] = true
	for _, id := range ids {
		if seen[strings.ToLower(id)] {
			continue
		}
		seen[strings.ToLower(id)] = true
		models = append(models, AgyModel{Slug: id, Name: id})
	}
	return models, status, nil
}

func parseCursorModelList(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if strings.HasPrefix(text, "[") {
		var ids []string
		if json.Unmarshal([]byte(text), &ids) == nil {
			return compactIDs(ids)
		}
		var objs []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Slug string `json:"slug"`
		}
		if json.Unmarshal([]byte(text), &objs) == nil {
			out := make([]string, 0, len(objs))
			for _, obj := range objs {
				id := firstNonEmpty(obj.ID, obj.Slug, obj.Name)
				if id != "" {
					out = append(out, id)
				}
			}
			return compactIDs(out)
		}
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		lowLine := strings.ToLower(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(lowLine, "available") || strings.HasPrefix(lowLine, "tip:") || strings.HasPrefix(lowLine, "note:") || strings.HasPrefix(lowLine, "usage:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		id := strings.Trim(fields[0], "-*")
		if id == "" || strings.HasSuffix(id, ":") || strings.EqualFold(id, "model") || strings.EqualFold(id, "models") {
			continue
		}
		out = append(out, id)
	}
	return compactIDs(out)
}

func compactIDs(ids []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[strings.ToLower(id)] {
			continue
		}
		seen[strings.ToLower(id)] = true
		out = append(out, id)
	}
	return out
}

func findNamedOnPath(name string) string {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		for _, ext := range pathextList() {
			candidate := filepath.Join(dir, name+ext)
			if fileExists(candidate) {
				return candidate
			}
		}
	}
	return ""
}

func cursorWellKnown() []string {
	var out []string
	if root := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); root != "" {
		out = append(out,
			filepath.Join(root, "cursor-agent", "agent.exe"),
			filepath.Join(root, "cursor-agent", "cursor-agent.exe"),
			filepath.Join(root, "cursor-agent", "agent.cmd"),
			filepath.Join(root, "cursor-agent", "cursor-agent.cmd"),
		)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out,
			filepath.Join(home, ".local", "bin", "agent"),
			filepath.Join(home, ".local", "bin", "cursor-agent"),
			filepath.Join(home, ".cursor", "bin", "agent"),
		)
	}
	return out
}

// readCursorStream/limitWriter vivem agora em cli_exec.go (leitor NDJSON
// único compartilhado com o agy — Spec 06 §3.1).

const maxCursorLine = maxCLILine
