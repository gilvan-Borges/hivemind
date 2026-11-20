package jev

import (
	"context"
	"fmt"
	"math"
	"strings"
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
	targetTier := initialTierFor(task.Class, task.Complexity)
	best, estMicro, refusal := selectBestInTier(task, catalog, policy, health, repoPolicy, targetTier)
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
		} else if refusal.Code == "" {
			refusal = cheapRef
		}
	}
	if best == nil {
		if refusal.Code == "" {
			refusal = Refusal{Code: "none_fit", Reason: "nenhum modelo do catálogo atende aos filtros da tarefa"}
		}
		return Choice{}, refusal
	}

	reasons := buildReasons(task, *best, targetTier)
	return finish(Choice{
		Provider:          best.Provider,
		Model:             best.Model,
		Tier:              best.Tier,
		Reasons:           reasons,
		EstimatedMicroUSD: estMicro,
	}), Refusal{}
}

func isExplicitAgentPin(pin AgentPin) bool {
	p := strings.ToLower(strings.TrimSpace(pin.Provider))
	m := strings.TrimSpace(pin.Model)
	if p == "jev" {
		return false
	}
	if p == "" && (m == "" || strings.EqualFold(m, "auto")) {
		return false
	}
	return p != "" && m != ""
}

func initialTierFor(class Class, comp Complexity) Tier {
	switch class {
	case ClassCoding:
		if comp == ComplexityHigh {
			return TierMid
		}
		return TierCheap
	case ClassQA:
		if comp == ComplexityHigh {
			return TierMid
		}
		return TierCheap
	case ClassPlanning:
		if comp == ComplexityHigh {
			return TierMid
		}
		return TierCheap
	case ClassVision:
		return TierCheap
	default:
		return TierCheap
	}
}

