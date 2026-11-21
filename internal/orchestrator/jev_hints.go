package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
)

type jevRouteHintKey struct{}

type jevRouteHint struct {
	RunMode      string
	Step         string
	ObjectiveLen int
	Objective    string
	InputTokens  int
	HasImages    bool
	NeedsTools   bool
	QAStall      int
	Workspace    string
	WorkDir      string
	RepoKey      string
	Author       string
	Label        string
	Stage        string
	Git          gitx.Git
	Approve      func(string) bool
	OnEvent      func(Event)
}

func withJevRouteHint(ctx context.Context, hint jevRouteHint) context.Context {
	return context.WithValue(ctx, jevRouteHintKey{}, hint)
}

func jevRouteHintFrom(ctx context.Context) (jevRouteHint, bool) {
	if ctx == nil {
		return jevRouteHint{}, false
	}
	h, ok := ctx.Value(jevRouteHintKey{}).(jevRouteHint)
	return h, ok
}

func classifyObjective(ctx context.Context, build func(context.Context, string, string) (model.LLM, error), injected func(context.Context, string) (string, error), objective string, useTypeSafe bool) (string, error) {
	if injected != nil {
		return injected(ctx, objective)
	}
	if strings.TrimSpace(objective) == "" {
		return "", nil
	}
	if useTypeSafe {
		if raw, err := modelx.ClassifyWithTypeSafeJev(ctx, objective); err == nil && strings.TrimSpace(raw) != "" {
			return raw, nil
		}
	}
	if build == nil {
		return "", nil
	}
	llm, err := build(ctx, "opencode", "deepseek-v4.1-flash")
	if err != nil {
		return "", err
	}
	return jev.CompleteClassifier(ctx, llm, objective)
}

func repoDeniesProvider(pol jev.RepoPolicy, provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" || provider == "jev" {
		return false
	}
	for _, denied := range pol.DenyProviders {
		if strings.EqualFold(strings.TrimSpace(denied), provider) {
			return true
		}
	}
	return false
}

func hasImageAttachments(atts []Attachment) bool {
	for _, att := range atts {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(att.MIMEType)), "image/") {
			return true
		}
	}
	return false
}

func hasImageContent(msg *genai.Content) bool {
	if msg == nil {
		return false
	}
	for _, p := range msg.Parts {
		if p != nil && p.InlineData != nil && strings.HasPrefix(strings.ToLower(strings.TrimSpace(p.InlineData.MIMEType)), "image/") {
			return true
		}
	}
	return false
}

func (r *Runner) resolveRepoKey(ctx context.Context) string {
	var remote string
	if r.Git != nil && r.Workspace != "" {
		if url, err := r.Git.RemoteURL(ctx, r.Workspace); err == nil {
			remote = url
		}
	}
	return jev.RepoKey(r.Workspace, remote)
}

func difficultyLabelPT(comp jev.Complexity) string {
	switch comp {
	case jev.ComplexityHigh:
		return "dificuldade alta"
	case jev.ComplexityMedium:
		return "dificuldade média"
	default:
		return "dificuldade baixa"
	}
}

func formatModelRouteText(c jev.Choice) string {
	reasonStr := strings.Join(c.Reasons, ", ")
	if c.Pinned {
		if c.DiscountPct > 0 {
			return fmt.Sprintf("Modelo fixado: %s/%s -%d%% (%s)", c.Provider, c.Model, c.DiscountPct, reasonStr)
		}
		return fmt.Sprintf("Modelo fixado: %s/%s (%s)", c.Provider, c.Model, reasonStr)
	}
	diffStr := difficultyLabelPT(c.Complexity)
	display := strings.TrimSpace(c.DisplayName)
	if c.DiscountPct > 0 {
		if display != "" {
			return fmt.Sprintf("Jev escolheu %s -%d%% (%s/%s · %s · faixa %s · %s)", display, c.DiscountPct, c.Provider, c.Model, diffStr, c.Tier, reasonStr)
		}
		return fmt.Sprintf("Jev escolheu %s/%s -%d%% (%s · faixa %s · %s)", c.Provider, c.Model, c.DiscountPct, diffStr, c.Tier, reasonStr)
	}
	if display != "" && !strings.EqualFold(display, c.Model) {
		return fmt.Sprintf("Jev escolheu %s (%s/%s · %s · faixa %s · %s)", display, c.Provider, c.Model, diffStr, c.Tier, reasonStr)
	}
	return fmt.Sprintf("Jev escolheu %s/%s (%s · faixa %s · %s)", c.Provider, c.Model, diffStr, c.Tier, reasonStr)
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
