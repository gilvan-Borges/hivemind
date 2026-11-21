package modelx

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/orgx"
	"google.golang.org/adk/v2/model"
)

type accountSwitchObserverKey struct{}

// AccountSwitchNotice describes an automatic per-model failover from one account
// in the provider pool to the next healthy account (e.g. Conta 1 -> Conta 2 for "argon").
type AccountSwitchNotice struct {
	Provider    string
	ModelBucket string
	FromAccount string
	ToAccount   string
	ToAccountID string
}

// WithAccountSwitchObserver attaches a callback to ctx that is invoked whenever
// the multi-account pool switches a model from a quota-exhausted account (e.g. Conta 1)
// to a reserve account (e.g. Conta 2).
func WithAccountSwitchObserver(ctx context.Context, fn func(AccountSwitchNotice)) context.Context {
	if ctx == nil || fn == nil {
		return ctx
	}
	return context.WithValue(ctx, accountSwitchObserverKey{}, fn)
}

func notifyAccountSwitch(ctx context.Context, n AccountSwitchNotice) {
	if ctx == nil {
		return
	}
	if fn, ok := ctx.Value(accountSwitchObserverKey{}).(func(AccountSwitchNotice)); ok && fn != nil {
		fn(n)
	}
}

const (
	defaultAccountModelQuotaCooldown = 15 * time.Minute
	defaultAccountModelRateCooldown  = 2 * time.Minute
	defaultAccountWideAuthCooldown   = 30 * time.Minute
)

// accountModelSlotKey uniquely identifies a (provider, accountID, modelBucket) cell
// in the multi-account quota matrix.
type accountModelSlotState struct {
	Provider    string    `json:"provider"`
	AccountID   string    `json:"accountId"`
	AccountName string    `json:"accountName,omitempty"`
	ModelBucket string    `json:"modelBucket"` // e.g. "argon", "gemini-4-neon-high", or "__all__"
	Reason      string    `json:"reason"`      // "quota", "rate_limit", "auth"
	OpenedAt    time.Time `json:"openedAt"`
	Until       time.Time `json:"until"`
}

type accountPoolHealthFile struct {
	Version int                              `json:"version"`
	Slots   map[string]accountModelSlotState `json:"slots,omitempty"`
}

var (
	accountPoolMu    sync.Mutex
	accountPoolLoaded bool
	accountPoolState  = accountPoolHealthFile{
		Version: 1,
		Slots:   map[string]accountModelSlotState{},
	}
)

func accountPoolHealthPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(base) == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", herr
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "HiveMind", "jev", "account-pool-health.json"), nil
}

func loadAccountPoolHealthLocked() {
	if accountPoolLoaded {
		return
	}
	accountPoolLoaded = true
	if accountPoolState.Slots == nil {
		accountPoolState.Slots = map[string]accountModelSlotState{}
	}
	p, err := accountPoolHealthPath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var parsed accountPoolHealthFile
	if json.Unmarshal(data, &parsed) == nil && parsed.Slots != nil {
		now := time.Now()
		for k, v := range parsed.Slots {
			if v.Until.After(now) {
				accountPoolState.Slots[k] = v
			}
		}
	}
}

