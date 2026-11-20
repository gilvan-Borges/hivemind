package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

type jevRouteHintKey struct{}

type jevRouteHint struct {
	RunMode      string
	Step         string
	ObjectiveLen int
	InputTokens  int
	HasImages    bool
	NeedsTools   bool
	QAStall      int
	Workspace    string
	RepoKey      string
	Author       string
	Label        string
	Stage        string
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

// JevFactoryOptions configures the Runner.NewModel factory returned by NewJevModelFactory.
type JevFactoryOptions struct {
	Workspace  string
	Policy     *jev.Policy
	Catalog    *jev.CatalogStore
	Health     func() jev.HealthSnapshot
	BuildModel func(ctx context.Context, provider, modelName string) (model.LLM, error)
	Approve    func(string) bool
	OnEvent    func(Event)
}

// NewJevModelFactory returns a Runner.NewModel callback that wraps modelx.New
// with Jev Phase-1 routing, pin precedence, budget approval gating, and
// model_route event emission.
func NewJevModelFactory(opts JevFactoryOptions) func(ctx context.Context, a team.Agent) (model.LLM, error) {
	buildModel := opts.BuildModel
	if buildModel == nil {
		buildModel = modelx.New
	}

	var (
		initOnce sync.Once
		policy   jev.Policy
		repoPol  jev.RepoPolicy
		catalog  *jev.CatalogStore
		healthFn func() jev.HealthSnapshot
	)
	init := func(workspace string) {
		initOnce.Do(func() {
			if opts.Policy != nil {
				policy = *opts.Policy
			} else {
				loaded, err := jev.LoadPolicy()
				if err != nil {
					policy = jev.DefaultPolicy()
				} else {
					policy = loaded
				}
			}
			ws := strings.TrimSpace(workspace)
			if ws == "" {
				ws = strings.TrimSpace(opts.Workspace)
			}
			rp, _, _ := jev.LoadRepoPolicy(ws)
			repoPol = rp
			if opts.Catalog != nil {
				catalog = opts.Catalog
			} else {
				catalog = modelx.NewJevCatalogStore()
			}
			if opts.Health != nil {
				healthFn = opts.Health
			} else {
				healthFn = func() jev.HealthSnapshot {
					return jev.HealthSnapshot{
						Credentials:     modelx.JevCredentialPresence(),
						AgyRemainingPct: -1,
					}
				}
			}
		})
	}

	return func(ctx context.Context, a team.Agent) (model.LLM, error) {
		hint, _ := jevRouteHintFrom(ctx)
		ws := hint.Workspace
		if ws == "" {
			ws = opts.Workspace
		}
		init(ws)

		provNorm := team.NormalizeProvider(a.Provider, a.Model)
		// Opt-in / backward compatibility: when policy.Enabled is false and the
		// agent is not set to provider "jev", behave identically to modelx.New.
		if !policy.Enabled && provNorm != "jev" {
			return buildModel(ctx, a.Provider, a.Model)
		}

		role := team.NormalizeRole(a.Role)
		inTok := hint.InputTokens
		if inTok <= 0 {
			inTok = jev.EstimateFirstStepInputTokens(hint.ObjectiveLen, 0, true)
		}
		class, comp := jev.Classify(jev.ClassifyInput{
			Role:         role,
			RunMode:      hint.RunMode,
			Step:         hint.Step,
			ObjectiveLen: hint.ObjectiveLen,
			InputTokens:  inTok,
			HasImage:     hint.HasImages,
			QAStall:      hint.QAStall,
			NeedsTools:   hint.NeedsTools || role == team.RoleDeveloper || role == team.RoleQA,
		})
		task := jev.Task{
			Role:        role,
			RunMode:     hint.RunMode,
			Step:        hint.Step,
			Class:       class,
			Complexity:  comp,
			InputTokens: inTok,
			NeedsTools:  hint.NeedsTools || role == team.RoleDeveloper || role == team.RoleQA,
			NeedsVision: hint.HasImages,
			QAStall:     hint.QAStall,
			RepoKey:     hint.RepoKey,
		}

		router := &jev.Router{
			Catalog: catalog,
			Policy:  policy,
			Health:  healthFn,
		}
		choice, ref := router.Route(ctx, jev.RouteInput{
			Task:       task,
			AgentPin:   jev.AgentPin{Provider: provNorm, Model: a.Model},
			RepoPolicy: repoPol,
		})

		approveFn := hint.Approve
		if approveFn == nil {
			approveFn = opts.Approve
		}
		onEvent := hint.OnEvent
		if onEvent == nil {
			onEvent = opts.OnEvent
		}

		if ref.Code == "budget" && ref.Need != nil {
			prompt := fmt.Sprintf(
				"Jev: o passo de %s precisa de %s/%s (~US$ %.4f por chamada), acima do teto US$ %.2f. Autorizar?",
				jev.RedactSecrets(firstNonEmptyString(a.Name, role)),
				ref.Need.Provider,
				ref.Need.Model,
				float64(ref.Need.EstimatedMicroUSD)/1_000_000.0,
				policy.AutoEscalateUSD,
			)
			if approveFn == nil || !approveFn(prompt) {
				return nil, fmt.Errorf("roteamento Jev bloqueado pelo teto de orçamento: %s", jev.RedactSecrets(ref.Reason))
			}
			choice = *ref.Need
			choice.Reasons = []string{"budget_approved"}
		} else if ref.Code != "" {
			return nil, fmt.Errorf("roteamento Jev recusou (%s): %s", ref.Code, jev.RedactSecrets(ref.Reason))
		}

		if onEvent != nil {
			author := firstNonEmptyString(hint.Author, a.Name)
			label := firstNonEmptyString(hint.Label, a.Name)
			stage := firstNonEmptyString(hint.Stage, inferStage(author, label))
			text := formatModelRouteText(choice)
			onEvent(Event{
				Kind:              "model_route",
				Stage:             stage,
				Author:            jev.RedactSecrets(author),
				Label:             jev.RedactSecrets(label),
				Text:              jev.RedactSecrets(text),
				Provider:          choice.Provider,
				Model:             choice.Model,
				Tier:              string(choice.Tier),
				Reasons:           append([]string(nil), choice.Reasons...),
				EstimatedMicroUSD: choice.EstimatedMicroUSD,
				DecisionMicros:    choice.DecisionMicros,
			})
		}

		llm, err := buildModel(ctx, choice.Provider, choice.Model)
		if err != nil {
			return nil, fmt.Errorf("%s", jev.RedactSecrets(err.Error()))
		}
		return llm, nil
	}
}

func formatModelRouteText(c jev.Choice) string {
	reasonStr := strings.Join(c.Reasons, ", ")
	if c.Pinned {
		return fmt.Sprintf("Modelo fixado: %s/%s (%s)", c.Provider, c.Model, reasonStr)
	}
	return fmt.Sprintf("Jev escolheu %s/%s (faixa %s · %s)", c.Provider, c.Model, c.Tier, reasonStr)
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
