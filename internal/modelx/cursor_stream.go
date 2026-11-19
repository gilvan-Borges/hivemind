package modelx

import (
	"encoding/json"
	"strings"
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
}

type cursorMessage struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// cursorNotice is what the HiveMind UI should show for one stream event.
// Duplicate assistant flushes are skipped. Tool results are not copied: they
// can contain file bodies.
type cursorNotice struct {
	Text    string
	Partial bool
	Final   bool
	Failed  bool
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
		text := strings.TrimSpace(ev.Message.text())
		if text == "" {
			return cursorNotice{}, false
		}
		return cursorNotice{Text: text, Partial: true}, true
	case "tool_call":
		if ev.Subtype != "" && ev.Subtype != "started" {
			return cursorNotice{}, false
		}
		name, path := toolCallNotice(ev.ToolCall)
		if name == "" {
			return cursorNotice{}, false
		}
		text := "Cursor · " + name
		if path != "" {
			text += " · " + path
		}
		return cursorNotice{Text: text, Partial: true}, true
	case "result":
		return cursorNotice{Text: ev.Result, Final: true, Failed: ev.IsError}, true
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
			Name string `json:"name"`
		}
		if json.Unmarshal(fn, &meta) == nil && meta.Name != "" {
			return meta.Name, ""
		}
	}
	for key, body := range obj {
		name = strings.TrimSuffix(key, "ToolCall")
		var args struct {
			Args struct {
				Path string `json:"path"`
			} `json:"args"`
		}
		_ = json.Unmarshal(body, &args)
		return name, args.Args.Path
	}
	return "", ""
}

func redactSecret(text, secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" || text == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "[redigido]")
}