func selectBestInTier(
	task Task,
	catalog Catalog,
	policy Policy,
	health HealthSnapshot,
	repoPolicy RepoPolicy,
	targetTier Tier,
) (*ModelEntry, int64, Refusal) {
	var sawAllowlist, sawCircuit, sawContext bool
	var overBudgetNeed *Choice
	var eligibleCount int
	var maxCost, maxLat float64

	autoCapMicroUSD := int64(math.Round(policy.AutoEscalateUSD * 1_000_000))
	minContext := int64(math.Ceil(float64(task.InputTokens) * 1.2))
	checkAgyQuota := health.AgyRemainingPct >= 0 && health.AgyRemainingPct < 5
	hasPaidHTTP := false
	hasPaidHTTPChecked := false

	// Pass 1: filter & compute normalization maxima with zero slice allocation.
	for i := range catalog.Entries {
		e := &catalog.Entries[i]
		if e.Tier != "" && e.Tier != targetTier {
			continue
		}
		if e.Tier == TierStrong {
			// strong never enters automatic selection without explicit escalation approval.
			continue
		}
		if !isCredentialed(e.Provider, e.Free, health) {
			continue
		}
		if health.ExcludePairs != nil && health.ExcludePairs[catalogKey(e.Provider, e.Model)] {
			continue
		}
		if !isPairCircuitEligible(e.Provider, e.Model, health) {
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
		if ref := checkDailyBudget(e.Provider, estMicro, policy, health); ref != nil {
			if overBudgetNeed == nil || estMicro < overBudgetNeed.EstimatedMicroUSD {
				cp := Choice{
					Provider:          e.Provider,
					Model:             e.Model,
					Tier:              tierForEst,
					Reasons:           []string{"budget_wait"},
					EstimatedMicroUSD: estMicro,
				}
				overBudgetNeed = &cp
			}
			continue
		}
		if autoCapMicroUSD > 0 && estMicro > autoCapMicroUSD {
			if overBudgetNeed == nil || estMicro < overBudgetNeed.EstimatedMicroUSD {
				cp := Choice{
					Provider:          e.Provider,
					Model:             e.Model,
					Tier:              tierForEst,
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
		eligibleCount++
		if estExact > maxCost {
			maxCost = estExact
		}
		if lat > maxLat {
			maxLat = lat
		}
	}

	if eligibleCount == 0 {
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

	// Pass 2: score eligible entries and pick the winner with zero allocations.
	w := policy.normalizedWeights()
	bestIdx := -1
	bestScore := math.Inf(-1)
	var bestCost, bestLat float64
	var bestEstMicro int64

	for i := range catalog.Entries {
		e := &catalog.Entries[i]
		if e.Tier != "" && e.Tier != targetTier {
			continue
		}
		if e.Tier == TierStrong {
			continue
		}
		if !isCredentialed(e.Provider, e.Free, health) || !isPairCircuitEligible(e.Provider, e.Model, health) {
			continue
		}
		if health.ExcludePairs != nil && health.ExcludePairs[catalogKey(e.Provider, e.Model)] {
			continue
		}
		if !isEntryAllowedByPolicy(*e, task.RepoKey, policy, repoPolicy) {
			continue
		}
		if task.InputTokens > 0 && e.ContextLength > 0 && e.ContextLength < minContext {
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
		if checkAgyQuota && strings.EqualFold(e.Provider, "agy") && hasPaidHTTP {
			continue
		}

		tierForEst := e.Tier
		if tierForEst == "" {
			tierForEst = targetTier
		}
		estMicro, estExact := estimateCallCost(task.InputTokens, *e, tierForEst)
		if checkDailyBudget(e.Provider, estMicro, policy, health) != nil {
			continue
		}
		if autoCapMicroUSD > 0 && estMicro > autoCapMicroUSD {
			continue
		}

		lat := float64(e.LatencyP50Ms)
		if lat <= 0 {
			lat = 1200
		}
		costNorm := 0.0
		if maxCost > 0 {
			costNorm = estExact / maxCost
		}
		latNorm := 0.0
		if maxLat > 0 {
			latNorm = lat / maxLat
		}
		score := w.Quality*e.Quality - w.Cost*costNorm - w.Latency*latNorm
		if bestIdx < 0 || score > bestScore+1e-12 ||
			(math.Abs(score-bestScore) <= 1e-12 && (estExact < bestCost || (estExact == bestCost && lat < bestLat))) {
			bestIdx = i
			bestScore = score
			bestCost = estExact
			bestLat = lat
			bestEstMicro = estMicro
		}
	}

	return &catalog.Entries[bestIdx], bestEstMicro, Refusal{}
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
	if health.Credentials[strings.ToLower(strings.TrimSpace(provider))] {
		return true
	}
	return free && health.Credentials["opencode_free"]
}

func modelIsFree(cat Catalog, provider, model string) bool {
	e, ok := cat.Lookup(provider, model)
	return ok && e.Free
}

func isCircuitEligible(provider string, health HealthSnapshot) bool {
	if health.Circuits == nil {
		return true
	}
	st, ok := health.Circuits[strings.ToLower(strings.TrimSpace(provider))]
	if !ok || st.Circuit == "" || st.Circuit == CircuitClosed {
		return true
	}
	return st.Circuit == CircuitHalfOpen && st.HalfOpenSlots > 0
}

func isPairCircuitEligible(provider, model string, health HealthSnapshot) bool {
	if !isCircuitEligible(provider, health) {
		return false
	}
	if health.ModelCircuits == nil {
		return true
	}
	st, ok := health.ModelCircuits[catalogKey(provider, model)]
	if !ok || st.Circuit == "" || st.Circuit == CircuitClosed {
		return true
	}
	return st.Circuit == CircuitHalfOpen && st.HalfOpenSlots > 0
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
	if targetTier == TierCheap || e.Tier == TierCheap {
		reasons = append(reasons, "cheap_default")
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
	Catalog *CatalogStore
	Policy  Policy
	Health  func() HealthSnapshot
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
	return Score(in.Task, cat, r.Policy, health, in.AgentPin, in.RepoPolicy)
}
