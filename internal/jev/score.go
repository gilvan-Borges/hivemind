package jev

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// Score chooses the best eligible provider+model pair from catalog in pure Go
// (zero network, zero disk, zero subprocess), respecting pin precedence,
// allowlists, context windows, capabilities, and cost ceilings.
func Score(task Task, catalog Catalog, policy Policy, health HealthSnapshot, agentPin AgentPin, repoPolicy RepoPolicy) (Choice, Refusal) {
	start := time.Now()
	finish := func(c Choice) Choice {
		c.DecisionMicros = time.Since(start).Microseconds()
		if c.DecisionMicros == 0 {
			c.DecisionMicros = 1
		}
		return c
	}

	taskRole := team.NormalizeRole(task.Role)
	if scProv := parseScopedJevProvider(agentPin); scProv != "" && task.OnlyProvider == "" {
		task.OnlyProvider = scProv
	}

	// 1. Agent pin in .hivemind/team.yaml (highest precedence).
	if isExplicitAgentPin(agentPin) {
		prov := team.NormalizeProvider(agentPin.Provider, agentPin.Model)
		mod := strings.TrimSpace(agentPin.Model)
		if !isProviderAllowedByPolicy(prov, task.RepoKey, policy, repoPolicy) {
			return Choice{}, Refusal{
				Code:   "allowlist",
				Reason: fmt.Sprintf("provedor pinado %s bloqueado pela política/allowlist do repositório", prov),
			}
		}
		entry, inCatalog := catalog.Lookup(prov, mod)
		est := estimateCallMicroUSD(task.InputTokens, entry, TierCheap)
		if inCatalog {
			if ref := checkDailyBudget(prov, est, policy, health); ref != nil {
				return Choice{}, *ref
			}
		}
		return finish(Choice{
			Provider:          prov,
			Model:             mod,
			Tier:              tierOrDefault(entry.Tier, TierCheap),
			Reasons:           []string{"pin"},
			EstimatedMicroUSD: est,
			Pinned:            true,
		}), Refusal{}
	}

	// 2. Role pin in user policy.json.
	if rolePin, ok := policy.Pins[taskRole]; ok && strings.TrimSpace(rolePin.Provider) != "" && strings.TrimSpace(rolePin.Model) != "" {
		prov := team.NormalizeProvider(rolePin.Provider, rolePin.Model)
		mod := strings.TrimSpace(rolePin.Model)
		allowed := isProviderAllowedByPolicy(prov, task.RepoKey, policy, repoPolicy)
		excluded := health.ExcludePairs != nil && health.ExcludePairs[catalogKey(prov, mod)]
		healthy := !excluded && isCredentialed(prov, modelIsFree(catalog, prov, mod), health) && isPairCircuitEligible(prov, mod, health)
		entry, inCatalog := catalog.Lookup(prov, mod)
		est := estimateCallMicroUSD(task.InputTokens, entry, TierCheap)
		budgetOK := checkDailyBudget(prov, est, policy, health) == nil
		if allowed && healthy && inCatalog && budgetOK {
			return finish(Choice{
				Provider:          prov,
				Model:             mod,
				Tier:              tierOrDefault(entry.Tier, TierCheap),
				Reasons:           []string{"pin"},
				EstimatedMicroUSD: est,
				Pinned:            true,
			}), Refusal{}
		}
		if rolePin.Strict {
			code := "pin_down"
			if !allowed {
				code = "allowlist"
			} else if !budgetOK {
				code = "budget"
			} else if !isPairCircuitEligible(prov, mod, health) {
				code = "circuit"
			}
			return Choice{}, Refusal{
				Code:   code,
				Reason: fmt.Sprintf("pin estrito %s/%s indisponível (%s)", prov, mod, code),
			}
		}
	}

	// 3. Automatic selection within initial tier.
	if task.Class == "" {
		task.Class, task.Complexity = Classify(ClassifyInput{
			Role:        taskRole,
			RunMode:     task.RunMode,
			Step:        task.Step,
			InputTokens: task.InputTokens,
			HasImage:    task.NeedsVision,
			QAStall:     task.QAStall,
			NeedsTools:  task.NeedsTools,
		})
	}
	naturalTier := initialTierFor(task.Class, task.Complexity)
	targetTier := naturalTier
	deescalated := false
	if task.Learn != nil {
		if next, ok := task.Learn.DropTier(task.Class, naturalTier); ok && next != naturalTier {
			targetTier = next
			deescalated = true
		}
	}
	best, estMicro, refusal := selectBestInTier(task, catalog, policy, health, repoPolicy, targetTier)
	// shortcut: faixa abaixo sem candidato (e sem teto) volta à faixa natural, quando a de-escalada deixar a tarefa sem modelo.
	if deescalated && best == nil && refusal.Code != "budget" {
		deescalated = false
		targetTier = naturalTier
		best, estMicro, refusal = selectBestInTier(task, catalog, policy, health, repoPolicy, targetTier)
	}
	if best == nil && task.Class == ClassVision && targetTier == TierCheap && refusal.Code != "budget" {
		midBest, midEst, midRef := selectBestInTier(task, catalog, policy, health, repoPolicy, TierMid)
		if midBest != nil {
			best = midBest
			estMicro = midEst
			targetTier = TierMid
		} else if midRef.Code != "" {
			refusal = midRef
		}
	}
	if best == nil && targetTier == TierMid && refusal.Code != "budget" {
		// If mid tier has no eligible candidates (and wasn't blocked by budget),
		// fall back to cheap tier within budget.
		cheapBest, cheapEst, cheapRef := selectBestInTier(task, catalog, policy, health, repoPolicy, TierCheap)
		if cheapBest != nil {
			best = cheapBest
			estMicro = cheapEst
			targetTier = TierCheap
		} else if cheapRef.Code != "" {
			refusal = cheapRef
		}
	}
	// If a provider-scoped auto request (e.g. openrouter:auto / jev:openrouter) found no
	// eligible model on that provider (for instance because OpenRouter balance is <= $1.00
	// reserve floor, or circuit is open / out of credits), do NOT break: automatically
	// fall back across all other active providers (agy, cursor, opencode, gemini).
	var crossProviderFallbackReason string
	if best == nil && task.OnlyProvider != "" && refusal.Code != "budget" && refusal.Code != "allowlist" {
		prevOnly := strings.ToLower(strings.TrimSpace(task.OnlyProvider))
		fallbackTask := task
		fallbackTask.OnlyProvider = ""
		fbBest, fbEst, fbRef := selectBestInTier(fallbackTask, catalog, policy, health, repoPolicy, targetTier)
		if fbBest == nil && targetTier == TierMid && fbRef.Code != "budget" {
			fbBest, fbEst, fbRef = selectBestInTier(fallbackTask, catalog, policy, health, repoPolicy, TierCheap)
			if fbBest != nil {
				targetTier = TierCheap
			}
		}
		if fbBest != nil {
			best = fbBest
			estMicro = fbEst
			if prevOnly == "openrouter" && openRouterOutOfCredits(health) {
				crossProviderFallbackReason = "openrouter_sem_credito"
			} else if prevOnly == "openrouter" && isOpenRouterReserveLocked(policy, health) {
				crossProviderFallbackReason = "openrouter_reserva_1usd"
			} else {
				crossProviderFallbackReason = "fallback_" + prevOnly
			}
		}
	}
	// Budget guard (Spec 06 §4): o cap diário é INVIOLÁVEL sem aprovação
	// explícita. O bypass silencioso `ignoreDailyBudget: true` foi removido:
	// quando todos os candidatos excedem o teto, o Refusal "budget" sobe com o
	// Need mais barato e o chamador direciona ao fluxo approveFn("budget").
	// Restra só ao caso de reserva: OpenRouter ainda com créditos reais acima
	// de $0.05 é liberado IGNORANDO APENAS a reserva de $1 (nunca o cap).
	if best == nil && refusal.Code != "allowlist" && (!health.OpenRouterBalanceKnown || health.OpenRouterBalanceUSD > 0.05) && isOpenRouterReserveLocked(policy, health) {
		openTask := task
		openTask.OnlyProvider = ""
		if rb, re, rr := selectTierWithOpts(openTask, catalog, policy, health, repoPolicy, targetTier, tierSelectOpts{ignoreOpenRouterReserve: true}); rb != nil {
			best = rb
			estMicro = re
			refusal = rr
			crossProviderFallbackReason = "openrouter_last_resort"
		}
	}
	if best == nil {
		if refusal.Code == "" {
			refusal = Refusal{Code: "none_fit", Reason: "nenhum modelo do catálogo atende aos filtros da tarefa"}
		}
		return Choice{}, refusal
	}

	reasons := buildReasons(task, *best, targetTier)
	if deescalated {
		reasons = append(reasons, "deescalated")
	}
	if crossProviderFallbackReason != "" {
		reasons = append([]string{crossProviderFallbackReason}, reasons...)
	} else if openRouterOutOfCredits(health) && !strings.EqualFold(best.Provider, "openrouter") {
		reasons = append([]string{"openrouter_sem_credito"}, reasons...)
	} else if isOpenRouterReserveLocked(policy, health) && !strings.EqualFold(best.Provider, "openrouter") {
		reasons = append([]string{"openrouter_reserva_1usd"}, reasons...)
	}
	return finish(Choice{
		Provider:          best.Provider,
		Model:             best.Model,
		DisplayName:       best.DisplayName,
		Tier:              best.Tier,
		Complexity:        task.Complexity,
		DiscountPct:       best.DiscountPct,
		Reasons:           reasons,
		EstimatedMicroUSD: estMicro,
	}), Refusal{}
}

