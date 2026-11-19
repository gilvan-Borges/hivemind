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

// AIUsageReport is the full snapshot shown in the IDE's AI Usage & Limits modal.
type AIUsageReport struct {
	GeneratedAt   string            `json:"generatedAt"`
	AgyQuota      AgyQuota          `json:"agyQuota"`
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
	usageMu              sync.Mutex
	usageFilePathFn      = defaultUsageFilePath
	fetchAgyUsageFn      = FetchAgyUsage
	fetchOpenCodeUsageFn = FetchOpenCodeUsage
)

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
	case strings.HasPrefix(m, "opencode-go:") || strings.HasPrefix(m, "opencode-go/") || m == "opencode-go":
		return "opencode-go"
	case strings.HasPrefix(m, "opencode:") || strings.HasPrefix(m, "opencode/") || strings.HasPrefix(m, "zen:") || m == "opencode":
		return "opencode"
	case strings.HasPrefix(m, "agy:") || m == "agy":
		return "agy"
	}
	p := strings.ToLower(strings.TrimSpace(provider))
	switch p {
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
		return "agy"
	}
}

func loadUsageStoreLocked() (usageStoreFile, string, error) {
	path, err := usageFilePathFn()
	if err != nil {
		return usageStoreFile{}, "", err
	}
	store := usageStoreFile{
		Version: 1,
		Limits:  make(map[string]ProviderLimits),
	}
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &store)
		if store.Limits == nil {
			store.Limits = make(map[string]ProviderLimits)
		}
	}
	return store, path, nil
}

func saveUsageStoreLocked(path string, store usageStoreFile) error {
	cutoff := time.Now().Add(-31 * 24 * time.Hour)
	pruned := store.Events[:0]
	for _, ev := range store.Events {
		if !ev.Timestamp.Before(cutoff) {
			pruned = append(pruned, ev)
		}
	}
	store.Events = pruned
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
	store, path, err := loadUsageStoreLocked()
	if err != nil {
		return
	}
	store.Events = append(store.Events, usageEvent{
		Timestamp:    time.Now().UTC(),
		Provider:     bucket,
		Model:        strings.TrimSpace(modelSpec),
		InputTokens:  inTok,
		OutputTokens: outTok,
		TotalTokens:  totTok,
	})
	_ = saveUsageStoreLocked(path, store)
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
	store, path, err := loadUsageStoreLocked()
	if err != nil {
		return err
	}
	if fiveHourTokens == 0 && weeklyTokens == 0 {
		delete(store.Limits, bucket)
	} else {
		store.Limits[bucket] = ProviderLimits{
			FiveHourTokens: fiveHourTokens,
			WeeklyTokens:   weeklyTokens,
		}
	}
	return saveUsageStoreLocked(path, store)
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
		"opencode-go": "OpenCode Go",
		"opencode":    "OpenCode Zen",
		"gemini":      "Google Gemini",
		"anthropic":   "Anthropic (Claude)",
		"openai":      "OpenAI / Grok",
	}
	order := []string{"agy", "opencode-go", "opencode", "gemini", "anthropic", "openai"}
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
		agyQ AgyQuota
		ocQ  OpenCodeQuota
		wg   sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		agyQ = fetchAgyUsageFn(ctx)
	}()
	go func() {
		defer wg.Done()
		ocQ = fetchOpenCodeUsageFn(ctx)
	}()
	wg.Wait()

	out := AIUsageReport{
		GeneratedAt:   now.Format(time.RFC3339),
		AgyQuota:      agyQ,
		OpenCodeQuota: ocQ,
	}

	ocKey, _, _ := LoadOpenCodeCredentials()
	hasOpenCode := strings.TrimSpace(ocKey) != ""
	hasAgy := agyQ.Available || AgyAvailable()

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
	// Guarantee at least agy + opencode-go on fresh unconfigured machines so the modal is never empty.
	if len(out.Providers) == 0 {
		out.Providers = append(out.Providers, *byProv["agy"], *byProv["opencode-go"])
	}
	return out
}
