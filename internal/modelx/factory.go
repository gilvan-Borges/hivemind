// Package modelx builds ADK models for Gemini, Vertex, Anthropic and OpenAI.
package modelx

import (
	"context"
	"fmt"
	"os"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/anthropicmodel"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/model/openaimodel"
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
		return NewAgy("", modelName)
	case "opencode", "opencode-zen", "zen":
		return NewOpenCode(ctx, modelName)
	case "gemini", "vertex":
		return newGemini(ctx, provider, modelName)
	case "anthropic":
		return newAnthropic(ctx, modelName)
	case "openai", "openai-responses":
		return newOpenAI(ctx, provider, modelName)
	default:
		return nil, fmt.Errorf("provedor %q não é suportado; use agy, opencode, gemini, vertex, anthropic ou openai", provider)
	}
}

func newGemini(ctx context.Context, provider, modelName string) (model.LLM, error) {
	cfg := &genai.ClientConfig{}
	useVertex := provider == "vertex" || envTruthy("GOOGLE_GENAI_USE_VERTEXAI")
	if useVertex {
		project := strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT"))
		if project == "" {
			return nil, fmt.Errorf("defina GOOGLE_CLOUD_PROJECT para usar o Vertex AI")
		}
		location := strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_LOCATION"))
		if location == "" {
			location = "us-central1"
		}
		cfg.Backend = genai.BackendVertexAI
		cfg.Project = project
		cfg.Location = location
		return gemini.NewModel(ctx, modelName, cfg)
	}
	key := firstEnv("GOOGLE_API_KEY", "GEMINI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("defina GOOGLE_API_KEY (ou GEMINI_API_KEY) para usar o Gemini, ou troque o provedor para agy se o Antigravity CLI já estiver autenticado")
	}
	cfg.APIKey = key
	return gemini.NewModel(ctx, modelName, cfg)
}

func newAnthropic(ctx context.Context, modelName string) (model.LLM, error) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ANTHROPIC_BACKEND")), "vertex") {
		project := strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT"))
		if project == "" {
			return nil, fmt.Errorf("defina GOOGLE_CLOUD_PROJECT para Claude no Vertex AI")
		}
		location := strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_LOCATION"))
		if location == "" {
			location = "us-central1"
		}
		return anthropicmodel.NewModel(ctx, modelName, anthropicmodel.ClientConfig{
			Backend:  anthropicmodel.BackendVertex,
			Project:  project,
			Location: location,
		})
	}
	key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if key == "" {
		return nil, fmt.Errorf("defina ANTHROPIC_API_KEY para usar a API da Anthropic")
	}
	return anthropicmodel.NewModel(ctx, modelName, anthropicmodel.ClientConfig{
		Backend: anthropicmodel.BackendAnthropic,
		APIKey:  key,
	})
}

func newOpenAI(ctx context.Context, provider, modelName string) (model.LLM, error) {
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	base := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	if key == "" && base == "" {
		return nil, fmt.Errorf("defina OPENAI_API_KEY para usar a OpenAI")
	}
	api := openaimodel.APIChatCompletions
	if provider == "openai-responses" {
		api = openaimodel.APIResponses
	}
	return openaimodel.NewModel(ctx, modelName, &openaimodel.ClientConfig{
		APIKey:  key,
		BaseURL: base,
		API:     api,
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
