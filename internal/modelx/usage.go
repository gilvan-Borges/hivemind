package modelx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// UsageWindow summarizes real HiveMind token consumption in a rolling time window.
type UsageWindow struct {
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	TotalTokens  int64  `json:"totalTokens"`
	Calls        int    `json:"calls"`
	OldestAt     string `json:"oldestAt,omitempty"`
}

// RemoteQuotaWindow holds an official server-reported quota window (e.g. OpenCode Go 5h / weekly / monthly).
type RemoteQuotaWindow struct {
	Percent  float64 `json:"percent"`
	ResetsAt string  `json:"resetsAt,omitempty"`
	Status   string  `json:"status,omitempty"`
	HasData  bool    `json:"hasData"`
}

// OpenCodeQuota holds official usage percentages returned by GET /zen/go/v1/usage.
type OpenCodeQuota struct {
	Available bool              `json:"available"`
	Detail    string            `json:"detail,omitempty"`
	Rolling5h RemoteQuotaWindow `json:"rolling5h"`
	Weekly    RemoteQuotaWindow `json:"weekly"`
	Monthly   RemoteQuotaWindow `json:"monthly"`
}

// ProviderLimits stores optional user-configured local token budgets (5h and 7d) for progress bars.
type ProviderLimits struct {
	FiveHourTokens int64 `json:"fiveHourTokens,omitempty"`
	WeeklyTokens   int64 `json:"weeklyTokens,omitempty"`
}

// AIProviderUsage summarizes usage, limits, and status for one AI provider.
type AIProviderUsage struct {
	Provider      string         `json:"provider"`
	Label         string         `json:"label"`
	Window5h      UsageWindow    `json:"window5h"`
	WindowWeekly  UsageWindow    `json:"windowWeekly"`
	WindowMonthly UsageWindow    `json:"windowMonthly"`
	Limits        ProviderLimits `json:"limits"`
}

// AgyQuotaBucket represents one server-reported Antigravity quota window (e.g. 5h or weekly).
type AgyQuotaBucket struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Window           string  `json:"window"` // "5h" or "weekly"
	UsedPercent      float64 `json:"usedPercent"`
	RemainingPercent float64 `json:"remainingPercent"`
	ResetTime        string  `json:"resetTime,omitempty"`
	Description      string  `json:"description,omitempty"`
}

// AgyQuotaGroup groups Antigravity models sharing a 5h and/or weekly quota pool.
type AgyQuotaGroup struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Buckets     []AgyQuotaBucket `json:"buckets"`
}

// AgyQuota holds the official quota snapshot returned by `agy --output-format json --print=/usage`.
type AgyQuota struct {
	Available bool            `json:"available"`
	Detail    string          `json:"detail,omitempty"`
	Groups    []AgyQuotaGroup `json:"groups,omitempty"`
}

// AgyAccountQuota is the official /usage snapshot for one Antigravity pool account.
type AgyAccountQuota struct {
	ProfileID string   `json:"profileId"`
	Name      string   `json:"name"`
	Email     string   `json:"email,omitempty"`
	Quota     AgyQuota `json:"quota"`
}

// AIUsageReport is the full snapshot shown in the IDE's AI Usage & Limits page.
type AIUsageReport struct {
	GeneratedAt   string            `json:"generatedAt"`
	AgyQuota      AgyQuota          `json:"agyQuota"`
	AgyAccounts   []AgyAccountQuota `json:"agyAccounts,omitempty"`
	OpenCodeQuota OpenCodeQuota     `json:"openCodeQuota"`
	Providers     []AIProviderUsage `json:"providers"`
}

type usageEvent struct {
	Timestamp    time.Time `json:"ts"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model,omitempty"`
	InputTokens  int64     `json:"in"`
	OutputTokens int64     `json:"out"`
	TotalTokens  int64     `json:"tot"`
}

