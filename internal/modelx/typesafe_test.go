package modelx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTypeSafeProbeAndSystemOneClassify(t *testing.T) {
	var sawAuth, sawPath, sawModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"jev-latest"},{"id":"jev-1.14.0"}]}`))
		case "/v1/systemone":
			var body struct {
				Model     string                 `json:"model"`
				State     string                 `json:"state"`
				Questions map[string]interface{} `json:"questions"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			sawModel = body.Model
			_, _ = w.Write([]byte(`{
				"model":"jev-1.14.0",
				"answers":{
					"class":{"type":"choice","choice":"coding","confidence":0.95},
					"complexity":{"type":"choice","choice":"high","confidence":0.89}
				}
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	t.Setenv("TYPESAFE_API_KEY", "ts_test_token_123456")
	t.Setenv("TYPESAFE_BASE_URL", srv.URL)

	models, err := ProbeTypeSafeJev(context.Background())
	if err != nil {
		t.Fatalf("ProbeTypeSafeJev failed: %v", err)
	}
	if sawAuth != "Bearer ts_test_token_123456" || sawPath != "/v1/models" {
		t.Fatalf("unexpected request headers/path: auth=%q path=%q", sawAuth, sawPath)
	}
	if len(models) != 2 || models[0] != "jev-latest" {
		t.Fatalf("unexpected models: %+v", models)
	}

	got, err := ClassifyWithTypeSafeJev(context.Background(), "Refatore o subsistema de autenticação multi-tenant")
	if err != nil {
		t.Fatalf("ClassifyWithTypeSafeJev failed: %v", err)
	}
	if sawModel != "jev-latest" || sawPath != "/v1/systemone" {
		t.Fatalf("unexpected systemone call: model=%q path=%q", sawModel, sawPath)
	}
	if got != "coding high" {
		t.Fatalf("expected 'coding high', got %q", got)
	}
}
