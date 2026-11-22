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
	"unicode/utf16"
	"unicode/utf8"

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
		finalEnv    agyEnvelope
		sawResult   bool
		deltaFilter agyDecisionDeltaFilter
	)
	handleLine := func(line string) bool {
		ev, ok := parseStreamLine([]byte(line))
		if !ok {
			return true
		}
		switch ev.Type {
		case "delta":
			if text := deltaFilter.Push(ev.Text); text != "" {
				yield(&model.LLMResponse{
					Partial: true,
					Content: genai.NewContentFromText(text, genai.RoleModel),
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
	if tail := deltaFilter.Flush(); tail != "" {
		yield(&model.LLMResponse{
			Partial: true,
			Content: genai.NewContentFromText(tail, genai.RoleModel),
		}, nil)
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

type agyDeltaMode uint8

const (
	deltaModeUnknown agyDeltaMode = iota
	deltaModePlain
	deltaModeJSON
	deltaModeDone
)

type agyJSONScanState uint8

const (
	jsonObjExpectKey agyJSONScanState = iota
	jsonObjKey
	jsonObjExpectColon
	jsonObjExpectVal
	jsonInTextString
	jsonSkipVal
	jsonObjAfterVal
)

// agyDecisionDeltaFilter extrai incrementalmente apenas o campo "text" quando o
// stream do `agy --json-schema` emite o envelope JSON `{"text":...,"tool_calls":[...]}`,
// inclusive após prosa inicial ou dentro de cercas markdown, evitando vazamento de JSON bruto no chat.
type agyDecisionDeltaFilter struct {
	mode       agyDeltaMode
	state      agyJSONScanState
	leadSpaces strings.Builder
	fenceBuf   strings.Builder
	inFence    bool
	plainProbe strings.Builder
	probing    bool

	keyBuf    strings.Builder
	strEsc    bool
	strEscU   int
	strHex    [4]byte
	hiSurr    rune
	targetKey bool

	skipInStr bool
	skipEsc   bool
	skipNest  int
}

func isJSONSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

func checkFencePrefix(s string) (readyAfterNL bool, valid bool) {
	if s == "" || s[0] != '`' {
		return false, false
	}
	ticks := 0
	for ticks < len(s) && s[ticks] == '`' {
		ticks++
	}
	if ticks > 3 {
		return false, false
	}
	if ticks < 3 {
		return false, len(s) == ticks
	}
	rest := s[3:]
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		trimmed := strings.ToLower(strings.TrimLeft(strings.TrimRight(rest, "\r"), " \t"))
		if trimmed == "" || strings.HasPrefix("json", trimmed) {
			return false, true
		}
		if strings.TrimRight(trimmed, " \t") == "json" {
			return false, true
		}
		return false, false
	}
	header := strings.ToLower(strings.TrimSpace(rest[:nl]))
	if header != "" && header != "json" {
		return false, false
	}
	after := rest[nl+1:]
	for i := 0; i < len(after); i++ {
		if !isJSONSpace(after[i]) {
			return false, false
		}
	}
	return true, true
}

func matchAgyEnvelopeStart(buf string) (matched bool, possible bool) {
	if len(buf) == 0 || buf[0] != '{' || len(buf) > 40 {
		return false, false
	}
	rest := strings.TrimLeft(buf[1:], " \t\r\n")
	if rest == "" {
		return false, true
	}
	for _, target := range []string{`"text"`, `"tool_calls"`} {
		if strings.HasPrefix(target, rest) && len(rest) < len(target) {
			return false, true
		}
		if strings.HasPrefix(rest, target) {
			after := strings.TrimLeft(rest[len(target):], " \t\r\n")
			if after == "" {
				return false, true
			}
			if after[0] == ':' {
				return true, true
			}
			return false, false
		}
	}
	return false, false
}

func hexVal(b byte) (rune, bool) {
	switch {
	case b >= '0' && b <= '9':
		return rune(b - '0'), true
	case b >= 'a' && b <= 'f':
		return rune(b-'a') + 10, true
	case b >= 'A' && b <= 'F':
		return rune(b-'A') + 10, true
	default:
		return 0, false
	}
}

func isHighSurrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDBFF
}

func isLowSurrogate(r rune) bool {
	return r >= 0xDC00 && r <= 0xDFFF
}

func (f *agyDecisionDeltaFilter) stepPlainByte(b byte, out *strings.Builder) {
	if f.probing {
		f.plainProbe.WriteByte(b)
		matched, possible := matchAgyEnvelopeStart(f.plainProbe.String())
		if matched {
			probeTail := f.plainProbe.String()[1:]
			f.plainProbe.Reset()
			f.probing = false
			f.mode = deltaModeJSON
			f.state = jsonObjExpectKey
			for j := 0; j < len(probeTail); j++ {
				if f.mode == deltaModeDone {
					break
				}
				f.stepJSON(probeTail[j], out)
			}
			return
		}
		if !possible {
			if b == '{' {
				prev := f.plainProbe.String()
				out.WriteString(prev[:len(prev)-1])
				f.plainProbe.Reset()
				f.plainProbe.WriteByte('{')
			} else {
				out.WriteString(f.plainProbe.String())
				f.plainProbe.Reset()
				f.probing = false
			}
		}
		return
	}
	if b == '{' {
		f.probing = true
		f.plainProbe.Reset()
		f.plainProbe.WriteByte('{')
		return
	}
	out.WriteByte(b)
}

func (f *agyDecisionDeltaFilter) Flush() string {
	switch f.mode {
	case deltaModeUnknown:
		s := f.leadSpaces.String() + f.fenceBuf.String()
		f.leadSpaces.Reset()
		f.fenceBuf.Reset()
		f.inFence = false
		if s != "" {
			f.mode = deltaModePlain
		}
		return s
	case deltaModePlain:
		if f.probing {
			s := f.plainProbe.String()
			f.plainProbe.Reset()
			f.probing = false
			return s
		}
	}
	return ""
}

func (f *agyDecisionDeltaFilter) Push(chunk string) string {
	if chunk == "" {
		return ""
	}
	if f.mode == deltaModeDone {
		return ""
	}

	var out strings.Builder
	for i := 0; i < len(chunk); i++ {
		b := chunk[i]
		if f.mode == deltaModePlain {
			f.stepPlainByte(b, &out)
			continue
		}
		if f.mode == deltaModeUnknown {
			if f.inFence {
				readyAfterNL, prevValid := checkFencePrefix(f.fenceBuf.String())
				if b == '{' && readyAfterNL && prevValid {
					f.leadSpaces.Reset()
					f.fenceBuf.Reset()
					f.inFence = false
					f.mode = deltaModeJSON
					f.state = jsonObjExpectKey
					continue
				}
				f.fenceBuf.WriteByte(b)
				if _, valid := checkFencePrefix(f.fenceBuf.String()); valid {
					continue
				}
				f.inFence = false
				pending := f.leadSpaces.String() + f.fenceBuf.String()
				f.leadSpaces.Reset()
				f.fenceBuf.Reset()
				f.mode = deltaModePlain
				for j := 0; j < len(pending); j++ {
					if f.mode == deltaModeJSON || f.mode == deltaModeDone {
						if f.mode == deltaModeJSON {
							f.stepJSON(pending[j], &out)
						}
						continue
					}
					f.stepPlainByte(pending[j], &out)
				}
				continue
			}
			if isJSONSpace(b) {
				f.leadSpaces.WriteByte(b)
				continue
			}
			if b == '`' {
				f.inFence = true
				f.fenceBuf.Reset()
				f.fenceBuf.WriteByte('`')
				continue
			}
			if b != '{' {
				f.mode = deltaModePlain
				pending := f.leadSpaces.String()
				f.leadSpaces.Reset()
				out.WriteString(pending)
				f.stepPlainByte(b, &out)
				continue
			}
			f.leadSpaces.Reset()
			f.mode = deltaModeJSON
			f.state = jsonObjExpectKey
			continue
		}

		if f.mode == deltaModeDone {
			break
		}

		f.stepJSON(b, &out)
	}
	return out.String()
}

func (f *agyDecisionDeltaFilter) flushSurrogate(dst *strings.Builder) {
	if f.hiSurr != 0 {
		dst.WriteRune(utf8.RuneError)
		f.hiSurr = 0
	}
}

func (f *agyDecisionDeltaFilter) stepStringByte(b byte, dst *strings.Builder) (done bool) {
	if f.strEscU > 0 {
		idx := 4 - f.strEscU
		f.strHex[idx] = b
		f.strEscU--
		if f.strEscU == 0 {
			var r rune
			ok := true
			for k := 0; k < 4; k++ {
				v, vok := hexVal(f.strHex[k])
				if !vok {
					ok = false
					break
				}
				r = (r << 4) | v
			}
			if !ok {
				f.flushSurrogate(dst)
				dst.WriteRune(utf8.RuneError)
				return false
			}
			if f.hiSurr != 0 {
				if isLowSurrogate(r) {
					dst.WriteRune(utf16.DecodeRune(f.hiSurr, r))
					f.hiSurr = 0
					return false
				}
				f.flushSurrogate(dst)
			}
			if isHighSurrogate(r) {
				f.hiSurr = r
				return false
			}
			dst.WriteRune(r)
		}
		return false
	}

	if f.strEsc {
		f.strEsc = false
		if b == 'u' {
			f.strEscU = 4
			return false
		}
		f.flushSurrogate(dst)
		switch b {
		case '"', '\\', '/':
			dst.WriteByte(b)
		case 'b':
			dst.WriteByte('\b')
		case 'f':
			dst.WriteByte('\f')
		case 'n':
			dst.WriteByte('\n')
		case 'r':
			dst.WriteByte('\r')
		case 't':
			dst.WriteByte('\t')
		default:
			dst.WriteByte(b)
		}
		return false
	}

	if b == '\\' {
		f.strEsc = true
		return false
	}
	if b == '"' {
		f.flushSurrogate(dst)
		return true
	}
	f.flushSurrogate(dst)
	dst.WriteByte(b)
	return false
}

func (f *agyDecisionDeltaFilter) stepJSON(b byte, out *strings.Builder) {
	switch f.state {
	case jsonObjExpectKey:
		if isJSONSpace(b) || b == ',' {
			return
		}
		if b == '"' {
			f.keyBuf.Reset()
			f.strEsc = false
			f.strEscU = 0
			f.hiSurr = 0
			f.state = jsonObjKey
			return
		}
		if b == '}' {
			f.mode = deltaModeDone
		}
	case jsonObjKey:
		if f.stepStringByte(b, &f.keyBuf) {
			f.targetKey = f.keyBuf.String() == "text"
			f.state = jsonObjExpectColon
		}
	case jsonObjExpectColon:
		if isJSONSpace(b) {
			return
		}
		if b == ':' {
			f.state = jsonObjExpectVal
		}
	case jsonObjExpectVal:
		if isJSONSpace(b) {
			return
		}
		if f.targetKey && b == '"' {
			f.strEsc = false
			f.strEscU = 0
			f.hiSurr = 0
			f.state = jsonInTextString
			return
		}
		f.state = jsonSkipVal
		f.skipInStr = false
		f.skipEsc = false
		f.skipNest = 0
		f.stepJSON(b, out)
	case jsonInTextString:
		if f.stepStringByte(b, out) {
			f.mode = deltaModeDone
		}
	case jsonSkipVal:
		if f.skipInStr {
			if f.skipEsc {
				f.skipEsc = false
				return
			}
			if b == '\\' {
				f.skipEsc = true
				return
			}
			if b == '"' {
				f.skipInStr = false
				if f.skipNest == 0 {
					f.state = jsonObjAfterVal
				}
			}
			return
		}
		switch b {
		case '"':
			f.skipInStr = true
		case '{', '[':
			f.skipNest++
		case '}', ']':
			if f.skipNest > 0 {
				f.skipNest--
				if f.skipNest == 0 {
					f.state = jsonObjAfterVal
				}
			} else if b == '}' {
				f.mode = deltaModeDone
			}
		case ',':
			if f.skipNest == 0 {
				f.state = jsonObjExpectKey
			}
		}
	case jsonObjAfterVal:
		if isJSONSpace(b) {
			return
		}
		switch b {
		case ',':
			f.state = jsonObjExpectKey
		case '}':
			f.mode = deltaModeDone
		}
	}
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