type usageStoreFile struct {
	Version int                       `json:"version"`
	Events  []usageEvent              `json:"events"`
	Limits  map[string]ProviderLimits `json:"limits,omitempty"`
}

var (
	usageMu                sync.Mutex
	usageFilePathFn        = defaultUsageFilePath
	usageFlushInterval     = 2 * time.Second
	usageMemState          *usageMem
	usageFlushTimer        *time.Timer
	fetchAgyUsageFn        = FetchAgyUsage
	fetchOpenCodeUsageFn   = FetchOpenCodeUsage
	listAgyProfilesUsageFn = listAgyProfilesForUsage
	activateAgyProfileFn   = ActivateAgyProfileSession
	readLiveAgyCredFn      = ReadLiveAgyOAuthCredential
	writeLiveAgyCredFn     = WriteLiveAgyOAuthCredential
	hasAgyVaultSessionFn   = HasAgyVaultSession
	restoreLiveAgyCredFn   = EnsureLiveAgyCredentialRestored
)

// usageMem buffers ledger events so RecordAIUsage does not rewrite the whole
// JSON file on every call. A crash loses only the events since the last flush.
// pending and limitEdits are this process's unsaved delta. The flush re-reads
// the file under an exclusive lock and merges that delta, so a concurrent IDE,
// CLI, or hivemind serve does not lose its own writes.
type usageMem struct {
	path       string
	store      usageStoreFile
	pending    []usageEvent
	limitEdits map[string]limitEdit
	dirty      bool
	modTime    time.Time
}

type limitEdit struct {
	remove bool
	value  ProviderLimits
}

func defaultUsageFilePath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(base) == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", fmt.Errorf("diretório de configuração indisponível: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "HiveMind", "ai-usage.json"), nil
}

// CanonicalUsageProvider maps a model spec or provider name to a stable usage bucket key.
func CanonicalUsageProvider(provider, modelSpec string) string {
	m := strings.ToLower(strings.TrimSpace(modelSpec))
	switch {
	case strings.HasPrefix(m, "cursor:") || strings.HasPrefix(m, "cursor/") || m == "cursor":
		return "cursor"
	case strings.HasPrefix(m, "opencode-go:") || strings.HasPrefix(m, "opencode-go/") || m == "opencode-go":
		return "opencode-go"
	case strings.HasPrefix(m, "opencode:") || strings.HasPrefix(m, "opencode/") || strings.HasPrefix(m, "zen:") || m == "opencode":
		return "opencode"
	case strings.HasPrefix(m, "agy:") || m == "agy":
		return "agy"
	}
	p := strings.ToLower(strings.TrimSpace(provider))
	switch p {
	case "cursor":
		return "cursor"
	case "agy", "antigravity":
		return "agy"
	case "opencode-go", "opencode_go":
		return "opencode-go"
	case "opencode", "opencode-zen", "zen":
		return "opencode"
	case "gemini", "vertex", "google":
		return "gemini"
	case "anthropic", "claude":
		return "anthropic"
	case "openai", "openai-responses", "chatgpt":
		return "openai"
	case "openrouter":
		return "openrouter"
	default:
		if strings.HasPrefix(m, "gemini") {
			return "gemini"
		}
		if strings.HasPrefix(m, "claude") {
			return "anthropic"
		}
		if strings.HasPrefix(m, "gpt") || strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4") || strings.HasPrefix(m, "grok") {
			return "openai"
		}
		if strings.Contains(m, "/") {
			// Vendor/model IDs (anthropic/claude-*, openai/gpt-*, ...) come from OpenRouter.
			return "openrouter"
		}
		return "agy"
	}
}

func loadUsageStoreLocked() (usageStoreFile, string, error) {
	if err := ensureUsageLocked(); err != nil {
		return usageStoreFile{}, "", err
	}
	return usageMemState.store, usageMemState.path, nil
}

