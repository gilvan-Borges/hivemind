package modelx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/anthropicmodel"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/model/openaimodel"
	"google.golang.org/genai"
)

// OpenCodeDefaultBaseURL is the public OpenCode Zen API gateway URL.
const OpenCodeDefaultBaseURL = "https://opencode.ai/zen/v1"

// OpenCodeStatus reports token configuration and API reachability for OpenCode Zen.
type OpenCodeStatus struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source"` // "env", "config", or ""
	BaseURL    string `json:"baseUrl"`
	KeyMasked  string `json:"keyMasked"`
	ModelCount int    `json:"modelCount"`
	Detail     string `json:"detail"`
}

type storedCredentials struct {
	OpenCodeAPIKey  string `json:"opencode_api_key,omitempty"`
	OpenCodeBaseURL string `json:"opencode_base_url,omitempty"`
}

func credentialsFilePath() (string, error) {
	if base, err := os.UserConfigDir(); err == nil && strings.TrimSpace(base) != "" {
		return filepath.Join(base, "HiveMind", "credentials.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hivemind", "credentials.json"), nil
}

// LoadOpenCodeCredentials resolves the OpenCode Zen API key and base URL from environment
// variables (OPENCODE_API_KEY / OPENCODE_ZEN_API_KEY / OPENCODE_BASE_URL) or the local
// user credentials file (~/.config/HiveMind/credentials.json or %AppData%\HiveMind\credentials.json).
func LoadOpenCodeCredentials() (apiKey, baseURL, source string) {
	baseURL = strings.TrimSpace(os.Getenv("OPENCODE_BASE_URL"))

	if envKey := firstEnv("OPENCODE_API_KEY", "OPENCODE_ZEN_API_KEY"); envKey != "" {
		apiKey = envKey
		source = "env"
	}

	if path, err := credentialsFilePath(); err == nil {
		if data, err := os.ReadFile(path); err == nil {
			var creds storedCredentials
			if json.Unmarshal(data, &creds) == nil {
				if apiKey == "" && strings.TrimSpace(creds.OpenCodeAPIKey) != "" {
					apiKey = strings.TrimSpace(creds.OpenCodeAPIKey)
					source = "config"
				}
				if baseURL == "" && strings.TrimSpace(creds.OpenCodeBaseURL) != "" {
					baseURL = strings.TrimSpace(creds.OpenCodeBaseURL)
				}
			}
		}
	}

	if baseURL == "" {
		baseURL = OpenCodeDefaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return apiKey, baseURL, source
}

// SaveOpenCodeCredentials stores or clears the OpenCode Zen API token in the user config dir
// (outside project repositories so secrets are never committed to Git).
func SaveOpenCodeCredentials(apiKey, baseURL string) error {
	path, err := credentialsFilePath()
	if err != nil {
		return err
	}
	var creds storedCredentials
	if existing, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(existing, &creds)
	}
	creds.OpenCodeAPIKey = strings.TrimSpace(apiKey)
	cleanURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if cleanURL == "" || cleanURL == OpenCodeDefaultBaseURL {
		creds.OpenCodeBaseURL = ""
	} else {
		creds.OpenCodeBaseURL = cleanURL
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func maskAPIKey(key string) string {
	k := strings.TrimSpace(key)
	if k == "" {
		return ""
	}
	if len(k) <= 8 {
		return "****"
	}
	return k[:4] + "…" + k[len(k)-4:]
}

// StripOpenCodePrefix extracts the raw OpenCode Zen model ID from specs like
// "opencode:claude-sonnet-5-5" or "opencode/gpt-5.4".
func StripOpenCodePrefix(spec string) string {
	s := strings.TrimSpace(spec)
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "opencode:"):
		return strings.TrimSpace(s[len("opencode:"):])
	case strings.HasPrefix(lower, "opencode/"):
		return strings.TrimSpace(s[len("opencode/"):])
	case strings.HasPrefix(lower, "zen:"):
		return strings.TrimSpace(s[len("zen:"):])
	default:
		return s
	}
}

func isOpenCodeFreeModel(modelID string) bool {
	m := strings.ToLower(strings.TrimSpace(modelID))
	return strings.HasSuffix(m, "-free") || m == "big-pickle"
}

// openCodeProtocol classifies which wire protocol OpenCode Zen expects for modelID.
func openCodeProtocol(modelID string) string {
	m := strings.ToLower(strings.TrimSpace(modelID))
	switch {
	case strings.HasPrefix(m, "claude-"):
		return "anthropic"
	case strings.HasPrefix(m, "gemini-"):
		return "gemini"
	case strings.HasPrefix(m, "gpt-5"),
		strings.HasPrefix(m, "gpt-6"),
		strings.HasPrefix(m, "o1"),
		strings.HasPrefix(m, "o3"),
		strings.HasPrefix(m, "o4"):
		return "openai-responses"
	default:
		return "openai-chat"
	}
}

// NewOpenCode constructs an ADK model.LLM backed by the OpenCode Zen HTTP API using a
// portable API token (OPENCODE_API_KEY / credentials.json) — no local CLI required.
func NewOpenCode(ctx context.Context, modelName string) (model.LLM, error) {
	modelID := StripOpenCodePrefix(modelName)
	if modelID == "" || strings.EqualFold(modelID, "opencode") {
		modelID = "big-pickle"
	}
	apiKey, baseURL, _ := LoadOpenCodeCredentials()
	if apiKey == "" {
		if isOpenCodeFreeModel(modelID) {
			apiKey = "public"
		} else {
			return nil, fmt.Errorf("token do OpenCode não configurado: defina OPENCODE_API_KEY ou salve seu token na IDE (barra inferior → OpenCode)")
		}
	}

	rootBase := strings.TrimSuffix(baseURL, "/v1")
	switch openCodeProtocol(modelID) {
	case "anthropic":
		return anthropicmodel.NewModel(ctx, modelID, anthropicmodel.ClientConfig{
			Backend:   anthropicmodel.BackendAnthropic,
			BaseURL:   rootBase,
			APIKey:    apiKey,
			AuthToken: apiKey,
		})
	case "gemini":
		headers := http.Header{}
		headers.Set("Authorization", "Bearer "+apiKey)
		return gemini.NewModel(ctx, modelID, &genai.ClientConfig{
			APIKey:  apiKey,
			Backend: genai.BackendGeminiAPI,
			HTTPOptions: genai.HTTPOptions{
				BaseURL:    rootBase + "/",
				APIVersion: "v1",
				Headers:    headers,
			},
		})
	case "openai-responses":
		return openaimodel.NewModel(ctx, modelID, &openaimodel.ClientConfig{
			APIKey:  apiKey,
			BaseURL: baseURL,
			API:     openaimodel.APIResponses,
		})
	default:
		return openaimodel.NewModel(ctx, modelID, &openaimodel.ClientConfig{
			APIKey:  apiKey,
			BaseURL: baseURL,
			API:     openaimodel.APIChatCompletions,
		})
	}
}

type openCodeModelsResp struct {
	Data []struct {
		ID      string `json:"id"`
		OwnedBy string `json:"owned_by"`
	} `json:"data"`
}

// ProbeAndListOpenCode checks the configured OpenCode Zen token and fetches the live model
// catalog from GET <baseURL>/models over HTTPS (works anywhere without the opencode CLI).
func ProbeAndListOpenCode(ctx context.Context) (OpenCodeStatus, []AgyModel) {
	apiKey, baseURL, source := LoadOpenCodeCredentials()
	st := OpenCodeStatus{
		Configured: apiKey != "",
		Source:     source,
		BaseURL:    baseURL,
		KeyMasked:  maskAPIKey(apiKey),
	}

	reqCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	authBearer := apiKey
	if authBearer == "" {
		authBearer = "public"
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		st.Detail = fmt.Sprintf("URL inválida: %v", err)
		return st, nil
	}
	req.Header.Set("Authorization", "Bearer "+authBearer)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		st.Detail = fmt.Sprintf("Falha ao consultar OpenCode Zen: %v", err)
		return st, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		st.Detail = fmt.Sprintf("Erro lendo resposta OpenCode: %v", err)
		return st, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		st.Detail = fmt.Sprintf("OpenCode API retornou HTTP %d", resp.StatusCode)
		return st, nil
	}

	var parsed openCodeModelsResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		st.Detail = fmt.Sprintf("JSON inválido do OpenCode: %v", err)
		return st, nil
	}

	seen := make(map[string]bool, len(parsed.Data))
	models := make([]AgyModel, 0, len(parsed.Data))
	for _, item := range parsed.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, AgyModel{
			Slug: id,
			Name: id,
		})
	}
	sort.Slice(models, func(i, j int) bool {
		// Float free models or keep alphabetical cleanly by family
		return models[i].Slug < models[j].Slug
	})
	st.ModelCount = len(models)
	if st.Configured {
		if source == "env" {
			st.Detail = fmt.Sprintf("Token via OPENCODE_API_KEY (%s) · %d modelos", st.KeyMasked, len(models))
		} else {
			st.Detail = fmt.Sprintf("Token salvo (%s) · %d modelos", st.KeyMasked, len(models))
		}
	} else {
		st.Detail = fmt.Sprintf("Sem token (somente modelos -free disponíveis · %d no catálogo)", len(models))
	}
	return st, models
}