func isExplicitAgentPin(pin AgentPin) bool {
	p := strings.ToLower(strings.TrimSpace(pin.Provider))
	m := strings.TrimSpace(pin.Model)
	if p == "jev" || parseScopedJevProvider(pin) != "" {
		return false
	}
	if p == "" && (m == "" || strings.EqualFold(m, "auto")) {
		return false
	}
	return p != "" && m != ""
}

// parseScopedJevProvider extracts a provider constraint when an agent is configured
// with a Jev provider-scoped auto spec such as "jev:openrouter", "openrouter:auto",
// or (Provider: "openrouter", Model: "jev:auto").
func parseScopedJevProvider(pin AgentPin) string {
	p := strings.ToLower(strings.TrimSpace(pin.Provider))
	m := strings.ToLower(strings.TrimSpace(pin.Model))
	m = strings.ReplaceAll(m, "/", ":")
	switch {
	case strings.HasPrefix(m, "jev:"):
		sub := strings.TrimPrefix(m, "jev:")
		sub = strings.TrimSuffix(strings.TrimSuffix(sub, ":auto"), "-auto")
		if sub != "" && sub != "auto" && sub != "jev" {
			return team.NormalizeProvider(sub, "")
		}
		if p != "" && p != "jev" {
			return team.NormalizeProvider(p, "")
		}
	case m == "auto" || m == "jev":
		if p != "" && p != "jev" && p != "cursor" {
			return team.NormalizeProvider(p, "")
		}
	case strings.HasSuffix(m, ":auto") || strings.HasSuffix(m, ":jev"):
		prefix := strings.TrimSuffix(strings.TrimSuffix(m, ":auto"), ":jev")
		if prefix != "" && prefix != "jev" && prefix != "cursor" {
			return team.NormalizeProvider(prefix, "")
		}
	}
	return ""
}