func ensureUsageLocked() error {
	path, err := usageFilePathFn()
	if err != nil {
		return err
	}
	if usageMemState != nil && usageMemState.path == path {
		if !usageMemState.dirty {
			if info, statErr := os.Stat(path); statErr == nil && info.ModTime().After(usageMemState.modTime) {
				return loadUsageMemLocked(path)
			}
		}
		return nil
	}
	if usageMemState != nil && usageMemState.dirty {
		_ = flushUsageLocked()
	}
	return loadUsageMemLocked(path)
}

func loadUsageMemLocked(path string) error {
	store := usageStoreFile{
		Version: 1,
		Limits:  make(map[string]ProviderLimits),
	}
	var mod time.Time
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &store)
		if store.Version == 0 {
			store.Version = 1
		}
		if store.Limits == nil {
			store.Limits = make(map[string]ProviderLimits)
		}
		if info, statErr := os.Stat(path); statErr == nil {
			mod = info.ModTime()
		}
	}
	usageMemState = &usageMem{path: path, store: store, modTime: mod}
	return nil
}

func flushUsageLocked() error {
	if usageFlushTimer != nil {
		usageFlushTimer.Stop()
		usageFlushTimer = nil
	}
	if usageMemState == nil || !usageMemState.dirty {
		return nil
	}
	unlock, err := lockUsageFile(usageMemState.path)
	if err != nil {
		return err
	}
	defer unlock()

	disk := readUsageFile(usageMemState.path)
	merged := mergeUsage(disk, usageMemState.pending, usageMemState.limitEdits)
	cutoff := time.Now().Add(-31 * 24 * time.Hour)
	pruned := make([]usageEvent, 0, len(merged.Events))
	for _, ev := range merged.Events {
		if !ev.Timestamp.Before(cutoff) {
			pruned = append(pruned, ev)
		}
	}
	merged.Events = pruned
	if err := writeUsageAtomic(usageMemState.path, merged); err != nil {
		return err
	}
	usageMemState.store = merged
	usageMemState.pending = nil
	usageMemState.limitEdits = nil
	usageMemState.dirty = false
	if info, statErr := os.Stat(usageMemState.path); statErr == nil {
		usageMemState.modTime = info.ModTime()
	}
	return nil
}

func readUsageFile(path string) usageStoreFile {
	store := usageStoreFile{Version: 1, Limits: make(map[string]ProviderLimits)}
	data, err := os.ReadFile(path)
	if err != nil {
		return store
	}
	_ = json.Unmarshal(data, &store)
	if store.Version == 0 {
		store.Version = 1
	}
	if store.Limits == nil {
		store.Limits = make(map[string]ProviderLimits)
	}
	return store
}

func mergeUsage(disk usageStoreFile, pending []usageEvent, edits map[string]limitEdit) usageStoreFile {
	if disk.Version == 0 {
		disk.Version = 1
	}
	if disk.Limits == nil {
		disk.Limits = make(map[string]ProviderLimits)
	}
	for _, ev := range pending {
		if containsUsageEvent(disk.Events, ev) {
			continue
		}
		disk.Events = append(disk.Events, ev)
	}
	for bucket, edit := range edits {
		if edit.remove {
			delete(disk.Limits, bucket)
			continue
		}
		disk.Limits[bucket] = edit.value
	}
	return disk
}

func containsUsageEvent(events []usageEvent, ev usageEvent) bool {
	for _, got := range events {
		if got.Timestamp.Equal(ev.Timestamp) && got.Provider == ev.Provider && got.Model == ev.Model &&
			got.InputTokens == ev.InputTokens && got.OutputTokens == ev.OutputTokens && got.TotalTokens == ev.TotalTokens {
			return true
		}
	}
	return false
}

func writeUsageAtomic(path string, store usageStoreFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return os.WriteFile(path, data, 0o600)
	}
	return nil
}

