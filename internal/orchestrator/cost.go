package orchestrator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// RoleCost aggregates accumulated USD cost for a canonical squad role.
type RoleCost struct {
	Role         string `json:"role"`
	Calls        int    `json:"calls"`
	TotalTokens  int64  `json:"totalTokens"`
	CostMicroUSD int64  `json:"costMicroUsd"`
}

// AgentCost aggregates accumulated USD cost for one agent in a run.
type AgentCost struct {
	Agent        string `json:"agent"`
	Role         string `json:"role"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	Calls        int    `json:"calls"`
	TotalTokens  int64  `json:"totalTokens"`
	CostMicroUSD int64  `json:"costMicroUsd"`
}

// CanonicalCostRole normalizes an ADK author or display label into Chefe, Dev, QA or Outro.
func CanonicalCostRole(author, label string) string {
	raw := strings.ToLower(strings.TrimSpace(author + " " + label))
	switch {
	case strings.Contains(raw, "qa") || strings.Contains(raw, "revisor"):
		return "QA"
	case strings.HasPrefix(strings.ToLower(strings.TrimSpace(author)), "dev") ||
		strings.Contains(raw, "desenvolvedor") || strings.Contains(raw, string(team.RoleDeveloper)):
		return "Dev"
	case strings.Contains(raw, "pm") || strings.Contains(raw, "chief") || strings.Contains(raw, "chefe") ||
		strings.Contains(raw, "architect") || strings.Contains(raw, "arquiteto") ||
		strings.Contains(raw, "tech_lead") || strings.Contains(raw, "líder") || strings.Contains(raw, "gerente") ||
		strings.Contains(raw, "closing"):
		return "Chefe"
	default:
		return "Outro"
	}
}

func (r *Runner) lookupCatalogEntry(prov, mod string) (jev.ModelEntry, bool) {
	p := team.NormalizeProvider(prov, mod)
	m := strings.TrimSpace(mod)
	if r.Catalog != nil {
		if entry, ok := r.Catalog.Snapshot().Lookup(p, m); ok {
			return entry, true
		}
	}
	if entry, ok := jev.DefaultStaticCatalog().Lookup(p, m); ok {
		return entry, true
	}
	return jev.ModelEntry{}, false
}

func (r *Runner) recordCallCost(author, label, prov, mod string, inTok, outTok, cachedRead int64, disc int) (costMicro int64, role string) {
	role = CanonicalCostRole(author, label)
	if entry, ok := r.lookupCatalogEntry(prov, mod); ok {
		costMicro = jev.ComputeCostMicroUSD(entry, inTok, outTok, cachedRead, disc)
	}
	totTok := inTok + outTok
	agentKey := strings.TrimSpace(author)
	if agentKey == "" {
		agentKey = strings.TrimSpace(label)
	}
	if agentKey == "" {
		agentKey = role
	}

	r.costMu.Lock()
	defer r.costMu.Unlock()
	r.turnCostMicroUSD += costMicro

	if r.roleCosts == nil {
		r.roleCosts = map[string]*RoleCost{}
	}
	rc := r.roleCosts[role]
	if rc == nil {
		rc = &RoleCost{Role: role}
		r.roleCosts[role] = rc
	}
	rc.Calls++
	rc.TotalTokens += totTok
	rc.CostMicroUSD += costMicro

	if r.agentCosts == nil {
		r.agentCosts = map[string]*AgentCost{}
	}
	ac := r.agentCosts[agentKey]
	if ac == nil {
		ac = &AgentCost{Agent: agentKey, Role: role}
		r.agentCosts[agentKey] = ac
	}
	if prov != "" {
		ac.Provider = prov
	}
	if mod != "" {
		ac.Model = mod
	}
	ac.Calls++
	ac.TotalTokens += totTok
	ac.CostMicroUSD += costMicro
	return costMicro, role
}

func (r *Runner) populateUsageMetrics(res *Result) {
	if res == nil {
		return
	}
	res.InputTokens = r.turnInputTokens
	res.OutputTokens = r.turnOutputTokens
	res.TotalTokens = r.turnTotalTokens
	res.CachedReadTokens = r.turnCachedRead

	r.costMu.Lock()
	defer r.costMu.Unlock()
	res.CostMicroUSD = r.turnCostMicroUSD

	roleOrder := map[string]int{"Chefe": 0, "Dev": 1, "QA": 2, "Outro": 3}
	if len(r.roleCosts) > 0 {
		roles := make([]RoleCost, 0, len(r.roleCosts))
		for _, rc := range r.roleCosts {
			if rc != nil {
				roles = append(roles, *rc)
			}
		}
		sort.Slice(roles, func(i, j int) bool {
			oi, oki := roleOrder[roles[i].Role]
			oj, okj := roleOrder[roles[j].Role]
			if oki && okj && oi != oj {
				return oi < oj
			}
			return roles[i].Role < roles[j].Role
		})
		res.CostByRole = roles
	}

	if len(r.agentCosts) > 0 {
		agents := make([]AgentCost, 0, len(r.agentCosts))
		for _, ac := range r.agentCosts {
			if ac != nil {
				agents = append(agents, *ac)
			}
		}
		sort.Slice(agents, func(i, j int) bool {
			if agents[i].CostMicroUSD != agents[j].CostMicroUSD {
				return agents[i].CostMicroUSD > agents[j].CostMicroUSD
			}
			return agents[i].Agent < agents[j].Agent
		})
		res.CostByAgent = agents
	}
}

func formatRoleCostBreakdown(roles []RoleCost) string {
	var parts []string
	for _, rc := range roles {
		if rc.CostMicroUSD > 0 {
			parts = append(parts, fmt.Sprintf("%s %s", rc.Role, jev.FormatMicroUSD(rc.CostMicroUSD)))
		}
	}
	return strings.Join(parts, " · ")
}
