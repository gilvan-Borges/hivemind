// Package jev implements the Jev model router: task classification, cached
// model catalog, user/repo policy loading, pure-Go scoring, and local
// outcome learning (outcomes.jsonl).
package jev

import "time"

// Class identifies the workload category of an orchestration step.
type Class string

const (
	ClassTrivial  Class = "trivial"
	ClassPlanning Class = "planning"
	ClassCoding   Class = "coding"
	ClassReview   Class = "review"
	ClassQA       Class = "qa"
	ClassVision   Class = "vision"
)

// Complexity identifies the estimated task difficulty.
type Complexity string

const (
	ComplexityLow    Complexity = "low"
	ComplexityMedium Complexity = "medium"
	ComplexityHigh   Complexity = "high"
)

// Tier represents the cost/capability band of a model.
type Tier string

const (
	TierCheap  Tier = "cheap"
	TierMid    Tier = "mid"
	TierStrong Tier = "strong"
)

// DataClass classifies where repository context is sent for a provider.
type DataClass string

const (
	DataClassCLIAccount     DataClass = "cli_account"
	DataClassHTTPAPI        DataClass = "http_api"
	DataClassWorkspaceAgent DataClass = "workspace_agent"
)

// CircuitState represents the provider/model circuit breaker state.
type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"
	CircuitOpen     CircuitState = "open"
	CircuitHalfOpen CircuitState = "half_open"
)

// Task describes one routing request derived strictly from host signals.
type Task struct {
	Role               string
	RunMode            string
	Step               string
	Class              Class
	Complexity         Complexity
	InputTokens        int
	NeedsTools         bool
	NeedsVision        bool
	HasPendingToolCall bool
	InStep             bool
	QAStall            int
	RepoKey            string
	OnlyProvider       string
	// Learn is an in-memory prior built from outcomes.jsonl. Nil keeps the
	// catalog's static quality and the class's initial tier. Score never reads disk.
	Learn *LearnSnapshot `json:"-"`
}

// Capabilities describes what a model supports inside HiveMind.
type Capabilities struct {
	Tools          bool `json:"tools"`
	Vision         bool `json:"vision"`
	Stream         bool `json:"stream"`
	WorkspaceAgent bool `json:"workspaceAgent,omitempty"`
}

// ModelEntry is one provider+model pair in the local Jev catalog.
type ModelEntry struct {
	Provider           string       `json:"provider"`
	Model              string       `json:"model"`
	DisplayName        string       `json:"displayName,omitempty"`
	ContextLength      int64        `json:"contextLength"`
	PromptMicroUSD     float64      `json:"promptMicroUsd"`
	CompletionMicroUSD float64      `json:"completionMicroUsd"`
	DiscountPct        int          `json:"discountPct,omitempty"`
	Capabilities       Capabilities `json:"capabilities"`
	LatencyP50Ms       int64        `json:"latencyP50Ms"`
	Quality            float64      `json:"quality"`
	DataClass          DataClass    `json:"dataClass"`
	Tier               Tier         `json:"tier"`
	Source             string       `json:"source"`
	RefreshedAt        time.Time    `json:"refreshedAt,omitempty"`
	Free               bool         `json:"free,omitempty"`
}

// Catalog holds the in-memory model entries and per-source refresh timestamps.
type Catalog struct {
	Version int                  `json:"version"`
	Entries []ModelEntry         `json:"entries"`
	Sources map[string]time.Time `json:"sources,omitempty"`
}

// ProviderHealth summarizes the current eligibility and daily spend of a provider.
type ProviderHealth struct {
	Circuit       CircuitState `json:"circuit"`
	Until         time.Time    `json:"until,omitempty"`
	Reason        string       `json:"reason,omitempty"`
	HalfOpenSlots int          `json:"halfOpenSlots,omitempty"`
}

// ProviderHealthView is the secret-safe status item emitted to the IDE and mobile SSE.
type ProviderHealthView struct {
	Provider          string  `json:"provider"`
	State             string  `json:"state"` // "closed" | "open" | "half_open"
	Reason            string  `json:"reason,omitempty"`
	Until             string  `json:"until,omitempty"`
	DailySpentMicro   int64   `json:"dailySpentMicroUsd,omitempty"`
	DailyCapMicro     int64   `json:"dailyCapMicroUsd,omitempty"`
	QuotaRemainingPct float64 `json:"quotaRemainingPct,omitempty"`
	QuotaStale        bool    `json:"quotaStale,omitempty"`
	HasCredential     bool    `json:"hasCredential"`
}

// HealthSnapshot holds in-memory credential presence, circuit states, and daily spend.
type HealthSnapshot struct {
	Credentials        map[string]bool
	Circuits           map[string]ProviderHealth
	ModelCircuits      map[string]ProviderHealth
	ExcludePairs       map[string]bool
	DailySpentMicroUSD map[string]int64
	// AgyRemainingPct holds the last known remaining quota percentage for agy.
	// A negative value means unknown (still eligible).
	AgyRemainingPct float64
	// OpenRouterBalanceUSD holds the last known remaining credit balance in USD on OpenRouter
	// when OpenRouterBalanceKnown is true. When <= OpenRouterMinReserveUSD ($1.00), Jev stops
	// selecting OpenRouter to preserve the $1.00 operational reserve and routes to other
	// active providers (agy, cursor, opencode, gemini).
	OpenRouterBalanceUSD   float64
	OpenRouterBalanceKnown bool
}

// AgentPin represents an explicit model pin from .hivemind/team.yaml.
type AgentPin struct {
	Provider string
	Model    string
}

// Choice is the selected provider+model pair and routing rationale.
type Choice struct {
	Provider          string     `json:"provider"`
	Model             string     `json:"model"`
	DisplayName       string     `json:"displayName,omitempty"`
	Tier              Tier       `json:"tier"`
	Complexity        Complexity `json:"complexity,omitempty"`
	DiscountPct       int        `json:"discountPct,omitempty"`
	Reasons           []string   `json:"reasons"`
	EstimatedMicroUSD int64      `json:"estimatedMicroUsd"`
	DecisionMicros    int64      `json:"decisionMicros"`
	Pinned            bool       `json:"pinned,omitempty"`
}

// Refusal explains why no candidate could be chosen without further approval or change.
type Refusal struct {
	Code   string  `json:"code"` // "" | "none_fit" | "budget" | "allowlist" | "circuit" | "pin_down"
	Reason string  `json:"reason,omitempty"`
	Need   *Choice `json:"need,omitempty"`
}
