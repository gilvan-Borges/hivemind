package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
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
	Workspace     string
	Git           gitx.Git
	Policy        *jev.Policy
	Catalog       *jev.CatalogStore
	HealthStore   *jev.HealthStore
	Health        func() jev.HealthSnapshot
	Creds         func() map[string]bool
	BuildModel    func(ctx context.Context, provider, modelName string) (model.LLM, error)
	Approve       func(string) bool
	ApproveGate   func(gate, prompt string) bool
	OnEvent       func(Event)
	ExtraSecrets  []string
	DisableWrap   bool
}

// NewJevModelFactory returns a Runner.NewModel callback that wraps modelx.New
// with Jev routing, pin precedence, budget/worktree approval gating, circuit
// breaker health tracking, between-call failover, and model_route/failover events.
func NewJevModelFactory(opts JevFactoryOptions) func(ctx context.Context, a team.Agent) (model.LLM, error) {
	buildModel := opts.BuildModel
	if buildModel == nil {
		buildModel = modelx.New
	}

	var (
		initOnce    sync.Once
		policy      jev.Policy
		repoPol     jev.RepoPolicy
		catalog     *jev.CatalogStore
		healthStore *jev.HealthStore
		credsFn     func() map[string]bool
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
			if opts.HealthStore != nil {
				healthStore = opts.HealthStore
			} else if opts.Health == nil {
				healthStore = modelx.DefaultJevHealthStore()
			}
			if opts.Creds != nil {
				credsFn = opts.Creds
			} else {
				credsFn = modelx.JevCredentialPresence
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

		if healthStore != nil && opts.Creds == nil {
			healthStore.SyncCredentialFingerprints(modelx.JevCredentialFingerprints())
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

		approveFn := func(gate, prompt string) bool {
			if opts.ApproveGate != nil {
				return opts.ApproveGate(gate, prompt)
			}
			if hint.Approve != nil {
				return hint.Approve(prompt)
			}
			if opts.Approve != nil {
				return opts.Approve(prompt)
			}
			return false
		}
		onEvent := hint.OnEvent
		if onEvent == nil {
			onEvent = opts.OnEvent
		}
		author := firstNonEmptyString(hint.Author, a.Name)
		label := firstNonEmptyString(hint.Label, a.Name)
		stage := firstNonEmptyString(hint.Stage, inferStage(author, label))
		g := hint.Git
		if g == nil {
			g = opts.Git
		}
		workDir := firstNonEmptyString(hint.WorkDir, ws)

		exclude := map[string]bool{}
		agentPin := jev.AgentPin{Provider: provNorm, Model: a.Model}

		var (
			choice       jev.Choice
			firstChoice  jev.Choice
			firstErr     jev.ErrorClass
			llm          model.LLM
		)
		for attempt := 0; attempt < 3; attempt++ {
			healthFn := opts.Health
			if healthFn == nil {
				creds := credsFn()
				healthFn = func() jev.HealthSnapshot {
					if healthStore != nil {
						return healthStore.Snapshot(creds, exclude)
					}
					return jev.HealthSnapshot{Credentials: creds, ExcludePairs: exclude, AgyRemainingPct: -1}
				}
			}

			router := &jev.Router{
				Catalog: catalog,
				Policy:  policy,
				Health:  healthFn,
			}
			var ref jev.Refusal
			choice, ref = router.Route(ctx, jev.RouteInput{
				Task:       task,
				AgentPin:   agentPin,
				RepoPolicy: repoPol,
			})

			if ref.Code == "budget" && ref.Need != nil {
				prompt := fmt.Sprintf(
					"Jev: o passo de %s precisa de %s/%s (~US$ %.4f por chamada), acima do teto US$ %.2f. Autorizar?",
					jev.RedactSecrets(firstNonEmptyString(a.Name, role), opts.ExtraSecrets...),
					ref.Need.Provider,
					ref.Need.Model,
					float64(ref.Need.EstimatedMicroUSD)/1_000_000.0,
					policy.AutoEscalateUSD,
				)
				if !approveFn("budget", prompt) {
					return nil, fmt.Errorf("roteamento Jev bloqueado pelo teto de orçamento: %s", jev.RedactSecrets(ref.Reason, opts.ExtraSecrets...))
				}
				choice = *ref.Need
				choice.Reasons = []string{"budget_approved"}
			} else if ref.Code != "" {
				if attempt > 0 && onEvent != nil {
					var sum []jev.ProviderHealthView
					if healthStore != nil {
						sum = healthStore.Summary(policy, credsFn())
					}
					onEvent(Event{
						Kind:   "provider_exhausted",
						Stage:  stage,
						Author: jev.RedactSecrets(author, opts.ExtraSecrets...),
						Label:  jev.RedactSecrets(label, opts.ExtraSecrets...),
						Text:   fmt.Sprintf("Todos os provedores estão esgotados ou indisponíveis (%s): %s", ref.Code, jev.RedactSecrets(ref.Reason, opts.ExtraSecrets...)),
						Health: sum,
					})
				}
				return nil, fmt.Errorf("roteamento Jev recusou (%s): %s", ref.Code, jev.RedactSecrets(ref.Reason, opts.ExtraSecrets...))
			}

			if healthStore != nil && !healthStore.TryAcquireHalfOpen(choice.Provider, choice.Model) {
				exclude[pairKey(choice.Provider, choice.Model)] = true
				agentPin = jev.AgentPin{}
				continue
			}

			built, err := buildModel(ctx, choice.Provider, choice.Model)
			if err != nil {
				buildClass := jev.ClassifyError(err)
				if buildClass == jev.ErrClassPolicy || ctx.Err() != nil {
					return nil, fmt.Errorf("%s", jev.RedactSecrets(err.Error(), opts.ExtraSecrets...))
				}
				if healthStore != nil {
					healthStore.RecordFailure(choice.Provider, choice.Model, buildClass, time.Time{})
				}
				if attempt == 0 {
					firstChoice = choice
					firstErr = buildClass
				}
				// If buildModel fails due to auth/quota/outage, exclude this pair and try next healthy provider.
				exclude[pairKey(choice.Provider, choice.Model)] = true
				agentPin = jev.AgentPin{}
				if attempt == 2 {
					return nil, fmt.Errorf("%s", jev.RedactSecrets(err.Error(), opts.ExtraSecrets...))
				}
				continue
			}
			llm = built
			break
		}

		if llm == nil {
			msg := "Todos os provedores estão esgotados ou indisponíveis."
			if onEvent != nil {
				var sum []jev.ProviderHealthView
				if healthStore != nil {
					sum = healthStore.Summary(policy, credsFn())
				}
				onEvent(Event{
					Kind:   "provider_exhausted",
					Stage:  stage,
					Author: jev.RedactSecrets(author, opts.ExtraSecrets...),
					Label:  jev.RedactSecrets(label, opts.ExtraSecrets...),
					Text:   msg,
					Health: sum,
				})
			}
			return nil, fmt.Errorf("%s", msg)
		}

		if onEvent != nil {
			if firstChoice.Provider != "" {
				var sum []jev.ProviderHealthView
				if healthStore != nil {
					sum = healthStore.Summary(policy, credsFn())
				}
				failText := fmt.Sprintf("%s respondeu %s em %s. Seguindo em %s/%s.", firstChoice.Provider, firstErr, firstChoice.Model, choice.Provider, choice.Model)
				onEvent(Event{
					Kind:              "provider_failover",
					Stage:             stage,
					Author:            jev.RedactSecrets(author, opts.ExtraSecrets...),
					Label:             jev.RedactSecrets(label, opts.ExtraSecrets...),
					Text:              jev.RedactSecrets(failText, opts.ExtraSecrets...),
					Provider:          choice.Provider,
					Model:             choice.Model,
					FromProvider:      firstChoice.Provider,
					FromModel:         firstChoice.Model,
					Tier:              string(choice.Tier),
					Reasons:           append([]string{"circuit_open"}, choice.Reasons...),
					EstimatedMicroUSD: choice.EstimatedMicroUSD,
					DecisionMicros:    choice.DecisionMicros,
					Health:            sum,
				})
			}
			text := formatModelRouteText(choice)
			onEvent(Event{
				Kind:              "model_route",
				Stage:             stage,
				Author:            jev.RedactSecrets(author, opts.ExtraSecrets...),
				Label:             jev.RedactSecrets(label, opts.ExtraSecrets...),
				Text:              jev.RedactSecrets(text, opts.ExtraSecrets...),
				Provider:          choice.Provider,
				Model:             choice.Model,
				Tier:              string(choice.Tier),
				Reasons:           append([]string(nil), choice.Reasons...),
				EstimatedMicroUSD: choice.EstimatedMicroUSD,
				DecisionMicros:    choice.DecisionMicros,
			})
		}

		if opts.DisableWrap || healthStore == nil {
			return llm, nil
		}

		return &jevModelWrapper{
			current:      llm,
			choice:       choice,
			taskBase:     task,
			policy:       policy,
			repoPol:      repoPol,
			catalog:      catalog,
			health:       healthStore,
			credsFn:      credsFn,
			buildModel:   buildModel,
			approve:      approveFn,
			onEvent:      onEvent,
			git:          g,
			workDir:      workDir,
			author:       author,
			label:        label,
			stage:        stage,
			extraSecrets: opts.ExtraSecrets,
		}, nil
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
