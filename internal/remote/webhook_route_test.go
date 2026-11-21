package remote

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/resident"
)

func TestServer_WebhookRoutesFailClosedAndVerifyHMAC(t *testing.T) {
	t.Setenv("HIVEMIND_DATA_DIR", t.TempDir())
	srv := NewServer(NewHub(), nil, Config{Port: 0})

	// Fail closed (503) when unconfigured.
	recOff := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recOff, httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewReader([]byte(`{}`))))
	if recOff.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when github webhook not mounted, got %d", recOff.Code)
	}

	// Protected routes remain bearer-authenticated.
	recAuth := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recAuth, httptest.NewRequest(http.MethodGet, "/api/v1/lanes", nil))
	if recAuth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on /api/v1/lanes without bearer token, got %d", recAuth.Code)
	}

	// Wired GitHub webhook verifies HMAC without needing bearer token.
	secret := "remote-gh-secret-0000"
	var triggered bool
	ghHandler := resident.NewWebhookHandler(resident.WebhookConfig{GitHubSecret: secret}, func(origin resident.Origin, prompt string) error {
		triggered = origin == resident.OriginWebhook
		return nil
	})
	srv.SetWebhookHandlers(ghHandler, nil)

	payload := []byte(`{"action":"opened","pull_request":{"title":"PR externo"}}`)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	recOK := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recOK, req)
	if recOK.Code != http.StatusAccepted || !triggered {
		t.Fatalf("expected webhook route to trigger with 202, got %d triggered=%v", recOK.Code, triggered)
	}
}
