package modelx

import (
	"context"
	"os"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/jev"
)

// JevCredentialPresence checks which providers currently have credentials or
// local CLI binaries available (including accounts saved in Pool de Contas),
// returning only booleans (never key material).
func JevCredentialPresence() map[string]bool {
	bg := context.Background()
	ocKey, _, _ := LoadOpenCodeCredentials()
	_, cursorErr := LookCursor()
	cursorKey := strings.TrimSpace(resolveEnvForContext(bg, "cursor", "CURSOR_API_KEY")) != ""

	return map[string]bool{
		"typesafe":         strings.TrimSpace(resolveEnvForContext(bg, "typesafe", "TYPESAFE_API_KEY")) != "",
		"agy":              AgyAvailable(),
		"cursor":           cursorErr == nil && cursorKey,
		"opencode":         strings.TrimSpace(ocKey) != "",
		"opencode_free":    true,
		"openrouter":       strings.TrimSpace(resolveEnvForContext(bg, "openrouter", "OPENROUTER_API_KEY")) != "",
		"gemini":           strings.TrimSpace(resolveEnvForContext(bg, "gemini", "GOOGLE_API_KEY", "GEMINI_API_KEY")) != "",
		"vertex":           strings.TrimSpace(resolveEnvForContext(bg, "gemini", "GOOGLE_CLOUD_PROJECT")) != "",
		"anthropic":        strings.TrimSpace(resolveEnvForContext(bg, "anthropic", "ANTHROPIC_API_KEY")) != "" || strings.EqualFold(strings.TrimSpace(os.Getenv("ANTHROPIC_BACKEND")), "vertex"),
		"openai":           strings.TrimSpace(resolveEnvForContext(bg, "openai", "OPENAI_API_KEY")) != "" || strings.TrimSpace(resolveEnvForContext(bg, "openai", "OPENAI_BASE_URL")) != "",
		"openai-responses": strings.TrimSpace(resolveEnvForContext(bg, "openai", "OPENAI_API_KEY")) != "" || strings.TrimSpace(resolveEnvForContext(bg, "openai", "OPENAI_BASE_URL")) != "",
	}
}

// JevSourceFetchers returns non-blocking per-source catalog refreshers backed by
// the existing modelx HTTP and CLI catalog probes.
func JevSourceFetchers() map[string]jev.SourceFetcher {
	return map[string]jev.SourceFetcher{
		"openrouter": fetchJevOpenRouter,
		"opencode":   fetchJevOpenCode,
		"cursor":     fetchJevCursor,
		"agy":        fetchJevAgy,
	}
}

// NewJevCatalogStore builds a CatalogStore backed by catalog.json and modelx fetchers.
func NewJevCatalogStore() *jev.CatalogStore {
	return jev.NewCatalogStore("", JevSourceFetchers())
}

func fetchJevOpenRouter(ctx context.Context) ([]jev.ModelEntry, error) {
	list, err := OpenRouterModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]jev.ModelEntry, 0, len(list))
	for _, m := range list {
		if !m.SupportsTools || !jev.IsTrustedOpenRouterModel(m.ID) {
			continue
		}
		disp, curTier, curQual, baseDisc, _ := jev.CuratedModelMeta("openrouter", m.ID)
		if disp == "" {
			disp = m.Name
		}
		disc := m.DiscountPct
		if disc <= 0 && baseDisc > 0 {
			disc = baseDisc
		}
		out = append(out, jev.ModelEntry{
			Provider:           "openrouter",
			Model:              m.ID,
			DisplayName:        disp,
			ContextLength:      m.ContextLength,
			PromptMicroUSD:     m.PromptPrice * 1_000_000.0,
			CompletionMicroUSD: m.CompletionPrice * 1_000_000.0,
			DiscountPct:        disc,
			Quality:            curQual,
			Tier:               curTier,
			Capabilities: jev.Capabilities{
				Tools:  m.SupportsTools,
				Vision: m.SupportsVision,
				Stream: true,
			},
			DataClass: jev.DataClassHTTPAPI,
			Source:    "openrouter",
			Free:      strings.HasSuffix(strings.ToLower(m.ID), ":free"),
		})
	}
	// A resposta de /models é a lista de candidatos. Semente que a API não devolveu
	// (promo com slug antigo) não entra; sem rede, o catálogo estático continua valendo.
	return out, nil
}

