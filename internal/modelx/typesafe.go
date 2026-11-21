package modelx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// DefaultTypeSafeBaseURL is the official TypeSafe AI System One API endpoint.
	DefaultTypeSafeBaseURL = "https://api.typesafe.ai"
	// DefaultTypeSafeModel is the flagship Jev decision model served by TypeSafe AI.
	DefaultTypeSafeModel = "jev-latest"
)

// ResolveTypeSafeConfig resolves the TypeSafe API key and base URL from context/profile,
// environment variables (TYPESAFE_API_KEY / TYPESAFE_BASE_URL), or IDE api-keys.json.
func ResolveTypeSafeConfig(ctx context.Context) (apiKey, baseURL string) {
	apiKey = strings.TrimSpace(resolveEnvForContext(ctx, "typesafe", "TYPESAFE_API_KEY"))
	baseURL = strings.TrimSpace(resolveEnvForContext(ctx, "typesafe", "TYPESAFE_BASE_URL"))
	if baseURL == "" {
		baseURL = DefaultTypeSafeBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return apiKey, baseURL
}

// TypeSafeAvailable reports whether a TypeSafe AI API key is configured.
func TypeSafeAvailable(ctx context.Context) bool {
	k, _ := ResolveTypeSafeConfig(ctx)
	return k != ""
}

// ProbeTypeSafeJev calls GET /v1/models on TypeSafe AI to verify the token and list
// available Jev decision models (e.g. jev-latest).
func ProbeTypeSafeJev(ctx context.Context) ([]string, error) {
	key, base := ResolveTypeSafeConfig(ctx)
	if key == "" {
		return nil, fmt.Errorf("defina TYPESAFE_API_KEY para conectar à TypeSafe AI (console.typesafe.ai/keys)")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("conectar em %s/v1/models: %w", base, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	switch resp.StatusCode {
	case http.StatusOK:
		return parseTypeSafeModels(body), nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("TypeSafe AI rejeitou o token (HTTP %d): confira a chave gerada em https://console.typesafe.ai/keys", resp.StatusCode)
	case http.StatusPaymentRequired:
		return nil, fmt.Errorf("TypeSafe AI retornou HTTP 402: créditos insuficientes em https://console.typesafe.ai")
	default:
		return nil, fmt.Errorf("TypeSafe AI respondeu HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func parseTypeSafeModels(body []byte) []string {
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return []string{DefaultTypeSafeModel}
	}
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[strings.ToLower(id)] {
			return
		}
		seen[strings.ToLower(id)] = true
		out = append(out, id)
	}
	for _, d := range parsed.Data {
		add(d.ID)
	}
	for _, m := range parsed.Models {
		add(firstNonEmpty(m.ID, m.Name))
	}
	if len(out) == 0 {
		return []string{DefaultTypeSafeModel}
	}
	return out
}

// ClassifyWithTypeSafeJev calls TypeSafe AI's official System One endpoint
// (POST /v1/systemone, model "jev-latest") to classify task class and complexity
// in structured form without relying on OpenRouter or OpenCode.
func ClassifyWithTypeSafeJev(ctx context.Context, objective string) (string, error) {
	key, base := ResolveTypeSafeConfig(ctx)
	if key == "" {
		return "", fmt.Errorf("TYPESAFE_API_KEY não configurada")
	}
	obj := strings.TrimSpace(objective)
	if obj == "" {
		return "", fmt.Errorf("objetivo vazio")
	}
	if len(obj) > 2000 {
		obj = obj[:2000]
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	payload := map[string]any{
		"model": DefaultTypeSafeModel,
		"state": obj,
		"questions": map[string]any{
			"class": map[string]any{
				"type":         "choice",
				"instructions": "Which task category best fits this software engineering request?",
				"criteria": map[string]string{
					"trivial":  "Short confirmation, simple status check, or single minor tweak.",
					"planning": "Architecture design, planning, specification, triage, or roadmap.",
					"coding":   "Writing, fixing, refactoring, or editing source code.",
					"review":   "Code review, PR review, or diff inspection.",
					"qa":       "Running tests, validating behavior, or debugging failures.",
					"vision":   "Inspecting screenshots, UI mockups, or image attachments.",
				},
			},
			"complexity": map[string]any{
				"type":         "choice",
				"instructions": "What is the complexity level of this request?",
				"criteria": map[string]string{
					"low":    "Single focused edit or quick question.",
					"medium": "Moderate feature or multi-file change.",
					"high":   "Complex architecture, cross-cutting refactor, or deep investigation.",
				},
			},
		},
	}

	rawReq, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, base+"/v1/systemone", bytes.NewReader(rawReq))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 4 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("TypeSafe System One falhou: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("TypeSafe System One retornou HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Answers struct {
			Class struct {
				Choice string `json:"choice"`
			} `json:"class"`
			Complexity struct {
				Choice string `json:"choice"`
			} `json:"complexity"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decodificar resposta TypeSafe System One: %w", err)
	}
	cls := strings.ToLower(strings.TrimSpace(parsed.Answers.Class.Choice))
	comp := strings.ToLower(strings.TrimSpace(parsed.Answers.Complexity.Choice))
	if cls == "" || comp == "" {
		return "", fmt.Errorf("resposta TypeSafe sem class/complexity válidos")
	}
	return cls + " " + comp, nil
}
