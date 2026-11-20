package modelx

import (
	"bufio"
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
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/procx"
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

// CursorStatus is safe to print. It never includes the API key.
type CursorStatus struct {
	Found  bool   `json:"found"`
	Path   string `json:"path"`
	HasKey bool   `json:"hasKey"`
	Detail string `json:"detail"`
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
		return st
	}
	st.Found = true
	st.Path = path
	if st.HasKey {
		st.Detail = "CLI encontrado e CURSOR_API_KEY definida (Pool de Contas, ambiente ou api-keys.json)"
	} else {
		st.Detail = "CLI encontrado, mas falta CURSOR_API_KEY. Cole a chave do painel Cursor em Chaves de API, ou exporte a variável. O modo headless não usa a sessão interativa do editor."
	}
	return st
}

// NewCursor builds the adapter. The key is read from the environment first,
// then from api-keys.json. The model id "auto" lets the Cursor CLI choose.
func NewCursor(ctx context.Context, modelName string) (model.LLM, error) {
	prof, mode, _ := ResolveProfile(ctx, "cursor")
	if mode == "isolated" {
		return nil, fmt.Errorf("nenhuma conta Cursor vinculada a esta organização")
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
		Model:      cursorModelID(modelName),
		Dir:        agentDirFrom(ctx),
		Key:        key,
		Env:        envMap,
		IsolateEnv: isolate,
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
	taskPath, err := writeCursorTask(dir, prompt)
	if err != nil {
		return nil, err
	}
	defer os.Remove(taskPath)

	args := cursorArgs(c.Model, dir)
	cmd := cursorCommand(runCtx, bin, args)
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
	procx.ConfigureContext(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("não foi possível executar o Cursor CLI: %s", redactSecret(err.Error(), c.Key))
	}

	var final string
	var sawResult bool
	var failed bool
	// Lê até o EOF, inclusive linhas maiores que o limite: um tool_call com o
	// corpo do arquivo pode passar de 4 MiB. Parar o scanner sem drenar enche
	// o pipe e o CLI trava até o timeout.
	readErr := readCursorStream(stdout, maxCursorLine, func(line string) bool {
		notice, ok := parseCursorStreamLine(line)
		if !ok {
			return true
		}
		notice.Text = redactSecret(notice.Text, c.Key)
		if notice.Final {
			final = notice.Text
			sawResult = true
			failed = notice.Failed
			return true
		}
		if notice.Text != "" && !emit(notice.Text) {
			return false
		}
		return true
	})
	if readErr != nil {
		cancel()
		_ = cmd.Wait()
		if errors.Is(readErr, errStopStream) {
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
	if failed {
		return nil, fmt.Errorf("o Cursor CLI terminou com erro: %s", truncateText(redactSecret(firstNonEmpty(final, stderr.String()), c.Key), 500))
	}
	if err != nil {
		detail := redactSecret(strings.TrimSpace(stderr.String()), c.Key)
		return nil, fmt.Errorf("o Cursor CLI falhou: %s", truncateText(firstNonEmpty(detail, redactSecret(err.Error(), c.Key)), 500))
	}
	if !sawResult {
		final = redactSecret(strings.TrimSpace(stderr.String()), c.Key)
	}
	if strings.TrimSpace(final) == "" {
		final = "O agente Cursor terminou sem texto. As alterações, se houver, estão no workspace deste agente."
	}
	return &model.LLMResponse{
		Content:      genai.NewContentFromText(final, genai.RoleModel),
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

func cursorArgs(modelID, workspace string) []string {
	sandboxMode := "enabled"
	if runtime.GOOS == "windows" {
		// Cursor CLI sandbox mode requires macOS or Linux and errors immediately on Windows
		// ("Error: Sandbox mode is enabled but not available on this system").
		sandboxMode = "disabled"
	}
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--stream-partial-output",
		"--trust",
		"--force",
		"--sandbox", sandboxMode,
		"--workspace", workspace,
		"Siga exatamente o arquivo .hivemind/cursor-task.md neste workspace. Resuma em português ao terminar.",
	}
	if modelID != "" && modelID != "auto" {
		args = append([]string{"--model", modelID}, args...)
	}
	return args
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
	return append(out, "CURSOR_API_KEY="+key)
}

func cursorCommand(ctx context.Context, bin string, args []string) *exec.Cmd {
	ext := strings.ToLower(filepath.Ext(bin))
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" && (ext == ".cmd" || ext == ".bat") {
		cmd = exec.CommandContext(ctx, "cmd.exe", append([]string{"/d", "/c", bin}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, bin, args...)
	}
	procx.ConfigureContext(cmd)
	return cmd
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
	cmd := cursorCommand(probeCtx, status.Path, []string{"models"})
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
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(strings.ToLower(line), "available") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		id := strings.Trim(fields[0], "-*")
		if id == "" || strings.EqualFold(id, "model") || strings.EqualFold(id, "models") {
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

const maxCursorLine = 1024 * 1024

var errStopStream = errors.New("stream interrompido")

// readCursorStream lê até o EOF. Uma linha maior que max é descartada, mas o
// restante do pipe continua sendo lido para o processo não bloquear.
func readCursorStream(r io.Reader, max int, handle func(line string) bool) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var buf []byte
	skipping := false
	for {
		chunk, err := br.ReadSlice('\n')
		if skipping {
			if err == bufio.ErrBufferFull {
				continue
			}
			skipping = false
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			continue
		}
		if err == bufio.ErrBufferFull {
			if len(buf)+len(chunk) > max {
				buf = nil
				skipping = true
				continue
			}
			buf = append(buf, chunk...)
			continue
		}
		if len(chunk) > 0 {
			buf = append(buf, chunk...)
		}
		if len(buf) > 0 && len(buf) <= max {
			line := string(buf)
			buf = nil
			if !handle(line) {
				return errStopStream
			}
		} else {
			buf = nil
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	orig := len(p)
	if l.n <= 0 || orig == 0 {
		return orig, nil
	}
	write := p
	if len(write) > l.n {
		write = write[:l.n]
	}
	n, err := l.w.Write(write)
	l.n -= n
	if err != nil || n < len(write) {
		return n, err
	}
	return orig, nil
}