func scheduleUsageFlushLocked() {
	if usageFlushTimer != nil {
		return
	}
	usageFlushTimer = time.AfterFunc(usageFlushInterval, func() {
		usageMu.Lock()
		defer usageMu.Unlock()
		_ = flushUsageLocked()
	})
}

// FlushAIUsage writes any buffered usage events. Call it when the process closes.
// Events still sitting in memory are the only ones a crash can drop.
func FlushAIUsage() {
	usageMu.Lock()
	defer usageMu.Unlock()
	_ = flushUsageLocked()
}

func resetUsageMemForTest() {
	usageMu.Lock()
	defer usageMu.Unlock()
	if usageFlushTimer != nil {
		usageFlushTimer.Stop()
		usageFlushTimer = nil
	}
	usageMemState = nil
}

// RecordAIUsage appends one completed LLM call's token counts into the local HiveMind usage ledger.
func RecordAIUsage(provider, modelSpec string, inTok, outTok, totTok int64) {
	if totTok == 0 && (inTok > 0 || outTok > 0) {
		totTok = inTok + outTok
	}
	if totTok <= 0 && inTok <= 0 && outTok <= 0 {
		return
	}
	bucket := CanonicalUsageProvider(provider, modelSpec)
	usageMu.Lock()
	defer usageMu.Unlock()
	if err := ensureUsageLocked(); err != nil || usageMemState == nil {
		return
	}
	ev := usageEvent{
		Timestamp:    time.Now().UTC(),
		Provider:     bucket,
		Model:        strings.TrimSpace(modelSpec),
		InputTokens:  inTok,
		OutputTokens: outTok,
		TotalTokens:  totTok,
	}
	usageMemState.store.Events = append(usageMemState.store.Events, ev)
	usageMemState.pending = append(usageMemState.pending, ev)
	usageMemState.dirty = true
	scheduleUsageFlushLocked()
}

// SaveAIUsageLimits stores optional user-configured 5h and 7d reference token limits for a provider.
func SaveAIUsageLimits(provider string, fiveHourTokens, weeklyTokens int64) error {
	bucket := CanonicalUsageProvider(provider, "")
	if fiveHourTokens < 0 {
		fiveHourTokens = 0
	}
	if weeklyTokens < 0 {
		weeklyTokens = 0
	}
	usageMu.Lock()
	defer usageMu.Unlock()
	if err := ensureUsageLocked(); err != nil || usageMemState == nil {
		return err
	}
	if usageMemState.store.Limits == nil {
		usageMemState.store.Limits = make(map[string]ProviderLimits)
	}
	if usageMemState.limitEdits == nil {
		usageMemState.limitEdits = make(map[string]limitEdit)
	}
	if fiveHourTokens == 0 && weeklyTokens == 0 {
		delete(usageMemState.store.Limits, bucket)
		usageMemState.limitEdits[bucket] = limitEdit{remove: true}
	} else {
		value := ProviderLimits{FiveHourTokens: fiveHourTokens, WeeklyTokens: weeklyTokens}
		usageMemState.store.Limits[bucket] = value
		usageMemState.limitEdits[bucket] = limitEdit{value: value}
	}
	usageMemState.dirty = true
	return flushUsageLocked()
}

