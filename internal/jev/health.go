package jev

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/fslockx"
)

// ErrorClass identifies the failure mode of a model call after provider-level retries.
type ErrorClass string

const (
	ErrClassNone       ErrorClass = ""
	ErrClassQuota      ErrorClass = "quota"
	ErrClassModelQuota ErrorClass = "model_quota" // cota/créditos do modelo e família no provedor — abre modelo+família por cooldown de quota
	ErrClassRateLimit  ErrorClass = "rate_limit"
	ErrClassAuth       ErrorClass = "auth"
	ErrClassOutage     ErrorClass = "outage"
	ErrClassSpike      ErrorClass = "spike"
	ErrClassModel      ErrorClass = "model"  // 400 de modelo/provedor incompatível — abre só o circuito do modelo
	ErrClassPolicy     ErrorClass = "policy" // jail, aprovação, pedido inválido para qualquer modelo — nunca abre circuito
)

// ClassifyError maps a provider error (already past internal/modelx retries)
// into a circuit-breaker ErrorClass per SPEC_JEV_ORQUESTRADOR §9.
func ClassifyError(err error) ErrorClass {
	if err == nil {
		return ErrClassNone
	}
	if errors.Is(err, context.Canceled) {
		return ErrClassPolicy
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrClassOutage
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return ErrClassOutage
	}

	// 1. 400 de modelo/provedor incompatível (incl. thought_signature ausente em proxy Gemini)
	// abre só o circuito do modelo e deixa o Jev tentar o próximo candidato.
	// Pedido inválido universal (jail, tool_call órfão, INVALID_ARGUMENT genérico) não faz failover.
	if isIncompatibleModel400(msg) {
		return ErrClassModel
	}
	if isRequestShapePolicy(msg) {
		return ErrClassPolicy
	}
	for _, pat := range []string{"http 400", "status 400", "400 bad request"} {
		if strings.Contains(msg, pat) {
			return ErrClassPolicy
		}
	}

	// 2. Quota / billing exhaustion -> opens entire provider.
	for _, pat := range []string{
		"402", "payment required", "créditos insuficientes", "insufficient credits",
		"resource_exhausted", "esgotou a quota", "quota/créditos", "quota exceeded",
		"insufficient_quota", "ai credits balance is too low", "credits balance is too low",
	} {
		if strings.Contains(msg, pat) {
			return ErrClassQuota
		}
	}

	// 3. Authentication / authorization -> opens entire provider until credential changes.
	for _, pat := range []string{
		"401", "403", "unauthorized", "forbidden", "rejeitou a chave",
		"não autenticado", "sem login", "invalid api key", "invalid_api_key",
		"authentication", "token do opencode não configurado", "defina cursor_api_key",
		"defina openrouter_api_key", "defina openai_api_key", "defina anthropic_api_key",
		"defina google_api_key", "defina google_cloud_project",
	} {
		if strings.Contains(msg, pat) {
			return ErrClassAuth
		}
	}

	// 4. Rate limit surviving provider retry -> opens model.
	for _, pat := range []string{
		"429", "too many requests", "rate limit", "rate_limit",
	} {
		if strings.Contains(msg, pat) {
			return ErrClassRateLimit
		}
	}

	// 5. Timeouts, 5xx, connection errors, CLI failures -> outage.
	return ErrClassOutage
}

// isRequestShapePolicy reports errors that would fail on every model, so failover
// must not resend the same request.
func isRequestShapePolicy(msg string) bool {
	for _, pat := range []string{
		"invalid_argument", "tool_call_id", "orphaned", "tool message",
		"recusado pela", "recusado fora", "fora do workspace", "jail",
		"política", "aprovação", "não permitido", "context canceled",
	} {
		if strings.Contains(msg, pat) {
			return true
		}
	}
	return false
}