func initialTierFor(class Class, comp Complexity) Tier {
	switch class {
	case ClassCoding, ClassPlanning, ClassReview:
		if comp == ComplexityHigh || comp == ComplexityMedium {
			return TierMid
		}
		return TierCheap
	case ClassQA:
		if comp == ComplexityHigh {
			return TierMid
		}
		return TierCheap
	case ClassVision:
		if comp == ComplexityHigh || comp == ComplexityMedium {
			return TierMid
		}
		return TierCheap
	default:
		return TierCheap
	}
}

func weightsForTask(policy Policy, task Task) Weights {
	base := policy.normalizedWeights()
	// Respect custom user weights if explicitly modified away from DefaultPolicy.
	isDefaultWeights := math.Abs(policy.Weights.Quality-0.35) < 1e-6 &&
		math.Abs(policy.Weights.Cost-0.50) < 1e-6 &&
		math.Abs(policy.Weights.Latency-0.15) < 1e-6
	if !isDefaultWeights {
		return base
	}
	switch task.Complexity {
	case ComplexityHigh:
		return Weights{Quality: 0.78, Cost: 0.12, Latency: 0.10}
	case ComplexityMedium:
		return Weights{Quality: 0.62, Cost: 0.23, Latency: 0.15}
	default:
		return base
	}
}

