package modelx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openai/openai-go/v3/option"
	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/openaimodel"
)

// The Cursor CLI's backend speaks an OpenAI-compatible chat API. A leaked
// OpenRouter id (particle-1:free, deepseek-v4.1-flash) makes that API return
// 400, and openai-go prints only "OpenAI API error: 400 Bad Request".
func TestCursorCompatible400KeepsModelAndSanitizedBody(t *testing.T) {
	const leaked = "openrouter/particle-ai/particle-1:free"
	const secret = "sk-or-v1-secretkeyvalue"
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), leaked) {
			t.Errorf("corpo enviado sem o id vazado: %s", raw)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"model `+leaked+` is not a Cursor model; key `+secret+`","type":"invalid_request_error","code":"model_not_found"}}`)
	}))
	defer srv.Close()

	llm, err := openaimodel.NewModel(context.Background(), leaked, &openaimodel.ClientConfig{
		APIKey:     "test-key",
		BaseURL:    srv.URL + "/v1",
		API:        openaimodel.APIChatCompletions,
		HTTPClient: retryHTTPClient(),
		Options:    []option.RequestOption{option.WithMaxRetries(0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got error
	for _, gErr := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("oi", genai.RoleUser)},
	}, false) {
		if gErr != nil {
			got = gErr
		}
	}
	if got == nil {
		t.Fatal("esperava 400 do servidor falso")
	}
	if calls.Load() != 1 {
		t.Fatalf("400 foi repetido %d vezes", calls.Load())
	}
	msg := FormatUpstreamError("cursor", leaked, got, secret)
	if !strings.Contains(msg, "cursor/"+leaked) {
		t.Fatalf("erro sem provedor/modelo: %s", msg)
	}
	if !strings.Contains(msg, "is not a Cursor model") || !strings.Contains(msg, "400") {
		t.Fatalf("corpo do provedor sumiu: %s", msg)
	}
	if strings.Contains(msg, secret) {
		t.Fatalf("chave no erro: %s", msg)
	}
}