// isIncompatibleModel400 reports a 400 that means this model or provider cannot
// serve the call (foreign id, unknown model, unsupported parameter).
func isIncompatibleModel400(msg string) bool {
	for _, pat := range []string{
		"modelo incompatível",
		"not a cursor model",
		"is not a cursor",
		"model not found",
		"unknown model",
		"invalid model",
		"not a valid model",
		"does not support",
		"não suporta",
		"unsupported parameter",
		"no endpoints found",
		"is not available",
		"not available for",
		"hosted outside",
		"select global at",
		"not available in your region",
		"thought_signature",
		"thought signature",
	} {
		if strings.Contains(msg, pat) {
			return true
		}
	}
	// openai-go esconde o corpo e deixa só "OpenAI API error: 400 Bad Request".
	// Esse resumo só é rejeição de modelo quando o adaptador Cursor já se
	// identificou. O mesmo texto vindo da OpenAI ou do OpenRouter continua
	// política: pode ser um pedido inválido que o próximo modelo repetiria.
	if !strings.Contains(msg, "openai api error: 400") {
		return false
	}
	return strings.Contains(msg, "cursor/") || strings.Contains(msg, "cursor cli") || strings.Contains(msg, "provedor cursor")
}

type outcomeSample struct {
	At time.Time `json:"at"`
	OK bool      `json:"ok"`
}

type circuitRecord struct {
	Circuit            CircuitState    `json:"circuit"`
	Reason             string          `json:"reason,omitempty"`
	FailureCount       int             `json:"failureCount,omitempty"`
	ConsecutiveOutages int             `json:"consecutiveOutages,omitempty"`
	OpenedAt           time.Time       `json:"openedAt,omitempty"`
	Until              time.Time       `json:"until,omitempty"`
	// HalfOpenLeaseUntil holds the TTL lease of the single half-open probe slot
	// (Spec 06 §3). Replaces the old HalfOpenInFlight boolean that leaked
	// forever when a probe died without a classifiable outcome.
	HalfOpenLeaseUntil time.Time       `json:"halfOpenLeaseUntil,omitempty"`
	Recent             []outcomeSample `json:"recent,omitempty"`
}

// halfOpenLeaseTTL bounds how long a claimed probe slot stays reserved when
// the probe never reports (panic, cancel, ErrClassPolicy early exit). The
// circuit self-heals after the TTL instead of staying wedged.
const halfOpenLeaseTTL = 2 * time.Minute

type providerStateRecord struct {
	circuitRecord
	QuotaRemainingPct float64   `json:"quotaRemainingPct"`
	QuotaResetsAt     time.Time `json:"quotaResetsAt,omitempty"`
	QuotaStale        bool      `json:"quotaStale,omitempty"`
	CredFingerprint   string    `json:"credFingerprint,omitempty"`
	DailyDate         string    `json:"dailyDate,omitempty"`
	DailySpentMicro   int64     `json:"dailySpentMicroUsd,omitempty"`
}

type healthFile struct {
	Version   int                             `json:"version"`
	Providers map[string]*providerStateRecord `json:"providers,omitempty"`
	Models    map[string]*circuitRecord       `json:"models,omitempty"`
}

// HealthStore manages process-wide provider/model circuit breakers, exponential backoff,
// UTC daily spend counters, and health.json persistence.
type HealthStore struct {
	mu       sync.Mutex
	path     string
	state    healthFile
	cooldown CooldownConfig
	nowFn    func() time.Time
	onChange func(opened bool, allClosed bool)

	// Spend write debounce (Spec 06 §3): the ledger is monotone, so it only
	// hits the disk once per recordSpendDebounce window; any circuit-changing
	// save flushes the pending delta immediately.
	lastSpendSave time.Time
	spendDirty   bool
}

// recordSpendDebounce bounds health.json rewrites caused by spend accounting.
const recordSpendDebounce = 2 * time.Second

// HealthPath returns the default user-level health.json path.
func HealthPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "health.json"), nil
}

// NewHealthStore loads persisted circuit/spend state from path (or default path)
// and configures cooldown durations.
func NewHealthStore(path string, cooldown CooldownConfig) *HealthStore {
	if path == "" {
		if def, err := HealthPath(); err == nil {
			path = def
		}
	}
	if cooldown == (CooldownConfig{}) {
		cooldown = DefaultPolicy().Cooldown
	}
	hs := &HealthStore{
		path:     path,
		cooldown: cooldown,
		nowFn:    time.Now,
		state: healthFile{
			Version:   1,
			Providers: map[string]*providerStateRecord{},
			Models:    map[string]*circuitRecord{},
		},
	}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var loaded healthFile
			if json.Unmarshal(data, &loaded) == nil {
				if loaded.Providers == nil {
					loaded.Providers = map[string]*providerStateRecord{}
				}
				if loaded.Models == nil {
					loaded.Models = map[string]*circuitRecord{}
				}
				for _, pr := range loaded.Providers {
					if pr.QuotaRemainingPct == 0 && !pr.QuotaStale && pr.Reason != "quota" {
						pr.QuotaRemainingPct = -1
					}
				}
				hs.state = loaded
			}
		}
	}
	return hs
}