func openRouterOutOfCredits(health HealthSnapshot) bool {
	return health.OpenRouterBalanceKnown && health.OpenRouterBalanceUSD <= 0
}

func isOpenRouterReserveLocked(policy Policy, health HealthSnapshot) bool {
	if health.TypeSafeDecisionReady {
		// Jev decision engine runs on TypeSafe AI; OpenRouter no longer needs a $1.00 reserve.
		return false
	}
	if !health.OpenRouterBalanceKnown {
		return false
	}
	minRes := policy.OpenRouterMinReserveUSD
	if minRes <= 0 {
		minRes = 1.00
	}
	return health.OpenRouterBalanceUSD <= minRes
}

type tierSelectOpts struct {
	ignoreDailyBudget       bool
	ignoreOpenRouterReserve bool
}

func selectTierWithOpts(
	task Task,
	catalog Catalog,
	policy Policy,
	health HealthSnapshot,
	repoPolicy RepoPolicy,
	targetTier Tier,
	opts tierSelectOpts,
) (*ModelEntry, int64, Refusal) {
	best, est, ref := selectBestInTierWithOpts(task, catalog, policy, health, repoPolicy, targetTier, opts)
	if best == nil && targetTier == TierMid && ref.Code != "budget" {
		cheapBest, cheapEst, cheapRef := selectBestInTierWithOpts(task, catalog, policy, health, repoPolicy, TierCheap, opts)
		if cheapBest != nil {
			return cheapBest, cheapEst, cheapRef
		}
		if cheapRef.Code != "" {
			ref = cheapRef
		}
	}
	return best, est, ref
}

func selectBestInTier(
	task Task,
	catalog Catalog,
	policy Policy,
	health HealthSnapshot,
	repoPolicy RepoPolicy,
	targetTier Tier,
) (*ModelEntry, int64, Refusal) {
	return selectBestInTierWithOpts(task, catalog, policy, health, repoPolicy, targetTier, tierSelectOpts{})
}