func fetchJevOpenCode(ctx context.Context) ([]jev.ModelEntry, error) {
	_, goModels, zenModels := ProbeAndListOpenCode(ctx)
	out := make([]jev.ModelEntry, 0, len(goModels)+len(zenModels))
	for _, m := range goModels {
		if strings.TrimSpace(m.Slug) == "" {
			continue
		}
		out = append(out, jev.ModelEntry{
			Provider:      "opencode",
			Model:         "opencode-go:" + m.Slug,
			ContextLength: 128_000,
			Capabilities:  jev.Capabilities{Tools: true, Stream: true},
			DataClass:     jev.DataClassHTTPAPI,
			Tier:          inferOpenCodeTier(m.Slug),
			Source:        "opencode",
		})
	}
	for _, m := range zenModels {
		if strings.TrimSpace(m.Slug) == "" {
			continue
		}
		free := isOpenCodeFreeModel(m.Slug)
		tier := inferOpenCodeTier(m.Slug)
		if free {
			tier = jev.TierCheap
		}
		out = append(out, jev.ModelEntry{
			Provider:      "opencode",
			Model:         "opencode:" + m.Slug,
			ContextLength: 128_000,
			Capabilities:  jev.Capabilities{Tools: true, Stream: true},
			DataClass:     jev.DataClassHTTPAPI,
			Tier:          tier,
			Source:        "opencode",
			Free:          free,
		})
	}
	return out, nil
}

func inferOpenCodeTier(slug string) jev.Tier {
	low := strings.ToLower(strings.TrimSpace(slug))
	switch {
	case strings.Contains(low, "opus"):
		return jev.TierStrong
	case strings.Contains(low, "flash"), strings.Contains(low, "mini"), strings.Contains(low, "haiku"), strings.Contains(low, "nano"), strings.Contains(low, "pickle"):
		return jev.TierCheap
	default:
		return jev.TierMid
	}
}

func fetchJevCursor(ctx context.Context) ([]jev.ModelEntry, error) {
	list, _, err := ListCursorModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]jev.ModelEntry, 0, len(list))
	for _, m := range list {
		slug := strings.TrimSpace(m.Slug)
		if slug == "" || strings.Contains(slug, "/") {
			continue
		}
		tier := jev.TierMid
		if strings.EqualFold(slug, "auto") || strings.Contains(strings.ToLower(slug), "flash") || strings.Contains(strings.ToLower(slug), "mini") {
			tier = jev.TierCheap
		}
		out = append(out, jev.ModelEntry{
			Provider:      "cursor",
			Model:         slug,
			ContextLength: 200_000,
			Capabilities:  jev.Capabilities{Tools: false, Vision: true, WorkspaceAgent: true},
			DataClass:     jev.DataClassWorkspaceAgent,
			Tier:          tier,
			Source:        "cursor",
		})
	}
	return out, nil
}

func fetchJevAgy(ctx context.Context) ([]jev.ModelEntry, error) {
	list, err := ListAgyModels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]jev.ModelEntry, 0, len(list)+1)
	out = append(out, jev.ModelEntry{
		Provider:      "agy",
		Model:         "agy",
		ContextLength: 200_000,
		Capabilities:  jev.Capabilities{Tools: true, Vision: true},
		DataClass:     jev.DataClassCLIAccount,
		Tier:          jev.TierCheap,
		Source:        "agy",
	})
	for _, m := range list {
		slug := strings.TrimSpace(m.Slug)
		if slug == "" {
			continue
		}
		tier := jev.TierMid
		if strings.Contains(strings.ToLower(slug), "flash") || strings.Contains(strings.ToLower(slug), "low") {
			tier = jev.TierCheap
		}
		out = append(out, jev.ModelEntry{
			Provider:      "agy",
			Model:         "agy:" + slug,
			ContextLength: 200_000,
			Capabilities:  jev.Capabilities{Tools: true, Vision: true},
			DataClass:     jev.DataClassCLIAccount,
			Tier:          tier,
			Source:        "agy",
		})
	}
	return out, nil
}
