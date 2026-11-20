package modelx

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// promptCacheTransport wraps an http.RoundTripper and automatically injects:
//  1. Anthropic & OpenRouter explicit Prompt Caching breakpoints (`cache_control: {"type": "ephemeral"}`)
//     on the system prompt, the last tool definition, and the penultimate conversation turn
//     for models that support explicit cache control (`anthropic/*`, `google/gemini-*`, Claude API).
//  2. OpenAI & OpenRouter sticky cache affinity (`prompt_cache_key` / `x-session-id` header)
//     for models with implicit prefix caching (`deepseek/*`, `openai/*`, `z-ai/*`, `moonshotai/*`, `qwen/*`, `x-ai/*`).
type promptCacheTransport struct {
	base http.RoundTripper
}

func newPromptCacheTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &promptCacheTransport{base: base}
}

func (t *promptCacheTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if req == nil || req.Method != http.MethodPost || req.Body == nil || req.URL == nil {
		return base.RoundTrip(req)
	}
	host := strings.ToLower(req.URL.Host)
	path := strings.ToLower(req.URL.Path)
	isChatOrMessages := strings.HasSuffix(path, "/chat/completions") ||
		strings.HasSuffix(path, "/messages") ||
		strings.HasSuffix(path, "/responses")
	if !isChatOrMessages {
		return base.RoundTrip(req)
	}

	rawBody, err := io.ReadAll(io.LimitReader(req.Body, retryBodyBufferLimit))
	_ = req.Body.Close()
	if err != nil || len(rawBody) == 0 {
		req.Body = io.NopCloser(bytes.NewReader(rawBody))
		return base.RoundTrip(req)
	}

	sid := sessionIDFromContext(req.Context())
	rewritten := injectPromptCachingJSON(rawBody, host, path, sid)

	clone := req.Clone(req.Context())
	if sid != "" && clone.Header.Get("x-session-id") == "" {
		clone.Header.Set("x-session-id", sid)
	}
	if strings.Contains(host, "anthropic.com") && clone.Header.Get("anthropic-beta") == "" {
		clone.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")
	}
	clone.Body = io.NopCloser(bytes.NewReader(rewritten))
	clone.ContentLength = int64(len(rewritten))
	clone.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(rewritten)), nil
	}
	return base.RoundTrip(clone)
}

func supportsExplicitCacheControl(host, path, modelID string) bool {
	m := strings.ToLower(strings.TrimSpace(modelID))
	if strings.Contains(host, "anthropic.com") || strings.HasSuffix(path, "/messages") {
		return true
	}
	if strings.Contains(host, "openrouter.ai") {
		return strings.HasPrefix(m, "anthropic/") ||
			strings.HasPrefix(m, "google/gemini-") ||
			strings.HasPrefix(m, "claude")
	}
	return false
}

func injectPromptCachingJSON(raw []byte, host, path, sessionID string) []byte {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil || payload == nil {
		return raw
	}
	modelID, _ := payload["model"].(string)
	modified := false

	// 1. Native Anthropic /v1/messages format: system blocks + tools[last] + messages[penultimate].
	if strings.HasSuffix(path, "/messages") || strings.Contains(host, "anthropic.com") {
		if sysStr, ok := payload["system"].(string); ok && len(sysStr) >= 512 {
			payload["system"] = []any{
				map[string]any{
					"type":          "text",
					"text":          sysStr,
					"cache_control": map[string]string{"type": "ephemeral"},
				},
			}
			modified = true
		} else if sysArr, ok := payload["system"].([]any); ok && len(sysArr) > 0 {
			if lastBlock, ok := sysArr[len(sysArr)-1].(map[string]any); ok {
				if _, exists := lastBlock["cache_control"]; !exists {
					lastBlock["cache_control"] = map[string]string{"type": "ephemeral"}
					modified = true
				}
			}
		}
		if toolsArr, ok := payload["tools"].([]any); ok && len(toolsArr) > 0 {
			if lastTool, ok := toolsArr[len(toolsArr)-1].(map[string]any); ok {
				if _, exists := lastTool["cache_control"]; !exists {
					lastTool["cache_control"] = map[string]string{"type": "ephemeral"}
					modified = true
				}
			}
		}
		if msgs, ok := payload["messages"].([]any); ok && len(msgs) >= 2 {
			if markMessageContentCacheControl(msgs[len(msgs)-2]) {
				modified = true
			}
		}
	} else if supportsExplicitCacheControl(host, path, modelID) {
		// 2. OpenRouter /chat/completions for Anthropic Claude & Google Gemini 3.8/4 models:
		// convert system message and penultimate history message to multipart content with cache_control.
		if msgs, ok := payload["messages"].([]any); ok && len(msgs) > 0 {
			if firstMsg, ok := msgs[0].(map[string]any); ok {
				role, _ := firstMsg["role"].(string)
				if role == "system" || role == "developer" {
					if markMessageContentCacheControl(firstMsg) {
						modified = true
					}
				}
			}
			if len(msgs) >= 3 {
				if markMessageContentCacheControl(msgs[len(msgs)-2]) {
					modified = true
				}
			}
		}
	}

	// 3. Sticky prompt_cache_key for OpenAI / OpenRouter implicit prefix caching.
	if sessionID != "" && (strings.Contains(host, "openai.com") || strings.Contains(host, "openrouter.ai")) {
		if strings.Contains(host, "openai.com") {
			if _, exists := payload["prompt_cache_key"]; !exists {
				payload["prompt_cache_key"] = "hm-" + sessionID
				modified = true
			}
		}
		if _, exists := payload["user"]; !exists {
			payload["user"] = "hm-" + sessionID
			modified = true
		}
	}

	if !modified {
		return raw
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return out
}

func markMessageContentCacheControl(msgAny any) bool {
	msg, ok := msgAny.(map[string]any)
	if !ok || msg == nil {
		return false
	}
	switch content := msg["content"].(type) {
	case string:
		if len(content) < 512 {
			return false
		}
		msg["content"] = []any{
			map[string]any{
				"type":          "text",
				"text":          content,
				"cache_control": map[string]string{"type": "ephemeral"},
			},
		}
		return true
	case []any:
		if len(content) == 0 {
			return false
		}
		if lastPart, ok := content[len(content)-1].(map[string]any); ok {
			if _, exists := lastPart["cache_control"]; !exists {
				lastPart["cache_control"] = map[string]string{"type": "ephemeral"}
				return true
			}
		}
	}
	return false
}