func selectBestInTierWithOpts(
	task Task,
	catalog Catalog,
	policy Policy,
	health HealthSnapshot,
	repoPolicy RepoPolicy,
	targetTier Tier,
	opts tierSelectOpts,
) (*ModelEntry, int64, Refusal) {
	var sawAllowlist, sawCircuit, sawContext bool
	var overBudgetNeed *Choice
	var maxCost, maxLat float64

	autoCapMicroUSD := int64(math.Round(policy.AutoEscalateUSD * 1_000_000))
	minContext := int64(math.Ceil(float64(task.InputTokens) * 1.2))
	checkAgyQuota := health.AgyRemainingPct >= 0 && health.AgyRemainingPct < 5
	hasPaidHTTP := false
	hasPaidHTTPChecked := false

	hasCreds := health.Credentials != nil
	hasCircuits := len(health.Circuits) > 0
	hasExcludes := len(health.ExcludePairs) > 0
	hasModelCircuits := len(health.ModelCircuits) > 0

	// Pass Único de filtragem (Spec 06 §4): Pass 1/Pass 2 duplicavam ~100
	// linhas de filtros que podiam divergir; agora a filtragem roda UMA vez e
	// coleta os elegíveis num slice reutilizável (sync.Pool) que a pontuação
	// percorre.
	onlyProv := strings.ToLower(strings.TrimSpace(task.OnlyProvider))
	orReserveLocked := !opts.ignoreOpenRouterReserve && isOpenRouterReserveLocked(policy, health)
	checkBlockedInline := len(catalog.Entries) <= 64

	candidates := tierCandidatePool.Get().(*[]tierCandidate)
	buf := *candidates
	if cap(buf) < len(catalog.Entries) {
		buf = make([]tierCandidate, 0, len(catalog.Entries))
	}
	buf = buf[:0]
	defer func() {
		*candidates = buf[:0]
		tierCandidatePool.Put(candidates)
	}()

	for i := range catalog.Entries {
		e := &catalog.Entries[i]
		if onlyProv != "" && !strings.EqualFold(e.Provider, onlyProv) {
			continue
		}
		// Reserve rule: when OpenRouter balance is <= $1.00, do not indicate or select
		// OpenRouter so that $1.00 stays reserved for operating Jev itself; route to agy/cursor/others.
		if strings.EqualFold(e.Provider, "openrouter") && openRouterOutOfCredits(health) {
			sawCircuit = true
			continue
		}
		if orReserveLocked && strings.EqualFold(e.Provider, "openrouter") {
			sawCircuit = true
			continue
		}
		if checkBlockedInline && strings.EqualFold(e.Provider, "openrouter") && isBlockedOpenRouterEndpoint(e.Model) {
			continue
		}
		if e.Tier != "" && e.Tier != targetTier {
			// On high complexity tasks, also allow strong-tier models that have an active promotion discount.
			if !(task.Complexity == ComplexityHigh && targetTier == TierMid && e.Tier == TierStrong && e.DiscountPct > 0) {
				continue
			}
		}
		if e.Tier == TierStrong && !(task.Complexity == ComplexityHigh && e.DiscountPct > 0) {
			// strong never enters automatic selection without explicit escalation approval unless promoted on high complexity.
			continue
		}
		if hasCreds && !isCredentialed(e.Provider, e.Free, health) {
			continue
		}
		if hasExcludes && health.ExcludePairs[catalogKey(e.Provider, e.Model)] {
			continue
		}
		if (hasCircuits && !isEntryCircuitEligible(*e, health)) || (hasModelCircuits && !isModelCircuitOnlyEligible(e.Provider, e.Model, health)) {
			sawCircuit = true
			continue
		}
		if !isEntryAllowedByPolicy(*e, task.RepoKey, policy, repoPolicy) {
			sawAllowlist = true
			continue
		}
		if task.InputTokens > 0 && e.ContextLength > 0 && e.ContextLength < minContext {
			sawContext = true
			continue
		}
		if task.NeedsVision && !e.Capabilities.Vision {
			continue
		}
		if task.NeedsTools && !e.Capabilities.Tools && !e.Capabilities.WorkspaceAgent {
			continue
		}
		if (task.HasPendingToolCall || task.InStep) && e.Capabilities.WorkspaceAgent {
			continue
		}
		// Quota rule (§7): agy with < 5% remaining does not beat a cheap HTTP model.
		if checkAgyQuota && strings.EqualFold(e.Provider, "agy") {
			if !hasPaidHTTPChecked {
				hasPaidHTTP = hasAlternativeCheapHTTP(catalog, policy, health, repoPolicy, task, minContext, autoCapMicroUSD)
				hasPaidHTTPChecked = true
			}
			if hasPaidHTTP {
				continue
			}
		}

		tierForEst := e.Tier
		if tierForEst == "" {
			tierForEst = targetTier
		}
		estMicro, estExact := estimateCallCost(task.InputTokens, *e, tierForEst)
		if !opts.ignoreDailyBudget {
			if ref := checkDailyBudget(e.Provider, estMicro, policy, health); ref != nil {
				if overBudgetNeed == nil || estMicro < overBudgetNeed.EstimatedMicroUSD {
					cp := Choice{
						Provider:          e.Provider,
						Model:             e.Model,
						DisplayName:       e.DisplayName,
						Tier:              tierForEst,
						Complexity:        task.Complexity,
						DiscountPct:       e.DiscountPct,
						Reasons:           []string{"budget_wait"},
						EstimatedMicroUSD: estMicro,
					}
					overBudgetNeed = &cp
				}
				continue
			}
		}
		effectiveAutoCap := autoCapMicroUSD
		if effectiveAutoCap > 0 && e.DiscountPct > 0 {
			// Discounted models (e.g. -50% promotion) get proportional headroom under the auto-escalation ceiling.
			effectiveAutoCap = int64(float64(effectiveAutoCap) * (1.0 + float64(e.DiscountPct)/25.0))
		}
		if effectiveAutoCap > 0 && estMicro > effectiveAutoCap {
			if overBudgetNeed == nil || estMicro < overBudgetNeed.EstimatedMicroUSD {
				cp := Choice{
					Provider:          e.Provider,
					Model:             e.Model,
					DisplayName:       e.DisplayName,
					Tier:              tierForEst,
					Complexity:        task.Complexity,
					DiscountPct:       e.DiscountPct,
					Reasons:           []string{"budget_wait"},
					EstimatedMicroUSD: estMicro,
				}
				overBudgetNeed = &cp
			}
			continue
		}

		lat := float64(e.LatencyP50Ms)
		if lat <= 0 {
			lat = 1200
		}
		if estExact > maxCost {
			maxCost = estExact
		}
		if lat > maxLat {
			maxLat = lat
		}
		buf = append(buf, tierCandidate{entry: e, estMicro: estMicro, estExact: estExact, lat: lat})
	}

	if len(buf) == 0 {
		if overBudgetNeed != nil {
			return nil, 0, Refusal{
				Code:   "budget",
				Reason: fmt.Sprintf("próximo candidato (%s/%s, ~US$ %.4f) excede o teto sem aprovação", overBudgetNeed.Provider, overBudgetNeed.Model, float64(overBudgetNeed.EstimatedMicroUSD)/1_000_000.0),
				Need:   overBudgetNeed,
			}
		}
		if sawCircuit {
			return nil, 0, Refusal{Code: "circuit", Reason: "provedores elegíveis estão com circuito aberto"}
		}
		if sawAllowlist {
			return nil, 0, Refusal{Code: "allowlist", Reason: "provedores elegíveis bloqueados pela allowlist/política do repositório"}
		}
		if sawContext {
			return nil, 0, Refusal{Code: "none_fit", Reason: "nenhum modelo elegível comporta a janela de contexto estimada"}
		}
		return nil, 0, Refusal{Code: "none_fit", Reason: "nenhum modelo elegível com credencial ativa"}
	}

	// Pontuação: modelos gratuitos (Free) só entram como fallback quando nenhum
	// modelo pago/assinatura elegível está disponível com quota.
	hasPaid := false
	for i := range buf {
		if !buf[i].entry.Free {
			hasPaid = true
			break
		}
	}

	w := weightsForTask(policy, task)
	var best *tierCandidate
	bestScore := math.Inf(-1)

	for i := range buf {
		c := &buf[i]
		e := c.entry
		if hasPaid && e.Free {
			continue
		}
		costNorm := 0.0
		if maxCost > 0 {
			costNorm = c.estExact / maxCost
		}
		latNorm := 0.0
		if maxLat > 0 {
			latNorm = c.lat / maxLat
		}
		promoBonus := 0.0
		if e.DiscountPct > 0 {
			// Daily promotion priority boost: models on discount (e.g. -50% Google Carbon)
			// get priority within their difficulty tier.
			promoBonus = 0.22 * (float64(e.DiscountPct) / 50.0)
		}
		q := e.Quality
		if task.Learn != nil {
			q = task.Learn.Quality(e.Provider, e.Model, q)
		}
		score := w.Quality*q - w.Cost*costNorm - w.Latency*latNorm + promoBonus
		if best == nil || score > bestScore+1e-12 ||
			(math.Abs(score-bestScore) <= 1e-12 && (c.estExact < best.estExact || (c.estExact == best.estExact && c.lat < best.lat))) {
			best = c
			bestScore = score
		}
	}

	return best.entry, best.estMicro, Refusal{}
}

