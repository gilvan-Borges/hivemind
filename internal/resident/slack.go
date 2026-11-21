package resident

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SlackConfig configures the optional Slack Events API connector.
type SlackConfig struct {
	SigningSecret string
	BotToken      string
	APIBaseURL    string
	MaxBodyBytes  int64
}

// NewSlackHandler handles Slack Events API (`url_verification` and `app_mention`),
// verifying `X-Slack-Signature` + timestamp replay window, wrapping channel text
// with WrapUntrustedData, and triggering the resident with OriginSlack.
func NewSlackHandler(cfg SlackConfig, trigger func(origin Origin, prompt, channel, threadTS string) error) http.Handler {
	secret := strings.TrimSpace(cfg.SigningSecret)
	maxBytes := cfg.MaxBodyBytes
	if maxBytes <= 0 {
		maxBytes = defaultWebhookMaxBodyBytes
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
			return
		}
		if secret == "" {
			http.Error(w, "conector Slack desativado (signing secret ausente)", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "corpo inválido", http.StatusBadRequest)
			return
		}
		if !verifySlackSignature(secret, r.Header.Get("X-Slack-Request-Timestamp"), r.Header.Get("X-Slack-Signature"), body, time.Now()) {
			http.Error(w, "assinatura Slack inválida", http.StatusUnauthorized)
			return
		}

		var env struct {
			Type      string `json:"type"`
			Challenge string `json:"challenge"`
			Event     struct {
				Type     string `json:"type"`
				User     string `json:"user"`
				Text     string `json:"text"`
				Channel  string `json:"channel"`
				TS       string `json:"ts"`
				ThreadTS string `json:"thread_ts"`
			} `json:"event"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}
		if env.Type == "url_verification" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": env.Challenge})
			return
		}
		if env.Type == "event_callback" && env.Event.Type == "app_mention" {
			target := env.Event.Channel
			if target == "" {
				target = "slack"
			}
			fenced := WrapUntrustedData("slack", target, env.Event.Text)
			prompt := fmt.Sprintf("Menção recebida no Slack (canal %s, usuário %s). Ações de escrita/shell/PR exigem aprovação HITL explícita:\n\n%s", target, env.Event.User, fenced)
			threadTS := env.Event.ThreadTS
			if threadTS == "" {
				threadTS = env.Event.TS
			}
			if trigger != nil {
				if err := trigger(OriginSlack, prompt, env.Event.Channel, threadTS); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}
		}
		w.WriteHeader(http.StatusOK)
	})
}

func verifySlackSignature(secret, tsHeader, sigHeader string, body []byte, now time.Time) bool {
	tsInt, err := strconv.ParseInt(strings.TrimSpace(tsHeader), 10, 64)
	if err != nil {
		return false
	}
	diff := now.Unix() - tsInt
	if diff < 0 {
		diff = -diff
	}
	if diff > 300 {
		return false
	}
	const prefix = "v0="
	if !strings.HasPrefix(sigHeader, prefix) {
		return false
	}
	sigHex := strings.TrimSpace(sigHeader[len(prefix):])
	gotSig, err := hex.DecodeString(sigHex)
	if err != nil || len(gotSig) != sha256.Size {
		return false
	}
	base := fmt.Sprintf("v0:%s:%s", strings.TrimSpace(tsHeader), string(body))
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(base))
	return hmac.Equal(gotSig, mac.Sum(nil))
}

// PostSlackReply posts a summary/PR reply to Slack using a safe HTTP client.
func PostSlackReply(ctx context.Context, cfg SlackConfig, channel, threadTS, text string) error {
	if strings.TrimSpace(cfg.BotToken) == "" {
		return fmt.Errorf("bot token Slack ausente")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	if baseURL == "" {
		baseURL = "https://slack.com"
	}
	payload, _ := json.Marshal(map[string]any{
		"channel":   channel,
		"thread_ts": threadTS,
		"text":      RedactSecrets(text),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/chat.postMessage", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+cfg.BotToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack chat.postMessage HTTP %d", resp.StatusCode)
	}
	return nil
}
