package modelx

import (
	"encoding/json"
	"strings"
	"unicode"
)

// cursorLine is one NDJSON event from `agent -p --output-format stream-json`.
// Unknown fields are ignored. Only the fields the adapter needs are decoded.
type cursorLine struct {
	Type        string          `json:"type"`
	Subtype     string          `json:"subtype"`
	TimestampMS *int64          `json:"timestamp_ms"`
	ModelCallID string          `json:"model_call_id"`
	IsError     bool            `json:"is_error"`
	Result      string          `json:"result"`
	Message     cursorMessage   `json:"message"`
	ToolCall    json.RawMessage `json:"tool_call"`
	Usage       *cursorUsage    `json:"usage"`
}

// cursorUsage is the token accounting the CLI attaches to result events.
// Field names follow the CLI's usage object; a CLI that omits them simply
// yields zero values (best-effort telemetry, never a hard dependency).
type cursorUsage struct {
	InputTokens     int64    `json:"input_tokens"`
	OutputTokens    int64    `json:"output_tokens"`
	CacheReadTokens int64    `json:"cache_read_input_tokens"`
	CostUSD         *float64 `json:"total_cost_usd"`
}

type cursorMessage struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// cursorNotice is what the HiveMind UI should show for one stream event.
// Text keeps the delta whitespace: the CLI often sends the space between
// words as its own delta, and TrimSpace would glue "Vou" + " " + "ler".
// Tool calls are separate from that text. Duplicate assistant flushes are
// skipped. Tool results are not copied: they can contain file bodies.
type cursorNotice struct {
	Text     string
	ToolName string
	ToolPath string
	Partial  bool
	Final    bool
	Failed   bool
	Usage    *cursorUsage
}

// CursorUsage is the token accounting parsed from one CLI result event.
type CursorUsage = cursorUsage

// CursorNotice is the exported representation of one parsed Cursor CLI stream-json event.
type CursorNotice = cursorNotice

// ParseCursorStreamLine parses a single NDJSON line from Cursor CLI (`agent --output-format stream-json`).
func ParseCursorStreamLine(line string) (CursorNotice, bool) {
	return parseCursorStreamLine(line)
}

func parseCursorStreamLine(line string) (cursorNotice, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return cursorNotice{}, false
	}
	var ev cursorLine
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return cursorNotice{}, false
	}
	switch ev.Type {
	case "assistant":
		// We always pass --stream-partial-output. Only a delta that has
		// timestamp_ms and no model_call_id is new text. The flush before a
		// tool call and the flush at the end of the turn repeat that text.
		if ev.ModelCallID != "" || ev.TimestampMS == nil {
			return cursorNotice{}, false
		}
		text := ev.Message.text()
		if text == "" {
			return cursorNotice{}, false
		}
		return cursorNotice{Text: text, Partial: true}, true
	case "tool_call":
		if ev.Subtype != "" && ev.Subtype != "started" {
			return cursorNotice{}, false
		}
		name, path := toolCallNotice(ev.ToolCall)
		if name == "" || cursorToolMetaKey(name) {
			return cursorNotice{}, false
		}
		return cursorNotice{ToolName: name, ToolPath: path, Partial: true}, true
	case "result":
		return cursorNotice{Text: ev.Result, Final: true, Failed: ev.IsError, Usage: ev.Usage}, true
	default:
		return cursorNotice{}, false
	}
}

func (m cursorMessage) text() string {
	var b strings.Builder
	for _, part := range m.Content {
		if part.Type != "" && part.Type != "text" {
			continue
		}
		b.WriteString(part.Text)
	}
	return b.String()
}

// cursorToolMetaKey reports JSON fields the CLI mixes into tool_call that are
// not a tool. Map iteration is random, so these must be skipped by name.
func cursorToolMetaKey(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "startedatms", "completedatms", "toolcallid", "hookadditionalcontexts",
		"result", "description", "sessionid", "modelcallid", "timestampms":
		return true
	default:
		return false
	}
}