// SetOnChange registers a callback invoked whenever a circuit opens or all circuits close.
func (h *HealthStore) SetOnChange(fn func(opened bool, allClosed bool)) {
	h.mu.Lock()
	h.onChange = fn
	h.mu.Unlock()
}

func (h *HealthStore) ensureProviderLocked(provider string, now time.Time) *providerStateRecord {
	p := strings.ToLower(strings.TrimSpace(provider))
	rec, ok := h.state.Providers[p]
	if !ok {
		rec = &providerStateRecord{
			circuitRecord:     circuitRecord{Circuit: CircuitClosed},
			QuotaRemainingPct: -1,
			DailyDate:         now.UTC().Format("2006-01-02"),
		}
		h.state.Providers[p] = rec
	}
	today := now.UTC().Format("2006-01-02")
	if rec.DailyDate != today {
		rec.DailyDate = today
		rec.DailySpentMicro = 0
	}
	return rec
}

func (h *HealthStore) ensureModelLocked(provider, model string) *circuitRecord {
	k := catalogKey(provider, model)
	rec, ok := h.state.Models[k]
	if !ok {
		rec = &circuitRecord{Circuit: CircuitClosed}
		h.state.Models[k] = rec
	}
	return rec
}

// SyncCredentialFingerprints resets auth-opened provider circuits when the
// SHA-256 credential fingerprint changes (e.g., user fixed an invalid API key).
func (h *HealthStore) SyncCredentialFingerprints(fps map[string]string) {
	if len(fps) == 0 {
		return
	}
	h.mu.Lock()
	now := h.nowFn()
	changed := false
	for prov, fp := range fps {
		rec := h.ensureProviderLocked(prov, now)
		if rec.CredFingerprint != "" && rec.CredFingerprint != fp && rec.Circuit == CircuitOpen && rec.Reason == string(ErrClassAuth) {
			rec.Circuit = CircuitClosed
			rec.Reason = ""
			rec.Until = time.Time{}
			rec.FailureCount = 0
			rec.HalfOpenLeaseUntil = time.Time{}
			changed = true
		}
		rec.CredFingerprint = fp
	}
	allClosed := h.allClosedLocked(now)
	fn := h.onChange
	if changed {
		h.saveLocked()
	}
	h.mu.Unlock()
	if changed && fn != nil {
		fn(false, allClosed)
	}
}

// TryAcquireHalfOpen atomically claims the single probe slot for a half-open
// provider or model so concurrent devs do not stampede recovery probes. The
// claim is a TTL lease: a probe that dies without calling RecordSuccess or
// RecordFailure (cancel, 400, panic) self-releases after halfOpenLeaseTTL.
func (h *HealthStore) TryAcquireHalfOpen(provider, model string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.nowFn()
	pr := h.ensureProviderLocked(provider, now)
	advanceLazyState(&pr.circuitRecord, now)
	if pr.Circuit == CircuitOpen {
		return false
	}
	if pr.Circuit == CircuitHalfOpen {
		if !leaseFree(pr.HalfOpenLeaseUntil, now) {
			return false
		}
		pr.HalfOpenLeaseUntil = now.Add(halfOpenLeaseTTL)
	}
	if strings.TrimSpace(model) != "" {
		mr := h.ensureModelLocked(provider, model)
		advanceLazyState(mr, now)
		if mr.Circuit == CircuitOpen {
			if pr.Circuit == CircuitHalfOpen {
				pr.HalfOpenLeaseUntil = time.Time{}
			}
			return false
		}
		mrAcquired := false
		if mr.Circuit == CircuitHalfOpen {
			if !leaseFree(mr.HalfOpenLeaseUntil, now) {
				if pr.Circuit == CircuitHalfOpen {
					pr.HalfOpenLeaseUntil = time.Time{}
				}
				return false
			}
			mr.HalfOpenLeaseUntil = now.Add(halfOpenLeaseTTL)
			mrAcquired = true
		}
		var acquiredFamily []*circuitRecord
		rollback := func() {
			if pr.Circuit == CircuitHalfOpen {
				pr.HalfOpenLeaseUntil = time.Time{}
			}
			if mrAcquired {
				mr.HalfOpenLeaseUntil = time.Time{}
			}
			for _, rec := range acquiredFamily {
				rec.HalfOpenLeaseUntil = time.Time{}
			}
		}
		for _, fk := range agyModelFamilyLookupKeys(provider, model) {
			fr, ok := h.state.Models[catalogKey(provider, fk)]
			if !ok || fr == nil {
				continue
			}
			advanceLazyState(fr, now)
			if fr.Circuit == CircuitOpen {
				rollback()
				return false
			}
			if fr.Circuit == CircuitHalfOpen {
				if !leaseFree(fr.HalfOpenLeaseUntil, now) {
					rollback()
					return false
				}
				fr.HalfOpenLeaseUntil = now.Add(halfOpenLeaseTTL)
				acquiredFamily = append(acquiredFamily, fr)
			}
		}
	}
	return true
}