// tierCandidate is one entry that passed the single filter pass.
type tierCandidate struct {
	entry    *ModelEntry
	estMicro int64
	estExact float64
	lat      float64
}

// tierCandidatePool reuses the candidate slice across Score calls so the hot
// path does not allocate per routing decision.
var tierCandidatePool = sync.Pool{
	New: func() any {
		s := make([]tierCandidate, 0, 64)
		return &s
	},
}

func hasAlternativeCheapHTTP(
	catalog Catalog,
	policy Policy,
	health HealthSnapshot,
	repoPolicy RepoPolicy,
	task Task,
	minContext int64,
	autoCapMicroUSD int64,
) bool {
	for _, e := range catalog.Entries {
		if e.DataClass != DataClassHTTPAPI || e.Tier != TierCheap {
			continue
		}
		if !isCredentialed(e.Provider, e.Free, health) || !isCircuitEligible(e.Provider, health) {
			continue
		}
		if !isEntryAllowedByPolicy(e, task.RepoKey, policy, repoPolicy) {
			continue
		}
		if task.InputTokens > 0 && e.ContextLength > 0 && e.ContextLength < minContext {
			continue
		}
		if task.NeedsVision && !e.Capabilities.Vision {
			continue
		}
		if task.NeedsTools && !e.Capabilities.Tools {
			continue
		}
		estMicro := estimateCallMicroUSD(task.InputTokens, e, TierCheap)
		if autoCapMicroUSD > 0 && estMicro > autoCapMicroUSD {
			continue
		}
		if checkDailyBudget(e.Provider, estMicro, policy, health) != nil {
			continue
		}
		return true
	}
	return false
}