func toolCallNotice(raw json.RawMessage) (name, path string) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", ""
	}
	if fn, ok := obj["function"]; ok {
		var meta struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(fn, &meta) == nil && meta.Name != "" && !cursorToolMetaKey(meta.Name) {
			return meta.Name, toolTargetFromRaw(meta.Arguments)
		}
	}
	var toolKey string
	for key := range obj {
		if cursorToolMetaKey(key) {
			continue
		}
		if strings.HasSuffix(strings.ToLower(key), "toolcall") {
			toolKey = key
			break
		}
		if toolKey == "" {
			toolKey = key
		}
	}
	if toolKey == "" {
		return "", ""
	}
	name = toolKey
	if i := strings.LastIndex(strings.ToLower(name), "toolcall"); i > 0 {
		name = name[:i]
	}
	return name, toolTargetFromRaw(obj[toolKey])
}

func toolTargetFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return toolTargetFromMap(map[string]any{"command": s})
		}
	}
	var asMap map[string]any
	if json.Unmarshal(raw, &asMap) != nil {
		return ""
	}
	if args, ok := asMap["args"]; ok {
		if nested, ok := args.(map[string]any); ok {
			return toolTargetFromMap(nested)
		}
	}
	if args, ok := asMap["arguments"]; ok {
		switch v := args.(type) {
		case map[string]any:
			return toolTargetFromMap(v)
		case string:
			var nested map[string]any
			if json.Unmarshal([]byte(v), &nested) == nil {
				return toolTargetFromMap(nested)
			}
		}
	}
	return toolTargetFromMap(asMap)
}

func toolTargetFromMap(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	for _, key := range []string{"path", "file_path", "filePath", "file", "target_file", "targetFile", "directory", "glob", "pattern", "query", "command"} {
		v, ok := args[key]
		if !ok || v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}

// formatCursorToolMarker is the line the UI splits back into a tool row.
// The name is a single token so a path may contain colons (Windows).
func formatCursorToolMarker(name, path string) string {
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			b.WriteRune(r)
		}
	}
	safeName := b.String()
	if safeName == "" {
		safeName = "tool"
	}
	path = strings.ReplaceAll(path, "\r", " ")
	path = strings.ReplaceAll(path, "\n", " ")
	path = strings.ReplaceAll(path, "]", "")
	return "[[hmtool:" + safeName + ":" + path + "]]"
}

func relativizeToolPath(workspace, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	ws := strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(workspace), "\\", "/"), "/")
	if ws != "" && len(p) >= len(ws) && strings.EqualFold(p[:len(ws)], ws) {
		rest := strings.TrimPrefix(p[len(ws):], "/")
		if rest != "" {
			return rest
		}
	}
	return p
}

// cursorTranscript accumulates assistant deltas and tool markers in order.
type cursorTranscript struct {
	b  strings.Builder
	nl bool
}

func (t *cursorTranscript) absorb(n cursorNotice, workspace string) string {
	if n.ToolName != "" {
		marker := formatCursorToolMarker(n.ToolName, relativizeToolPath(workspace, n.ToolPath))
		var chunk strings.Builder
		if t.b.Len() > 0 && !t.nl {
			chunk.WriteByte('\n')
		}
		chunk.WriteString(marker)
		chunk.WriteByte('\n')
		s := chunk.String()
		t.b.WriteString(s)
		t.nl = true
		return s
	}
	if n.Text == "" {
		return ""
	}
	t.b.WriteString(n.Text)
	t.nl = strings.HasSuffix(n.Text, "\n")
	return n.Text
}

func (t *cursorTranscript) finish(result string) string {
	body := t.b.String()
	summary := strings.TrimSpace(result)
	if summary != "" && !strings.Contains(body, summary) {
		if body != "" && !strings.HasSuffix(body, "\n") {
			body += "\n\n"
		} else if body != "" && !strings.HasSuffix(body, "\n\n") {
			body += "\n"
		}
		body += summary
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
	}
	if strings.TrimSpace(body) == "" {
		return "O agente Cursor terminou sem texto. As alterações, se houver, estão no workspace deste agente."
	}
	return body
}

func redactSecret(text, secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" || text == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "[redigido]")
}