// ReleaseHalfOpen frees the probe slot claimed by TryAcquireHalfOpen without
// changing the circuit state (Spec 06 §3: "liberação garantida via defer caso o
// probe não conclua com sucesso/falha classificável").
func (h *HealthStore) ReleaseHalfOpen(provider, model string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if rec, ok := h.state.Providers[strings.ToLower(strings.TrimSpace(provider))]; ok && rec != nil {
		rec.HalfOpenLeaseUntil = time.Time{}
	}
	if strings.TrimSpace(model) != "" {
		if rec, ok := h.state.Models[catalogKey(provider, model)]; ok && rec != nil {
			rec.HalfOpenLeaseUntil = time.Time{}
		}
		for _, fk := range agyModelFamilyLookupKeys(provider, model) {
			if rec, ok := h.state.Models[catalogKey(provider, fk)]; ok && rec != nil {
				rec.HalfOpenLeaseUntil = time.Time{}
			}
		}
	}
}

func leaseFree(until, now time.Time) bool {
	return until.IsZero() || !now.Before(until)
}

// RecordSuccess closes any open/half-open circuits on provider+model and records a clean outcome.
func (h *HealthStore) RecordSuccess(provider, model string) {
	h.mu.Lock()
	now := h.nowFn()
	pr := h.ensureProviderLocked(provider, now)
	wasOpen := pr.Circuit != CircuitClosed
	pr.Circuit = CircuitClosed
	pr.Reason = ""
	pr.Until = time.Time{}
	pr.FailureCount = 0
	pr.ConsecutiveOutages = 0
	pr.HalfOpenLeaseUntil = time.Time{}

	if strings.TrimSpace(model) != "" {
		mr := h.ensureModelLocked(provider, model)
		if mr.Circuit != CircuitClosed {
			wasOpen = true
		}
		mr.Circuit = CircuitClosed
		mr.Reason = ""
		mr.Until = time.Time{}
		mr.FailureCount = 0
		mr.HalfOpenLeaseUntil = time.Time{}
		mr.Recent = appendOutcomeSample(mr.Recent, outcomeSample{At: now, OK: true})
		for _, fk := range agyModelFamilyLookupKeys(provider, model) {
			if fr, ok := h.state.Models[catalogKey(provider, fk)]; ok && fr != nil {
				if fr.Circuit != CircuitClosed {
					wasOpen = true
				}
				fr.Circuit = CircuitClosed
				fr.Reason = ""
				fr.Until = time.Time{}
				fr.FailureCount = 0
				fr.HalfOpenLeaseUntil = time.Time{}
			}
		}
	}
	allClosed := h.allClosedLocked(now)
	fn := h.onChange
	if wasOpen {
		h.saveLocked()
	}
	h.mu.Unlock()
	if wasOpen && fn != nil {
		fn(false, allClosed)
	}
}

// RecordSpend adds estimated/actual microUSD spend to provider for the current UTC day.
// Disk writes are debounced: the counter is monotone, so it persists at most
// once per recordSpendDebounce (circuit events flush it sooner).
func (h *HealthStore) RecordSpend(provider string, microUSD int64) {
	if microUSD <= 0 {
		return
	}
	h.mu.Lock()
	now := h.nowFn()
	pr := h.ensureProviderLocked(provider, now)
	pr.DailySpentMicro += microUSD
	if h.spendDirty || now.Sub(h.lastSpendSave) >= recordSpendDebounce {
		h.saveLocked()
		h.lastSpendSave = now
	} else {
		h.spendDirty = true
	}
	h.mu.Unlock()
}