func estimateCallCost(inputTokens int, e ModelEntry, tier Tier) (int64, float64) {
	if inputTokens < 0 {
		inputTokens = 0
	}
	outTok := 2_000
	switch tier {
	case TierMid:
		outTok = 4_000
	case TierStrong:
		outTok = 8_000
	}
	exact := float64(inputTokens)*e.PromptMicroUSD + float64(outTok)*e.CompletionMicroUSD
	if exact <= 0 {
		return 0, 0
	}
	return int64(math.Round(exact)), exact
}

func estimateCallMicroUSD(inputTokens int, e ModelEntry, tier Tier) int64 {
	m, _ := estimateCallCost(inputTokens, e, tier)
	return m
}

func checkDailyBudget(provider string, estMicro int64, policy Policy, health HealthSnapshot) *Refusal {
	dailyUSD, ok := policy.DailyUSD[strings.ToLower(strings.TrimSpace(provider))]
	if !ok || dailyUSD <= 0 {
		return nil
	}
	capMicro := int64(math.Round(dailyUSD * 1_000_000))
	spent := health.DailySpentMicroUSD[strings.ToLower(strings.TrimSpace(provider))]
	if spent+estMicro > capMicro {
		return &Refusal{
			Code:   "budget",
			Reason: fmt.Sprintf("teto diário de US$ %.2f atingido para %s", dailyUSD, provider),
		}
	}
	return nil
}

func isCredentialed(provider string, free bool, health HealthSnapshot) bool {
	if health.Credentials == nil {
		return true
	}
	if health.Credentials[provider] {
		return true
	}
	if low := strings.ToLower(strings.TrimSpace(provider)); low != provider && health.Credentials[low] {
		return true
	}
	return free && health.Credentials["opencode_free"]
}

func modelIsFree(cat Catalog, provider, model string) bool {
	e, ok := cat.Lookup(provider, model)
	return ok && e.Free
}

func isEntryCircuitEligible(e ModelEntry, health HealthSnapshot) bool {
	if isCircuitEligible(e.Provider, health) {
		return true
	}
	// A free OpenCode Zen model (e.g. opencode:big-pickle) does not consume paid OpenCode Go
	// quota; if the opencode provider circuit is open solely due to quota exhaustion, keep
	// free Zen models eligible unless their own model circuit is open.
	if e.Free && strings.EqualFold(e.Provider, "opencode") && !strings.HasPrefix(strings.ToLower(e.Model), "opencode-go:") {
		st, ok := health.Circuits["opencode"]
		if ok && st.Reason == string(ErrClassQuota) {
			return true
		}
	}
	return false
}

func isCircuitEligible(provider string, health HealthSnapshot) bool {
	if len(health.Circuits) == 0 {
		return true
	}
	st, ok := health.Circuits[provider]
	if !ok {
		if low := strings.ToLower(strings.TrimSpace(provider)); low != provider {
			st, ok = health.Circuits[low]
		}
	}
	if !ok || st.Circuit == "" || st.Circuit == CircuitClosed {
		return true
	}
	return st.Circuit == CircuitHalfOpen && st.HalfOpenSlots > 0
}

