package resident

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

const defaultWebhookMaxBodyBytes = 1 << 20 // 1 MiB

// WebhookConfig configures signature verification for incoming event-driven webhooks.
type WebhookConfig struct {
	StandardSecret string
	GitHubSecret   string
	MaxBodyBytes   int64
}

// NewWebhookHandler verifies incoming webhooks (Standard Webhooks HMAC headers or
// GitHub X-Hub-Signature-256) fail-closed, deduplicates delivery IDs, wraps the
// untrusted event payload in WrapUntrustedData, and dispatches trigger(OriginWebhook).
func NewWebhookHandler(cfg WebhookConfig, trigger func(origin Origin, prompt string) error) http.Handler {
	var (
		mu       sync.Mutex
		seenIDs  = map[string]time.Time{}
		maxBytes = cfg.MaxBodyBytes
	)
	if maxBytes <= 0 {
		maxBytes = defaultWebhookMaxBodyBytes
	}

	var stdVerifier *standardwebhooks.Webhook
	if s := strings.TrimSpace(cfg.StandardSecret); s != "" {
		if wh, err := standardwebhooks.NewWebhook(s); err == nil {
			stdVerifier = wh
		}
	}
	ghSecret := strings.TrimSpace(cfg.GitHubSecret)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método não permitido", http.StatusMethodNotAllowed)
			return
		}
		if stdVerifier == nil && ghSecret == "" {
			http.Error(w, "receptor de webhook desativado (segredo não configurado)", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "corpo inválido ou excede limite", http.StatusBadRequest)
			return
		}

		verified := false
		if stdVerifier != nil && r.Header.Get(standardwebhooks.HeaderWebhookID) != "" {
			if err := stdVerifier.Verify(body, r.Header); err == nil {
				verified = true
			}
		}
		if !verified && ghSecret != "" {
			if verifyGitHubSignature256(ghSecret, r.Header.Get("X-Hub-Signature-256"), body) {
				verified = true
			}
		}
		if !verified {
			http.Error(w, "assinatura de webhook inválida", http.StatusUnauthorized)
			return
		}

		deliveryID := strings.TrimSpace(r.Header.Get(standardwebhooks.HeaderWebhookID))
		if deliveryID == "" {
			deliveryID = strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
		}
		if deliveryID != "" {
			mu.Lock()
			now := time.Now().UTC()
			for k, ts := range seenIDs {
				if now.Sub(ts) > 15*time.Minute {
					delete(seenIDs, k)
				}
			}
			if _, dup := seenIDs[deliveryID]; dup {
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ok":true,"duplicate":true}`))
				return
			}
			seenIDs[deliveryID] = now
			mu.Unlock()
		}

		eventName := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
		if eventName == "" {
			eventName = "webhook_event"
		}
		summary := summarizeGitHubWebhookPayload(eventName, body)
		fenced := WrapUntrustedData("webhook:github", eventName, summary)
		prompt := fmt.Sprintf("Evento GitHub (%s) recebido por webhook. Analise e proponha próximos passos (ações de escrita/execução exigem aprovação humana):\n\n%s", eventName, fenced)

		if trigger != nil {
			if err := trigger(OriginWebhook, prompt); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true,"accepted":true}`))
	})
}

func verifyGitHubSignature256(secret, header string, payload []byte) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	sigBytes, err := hex.DecodeString(strings.TrimSpace(header[len(prefix):]))
	if err != nil || len(sigBytes) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	expected := mac.Sum(nil)
	return hmac.Equal(sigBytes, expected)
}

func summarizeGitHubWebhookPayload(eventName string, body []byte) string {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return strx.TruncateBytes(strings.TrimSpace(string(body)), 1500, "…")
	}
	action, _ := raw["action"].(string)
	repoName := nestedString(raw, "repository", "full_name")
	var b strings.Builder
	fmt.Fprintf(&b, "evento=%s", eventName)
	if action != "" {
		fmt.Fprintf(&b, " action=%s", action)
	}
	if repoName != "" {
		fmt.Fprintf(&b, " repo=%s", repoName)
	}
	if prTitle := nestedString(raw, "pull_request", "title"); prTitle != "" {
		fmt.Fprintf(&b, "\nPR: %s", prTitle)
	}
	if prURL := nestedString(raw, "pull_request", "html_url"); prURL != "" {
		fmt.Fprintf(&b, " (%s)", prURL)
	}
	if reviewState := nestedString(raw, "review", "state"); reviewState != "" {
		fmt.Fprintf(&b, "\nReview state=%s", reviewState)
	}
	if checkName := nestedString(raw, "check_run", "name"); checkName != "" {
		conc := nestedString(raw, "check_run", "conclusion")
		fmt.Fprintf(&b, "\nCheck: %s conclusion=%s", checkName, conc)
	}
	if suiteConc := nestedString(raw, "check_suite", "conclusion"); suiteConc != "" {
		fmt.Fprintf(&b, "\nCheckSuite conclusion=%s", suiteConc)
	}
	return strx.TruncateBytes(b.String(), 2048, "\n…")
}

func nestedString(m map[string]any, keys ...string) string {
	cur := any(m)
	for _, k := range keys {
		asMap, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = asMap[k]
	}
	s, _ := cur.(string)
	return strings.TrimSpace(s)
}