// RecordFailure classifies and records a call failure, opening the appropriate
// provider- or model-scoped circuit with exponential backoff.
func (h *HealthStore) RecordFailure(provider, model string, class ErrorClass, resetsAt time.Time) (scope string, state CircuitState) {
	if class == ErrClassNone || class == ErrClassPolicy {
		return "", CircuitClosed
	}
	h.mu.Lock()
	now := h.nowFn()
	pr := h.ensureProviderLocked(provider, now)
	mr := h.ensureModelLocked(provider, model)
	pr.HalfOpenLeaseUntil = time.Time{}
	mr.HalfOpenLeaseUntil = time.Time{}
	for _, fk := range agyModelFamilyLookupKeys(provider, model) {
		if fr, ok := h.state.Models[catalogKey(provider, fk)]; ok && fr != nil {
			fr.HalfOpenLeaseUntil = time.Time{}
		}
	}
	mr.Recent = appendOutcomeSample(mr.Recent, outcomeSample{At: now, OK: false})

	opened := false
	switch class {
	case ErrClassQuota:
		pr.FailureCount++
		pr.ConsecutiveOutages = 0
		pr.Circuit = CircuitOpen
		pr.Reason = string(ErrClassQuota)
		pr.OpenedAt = now
		if resetsAt.After(now) {
			pr.Until = resetsAt
		} else {
			pr.Until = now.Add(h.backoffCooldown(h.cooldownDuration(ErrClassQuota), pr.FailureCount))
		}
		scope = "provider"
		state = CircuitOpen
		opened = true

	case ErrClassModelQuota:
		mr.FailureCount++
		mr.Circuit = CircuitOpen
		mr.Reason = string(ErrClassModelQuota)
		mr.OpenedAt = now
		if resetsAt.After(now) {
			mr.Until = resetsAt
		} else {
			mr.Until = now.Add(h.backoffCooldown(h.cooldownDuration(ErrClassQuota), mr.FailureCount))
		}
		scope = "model"
		state = CircuitOpen
		opened = true
		for _, fk := range AgyQuotaFamilyKeys(provider, model) {
			fr := h.ensureModelLocked(provider, fk)
			if fr.Circuit != CircuitOpen {
				fr.FailureCount++
			}
			fr.Circuit = CircuitOpen
			fr.Reason = string(ErrClassModelQuota)
			fr.OpenedAt = now
			if mr.Until.After(fr.Until) {
				fr.Until = mr.Until
			}
			fr.HalfOpenLeaseUntil = time.Time{}
		}

	case ErrClassAuth:
		pr.FailureCount++
		pr.Circuit = CircuitOpen
		pr.Reason = string(ErrClassAuth)
		pr.OpenedAt = now
		pr.Until = now.Add(24 * time.Hour) // cleared immediately when credential fingerprint changes
		scope = "provider"
		state = CircuitOpen
		opened = true

	case ErrClassRateLimit:
		mr.FailureCount++
		mr.Circuit = CircuitOpen
		mr.Reason = string(ErrClassRateLimit)
		mr.OpenedAt = now
		mr.Until = now.Add(h.backoffCooldown(h.cooldownDuration(ErrClassRateLimit), mr.FailureCount))
		scope = "model"
		state = CircuitOpen
		opened = true

	case ErrClassModel:
		mr.FailureCount++
		mr.Circuit = CircuitOpen
		mr.Reason = string(ErrClassModel)
		mr.OpenedAt = now
		mr.Until = now.Add(h.backoffCooldown(h.cooldownDuration(ErrClassRateLimit), mr.FailureCount))
		scope = "model"
		state = CircuitOpen
		opened = true

	case ErrClassOutage:
		mr.FailureCount++
		mr.Circuit = CircuitOpen
		mr.Reason = string(ErrClassOutage)
		mr.OpenedAt = now
		mr.Until = now.Add(h.backoffCooldown(h.cooldownDuration(ErrClassOutage), mr.FailureCount))
		scope = "model"
		state = CircuitOpen
		opened = true

		pr.ConsecutiveOutages++
		if pr.ConsecutiveOutages >= 5 {
			pr.FailureCount++
			pr.Circuit = CircuitOpen
			pr.Reason = string(ErrClassOutage)
			pr.OpenedAt = now
			pr.Until = now.Add(h.backoffCooldown(h.cooldownDuration(ErrClassOutage), pr.FailureCount))
			scope = "provider"
		}
	}

	// Spike detector (§9): >50% of last 10 model calls failed in last 2 min with >=4 failures.
	if isSpike(mr.Recent, now) && mr.Circuit != CircuitOpen {
		mr.FailureCount++
		mr.Circuit = CircuitOpen
		mr.Reason = string(ErrClassSpike)
		mr.OpenedAt = now
		mr.Until = now.Add(2 * time.Minute)
		if scope == "" {
			scope = "model"
			state = CircuitOpen
		}
		opened = true
	}

	fn := h.onChange
	if opened {
		h.saveLocked()
	}
	h.mu.Unlock()
	if opened && fn != nil {
		fn(true, false)
	}
	return scope, state
}

