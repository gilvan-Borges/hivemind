package modelx

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
)

func fastAgyRetries(t *testing.T) {
	t.Helper()
	prev := agyRetryDelays
	agyRetryDelays = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}
	t.Cleanup(func() { agyRetryDelays = prev })
}

func TestParseAgyError(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		wantOK bool
		status string
		code   int
	}{
		{
			name: "envelope real de quota",
			stderr: `agy : error: Your AI credits balance is too low to continue.` + "\n" +
				`AGY_ERROR: {"short_error":"RESOURCE_EXHAUSTED (code 429): Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED","error_code":429,"code_kind":"http","retryable":true,"error_id":"65e473c0"}`,
			wantOK: true,
			status: "RESOURCE_EXHAUSTED",
			code:   429,
		},
		{
			name:   "envelope com linhas depois",
			stderr: "AGY_ERROR: {\"status\":\"UNAVAILABLE\",\"error_code\":503,\"code_kind\":\"http\",\"retryable\":true}\nsome other log line",
			wantOK: true,
			status: "UNAVAILABLE",
			code:   503,
		},
		{
			name:   "sem marcador",
			stderr: "qualquer coisa no stderr",
			wantOK: false,
		},
		{
			name:   "json quebrado",
			stderr: "AGY_ERROR: {não é json",
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseAgyError(tc.stderr)
			if !tc.wantOK {
				if got != nil {
					t.Fatalf("parseAgyError deveria ser nil: %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("parseAgyError devolveu nil")
			}
			if got.Status != tc.status || got.ErrorCode != tc.code || !got.Retryable {
				t.Fatalf("envelope = %+v, queria status=%q code=%d retryable", got, tc.status, tc.code)
			}
		})
	}
}

// TestAgyRetriesQuota429 reproduces dogfooding achado #12: agy marks quota
// errors as retryable — a single 429 blip must not kill the whole run.
func TestAgyRetriesQuota429(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake agy é script sh; coberto no CI Linux")
	}
	dir := t.TempDir()
	bin := writeFakeAgy(t, dir)
	t.Setenv("AGY_FAKE_DIR", dir)
	t.Setenv("AGY_FAKE_MODE", "quota-once")
	fastAgyRetries(t)

	llm := &Agy{Bin: bin, Dir: filepath.Join(dir, "work"), Timeout: 5 * time.Second}
	var resp *model.LLMResponse
	for r, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "diga oi"}}}},
	}, false) {
		if err != nil {
			t.Fatal(err)
		}
		resp = r
	}
	if resp == nil || !strings.Contains(resp.Content.Parts[0].Text, "depois da quota") {
		t.Fatalf("resposta = %+v, queria o sucesso pós-retry", resp)
	}
}

// TestAgyQuotaErrorAfterRetries ensures quota exhaustion surfaces an actionable
// message after the retries run out instead of the generic "exit 3" error.
func TestAgyQuotaErrorAfterRetries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake agy é script sh; coberto no CI Linux")
	}
	dir := t.TempDir()
	bin := writeFakeAgy(t, dir)
	t.Setenv("AGY_FAKE_DIR", dir)
	t.Setenv("AGY_FAKE_MODE", "quota-always")
	fastAgyRetries(t)

	llm := &Agy{Bin: bin, Dir: filepath.Join(dir, "work"), Timeout: 5 * time.Second}
	var lastErr error
	for _, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "diga oi"}}}},
	}, false) {
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		t.Fatal("esperava erro de quota após esgotar os retries")
	}
	msg := lastErr.Error()
	if !strings.Contains(msg, "quota/créditos") || !strings.Contains(msg, "429") {
		t.Fatalf("erro deveria ser acionável (quota/créditos + 429): %q", msg)
	}
	if !strings.Contains(msg, "gemini-3.8-flash-low") {
		t.Fatalf("erro deveria sugerir modelo mais leve: %q", msg)
	}
}