func saveAccountPoolHealthLocked() {
	p, err := accountPoolHealthPath()
	if err != nil {
		return
	}
	now := time.Now()
	for k, v := range accountPoolState.Slots {
		if !v.Until.After(now) {
			delete(accountPoolState.Slots, k)
		}
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	data, err := json.MarshalIndent(accountPoolState, "", "  ")
	if err != nil {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err == nil {
		_ = os.Rename(tmp, p)
	}
}

func slotKey(provider, accountID, bucket string) string {
	return NormalizeProfileProvider(provider) + "\x00" +
		strings.ToLower(strings.TrimSpace(accountID)) + "\x00" +
		strings.ToLower(strings.TrimSpace(bucket))
}

// NormalizeModelQuotaBucket maps a model spec (such as "agy:argon:high" or
// "agy:gemini-4-neon-high") to its canonical per-account quota bucket ("argon",
// "gemini-4-neon-high"). Different models on the same account keep separate
// buckets so exhausting "argon" on Account 1 leaves "gemini-4-neon-high" active
// on Account 1 while routing "argon" to Account 2.
func NormalizeModelQuotaBucket(provider, modelName string) string {
	prov := NormalizeProfileProvider(provider)
	_, _, clean := SplitAccountModelSpec(prov, modelName)
	m := strings.ToLower(strings.TrimSpace(clean))
	if prov == "agy" {
		slug, _ := ParseAgySpec(m)
		if slug == "" {
			return "default"
		}
		return strings.ToLower(strings.TrimSpace(slug))
	}
	for _, pref := range []string{"opencode-go:", "opencode:", "zen:", "cursor:", "openrouter:"} {
		if strings.HasPrefix(m, pref) {
			m = strings.TrimPrefix(m, pref)
			break
		}
	}
	if m == "" {
		return "default"
	}
	return m
}

// SplitAccountModelSpec parses optional account pinning syntax in provider or modelName:
//   - provider="agy@conta-2", model="argon" -> ("agy", "conta-2", "argon")
//   - provider="agy", model="agy@conta-2:argon" -> ("agy", "conta-2", "agy:argon")
//   - provider="openrouter", model="openrouter@or-2:deepseek/deepseek-chat" -> ("openrouter", "or-2", "deepseek/deepseek-chat")
func SplitAccountModelSpec(provider, modelName string) (baseProvider, accountID, cleanModel string) {
	rawProv := strings.TrimSpace(provider)
	rawMod := strings.TrimSpace(modelName)

	if i := strings.IndexByte(rawProv, '@'); i > 0 {
		accountID = sanitizeSlug(rawProv[i+1:])
		rawProv = rawProv[:i]
	}

	if i := strings.IndexByte(rawMod, '@'); i > 0 {
		prefix := strings.ToLower(strings.TrimSpace(rawMod[:i]))
		rest := rawMod[i+1:]
		switch prefix {
		case "agy", "antigravity", "openrouter", "opencode", "opencode-go", "gemini", "vertex", "anthropic", "openai", "cursor":
			var profPart, tailModel string
			if colonIdx := strings.IndexAny(rest, ":/"); colonIdx > 0 {
				profPart = rest[:colonIdx]
				tailModel = rest[colonIdx+1:]
			} else {
				profPart = rest
			}
			if pid := sanitizeSlug(profPart); pid != "" {
				accountID = pid
			}
			if rawProv == "" {
				rawProv = prefix
			}
			if tailModel == "" {
				rawMod = prefix
			} else if prefix == "agy" || prefix == "opencode" || prefix == "opencode-go" || prefix == "cursor" {
				rawMod = prefix + ":" + tailModel
			} else {
				rawMod = tailModel
			}
		}
	}

	baseProvider = strings.ToLower(strings.TrimSpace(rawProv))
	cleanModel = rawMod
	return baseProvider, accountID, cleanModel
}

// IsAccountModelSlotHealthy reports whether (provider, accountID, modelBucket)
// AND the account-wide "__all__" slot are currently closed (available).
func IsAccountModelSlotHealthy(provider, accountID, modelName string) bool {
	bucket := NormalizeModelQuotaBucket(provider, modelName)
	accountPoolMu.Lock()
	defer accountPoolMu.Unlock()
	loadAccountPoolHealthLocked()

	now := time.Now()
	if st, ok := accountPoolState.Slots[slotKey(provider, accountID, "__all__")]; ok && st.Until.After(now) {
		return false
	}
	if st, ok := accountPoolState.Slots[slotKey(provider, accountID, bucket)]; ok && st.Until.After(now) {
		return false
	}
	return true
}

// RecordAccountModelFailure marks ONLY the affected (provider, accountID, modelBucket)
// cell in cooldown when a model hits quota/rate-limit on that account, or marks
// (provider, accountID, "__all__") when the account credential itself is unauthorized
// or out of account-wide billing credits.
func RecordAccountModelFailure(provider, accountID, accountName, modelName string, err error) (blockedBucket string, cooldown time.Duration, shouldFailoverAccount bool) {
	if err == nil || strings.TrimSpace(accountID) == "" {
		return "", 0, false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return "", 0, false
	}
	// Never fail over on client/policy 400 errors or context cancellation.
	for _, pat := range []string{"http 400", "status 400", "400 bad request", "invalid_argument", "context canceled"} {
		if strings.Contains(msg, pat) {
			return "", 0, false
		}
	}

	prov := NormalizeProfileProvider(provider)
	bucket := NormalizeModelQuotaBucket(prov, modelName)
	reason := ""

	// 1. Account-wide auth failure -> blocks "__all__" models on this specific account.
	for _, pat := range []string{"401", "403", "unauthorized", "forbidden", "rejeitou a chave", "invalid api key", "invalid_api_key", "não autenticado", "sem login"} {
		if strings.Contains(msg, pat) {
			bucket = "__all__"
			reason = "auth"
			cooldown = defaultAccountWideAuthCooldown
			break
		}
	}

	// 2. Payment required (402) on API-key providers is account-wide billing balance;
	// on AGY/Gemini/OpenCode model-specific quota exhaustion ("resource_exhausted",
	// "quota exceeded", "esgotou a quota", "429") is scoped strictly to the model bucket!
	if reason == "" {
		for _, pat := range []string{"402", "payment required", "créditos insuficientes", "insufficient credits"} {
			if strings.Contains(msg, pat) {
				bucket = "__all__"
				reason = "billing_quota"
				cooldown = defaultAccountModelQuotaCooldown
				break
			}
		}
	}

	// 3. Per-model quota / token window exhaustion (e.g. "argon" 5h limit reached on Account 1,
	// while "gemini-4-neon-high" on Account 1 still has remaining quota).
	if reason == "" {
		for _, pat := range []string{
			"resource_exhausted", "esgotou a quota", "quota/créditos", "quota exceeded",
			"insufficient_quota", "429", "too many requests", "rate limit", "rate_limit",
			"limite", "exhausted",
		} {
			if strings.Contains(msg, pat) {
				reason = "model_quota"
				cooldown = defaultAccountModelQuotaCooldown
				if strings.Contains(msg, "rate") || strings.Contains(msg, "429") {
					cooldown = defaultAccountModelRateCooldown
				}
				break
			}
		}
	}

	if reason == "" {
		return "", 0, false
	}

	now := time.Now()
	accountPoolMu.Lock()
	loadAccountPoolHealthLocked()
	accountPoolState.Slots[slotKey(prov, accountID, bucket)] = accountModelSlotState{
		Provider:    prov,
		AccountID:   accountID,
		AccountName: accountName,
		ModelBucket: bucket,
		Reason:      reason,
		OpenedAt:    now,
		Until:       now.Add(cooldown),
	}
	saveAccountPoolHealthLocked()
	accountPoolMu.Unlock()
	return bucket, cooldown, true
}

// ClearAccountModelSlot clears any active cooldown on (provider, accountID, modelName)
// after a successful call.
func ClearAccountModelSlot(provider, accountID, modelName string) {
	if strings.TrimSpace(accountID) == "" {
		return
	}
	prov := NormalizeProfileProvider(provider)
	bucket := NormalizeModelQuotaBucket(prov, modelName)
	accountPoolMu.Lock()
	loadAccountPoolHealthLocked()
	k := slotKey(prov, accountID, bucket)
	if _, exists := accountPoolState.Slots[k]; exists {
		delete(accountPoolState.Slots, k)
		saveAccountPoolHealthLocked()
	}
	accountPoolMu.Unlock()
}

// ListAccountPoolForProvider returns the ordered pool of ready accounts for provider
// (e.g. [Conta 1 (Principal), Conta 2 (Particular), Conta 3 (Reserva)]).
// If preferredAccountID is specified (or resolved from workspace/org), it is placed first,
// followed by all other ready accounts for that provider as automatic failover reserves.
func ListAccountPoolForProvider(ctx context.Context, provider, preferredAccountID string) []ProviderProfile {
	prov := NormalizeProfileProvider(provider)
	if prov == "" {
		return nil
	}
	if CredentialIsolated(ctx, prov) && preferredAccountID == "" {
		return nil
	}

	profilesMu.Lock()
	doc, _ := loadProfilesLocked()
	profilesMu.Unlock()

	ws := WorkspaceFromContext(ctx)
	var orgID orgx.Identity
	matchedWorkspaceOrg := false
	hasAnyOrgScoped := false
	if ws != "" {
		orgID = orgx.Detect(ws)
	}
	for _, p := range doc.Profiles {
		if p.Provider != prov || len(p.OrgPatterns) == 0 {
			continue
		}
		hasAnyOrgScoped = true
		if ws != "" {
			if matched, _ := orgx.MatchPatterns(orgID, p.OrgPatterns); matched {
				matchedWorkspaceOrg = true
			}
		}
	}

	resolvedProf, resolvedMode, _ := ResolveProfile(ctx, prov)
	pinnedID := ""
	if resolvedProf != nil && (resolvedMode == "manual" || resolvedMode == "override") {
		pinnedID = resolvedProf.ID
	}

	var allForProv []ProviderProfile
	for _, p := range doc.Profiles {
		if p.Provider != prov {
			continue
		}
		isPreferred := preferredAccountID != "" && strings.EqualFold(p.ID, preferredAccountID)
		isPinned := pinnedID != "" && strings.EqualFold(p.ID, pinnedID)
		if len(p.OrgPatterns) > 0 {
			if ws != "" {
				if matched, _ := orgx.MatchPatterns(orgID, p.OrgPatterns); !matched && !isPreferred && !isPinned {
					continue
				}
			} else if !isPreferred && !isPinned {
				continue
			}
		} else if matchedWorkspaceOrg && !isPreferred && !isPinned {
			// Workspace matched an org-scoped profile: keep pool strictly within that org.
			continue
		}
		ready, _ := evaluateProfileReadiness(p)
		if !ready {
			continue
		}
		if prep := prepareProfile(p); prep != nil {
			allForProv = append(allForProv, *prep)
		}
	}

	// Also include the system/global account when available and not already represented
	// so a user with 1 system account + 1 extra profile automatically has a 2-account pool.
	if prov == "agy" && AgyAvailable() && !matchedWorkspaceOrg && (!hasAnyOrgScoped || strings.EqualFold(pinnedID, "__global__")) {
		hasLive := false
		for _, p := range allForProv {
			if strings.EqualFold(p.AuthMode, "live") || p.AuthMode == "" {
				hasLive = true
				break
			}
		}
		if !hasLive && len(allForProv) > 0 {
			allForProv = append(allForProv, ProviderProfile{
				ID:       "__system__",
				Provider: "agy",
				Name:     "Sessão Padrão do Sistema (agy)",
				AuthMode: "live",
			})
		}
	}

	if len(allForProv) == 0 {
		return nil
	}

	primaryID := sanitizeSlug(preferredAccountID)
	if primaryID == "" {
		if prof, _, _ := ResolveProfile(ctx, prov); prof != nil {
			primaryID = prof.ID
		}
	}

	ordered := make([]ProviderProfile, 0, len(allForProv))
	seen := make(map[string]bool, len(allForProv))
	add := func(p ProviderProfile) {
		key := strings.ToLower(p.ID)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		ordered = append(ordered, p)
	}

	// 1. Primary (explicitly requested or workspace-resolved) account first.
	if primaryID != "" {
		for _, p := range allForProv {
			if strings.EqualFold(p.ID, primaryID) {
				add(p)
				break
			}
		}
	}
	// 2. Default account next.
	for _, p := range allForProv {
		if p.IsDefault {
			add(p)
		}
	}
	// 3. Remaining ready accounts in saved order (Conta 2, Conta 3...).
	for _, p := range allForProv {
		add(p)
	}
	return ordered
}

// pooledAccountLLM wraps a provider+model across an ordered pool of accounts
// (e.g. Conta 1 -> Conta 2 -> Conta 3), routing each model independently so that
// if "argon" runs out of quota on Conta 1, only (Conta 1, argon) switches to Conta 2
// while other models (like "gemini-4-neon-high") continue running on Conta 1.
type pooledAccountLLM struct {
	baseCtx      context.Context
	provider     string
	rawProvider  string
	modelName    string
	pool         []ProviderProfile
	buildRaw     func(ctx context.Context, provider, modelName string) (model.LLM, error)
	mu           sync.Mutex
	activeIdx    int
	activeLLM    model.LLM
}

func (p *pooledAccountLLM) Name() string {
	p.mu.Lock()
	idx := p.activeIdx
	cur := p.activeLLM
	p.mu.Unlock()
	baseName := p.provider + ":" + p.modelName
	if cur != nil {
		baseName = cur.Name()
	}
	if idx >= 0 && idx < len(p.pool) {
		acc := p.pool[idx]
		if acc.ID != "" && acc.ID != "__system__" {
			return fmt.Sprintf("%s@%s", baseName, acc.ID)
		}
	}
	return baseName
}

func (p *pooledAccountLLM) contextForAccount(ctx context.Context, acc ProviderProfile) context.Context {
	if acc.ID == "" || acc.ID == "__system__" {
		return WithProfileOverride(ctx, p.provider, "__global__")
	}
	return WithProfileOverride(ctx, p.provider, acc.ID)
}

func (p *pooledAccountLLM) selectHealthyAccountIndex(exclude map[string]bool) int {
	// First pass: find the highest-priority account in the pool where (Account, ModelBucket) is healthy.
	for i, acc := range p.pool {
		if exclude[strings.ToLower(acc.ID)] {
			continue
		}
		if IsAccountModelSlotHealthy(p.provider, acc.ID, p.modelName) {
			return i
		}
	}
	// Second pass: if all accounts are in cooldown for this model, pick the first non-excluded account.
	for i, acc := range p.pool {
		if !exclude[strings.ToLower(acc.ID)] {
			return i
		}
	}
	return 0
}

func (p *pooledAccountLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		excludedAccounts := map[string]bool{}
		maxAttempts := len(p.pool)
		if maxAttempts < 1 {
			maxAttempts = 1
		}

		for attempt := 0; attempt < maxAttempts; attempt++ {
			idx := p.selectHealthyAccountIndex(excludedAccounts)
			acc := p.pool[idx]
			accCtx := p.contextForAccount(ctx, acc)

			p.mu.Lock()
			llm := p.activeLLM
			if llm == nil || p.activeIdx != idx {
				built, buildErr := p.buildRaw(accCtx, p.rawProvider, p.modelName)
				if buildErr != nil {
					p.mu.Unlock()
					_, _, canSwitch := RecordAccountModelFailure(p.provider, acc.ID, acc.Name, p.modelName, buildErr)
					excludedAccounts[strings.ToLower(acc.ID)] = true
					if canSwitch && attempt+1 < maxAttempts {
						continue
					}
					yield(nil, buildErr)
					return
				}
				p.activeIdx = idx
				p.activeLLM = built
				llm = built
			}
			p.mu.Unlock()

			var lastErr error
			deliveredAny := false
			for resp, err := range llm.GenerateContent(accCtx, req, stream) {
				if err != nil {
					lastErr = err
					break
				}
				if resp == nil {
					continue
				}
				deliveredAny = true
				if !yield(resp, nil) {
					return
				}
			}

			if lastErr == nil {
				ClearAccountModelSlot(p.provider, acc.ID, p.modelName)
				return
			}

			// If parts/tool calls were already streamed to ADK, we cannot replay the call on another account,
			// but we still record the quota failure so the NEXT call uses Account 2.
			_, _, canSwitch := RecordAccountModelFailure(p.provider, acc.ID, acc.Name, p.modelName, lastErr)
			if deliveredAny || !canSwitch || attempt+1 >= maxAttempts {
				yield(nil, lastErr)
				return
			}

			// Transparently switch this model to the next healthy account in the pool!
			excludedAccounts[strings.ToLower(acc.ID)] = true
			nextIdx := p.selectHealthyAccountIndex(excludedAccounts)
			if nextIdx >= 0 && nextIdx < len(p.pool) {
				nextAcc := p.pool[nextIdx]
				notifyAccountSwitch(ctx, AccountSwitchNotice{
					Provider:    p.provider,
					ModelBucket: NormalizeModelQuotaBucket(p.provider, p.modelName),
					FromAccount: acc.Name,
					ToAccount:   nextAcc.Name,
					ToAccountID: nextAcc.ID,
				})
			}
			p.mu.Lock()
			p.activeLLM = nil
			p.mu.Unlock()
		}
	}
}

// LaunchAgyADCLogin opens an interactive terminal window running
// `gcloud auth application-default login` with CLOUDSDK_CONFIG pointed at
// the isolated directory for profileID so the user can authenticate multiple
// simultaneous Google/AGY accounts without overwriting their primary login.
func LaunchAgyADCLogin(profileID string) (string, error) {
	dir, err := AgyADCConfigDir(profileID)
	if err != nil || strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("não foi possível resolver diretório ADC para o perfil %q", profileID)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("criar diretório ADC: %w", err)
	}
	if runtime.GOOS == "windows" {
		cmdLine := fmt.Sprintf(`set "CLOUDSDK_CONFIG=%s" && echo Autenticando conta AGY (%s) em %s... && gcloud auth application-default login`, dir, profileID, dir)
		cmd := exec.Command("cmd.exe", "/c", "start", "Autenticar Conta AGY - "+profileID, "cmd.exe", "/k", cmdLine)
		if err := cmd.Start(); err != nil {
			return dir, fmt.Errorf("abrir terminal de login gcloud: %w", err)
		}
		return dir, nil
	}
	return dir, fmt.Errorf("execute no terminal: CLOUDSDK_CONFIG=%q gcloud auth application-default login", dir)
}
