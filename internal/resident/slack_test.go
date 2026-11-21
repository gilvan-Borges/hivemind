package resident

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func signSlackReq(t *testing.T, secret string, ts int64, body []byte) http.Header {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(fmt.Sprintf("v0:%d:%s", ts, string(body))))
	h := http.Header{}
	h.Set("X-Slack-Request-Timestamp", fmt.Sprintf("%d", ts))
	h.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
	return h
}

func TestSlackHandler_AppMentionFencedAndPostReply(t *testing.T) {
	secret := "slack-signing-secret-test"
	var gotOrigin Origin
	var gotPrompt, gotChannel, gotTS string

	h := NewSlackHandler(SlackConfig{SigningSecret: secret}, func(origin Origin, prompt, channel, ts string) error {
		gotOrigin = origin
		gotPrompt = prompt
		gotChannel = channel
		gotTS = ts
		return nil
	})

	mentionBody := []byte(`{"type":"event_callback","event":{"type":"app_mention","user":"U123","channel":"C999","ts":"1700.1","text":"<@U01> resuma PR 42"}}`)

	// Expired timestamp (>5m) rejected.
	oldTS := time.Now().Add(-10 * time.Minute).Unix()
	reqOld := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/slack", bytes.NewReader(mentionBody))
	reqOld.Header = signSlackReq(t, secret, oldTS, mentionBody)
	recOld := httptest.NewRecorder()
	h.ServeHTTP(recOld, reqOld)
	if recOld.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on stale timestamp, got %d", recOld.Code)
	}

	// Valid app_mention fenced in WrapUntrustedData.
	nowTS := time.Now().Unix()
	reqOK := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/slack", bytes.NewReader(mentionBody))
	reqOK.Header = signSlackReq(t, secret, nowTS, mentionBody)
	recOK := httptest.NewRecorder()
	h.ServeHTTP(recOK, reqOK)
	if recOK.Code != http.StatusOK {
		t.Fatalf("expected 200 on valid mention, got %d", recOK.Code)
	}
	if gotOrigin != OriginSlack || gotChannel != "C999" || gotTS != "1700.1" {
		t.Fatalf("unexpected trigger metadata: origin=%s ch=%s ts=%s", gotOrigin, gotChannel, gotTS)
	}
	if !strings.Contains(gotPrompt, "[DADO_NAO_CONFIAVEL source=slack target=C999") {
		t.Fatalf("expected WrapUntrustedData fencing in Slack prompt, got:\n%s", gotPrompt)
	}

	// Verify PostSlackReply hits mock endpoint and redacts secrets.
	var received string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		received = buf.String()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := PostSlackReply(context.Background(), SlackConfig{BotToken: "xoxb-fake-token-test", APIBaseURL: srv.URL}, "C999", "1700.1", "PR aberto em safety sk-test-secret-000011112222"); err != nil {
		t.Fatalf("PostSlackReply failed: %v", err)
	}
	if strings.Contains(received, "sk-test-secret-000011112222") {
		t.Fatal("PostSlackReply must redact secrets before sending outbound reply")
	}
}