// UpdateQuotaProbe applies a background probe result without opening outage on probe failure.
func (h *HealthStore) UpdateQuotaProbe(provider string, remainingPct float64, resetsAt time.Time, probeErr error) {
	h.mu.Lock()
	now := h.nowFn()
	pr := h.ensureProviderLocked(provider, now)
	changed := false
	opened := false
	if probeErr != nil {
		// Spec §9: "Falha da sonda não marca outage. A última quota conhecida vale, com o flag stale."
		if !pr.QuotaStale {
			pr.QuotaStale = true
			changed = true
		}
	} else {
		pr.QuotaStale = false
		if remainingPct >= 0 {
			pr.QuotaRemainingPct = remainingPct
			pr.QuotaResetsAt = resetsAt
		}
		if remainingPct >= 0 && remainingPct < 5 {
			if pr.Circuit != CircuitOpen || pr.Reason != string(ErrClassQuota) {
				opened = true
			}
			pr.Circuit = CircuitOpen
			pr.Reason = string(ErrClassQuota)
			pr.OpenedAt = now
			if resetsAt.After(now) {
				pr.Until = resetsAt
			} else {
				pr.Until = now.Add(h.cooldownDuration(ErrClassQuota))
			}
			changed = true
		} else if pr.Circuit == CircuitHalfOpen || (pr.Circuit == CircuitOpen && pr.Reason == string(ErrClassQuota) && remainingPct >= 5) {
			pr.Circuit = CircuitClosed
			pr.Reason = ""
			pr.Until = time.Time{}
			pr.FailureCount = 0
			pr.HalfOpenLeaseUntil = time.Time{}
			changed = true
		}
	}
	allClosed := h.allClosedLocked(now)
	fn := h.onChange
	if changed {
		h.saveLocked()
	}
	h.mu.Unlock()
	if changed && fn != nil && (opened || allClosed) {
		fn(opened, allClosed)
	}
}

// Snapshot returns a read-consistent HealthSnapshot for Score/Route in pure memory.
func (h *HealthStore) Snapshot(creds map[string]bool, excludePairs map[string]bool) HealthSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.nowFn()
	snap := HealthSnapshot{
		Credentials:        creds,
		Circuits:           make(map[string]ProviderHealth, len(h.state.Providers)),
		ModelCircuits:      make(map[string]ProviderHealth, len(h.state.Models)),
		ExcludePairs:       excludePairs,
		DailySpentMicroUSD: make(map[string]int64, len(h.state.Providers)),
		AgyRemainingPct:    -1,
	}
	for prov, pr := range h.state.Providers {
		today := now.UTC().Format("2006-01-02")
		if pr.DailyDate != today {
			pr.DailyDate = today
			pr.DailySpentMicro = 0
		}
		advanceLazyState(&pr.circuitRecord, now)
		slots := 0
		if pr.Circuit == CircuitHalfOpen && leaseFree(pr.HalfOpenLeaseUntil, now) {
			slots = 1
		}
		snap.Circuits[prov] = ProviderHealth{
			Circuit:       pr.Circuit,
			Until:         pr.Until,
			Reason:        pr.Reason,
			HalfOpenSlots: slots,
		}
		snap.DailySpentMicroUSD[prov] = pr.DailySpentMicro
		if prov == "agy" && pr.QuotaRemainingPct >= 0 {
			snap.AgyRemainingPct = pr.QuotaRemainingPct
		}
	}
	for key, mr := range h.state.Models {
		advanceLazyState(mr, now)
		slots := 0
		if mr.Circuit == CircuitHalfOpen && leaseFree(mr.HalfOpenLeaseUntil, now) {
			slots = 1
		}
		snap.ModelCircuits[key] = ProviderHealth{
			Circuit:       mr.Circuit,
			Until:         mr.Until,
			Reason:        mr.Reason,
			HalfOpenSlots: slots,
		}
	}
	return snap
}