// ParseOpenCodeUsageJSON parses the OpenCode Go usage response JSON into OpenCodeQuota.
func ParseOpenCodeUsageJSON(data []byte) (OpenCodeQuota, error) {
	type rawWin struct {
		Status   string  `json:"status"`
		Percent  float64 `json:"percent"`
		UsedPct  float64 `json:"usedPercent"`
		ResetsAt string  `json:"resetsAt"`
		ResetAt  string  `json:"reset_at"`
	}
	type rawBody struct {
		Rolling  *rawWin `json:"rolling"`
		FiveHour *rawWin `json:"fiveHour"`
		Weekly   *rawWin `json:"weekly"`
		Monthly  *rawWin `json:"monthly"`
	}
	var wrapper struct {
		Usage    *rawBody `json:"usage"`
		Rolling  *rawWin  `json:"rolling"`
		FiveHour *rawWin  `json:"fiveHour"`
		Weekly   *rawWin  `json:"weekly"`
		Monthly  *rawWin  `json:"monthly"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return OpenCodeQuota{}, err
	}
	body := wrapper.Usage
	if body == nil {
		body = &rawBody{
			Rolling:  wrapper.Rolling,
			FiveHour: wrapper.FiveHour,
			Weekly:   wrapper.Weekly,
			Monthly:  wrapper.Monthly,
		}
	}
	conv := func(rw *rawWin) RemoteQuotaWindow {
		if rw == nil {
			return RemoteQuotaWindow{}
		}
		pct := rw.Percent
		if pct == 0 && rw.UsedPct > 0 {
			pct = rw.UsedPct
		}
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		resets := strings.TrimSpace(rw.ResetsAt)
		if resets == "" {
			resets = strings.TrimSpace(rw.ResetAt)
		}
		return RemoteQuotaWindow{
			Percent:  pct,
			ResetsAt: resets,
			Status:   strings.TrimSpace(rw.Status),
			HasData:  true,
		}
	}
	rolling := body.Rolling
	if rolling == nil {
		rolling = body.FiveHour
	}
	q := OpenCodeQuota{
		Rolling5h: conv(rolling),
		Weekly:    conv(body.Weekly),
		Monthly:   conv(body.Monthly),
	}
	q.Available = q.Rolling5h.HasData || q.Weekly.HasData || q.Monthly.HasData
	if q.Available {
		q.Detail = "Cota oficial obtida via API OpenCode Go"
	} else {
		q.Detail = "API respondeu sem janelas de cota"
	}
	return q, nil
}

// FetchOpenCodeUsage queries GET https://opencode.ai/zen/go/v1/usage when an OpenCode API key is configured.
func FetchOpenCodeUsage(ctx context.Context) OpenCodeQuota {
	apiKey, zenBase, _ := LoadOpenCodeCredentials()
	if strings.TrimSpace(apiKey) == "" {
		return OpenCodeQuota{
			Available: false,
			Detail:    "Configure o token OpenCode para consultar a cota oficial de 5h / semanal.",
		}
	}
	goBase := resolveGoCatalogURL(zenBase)
	endpoint := strings.TrimRight(goBase, "/") + "/usage"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return OpenCodeQuota{Available: false, Detail: "Falha ao montar requisição OpenCode Go"}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-opencode-client", "hivemind-ide/1.0")

	client := &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return OpenCodeQuota{Available: false, Detail: "Sem resposta da API de cota OpenCode Go (usando telemetria local)"}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return OpenCodeQuota{Available: false, Detail: "Falha ao ler resposta de cota OpenCode Go"}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return OpenCodeQuota{Available: false, Detail: "Token OpenCode sem acesso à cota Go (HTTP " + fmt.Sprintf("%d", resp.StatusCode) + ")"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return OpenCodeQuota{Available: false, Detail: fmt.Sprintf("Endpoint de cota OpenCode Go indisponível (HTTP %d)", resp.StatusCode)}
	}
	q, err := ParseOpenCodeUsageJSON(body)
	if err != nil {
		return OpenCodeQuota{Available: false, Detail: "Formato inesperado na resposta de cota OpenCode Go"}
	}
	return q
}

// ParseAgyUsageJSON parses the JSON output of `agy --output-format json --print=/usage`.
func ParseAgyUsageJSON(data []byte) (AgyQuota, error) {
	trimmed := bytes.TrimSpace(data)
	// agy may emit leading status lines before the JSON object on mixed streams; slice from first '{'.
	if idx := bytes.IndexByte(trimmed, '{'); idx > 0 {
		trimmed = trimmed[idx:]
	}
	var env struct {
		Status  string `json:"status"`
		Error   string `json:"error"`
		Command struct {
			Name string `json:"name"`
			Data struct {
				Description string `json:"description"`
				Groups      []struct {
					Name        string `json:"name"`
					Description string `json:"description"`
					Buckets     []struct {
						ID                string  `json:"id"`
						Name              string  `json:"name"`
						Description       string  `json:"description"`
						Window            string  `json:"window"`
						RemainingFraction float64 `json:"remaining_fraction"`
						ResetTime         string  `json:"reset_time"`
					} `json:"buckets"`
				} `json:"groups"`
			} `json:"data"`
		} `json:"command"`
	}
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return AgyQuota{}, err
	}
	if env.Status != "" && !strings.EqualFold(env.Status, "SUCCESS") {
		msg := strings.TrimSpace(env.Error)
		if msg == "" {
			msg = env.Status
		}
		return AgyQuota{Available: false, Detail: "agy /usage retornou erro: " + msg}, nil
	}
	groups := make([]AgyQuotaGroup, 0, len(env.Command.Data.Groups))
	for _, g := range env.Command.Data.Groups {
		if len(g.Buckets) == 0 {
			continue
		}
		var b5h, bWeekly, bOther []AgyQuotaBucket
		for _, b := range g.Buckets {
			rem := b.RemainingFraction * 100
			if rem < 0 {
				rem = 0
			}
			if rem > 100 {
				rem = 100
			}
			rem = math.Round(rem*10) / 10
			used := math.Round((100-rem)*10) / 10
			if used < 0 {
				used = 0
			}
			if used > 100 {
				used = 100
			}
			win := strings.ToLower(strings.TrimSpace(b.Window))
			item := AgyQuotaBucket{
				ID:               strings.TrimSpace(b.ID),
				Name:             strings.TrimSpace(b.Name),
				Window:           win,
				UsedPercent:      used,
				RemainingPercent: rem,
				ResetTime:        strings.TrimSpace(b.ResetTime),
				Description:      strings.TrimSpace(b.Description),
			}
			switch win {
			case "5h":
				b5h = append(b5h, item)
			case "weekly":
				bWeekly = append(bWeekly, item)
			default:
				bOther = append(bOther, item)
			}
		}
		ordered := make([]AgyQuotaBucket, 0, len(g.Buckets))
		ordered = append(ordered, b5h...)
		ordered = append(ordered, bWeekly...)
		ordered = append(ordered, bOther...)
		groups = append(groups, AgyQuotaGroup{
			Name:        strings.TrimSpace(g.Name),
			Description: strings.TrimSpace(g.Description),
			Buckets:     ordered,
		})
	}
	if len(groups) == 0 {
		return AgyQuota{Available: false, Detail: "agy /usage respondeu sem grupos de cota"}, nil
	}
	return AgyQuota{
		Available: true,
		Detail:    "Cota oficial obtida via agy /usage (0 tokens consumidos)",
		Groups:    groups,
	}, nil
}

// FetchAgyUsage invokes the documented `agy --output-format json --print=/usage` command
// (0 turns / 0 tokens consumed) to retrieve live 5h and weekly quota percentages per model group.
func FetchAgyUsage(ctx context.Context) AgyQuota {
	bin, err := LookAgy()
	if err != nil {
		return AgyQuota{
			Available: false,
			Detail:    "Antigravity CLI (agy) não instalado no PATH.",
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	dir, err := os.MkdirTemp("", "hivemind-agy-usage-")
	if err != nil {
		return AgyQuota{Available: false, Detail: "Falha ao preparar diretório temporário para agy /usage."}
	}
	defer os.RemoveAll(dir)

	cmd := agyCommand(runCtx, bin, []string{
		"--output-format", "json",
		"--print-timeout", "12s",
		"--print=/usage",
	})
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if stdout.Len() > 0 {
		if q, parseErr := ParseAgyUsageJSON(stdout.Bytes()); parseErr == nil && (q.Available || q.Detail != "") {
			return q
		}
	}
	if runCtx.Err() != nil {
		return AgyQuota{Available: false, Detail: "Consulta `agy /usage` excedeu o tempo limite."}
	}
	errText := strings.TrimSpace(stderr.String())
	if looksLikeAuth(errText) {
		return AgyQuota{Available: false, Detail: "Antigravity CLI não autenticado. Rode `agy` no terminal para entrar."}
	}
	if runErr != nil {
		return AgyQuota{Available: false, Detail: "Não foi possível consultar `agy /usage` agora (usando telemetria local)."}
	}
	return AgyQuota{Available: false, Detail: "Resposta vazia de `agy /usage`."}
}

func listAgyProfilesForUsage() []ProviderProfileSummary {
	ov := ListProfilesOverview("")
	out := make([]ProviderProfileSummary, 0)
	for _, p := range ov.Profiles {
		if p.Provider == "agy" {
			out = append(out, p)
		}
	}
	return out
}

// fetchAllAgyAccountQuotas queries /usage for every Antigravity pool account.
// It temporarily switches the live OAuth slot per vault and restores the previous session.
func fetchAllAgyAccountQuotas(ctx context.Context) []AgyAccountQuota {
	profiles := listAgyProfilesUsageFn()
	if len(profiles) == 0 {
		q := fetchAgyUsageFn(ctx)
		if !q.Available && strings.TrimSpace(q.Detail) == "" {
			return nil
		}
		return []AgyAccountQuota{{
			ProfileID: "",
			Name:      "Antigravity CLI (agy)",
			Quota:     q,
		}}
	}

	prevRaw, _, _ := readLiveAgyCredFn()
	defer func() {
		if len(prevRaw) > 0 {
			_ = writeLiveAgyCredFn(prevRaw)
			return
		}
		restoreLiveAgyCredFn()
	}()

	out := make([]AgyAccountQuota, 0, len(profiles))
	for _, p := range profiles {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			name = p.ID
		}
		email := ""
		if ok, em := hasAgyVaultSessionFn(p.ID); ok {
			email = strings.TrimSpace(em)
		}
		acc := AgyAccountQuota{ProfileID: p.ID, Name: name, Email: email}
		if err := activateAgyProfileFn(p.ID); err != nil {
			acc.Quota = AgyQuota{Available: false, Detail: err.Error()}
			out = append(out, acc)
			continue
		}
		acc.Quota = fetchAgyUsageFn(ctx)
		out = append(out, acc)
	}
	return out
}

// GetAIUsageReport returns live Antigravity (/usage) and OpenCode Go quotas plus rolling 5h/weekly/monthly local telemetry.
func GetAIUsageReport(ctx context.Context) AIUsageReport {
	now := time.Now().UTC()
	cut5h := now.Add(-5 * time.Hour)
	cut7d := now.Add(-7 * 24 * time.Hour)
	cut30d := now.Add(-30 * 24 * time.Hour)

	usageMu.Lock()
	store, _, _ := loadUsageStoreLocked()
	events := append([]usageEvent(nil), store.Events...)
	limits := make(map[string]ProviderLimits, len(store.Limits))
	for k, v := range store.Limits {
		limits[k] = v
	}
	usageMu.Unlock()

	labels := map[string]string{
		"agy":         "Antigravity CLI (agy)",
		"cursor":      "Cursor CLI (agent)",
		"opencode-go": "OpenCode Go",
		"opencode":    "OpenCode Zen",
		"gemini":      "Google Gemini",
		"anthropic":   "Anthropic (Claude)",
		"openai":      "OpenAI / Grok",
		"openrouter":  "OpenRouter",
	}
	order := []string{"agy", "cursor", "opencode-go", "opencode", "gemini", "anthropic", "openai", "openrouter"}
	byProv := make(map[string]*AIProviderUsage, len(order))
	for _, p := range order {
		byProv[p] = &AIProviderUsage{
			Provider: p,
			Label:    labels[p],
			Limits:   limits[p],
		}
	}

	oldest5h := make(map[string]time.Time)
	oldest7d := make(map[string]time.Time)

	for _, ev := range events {
		bucket := CanonicalUsageProvider(ev.Provider, ev.Model)
		dst, ok := byProv[bucket]
		if !ok {
			dst = &AIProviderUsage{Provider: bucket, Label: bucket, Limits: limits[bucket]}
			byProv[bucket] = dst
			order = append(order, bucket)
		}
		if !ev.Timestamp.Before(cut30d) {
			dst.WindowMonthly.InputTokens += ev.InputTokens
			dst.WindowMonthly.OutputTokens += ev.OutputTokens
			dst.WindowMonthly.TotalTokens += ev.TotalTokens
			dst.WindowMonthly.Calls++
		}
		if !ev.Timestamp.Before(cut7d) {
			dst.WindowWeekly.InputTokens += ev.InputTokens
			dst.WindowWeekly.OutputTokens += ev.OutputTokens
			dst.WindowWeekly.TotalTokens += ev.TotalTokens
			dst.WindowWeekly.Calls++
			if prev, exists := oldest7d[bucket]; !exists || ev.Timestamp.Before(prev) {
				oldest7d[bucket] = ev.Timestamp
			}
		}
		if !ev.Timestamp.Before(cut5h) {
			dst.Window5h.InputTokens += ev.InputTokens
			dst.Window5h.OutputTokens += ev.OutputTokens
			dst.Window5h.TotalTokens += ev.TotalTokens
			dst.Window5h.Calls++
			if prev, exists := oldest5h[bucket]; !exists || ev.Timestamp.Before(prev) {
				oldest5h[bucket] = ev.Timestamp
			}
		}
	}

	for p, ts := range oldest5h {
		if dst := byProv[p]; dst != nil {
			dst.Window5h.OldestAt = ts.Format(time.RFC3339)
		}
	}
	for p, ts := range oldest7d {
		if dst := byProv[p]; dst != nil {
			dst.WindowWeekly.OldestAt = ts.Format(time.RFC3339)
		}
	}

	var (
		agyAccounts []AgyAccountQuota
		ocQ         OpenCodeQuota
		wg          sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		agyAccounts = fetchAllAgyAccountQuotas(ctx)
	}()
	go func() {
		defer wg.Done()
		ocQ = fetchOpenCodeUsageFn(ctx)
	}()
	wg.Wait()

	agyQ := AgyQuota{}
	if len(agyAccounts) > 0 {
		agyQ = agyAccounts[0].Quota
	}

	out := AIUsageReport{
		GeneratedAt:   now.Format(time.RFC3339),
		AgyQuota:      agyQ,
		AgyAccounts:   agyAccounts,
		OpenCodeQuota: ocQ,
	}

	ocKey, _, _ := LoadOpenCodeCredentials()
	hasOpenCode := strings.TrimSpace(ocKey) != ""
	hasAgy := len(agyAccounts) > 0 || agyQ.Available || AgyAvailable()

	for _, p := range order {
		u := byProv[p]
		hasActivity := u.WindowMonthly.Calls > 0 || u.WindowMonthly.TotalTokens > 0 || u.Limits.FiveHourTokens > 0 || u.Limits.WeeklyTokens > 0
		include := hasActivity
		switch p {
		case "agy":
			include = include || hasAgy
		case "opencode-go":
			include = include || ocQ.Available || hasOpenCode
		}
		if include {
			out.Providers = append(out.Providers, *u)
		}
	}
	// Guarantee at least agy + opencode-go on fresh unconfigured machines so the page is never empty.
	if len(out.Providers) == 0 {
		out.Providers = append(out.Providers, *byProv["agy"], *byProv["opencode-go"])
	}
	return out
}
