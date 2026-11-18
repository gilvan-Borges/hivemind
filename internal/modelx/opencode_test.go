package modelx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseOpenCodeSpec(t *testing.T) {
	tier, id := ParseOpenCodeSpec("opencode-go:glm-5.3")
	if tier != "go" || id != "glm-5.3" {
		t.Fatalf("got (%q, %q)", tier, id)
	}
	tier, id = ParseOpenCodeSpec("opencode-go/minimax-m3")
	if tier != "go" || id != "minimax-m3" {
		t.Fatalf("got (%q, %q)", tier, id)
	}
	tier, id = ParseOpenCodeSpec("opencode:claude-sonnet-5-5")
	if tier != "zen" || id != "claude-sonnet-5-5" {
		t.Fatalf("got (%q, %q)", tier, id)
	}
	tier, id = ParseOpenCodeSpec("zen:big-pickle")
	if tier != "zen" || id != "big-pickle" {
		t.Fatalf("got (%q, %q)", tier, id)
	}
}

func TestProbeAndListOpenCodeGoAndZenHTTP(t *testing.T) {
	var seenGoAuth, seenZenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/zen/go/v1/models":
			seenGoAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{
						"id":   "minimax-m3",
						"name": "MiniMax M3",
						"api": map[string]any{
							"messages": map[string]any{"base_url": "https://opencode.ai/inference/go/anthropic/v1"},
						},
					},
					{
						"id":   "glm-5.3",
						"name": "GLM-5.3",
						"api": map[string]any{
							"chat-completions": map[string]any{"base_url": "https://opencode.ai/inference/go/openai/v1"},
						},
					},
					{
						"id":   "gemini-4-argon",
						"name": "Gemini 4 Argon",
						"api": map[string]any{
							"generate-content": map[string]any{"base_url": "https://opencode.ai/inference/go/google/v1beta"},
						},
					},
				},
			})
		case "/zen/v1/models":
			seenZenAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{
						"id":   "gpt-6-astra",
						"name": "GPT-6 Astra",
						"api": map[string]any{
							"responses": map[string]any{"base_url": "https://opencode.ai/inference/openai/v1"},
						},
					},
					{
						"id":   "claude-sonnet-5-5",
						"name": "Claude Sonnet 5.5",
						"api": map[string]any{
							"messages": map[string]any{"base_url": "https://opencode.ai/inference/anthropic/v1"},
						},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("OPENCODE_API_KEY", "zen_test_token_123456")
	t.Setenv("OPENCODE_BASE_URL", srv.URL+"/zen/v1")

	st, goModels, zenModels := ProbeAndListOpenCode(context.Background())
	if !st.Configured || st.Source != "env" || st.GoCount != 3 || st.ZenCount != 2 || st.ModelCount != 5 {
		t.Fatalf("unexpected status: %+v", st)
	}
	if seenGoAuth != "Bearer zen_test_token_123456" || seenZenAuth != "Bearer zen_test_token_123456" {
		t.Fatalf("expected Bearer auth on both Go & Zen: go=%q zen=%q", seenGoAuth, seenZenAuth)
	}
	if len(goModels) != 3 || len(zenModels) != 2 {
		t.Fatalf("unexpected models count: go=%d zen=%d", len(goModels), len(zenModels))
	}

	// Verify exact cached routing for Go MiniMax (Anthropic messages!) and Go Gemini (v1beta).
	rt := lookupOpenCodeRoute(context.Background(), "go", "minimax-m3", "zen_test_token_123456", srv.URL+"/zen/v1")
	if rt.Protocol != "messages" || rt.BaseURL != "https://opencode.ai/inference/go/anthropic/v1" {
		t.Fatalf("expected Go minimax-m3 -> messages @ go/anthropic/v1, got %+v", rt)
	}

	// Verify factory constructs all Go and Zen model families cleanly.
	for _, spec := range []string{
		"opencode-go:minimax-m3",
		"opencode-go:glm-5.3",
		"opencode-go:gemini-4-argon",
		"opencode:claude-sonnet-5-5",
		"opencode:gpt-6-astra",
	} {
		m, err := New(context.Background(), "opencode", spec)
		if err != nil || m == nil {
			t.Fatalf("New(opencode, %q) failed: %v", spec, err)
		}
	}
}

func TestOpenCodeTransportInjectsSessionHeader(t *testing.T) {
	var seenSession, seenClient string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSession = r.Header.Get("x-opencode-session")
		seenClient = r.Header.Get("x-opencode-client")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := newOpenCodeHTTPClient("fallback-sess", "tok")
	ctx := WithSessionID(context.Background(), "hm-conv-42")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if seenSession != "hm-conv-42" || seenClient == "" {
		t.Fatalf("expected x-opencode-session=hm-conv-42 and x-opencode-client set, got session=%q client=%q", seenSession, seenClient)
	}

	// Without context session ID, fallback must be sent.
	req2, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if seenSession != "fallback-sess" {
		t.Fatalf("expected fallback session header, got %q", seenSession)
	}
}
