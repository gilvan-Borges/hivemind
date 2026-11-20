// Package modelx builds ADK models for Gemini, Vertex, Anthropic and OpenAI.
package modelx

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/anthropicmodel"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/model/openaimodel"
)

var (
	orAuthMu sync.Mutex
	orAuthOK map[string]time.Time
)

// New returns the ADK model for a provider id produced by team.NormalizeProvider.
func New(ctx context.Context, provider, modelName string) (model.LLM, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return nil, fmt.Errorf("modelo vazio")
	}
	switch provider {
	case "agy", "antigravity":
		return NewAgyForContext(ctx, "", modelName)
	case "opencode", "opencode-go", "opencode_go", "opencode-zen", "zen":
		return NewOpenCode(ctx, modelName)
	case "gemini", "vertex":
		return newGemini(ctx, provider, modelName)
	case "anthropic":
		return newAnthropic(ctx, modelName)
	case "openai", "openai-responses":
		return newOpenAI(ctx, provider, modelName)
	case "openrouter":
		return newOpenRouter(ctx, modelName)
	case "cursor":
		return NewCursor(ctx, modelName)
	default:
		return nil, fmt.Errorf("provedor %q não é suportado; use agy, opencode, gemini, vertex, anthropic, openai, openrouter ou cursor", provider)
	}
}

func newGemini(ctx context.Context, provider, modelName string) (model.LLM, error) {
	cfg := &genai.ClientConfig{}
	useVertex := provider == "vertex" || strings.EqualFold(resolveEnvForContext(ctx, "gemini", "GOOGLE_GENAI_USE_VERTEXAI"), "true") || envTruthy("GOOGLE_GENAI_USE_VERTEXAI")
	if useVertex {
		project := resolveEnvForContext(ctx, "gemini", "GOOGLE_CLOUD_PROJECT")
		if project == "" {
			return nil, fmt.Errorf("defina GOOGLE_CLOUD_PROJECT para usar o Vertex AI")
		}
		location := resolveEnvForContext(ctx, "gemini", "GOOGLE_CLOUD_LOCATION")
		if location == "" {
			location = "us-central1"
		}
		cfg.Backend = genai.BackendVertexAI
		cfg.Project = project
		cfg.Location = location
		cfg.HTTPClient = retryHTTPClient()
		return gemini.NewModel(ctx, modelName, cfg)
	}
	key := resolveEnvForContext(ctx, "gemini", "GOOGLE_API_KEY", "GEMINI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("defina GOOGLE_API_KEY (ou GEMINI_API_KEY) para usar o Gemini, ou troque o provedor para agy se o Antigravity CLI já estiver autenticado")
	}
	cfg.APIKey = key
	cfg.HTTPClient = retryHTTPClient()
	return gemini.NewModel(ctx, modelName, cfg)
}

func newAnthropic(ctx context.Context, modelName string) (model.LLM, error) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ANTHROPIC_BACKEND")), "vertex") {
		project := resolveEnvForContext(ctx, "gemini", "GOOGLE_CLOUD_PROJECT")
		if project == "" {
			return nil, fmt.Errorf("defina GOOGLE_CLOUD_PROJECT para Claude no Vertex AI")
		}
		location := resolveEnvForContext(ctx, "gemini", "GOOGLE_CLOUD_LOCATION")
		if location == "" {
			location = "us-central1"
		}
		return anthropicmodel.NewModel(ctx, modelName, anthropicmodel.ClientConfig{
			Backend:  anthropicmodel.BackendVertex,
			Project:  project,
			Location: location,
		})
	}
	key := resolveEnvForContext(ctx, "anthropic", "ANTHROPIC_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("defina ANTHROPIC_API_KEY para usar a API da Anthropic")
	}
	return anthropicmodel.NewModel(ctx, modelName, anthropicmodel.ClientConfig{
		Backend:    anthropicmodel.BackendAnthropic,
		APIKey:     key,
		HTTPClient: retryHTTPClient(),
	})
}