func isPairCircuitEligible(provider, model string, health HealthSnapshot) bool {
	if !isCircuitEligible(provider, health) {
		return false
	}
	return isModelCircuitOnlyEligible(provider, model, health)
}

func isModelCircuitOnlyEligible(provider, model string, health HealthSnapshot) bool {
	if len(health.ModelCircuits) == 0 {
		return true
	}
	if st, ok := health.ModelCircuits[catalogKey(provider, model)]; ok && st.Circuit != "" && st.Circuit != CircuitClosed {
		if st.Circuit != CircuitHalfOpen || st.HalfOpenSlots <= 0 {
			return false
		}
	}
	for _, fk := range agyModelFamilyLookupKeys(provider, model) {
		if st, ok := health.ModelCircuits[catalogKey(provider, fk)]; ok && st.Circuit != "" && st.Circuit != CircuitClosed {
			if st.Circuit != CircuitHalfOpen || st.HalfOpenSlots <= 0 {
				return false
			}
		}
	}
	return true
}

func isProviderAllowedByPolicy(provider, repoKey string, policy Policy, repoPolicy RepoPolicy) bool {
	prov := strings.ToLower(strings.TrimSpace(provider))
	if policy.isProviderDenied(prov, repoKey, repoPolicy) {
		return false
	}
	dc := defaultDataClass(prov)
	return policy.isDataClassAllowed(dc)
}

func isEntryAllowedByPolicy(e ModelEntry, repoKey string, policy Policy, repoPolicy RepoPolicy) bool {
	if policy.isProviderDenied(e.Provider, repoKey, repoPolicy) {
		return false
	}
	dc := e.DataClass
	if dc == "" {
		dc = defaultDataClass(e.Provider)
	}
	return policy.isDataClassAllowed(dc)
}

func tierOrDefault(t, def Tier) Tier {
	if t != "" {
		return t
	}
	return def
}

func buildReasons(task Task, e ModelEntry, targetTier Tier) []string {
	var reasons []string
	if e.DiscountPct > 0 {
		reasons = append(reasons, fmt.Sprintf("promo_-%d%%", e.DiscountPct))
	}
	switch task.Complexity {
	case ComplexityHigh:
		reasons = append(reasons, "dificuldade_alta")
	case ComplexityMedium:
		reasons = append(reasons, "dificuldade_media")
	default:
		if targetTier == TierCheap || e.Tier == TierCheap {
			reasons = append(reasons, "cheap_default")
		}
	}
	if task.InputTokens > 0 && e.ContextLength > 0 {
		reasons = append(reasons, "context_fit")
	}
	if task.NeedsVision && e.Capabilities.Vision {
		reasons = append(reasons, "vision")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "cheap_default")
	}
	return reasons
}

// RouteInput groups the inputs for Router.Route.
type RouteInput struct {
	Task       Task
	AgentPin   AgentPin
	RepoPolicy RepoPolicy
}

// Router holds the in-memory catalog store, policy, and credential/health callbacks.
type Router struct {
	Catalog  *CatalogStore
	Policy   Policy
	Health   func() HealthSnapshot
	Outcomes *OutcomeLog
}

// Route triggers a non-blocking background refresh if stale and computes Choice
// strictly in memory within the hot-path latency budget.
func (r *Router) Route(ctx context.Context, in RouteInput) (Choice, Refusal) {
	if r.Catalog != nil {
		r.Catalog.RefreshIfStale(ctx)
	}
	var cat Catalog
	if r.Catalog != nil {
		cat = r.Catalog.Snapshot()
	} else {
		cat = DefaultStaticCatalog()
	}
	health := HealthSnapshot{AgyRemainingPct: -1}
	if r.Health != nil {
		health = r.Health()
	}
	if r.Outcomes != nil && in.Task.Learn == nil {
		in.Task.Learn = r.Outcomes.Snapshot(in.Task.RepoKey)
	}
	return Score(in.Task, cat, r.Policy, health, in.AgentPin, in.RepoPolicy)
}