// Summary returns the secret-safe status list of known providers for UI and provider_health SSE.
func (h *HealthStore) Summary(policy Policy, creds map[string]bool) []ProviderHealthView {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.nowFn()

	provSet := map[string]bool{
		"agy": true, "opencode": true, "openrouter": true, "cursor": true,
		"gemini": true, "anthropic": true, "openai": true,
	}
	for p := range h.state.Providers {
		provSet[p] = true
	}
	for p, ok := range creds {
		if ok && p != "opencode_free" {
			provSet[p] = true
		}
	}
	provs := make([]string, 0, len(provSet))
	for p := range provSet {
		provs = append(provs, p)
	}
	sort.Strings(provs)

	out := make([]ProviderHealthView, 0, len(provs))
	for _, p := range provs {
		// Read-only (Spec 06 §2/§3): Summary must never INSERT provider records
		// into the shared state. Unknown providers render a closed default view.
		view := providerStateRecord{
			circuitRecord:     circuitRecord{Circuit: CircuitClosed},
			QuotaRemainingPct: -1,
			DailyDate:         now.UTC().Format("2006-01-02"),
		}
		if pr, ok := h.state.Providers[p]; ok && pr != nil {
			view = *pr
		}
		today := now.UTC().Format("2006-01-02")
		if view.DailyDate != today {
			view.DailyDate = today
			view.DailySpentMicro = 0
		}
		advanceLazyState(&view.circuitRecord, now)
		untilStr := ""
		if !view.Until.IsZero() && view.Circuit != CircuitClosed {
			untilStr = view.Until.UTC().Format(time.RFC3339)
		}
		var capMicro int64
		if dailyUSD, ok := policy.DailyUSD[p]; ok && dailyUSD > 0 {
			capMicro = int64(dailyUSD * 1_000_000)
		}
		var remPct float64
		if view.QuotaRemainingPct >= 0 {
			remPct = view.QuotaRemainingPct
		}
		out = append(out, ProviderHealthView{
			Provider:          p,
			State:             string(view.Circuit),
			Reason:            view.Reason,
			Until:             untilStr,
			DailySpentMicro:   view.DailySpentMicro,
			DailyCapMicro:     capMicro,
			QuotaRemainingPct: remPct,
			QuotaStale:        view.QuotaStale,
			HasCredential:     creds[p],
		})
	}
	return out
}

func (h *HealthStore) allClosedLocked(now time.Time) bool {
	for _, pr := range h.state.Providers {
		advanceLazyState(&pr.circuitRecord, now)
		if pr.Circuit != CircuitClosed {
			return false
		}
	}
	for _, mr := range h.state.Models {
		advanceLazyState(mr, now)
		if mr.Circuit != CircuitClosed {
			return false
		}
	}
	return true
}

func advanceLazyState(rec *circuitRecord, now time.Time) {
	if rec.Circuit == CircuitOpen && !rec.Until.IsZero() && !now.Before(rec.Until) {
		rec.Circuit = CircuitHalfOpen
		rec.HalfOpenLeaseUntil = time.Time{}
	}
}

func (h *HealthStore) cooldownDuration(class ErrorClass) time.Duration {
	switch class {
	case ErrClassQuota, ErrClassModelQuota:
		if d, err := time.ParseDuration(h.cooldown.Quota); err == nil && d > 0 {
			return d
		}
		return 15 * time.Minute
	case ErrClassRateLimit:
		if d, err := time.ParseDuration(h.cooldown.RateLimit); err == nil && d > 0 {
			return d
		}
		return 2 * time.Minute
	default:
		if d, err := time.ParseDuration(h.cooldown.Outage); err == nil && d > 0 {
			return d
		}
		return 1 * time.Minute
	}
}

func (h *HealthStore) backoffCooldown(base time.Duration, failureCount int) time.Duration {
	shift := failureCount - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 4 {
		shift = 4
	}
	d := base << shift
	const maxBackoff = 30 * time.Minute
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}

func appendOutcomeSample(list []outcomeSample, s outcomeSample) []outcomeSample {
	list = append(list, s)
	if len(list) > 10 {
		list = list[len(list)-10:]
	}
	return list
}

