package modelx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/jev"
)

// StreamEvent is one headless agy event. Deltas arrive before the final result.
type StreamEvent struct {
	Type     string // delta, tool, result
	Text     string
	ToolName string
	ToolArgs string
	ToolOut  string
	Tokens   int
}

// Stream runs agy with --output-format stream-json. The prompt is UTF-8 on
// stdin and --json-schema is a temp file, same as a normal turn.
func (a *Agy) Stream(ctx context.Context, prompt string, fn func(StreamEvent)) error {
	if a == nil {
		a = &Agy{}
	}
	if a.Bin == "" {
		found, err := LookAgy()
		if err != nil {
			return err
		}
		a = &Agy{Bin: found, Model: a.Model, Effort: a.Effort, Timeout: a.Timeout, Dir: a.Dir}
	}
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
	schema, err := writeDecisionSchema()
	if err != nil {
		return err
	}
	defer os.Remove(schema)
	args := []string{
		"--output-format", "stream-json",
		"--input-format", "text",
		"--print-timeout", formatAgyTimeout(timeout),
		"--json-schema", schema,
	}
	if a.Model != "" {
		args = append(args, "--model", a.Model)
	}
	if a.Effort != "" {
		args = append(args, "--effort", a.Effort)
	}
	dir := a.Dir
	if dir == "" {
		fresh, err := os.MkdirTemp("", "hivemind-agy-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(fresh)
		dir = fresh
	}
	return streamInvoke(runCtx, a.Bin, dir, args, prompt, fn)
}

func streamInvoke(ctx context.Context, bin, dir string, args []string, prompt string, fn func(StreamEvent)) error {
	cmd := cliCommand(ctx, bin, args)
	if dir != "" {
		cmd.Dir = dir
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	streamDone := make(chan error, 1)
	go func() {
		// Leitor NDJSON único (cli_exec.go): drena linhas maiores que o limite
		// sem travar o pipe — mesmo comportamento robusto do Cursor CLI.
		streamDone <- readCLIStream(stdout, maxCLILine, func(line string) bool {
			ev, ok := parseStreamLine([]byte(line))
			if ok && fn != nil {
				fn(ev)
			}
			return true
		})
	}()
	_, werr := io.Copy(stdin, strings.NewReader(prompt))
	cerr := stdin.Close()
	if werr != nil && !isBrokenPipe(werr) {
		_ = cmd.Process.Kill()
	} else if cerr != nil && !isBrokenPipe(cerr) && werr == nil {
		_ = cmd.Process.Kill()
	}
	// Read stdout to EOF before Wait: cmd.Wait closes the StdoutPipe, and
	// calling it first can drop events the process already wrote (flaky when
	// the child is fast, e.g. the Go test fake). darwin/Linux pipes deliver
	// EOF when the process exits, so this cannot deadlock.
	<-streamDone
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if waitErr != nil && !isBrokenPipe(waitErr) {
		code := -1
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			code = ee.ExitCode()
		}
		detail := strings.TrimSpace(stderr.String())
		if looksLikeAuth(detail) {
			return errAuth(code, detail)
		}
		return errExit(code, firstNonEmpty(detail, waitErr.Error()))
	}
	return nil
}

func parseStreamLine(line []byte) (StreamEvent, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return StreamEvent{}, false
	}
	var msg struct {
		Event      string `json:"event"`
		StepUpdate struct {
			State     string `json:"state"`
			StepType  string `json:"step_type"`
			TextDelta string `json:"text_delta"`
			ToolName  string `json:"tool_name"`
			ToolInfo  struct {
				Name       string         `json:"name"`
				Parameters map[string]any `json:"parameters"`
				Output     string         `json:"output"`
			} `json:"tool_info"`
		} `json:"step_update"`
		Result agyEnvelope `json:"result"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return StreamEvent{}, false
	}
	switch msg.Event {
	case "step_update":
		if msg.StepUpdate.TextDelta != "" {
			return StreamEvent{Type: "delta", Text: msg.StepUpdate.TextDelta}, true
		}
		if msg.StepUpdate.StepType == "tool" || msg.StepUpdate.ToolName != "" || msg.StepUpdate.ToolInfo.Name != "" {
			name := msg.StepUpdate.ToolName
			if name == "" {
				name = msg.StepUpdate.ToolInfo.Name
			}
			raw, _ := json.Marshal(msg.StepUpdate.ToolInfo.Parameters)
			return StreamEvent{Type: "tool", ToolName: name, ToolArgs: string(raw), ToolOut: msg.StepUpdate.ToolInfo.Output}, true
		}
		return StreamEvent{}, false
	case "result":
		text := strings.TrimSpace(msg.Result.Response)
		if len(msg.Result.Structured) > 0 {
			if decision, err := decodeDecision(msg.Result.Structured, msg.Result.Response); err == nil && strings.TrimSpace(decision.Text) != "" {
				text = strings.TrimSpace(decision.Text)
			}
		}
		return StreamEvent{Type: "result", Text: text, Tokens: int(msg.Result.Usage.Total)}, true
	case "", "init":
		if msg.Event == "" && (msg.Result.Response != "" || msg.Result.Status != "") {
			return StreamEvent{Type: "result", Text: strings.TrimSpace(msg.Result.Response)}, true
		}
		return StreamEvent{}, false
	default:
		return StreamEvent{}, false
	}
}

func errAuth(code int, detail string) error {
	return errExit(code, "não autenticado. Rode `agy` e entre com a conta Google Pro. "+jev.RedactSecrets(detail))
}

func errExit(code int, detail string) error {
	// Redação única na borda do CLI (Spec 06 §3.2): stderr do processo filho
	// pode carregar tokens ya29./oc_ que o envelope JSON não mascara.
	return &streamError{code: code, detail: truncateText(jev.RedactSecrets(detail), 500)}
}

// invokeAgyStreamWithEnv é o invoker de streaming do agy: mescla env/isolação
// e lease do vault igual ao invokeAgyWithEnv, mas entrega cada linha NDJSON ao
// handler DURANTE o processo em vez de coletar stdout no final.
func invokeAgyStreamWithEnv(ctx context.Context, bin, dir string, args []string, stdinText string, extraEnv map[string]string, isolate bool, handleLine func(line string) bool) (string, int, error) {
	cmd := cliCommand(ctx, bin, args)
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
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", -1, err
	}
	var stderr strings.Builder
	cmd.Stderr = &limitWriter{w: &stderr, n: 16 * 1024}
	var stdinPipe io.WriteCloser
	if stdinText != "" {
		stdinPipe, err = cmd.StdinPipe()
		if err != nil {
			return "", -1, err
		}
	}
	if err := cmd.Start(); err != nil {
		return stderr.String(), -1, err
	}
	streamDone := make(chan error, 1)
	go func() {
		streamDone <- readCLIStream(stdout, maxCLILine, handleLine)
	}()
	if stdinPipe != nil {
		_, werr := io.Copy(stdinPipe, strings.NewReader(stdinText))
		cerr := stdinPipe.Close()
		if werr != nil && !isBrokenPipe(werr) {
			_ = cmd.Process.Kill()
		} else if cerr != nil && !isBrokenPipe(cerr) && werr == nil {
			_ = cmd.Process.Kill()
		}
	}
	// Drenar stdout ANTES do Wait (mesma regra do invokeAgyWithEnv).
	rdErr := <-streamDone
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return stderr.String(), -1, ctx.Err()
	}
	if errors.Is(rdErr, errStopCLIStream) {
		return stderr.String(), -1, ctx.Err()
	}
	if rdErr != nil {
		return stderr.String(), -1, rdErr
	}
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
	return stderr.String(), code, waitErr
}

// generateStreaming runs agy with --output-format stream-json through the same
// vault-lease/env path as generate, yielding Partial deltas (text + tool
// markers) and returning the final response built from the result envelope
// (Spec 06 §3.1: "Agy.GenerateContent(..., stream=true) emitir deltas").
func (a *Agy) generateStreaming(ctx context.Context, req *model.LLMRequest, yield func(*model.LLMResponse, error) bool) (*model.LLMResponse, error) {
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
		"--output-format", "stream-json",
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
		finalEnv  agyEnvelope
		sawResult bool
	)
	handleLine := func(line string) bool {
		ev, ok := parseStreamLine([]byte(line))
		if !ok {
			return true
		}
		switch ev.Type {
		case "delta":
			if ev.Text != "" {
				yield(&model.LLMResponse{
					Partial: true,
					Content: genai.NewContentFromText(ev.Text, genai.RoleModel),
				}, nil)
			}
		case "tool":
			if ev.ToolName != "" {
				marker := formatCursorToolMarker(ev.ToolName, ev.ToolOut)
				yield(&model.LLMResponse{
					Partial: true,
					Content: genai.NewContentFromText(marker, genai.RoleModel),
				}, nil)
			}
		case "result":
			sawResult = true
			// O envelope completo (tool_calls + usage) mora na linha result.
			var msg struct {
				Result agyEnvelope `json:"result"`
			}
			if json.Unmarshal([]byte(line), &msg) == nil {
				finalEnv = msg.Result
			}
		}
		return true
	}

	var stderrText string
	var code int
	var runErr error
	leaseErr := WithAgyVaultLease(a.ProfileID, func() error {
		stderrText, code, runErr = invokeAgyStreamWithEnv(runCtx, a.Bin, dir, args, prompt, a.Env, a.IsolateEnv, handleLine)
		return nil
	})
	if leaseErr != nil && runErr == nil {
		runErr = leaseErr
	}
	if runCtx.Err() != nil && ctx.Err() == nil {
		return nil, fmt.Errorf("agy excedeu o tempo (%s). Confira o login com `agy` ou aumente o contexto", formatAgyTimeout(timeout))
	}
	if code != 0 || runErr != nil {
		detail := strings.TrimSpace(stderrText)
		if finalEnv.Error != "" {
			detail = finalEnv.Error
		}
		if looksLikeAuth(detail) || looksLikeAuth(stderrText) {
			return nil, fmt.Errorf("agy encerrou com código %d e não está autenticado. Abra um terminal, rode `agy` e entre com a conta Google Pro. %s", code, truncateText(detail, 500))
		}
		if quota := parseAgyError(stderrText); quota != nil && (quota.ErrorCode == 429 || strings.EqualFold(quota.Status, "RESOURCE_EXHAUSTED")) {
			return nil, fmt.Errorf(
				"agy esgotou a quota/créditos (429 %s). Aguarde a janela de uso reabrir, fixe um modelo mais leve (ex.: agy:gemini-3.8-flash-low) ou troque de provedor. %s",
				quota.Status, truncateText(firstNonEmpty(finalEnv.Error, quota.ShortError), 300))
		}
		return nil, fmt.Errorf("agy encerrou com código %d: %s", code, truncateText(firstNonEmpty(detail, errString(runErr)), 500))
	}
	if !sawResult || finalEnv.Status == "" {
		// Sem evento result parseável: cai no texto acumulado dos deltas.
		if finalEnv.Response == "" {
			return nil, fmt.Errorf("não consegui ler a resposta em stream do agy")
		}
	}
	if !strings.EqualFold(finalEnv.Status, "SUCCESS") && finalEnv.Status != "" {
		msg := finalEnv.Error
		if msg == "" {
			msg = finalEnv.Status
		}
		if looksLikeAuth(msg) {
			return nil, fmt.Errorf("agy não está autenticado. Abra um terminal, rode `agy` e entre com a conta Google Pro")
		}
		return nil, fmt.Errorf("agy devolveu status %s: %s", finalEnv.Status, truncateText(msg, 500))
	}
	return finalEnv.toResponse(true)
}

type streamError struct {
	code   int
	detail string
}

func (e *streamError) Error() string {
	return "agy encerrou com código " + itoa(e.code) + ": " + e.detail
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