func newOpenAI(ctx context.Context, provider, modelName string) (model.LLM, error) {
	key := resolveEnvForContext(ctx, "openai", "OPENAI_API_KEY")
	base := resolveEnvForContext(ctx, "openai", "OPENAI_BASE_URL")
	if key == "" && base == "" {
		return nil, fmt.Errorf("defina OPENAI_API_KEY para usar a OpenAI")
	}
	api := openaimodel.APIChatCompletions
	if provider == "openai-responses" {
		api = openaimodel.APIResponses
	}
	return openaimodel.NewModel(ctx, modelName, &openaimodel.ClientConfig{
		APIKey:     key,
		BaseURL:    base,
		API:        api,
		HTTPClient: retryHTTPClient(),
	})
}

// openRouterCheckAuth preflights the API key against GET /key so a bad/revoked
// key fails immediately with a clear message instead of a raw 401 mid-run.
// Successful checks are cached in memory for a few minutes per key.
func openRouterCheckAuth(ctx context.Context, key, base string) error {
	orAuthMu.Lock()
	if t, ok := orAuthOK[key]; ok && time.Since(t) < 10*time.Minute {
		orAuthMu.Unlock()
		return nil
	}
	orAuthMu.Unlock()

	reqCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	url := strings.TrimRight(base, "/") + "/key"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("validar chave OpenRouter: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("validar chave OpenRouter em %s: %w (verifique OPENROUTER_BASE_URL e sua conexão)", url, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		orAuthMu.Lock()
		if orAuthOK == nil {
			orAuthOK = map[string]time.Time{}
		}
		orAuthOK[key] = time.Now()
		orAuthMu.Unlock()
		return nil
	case http.StatusUnauthorized:
		hint := "confira OPENROUTER_API_KEY em https://openrouter.ai/keys — a chave salva na IDE pode estar antiga/revogada; abra o menu do app → Chaves de API e salve a chave atual"
		if strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")) != "" {
			hint = "a variável de ambiente OPENROUTER_API_KEY está definida no sistema e TEM prioridade sobre a chave salva na IDE — e o OpenRouter a rejeitou; remova/corrija a variável de ambiente (ou salve a chave correta na IDE e remova a variável) em Configurações do Windows → Variáveis de Ambiente"
		}
		return fmt.Errorf("OpenRouter rejeitou a chave (HTTP 401 Unauthorized): %s", hint)
	case http.StatusForbidden:
		return fmt.Errorf("OpenRouter negou acesso (HTTP 403): a chave não tem permissão para esta conta/provedor")
	case http.StatusPaymentRequired:
		return fmt.Errorf("OpenRouter respondeu 402: créditos insuficientes ou cobrança pendente em https://openrouter.ai/settings/credits")
	default:
		return fmt.Errorf("OpenRouter respondeu HTTP %d ao validar a chave", resp.StatusCode)
	}
}

// Model IDs follow the vendor format (e.g. "anthropic/claude-sonnet-4-5",
// "openai/gpt-4.1", "google/gemini-2.5-pro", "deepseek/deepseek-chat").
func newOpenRouter(ctx context.Context, modelName string) (model.LLM, error) {
	key := resolveEnvForContext(ctx, "openrouter", "OPENROUTER_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("defina OPENROUTER_API_KEY para usar o OpenRouter (chave em https://openrouter.ai/keys); o modelo usa o formato fornecedor/modelo, ex.: anthropic/claude-sonnet-4-5")
	}
	base := resolveEnvForContext(ctx, "openrouter", "OPENROUTER_BASE_URL")
	if base == "" {
		base = "https://openrouter.ai/api/v1"
	}
	if err := openRouterCheckAuth(ctx, key, base); err != nil {
		return nil, err
	}
	return openaimodel.NewModel(ctx, modelName, &openaimodel.ClientConfig{
		APIKey:     key,
		BaseURL:    base,
		API:        openaimodel.APIChatCompletions,
		HTTPClient: retryHTTPClient(),
	})
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func envTruthy(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
