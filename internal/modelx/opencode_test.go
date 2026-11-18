package modelx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenCodeProtocolRouting(t *testing.T) {
	cases := map[string]string{
		"claude-sonnet-5-5":        "anthropic",
		"claude-opus-4-6":          "anthropic",
		"gemini-4-argon":           "gemini",
		"gemini-3.5-flash":         "gemini",
		"gpt-5.4":                  "openai-responses",
		"gpt-6-astra":              "openai-responses",
		"o3-mini":                  "openai-responses",
		"deepseek-v4.1-flash-free": "openai-chat",
		"big-pickle":               "openai-chat",
		"kimi-k2.7-code":           "openai-chat",
		"qwen3.6-plus":             "openai-chat",
	}
	for id, want := range cases {
		if got := openCodeProtocol(id); got != want {
			t.Errorf("openCodeProtocol(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestStripOpenCodePrefix(t *testing.T) {
	if got := StripOpenCodePrefix("opencode:claude-sonnet-5-5"); got != "claude-sonnet-5-5" {
		t.Fatalf("got %q", got)
	}
	if got := StripOpenCodePrefix("opencode/gpt-5.4"); got != "gpt-5.4" {
		t.Fatalf("got %q", got)
	}
	if got := StripOpenCodePrefix("zen:big-pickle"); got != "big-pickle" {
		t.Fatalf("got %q", got)
	}
}

func TestProbeAndListOpenCodeHTTP(t *testing.T) {
	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "gpt-6-astra", "owned_by": "opencode"},
				{"id": "claude-sonnet-5-5", "owned_by": "opencode"},
				{"id": "deepseek-v4.1-flash-free", "owned_by": "opencode"},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("OPENCODE_API_KEY", "zen_test_token_123456")
	t.Setenv("OPENCODE_BASE_URL", srv.URL+"/v1")

	st, models := ProbeAndListOpenCode(context.Background())
	if !st.Configured || st.Source != "env" || st.ModelCount != 3 {
		t.Fatalf("unexpected status: %+v", st)
	}
	if seenAuth != "Bearer zen_test_token_123456" {
		t.Fatalf("expected Bearer token header, got %q", seenAuth)
	}
	if len(models) != 3 || models[0].Slug != "claude-sonnet-5-5" {
		t.Fatalf("unexpected models: %+v", models)
	}

	// Verify factory builds models for all 4 OpenCode protocol families with token set.
	for _, spec := range []string{"opencode:claude-sonnet-5-5", "opencode:gemini-4-argon", "opencode:gpt-6-astra", "opencode:deepseek-v4.1-flash-free"} {
		m, err := New(context.Background(), "opencode", spec)
		if err != nil || m == nil {
			t.Fatalf("New(opencode, %q) failed: %v", spec, err)
		}
	}
}
