package resident

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

func TestWebhookHandler_VerifiesStandardAndGitHubHMACAndFencesPayload(t *testing.T) {
	rawSecret := "whsec_dGVzdC1zZWNyZXQtaGl2ZW1pbmQtb25kYTY=" // base64 secret for standard-webhooks
	ghSecret := "gh-test-secret-0000"

	var gotOrigin Origin
	var gotPrompt string
	h := NewWebhookHandler(WebhookConfig{
		StandardSecret: rawSecret,
		GitHubSecret:   ghSecret,
	}, func(origin Origin, prompt string) error {
		gotOrigin = origin
		gotPrompt = prompt
		return nil
	})

	// 1. Unauthenticated / invalid signature must return 401.
	payload := []byte(`{"action":"opened","repository":{"full_name":"acme/app-mobile"},"pull_request":{"title":"Fix auth","html_url":"https://example.com/pr/1"}}`)
	reqBad := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewReader(payload))
	reqBad.Header.Set("X-GitHub-Event", "pull_request")
	reqBad.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	recBad := httptest.NewRecorder()
	h.ServeHTTP(recBad, reqBad)
	if recBad.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad signature, got %d", recBad.Code)
	}

	// 2. Valid GitHub HMAC-SHA256 succeeds and wraps payload with WrapUntrustedData.
	mac := hmac.New(sha256.New, []byte(ghSecret))
	_, _ = mac.Write(payload)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	reqGH := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewReader(payload))
	reqGH.Header.Set("X-GitHub-Event", "pull_request")
	reqGH.Header.Set("X-GitHub-Delivery", "deliv-gh-1")
	reqGH.Header.Set("X-Hub-Signature-256", sig)
	recGH := httptest.NewRecorder()
	h.ServeHTTP(recGH, reqGH)
	if recGH.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for valid GitHub webhook, got %d body=%s", recGH.Code, recGH.Body.String())
	}
	if gotOrigin != OriginWebhook || !strings.Contains(gotPrompt, "[DADO_NAO_CONFIAVEL source=webhook:github target=pull_request") {
		t.Fatalf("expected wrapped untrusted webhook payload with OriginWebhook, got origin=%s prompt=%s", gotOrigin, gotPrompt)
	}

	// 3. Valid Standard Webhooks signature succeeds.
	gotPrompt = ""
	wh, err := standardwebhooks.NewWebhook(rawSecret)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	swPayload := []byte(`{"action":"completed","check_run":{"name":"ci-test","conclusion":"failure"}}`)
	swSig, err := wh.Sign("msg-sw-1", now, swPayload)
	if err != nil {
		t.Fatal(err)
	}
	reqSW := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewReader(swPayload))
	reqSW.Header.Set(standardwebhooks.HeaderWebhookID, "msg-sw-1")
	reqSW.Header.Set("webhook-timestamp", fmt.Sprintf("%d", now.Unix()))
	reqSW.Header.Set("webhook-signature", swSig)
	reqSW.Header.Set("X-GitHub-Event", "check_run")
	recSW := httptest.NewRecorder()
	h.ServeHTTP(recSW, reqSW)
	if recSW.Code != http.StatusAccepted || !strings.Contains(gotPrompt, "conclusion=failure") {
		t.Fatalf("expected valid standard-webhook acceptance, code=%d body=%s prompt=%s", recSW.Code, recSW.Body.String(), gotPrompt)
	}

	// 4. Empty secrets fail closed with 503.
	hOff := NewWebhookHandler(WebhookConfig{}, nil)
	recOff := httptest.NewRecorder()
	hOff.ServeHTTP(recOff, httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewReader(payload)))
	if recOff.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when webhook secret is unconfigured, got %d", recOff.Code)
	}
}
