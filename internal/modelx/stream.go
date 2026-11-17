package modelx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
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
	if a == nil || a.Bin == "" {
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
	cmd := agyCommand(ctx, bin, args)
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
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			ev, ok := parseStreamLine(sc.Bytes())
			if ok && fn != nil {
				fn(ev)
			}
		}
	}()
	_, werr := io.Copy(stdin, strings.NewReader(prompt))
	cerr := stdin.Close()
	if werr != nil && !isBrokenPipe(werr) {
		_ = cmd.Process.Kill()
	} else if cerr != nil && !isBrokenPipe(cerr) && werr == nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	<-done
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
	return errExit(code, "não autenticado. Rode `agy` e entre com a conta Google Pro. "+detail)
}

func errExit(code int, detail string) error {
	return &streamError{code: code, detail: truncateText(detail, 500)}
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