func isSpike(list []outcomeSample, now time.Time) bool {
	cutoff := now.Add(-2 * time.Minute)
	total := 0
	fails := 0
	for _, s := range list {
		if s.At.Before(cutoff) {
			continue
		}
		total++
		if !s.OK {
			fails++
		}
	}
	return fails >= 4 && fails*2 > total
}

func (h *HealthStore) saveLocked() {
	if h.path == "" {
		return
	}
	h.spendDirty = false
	// Cross-process contract (Spec 06 §3): hold the OS file lock and adopt
	// records another process (CLI / IDE / serve) wrote and we never saw, so
	// the last writer never erases the others' circuit state. Shared records
	// keep OUR in-memory view; daily spend merges as max (monotone counter).
	if release, err := fslockx.Lock(h.path, 5*time.Second); err == nil && release != nil {
		defer release()
		if data, rerr := os.ReadFile(h.path); rerr == nil {
			var disk healthFile
			if json.Unmarshal(data, &disk) == nil {
				h.mergeDiskStateLocked(disk)
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		return
	}
	data, err := json.MarshalIndent(h.state, "", "  ")
	if err != nil {
		return
	}
	tmp := h.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, h.path); err != nil {
		_ = os.Remove(tmp)
		_ = os.WriteFile(h.path, data, 0o600)
	}
}

// mergeDiskStateLocked folds records that exist only on disk into memory.
func (h *HealthStore) mergeDiskStateLocked(disk healthFile) {
	for k, rec := range disk.Providers {
		if rec == nil {
			continue
		}
		own, ok := h.state.Providers[k]
		if !ok {
			cp := *rec
			h.state.Providers[k] = &cp
			continue
		}
		if rec.DailySpentMicro > own.DailySpentMicro {
			own.DailySpentMicro = rec.DailySpentMicro
		}
	}
	for k, rec := range disk.Models {
		if rec == nil {
			continue
		}
		if _, ok := h.state.Models[k]; !ok {
			cp := *rec
			h.state.Models[k] = &cp
		}
	}
}

func normalizeAgyQuotaSlug(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	m = strings.TrimPrefix(m, "agy:")
	if idx := strings.LastIndex(m, ":"); idx >= 0 {
		switch m[idx+1:] {
		case "low", "medium", "high", "xhigh", "max":
			m = m[:idx]
		}
	}
	m = strings.TrimSpace(m)
	if m == "" || m == "agy" || m == "default" {
		return ""
	}
	return m
}

func stripAgySlugEffort(slug string) string {
	for _, suf := range []string{"-thinking", "-xhigh", "-medium", "-high", "-low", "-max"} {
		if strings.HasSuffix(slug, suf) {
			return strings.TrimSuffix(slug, suf)
		}
	}
	return slug
}

// AgyQuotaFamilyKeys returns sibling-effort quota family keys for Antigravity
// models so exhausting e.g. agy:claude-opus-5-5-medium also suppresses
// agy:claude-opus-5-5-high and agy:claude-opus-5-5-low.
func AgyQuotaFamilyKeys(provider, model string) []string {
	if !strings.EqualFold(strings.TrimSpace(provider), "agy") {
		return nil
	}
	slug := normalizeAgyQuotaSlug(model)
	if slug == "" {
		return nil
	}
	base := stripAgySlugEffort(slug)
	if base != "" && base != slug {
		return []string{"__family__:" + base}
	}
	return nil
}

// AgyCreditsFamilyKey returns the broader credits family key (e.g.
// "__family__:claude") for Antigravity models sharing paid AI credits.
func AgyCreditsFamilyKey(provider, model string) string {
	if !strings.EqualFold(strings.TrimSpace(provider), "agy") {
		return ""
	}
	slug := normalizeAgyQuotaSlug(model)
	if slug == "" {
		return ""
	}
	if strings.Contains(slug, "claude") {
		return "__family__:claude"
	}
	return ""
}

func agyModelFamilyLookupKeys(provider, model string) []string {
	keys := AgyQuotaFamilyKeys(provider, model)
	if cfk := AgyCreditsFamilyKey(provider, model); cfk != "" {
		dup := false
		for _, k := range keys {
			if k == cfk {
				dup = true
				break
			}
		}
		if !dup {
			keys = append(keys, cfk)
		}
	}
	return keys
}
