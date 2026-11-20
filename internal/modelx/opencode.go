package modelx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/anthropicmodel"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/model/openaimodel"
	"google.golang.org/genai"
)

type sessionIDCtxKey struct{}

// WithSessionID attaches a stable conversation session ID to ctx so OpenCode Go/Zen
// receives a consistent x-opencode-session header for routing and prompt-cache locality.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionIDCtxKey{}, id)
}

func sessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(sessionIDCtxKey{}).(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func newFallbackSessionID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "hivemind-" + hex.EncodeToString(b[:])
}

// openCodeTransport injects the x-opencode-session (required by OpenCode Go) and
// x-opencode-client headers on every outgoing HTTP request.
type openCodeTransport struct {
	base      http.RoundTripper
	fallback  string
	authToken string
}

func newOpenCodeHTTPClient(fallbackSessionID, authToken string) *http.Client {
	if strings.TrimSpace(fallbackSessionID) == "" {
		fallbackSessionID = newFallbackSessionID()
	}
	return &http.Client{
		Transport: &openCodeTransport{
			base:      newRetryTransport(nil),
			fallback:  fallbackSessionID,
			authToken: authToken,
		},
	}
}

func (t *openCodeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	sid := sessionIDFromContext(req.Context())
	if sid == "" {
		sid = t.fallback
	}
	if clone.Header.Get("x-opencode-session") == "" {
		clone.Header.Set("x-opencode-session", sid)
	}
	if clone.Header.Get("x-opencode-client") == "" {
		clone.Header.Set("x-opencode-client", "hivemind-ide/1.0")
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

const (
	// OpenCodeDefaultBaseURL is the OpenCode Zen pay-as-you-go / free gateway URL.
	OpenCodeDefaultBaseURL = "https://opencode.ai/zen/v1"
	// OpenCodeGoBaseURL is the OpenCode Go subscription gateway URL.
	OpenCodeGoBaseURL = "https://opencode.ai/zen/go/v1"
)

// OpenCodeStatus reports token configuration and API reachability for OpenCode Go & Zen.
type OpenCodeStatus struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source"` // "env", "config", or ""
	BaseURL    string `json:"baseUrl"`
	KeyMasked  string `json:"keyMasked"`
	GoCount    int    `json:"goCount"`
	ZenCount   int    `json:"zenCount"`
	ModelCount int    `json:"modelCount"`
	Detail     string `json:"detail"`
}

type openCodeRoute struct {
	Protocol string // "chat-completions", "responses", "messages", "generate-content"
	BaseURL  string
}

var (
	ocRouteMu    sync.RWMutex
	ocRouteCache = map[string]openCodeRoute{} // key: "go:<id>" or "zen:<id>"
)

type storedCredentials struct {
	OpenCodeAPIKey  string `json:"opencode_api_key,omitempty"`
	OpenCodeBaseURL string `json:"opencode_base_url,omitempty"`
}

func credentialsFilePath() (string, error) {
	if base, err := os.UserConfigDir(); err == nil && strings.TrimSpace(base) != "" {
		return filepath.Join(base, "HiveMind", "credentials.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hivemind", "credentials.json"), nil
}

// LoadOpenCodeCredentials resolves the OpenCode API key and base URL from environment
// variables (OPENCODE_API_KEY / OPENCODE_ZEN_API_KEY / OPENCODE_BASE_URL) or the local
// user credentials file (~/.config/HiveMind/credentials.json or %AppData%\HiveMind\credentials.json).
func LoadOpenCodeCredentials() (apiKey, baseURL, source string) {
	baseURL = strings.TrimSpace(os.Getenv("OPENCODE_BASE_URL"))

	if envKey := firstEnv("OPENCODE_API_KEY", "OPENCODE_ZEN_API_KEY"); envKey != "" {
		apiKey = envKey
		source = "env"
	}

	if fileKey, fileBase := readOpenCodeFile(); fileKey != "" || fileBase != "" {
		if apiKey == "" && fileKey != "" {
			apiKey = fileKey
			source = "config"
		}
		if baseURL == "" && fileBase != "" {
			baseURL = fileBase
		}
	}

	if baseURL == "" {
		baseURL = OpenCodeDefaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return apiKey, baseURL, source
}

// SaveOpenCodeCredentials stores or clears the OpenCode API token in the user config dir
// (outside project repositories so secrets are never committed to Git).
func SaveOpenCodeCredentials(apiKey, baseURL string) error {
	path, err := credentialsFilePath()
	if err != nil {
		return err
	}
	var creds storedCredentials
	if existing, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(existing, &creds)
	}
	creds.OpenCodeAPIKey = strings.TrimSpace(apiKey)
	cleanURL := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if cleanURL == "" || cleanURL == OpenCodeDefaultBaseURL {
		creds.OpenCodeBaseURL = ""
	} else {
		creds.OpenCodeBaseURL = cleanURL
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	if err := writeSecretFile(path, append(data, '\n')); err != nil {
		return err
	}
	ocFileMu.Lock()
	ocFileCache.ok = false
	ocFileMu.Unlock()
	return nil
}

var (
	ocFileMu    sync.Mutex
	ocFileCache struct {
		path string
		mod  time.Time
		size int64
		key  string
		base string
		ok   bool
	}
)

func readOpenCodeFile() (string, string) {
	path, err := credentialsFilePath()
	if err != nil {
		return "", ""
	}
	ocFileMu.Lock()
	defer ocFileMu.Unlock()
	info, err := os.Stat(path)
	if err != nil {
		ocFileCache.ok = false
		return "", ""
	}
	if ocFileCache.ok && ocFileCache.path == path && ocFileCache.size == info.Size() && ocFileCache.mod.Equal(info.ModTime()) {
		return ocFileCache.key, ocFileCache.base
	}
	data, err := os.ReadFile(path)
	if err != nil {
		ocFileCache.ok = false
		return "", ""
	}
	var creds storedCredentials
	if json.Unmarshal(data, &creds) != nil {
		ocFileCache.ok = false
		return "", ""
	}
	ocFileCache.path = path
	ocFileCache.mod = info.ModTime()
	ocFileCache.size = info.Size()
	ocFileCache.key = strings.TrimSpace(creds.OpenCodeAPIKey)
	ocFileCache.base = strings.TrimSpace(creds.OpenCodeBaseURL)
	ocFileCache.ok = true
	return ocFileCache.key, ocFileCache.base
}

func maskAPIKey(key string) string {
	k := strings.TrimSpace(key)
	if k == "" {
		return ""
	}
	if len(k) <= 8 {
		return "****"
	}
	return "****" + k[len(k)-4:]
}

// ParseOpenCodeSpec splits a model spec into tier ("go" or "zen") and raw model ID.
// Examples:
//   - "opencode-go:glm-5.3"     -> ("go", "glm-5.3")
//   - "opencode-go/minimax-m3"  -> ("go", "minimax-m3")
//   - "opencode:claude-sonnet-5-5" -> ("zen", "claude-sonnet-5-5")
//   - "zen:gpt-6-astra"         -> ("zen", "gpt-6-astra")
func ParseOpenCodeSpec(spec string) (tier, modelID string) {
	s := strings.TrimSpace(spec)
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "opencode-go:"):
		return "go", strings.TrimSpace(s[len("opencode-go:"):])
	case strings.HasPrefix(lower, "opencode-go/"):
		return "go", strings.TrimSpace(s[len("opencode-go/"):])
	case strings.HasPrefix(lower, "opencode_go:"):
		return "go", strings.TrimSpace(s[len("opencode_go:"):])
	case strings.HasPrefix(lower, "opencode:"):
		return "zen", strings.TrimSpace(s[len("opencode:"):])
	case strings.HasPrefix(lower, "opencode/"):
		return "zen", strings.TrimSpace(s[len("opencode/"):])
	case strings.HasPrefix(lower, "zen:"):
		return "zen", strings.TrimSpace(s[len("zen:"):])
	default:
		return "zen", s
	}
}

// StripOpenCodePrefix extracts the raw OpenCode model ID from any opencode / opencode-go spec.
func StripOpenCodePrefix(spec string) string {
	_, id := ParseOpenCodeSpec(spec)
	return id
}

func isOpenCodeFreeModel(modelID string) bool {
	m := strings.ToLower(strings.TrimSpace(modelID))
	return strings.HasSuffix(m, "-free") || m == "big-pickle"
}

func resolveGoCatalogURL(zenBaseURL string) string {
	if customGo := strings.TrimSpace(os.Getenv("OPENCODE_GO_BASE_URL")); customGo != "" {
		return strings.TrimRight(customGo, "/")
	}
	clean := strings.TrimRight(strings.TrimSpace(zenBaseURL), "/")
	if clean == "" || clean == OpenCodeDefaultBaseURL {
		return OpenCodeGoBaseURL
	}
	if strings.HasSuffix(clean, "/go/v1") {
		return clean
	}
	if strings.HasSuffix(clean, "/v1") {
		return strings.TrimSuffix(clean, "/v1") + "/go/v1"
	}
	return clean + "/go/v1"
}

// fallbackOpenCodeRoute infers protocol and endpoint when the live catalog hasn't been fetched yet.
func fallbackOpenCodeRoute(tier, modelID, zenBaseURL string) openCodeRoute {
	m := strings.ToLower(strings.TrimSpace(modelID))
	base := strings.TrimRight(zenBaseURL, "/")
	if tier == "go" {
		base = resolveGoCatalogURL(zenBaseURL)
	}
	switch {
	case strings.HasPrefix(m, "claude-"):
		return openCodeRoute{Protocol: "messages", BaseURL: base}
	case tier == "go" && (strings.HasPrefix(m, "minimax-m3") && !strings.Contains(m, "flash") || strings.HasPrefix(m, "qwen")):
		return openCodeRoute{Protocol: "messages", BaseURL: base}
	case strings.HasPrefix(m, "gemini-"):
		return openCodeRoute{Protocol: "generate-content", BaseURL: base}
	case strings.HasPrefix(m, "gpt-5"),
		strings.HasPrefix(m, "gpt-6"),
		strings.HasPrefix(m, "o1"),
		strings.HasPrefix(m, "o3"),
		strings.HasPrefix(m, "o4"),
		strings.HasPrefix(m, "grok-4.7"),
		strings.HasPrefix(m, "muse-spark"):
		return openCodeRoute{Protocol: "responses", BaseURL: base}
	default:
		return openCodeRoute{Protocol: "chat-completions", BaseURL: base}
	}
}

func lookupOpenCodeRoute(ctx context.Context, tier, modelID, apiKey, zenBaseURL string) openCodeRoute {
	cacheKey := tier + ":" + strings.ToLower(strings.TrimSpace(modelID))
	ocRouteMu.RLock()
	rt, ok := ocRouteCache[cacheKey]
	ocRouteMu.RUnlock()
	if ok && rt.Protocol != "" && rt.BaseURL != "" {
		return rt
	}

	// Cold start (e.g. CLI run): fetch the tier's /models catalog once to discover exact protocol + base_url.
	catURL := zenBaseURL
	if tier == "go" {
		catURL = resolveGoCatalogURL(zenBaseURL)
	}
	_, _ = fetchOpenCodeCatalog(ctx, tier, catURL, apiKey)

	ocRouteMu.RLock()
	rt, ok = ocRouteCache[cacheKey]
	ocRouteMu.RUnlock()
	if ok && rt.Protocol != "" && rt.BaseURL != "" {
		return rt
	}
	return fallbackOpenCodeRoute(tier, modelID, zenBaseURL)
}

// NewOpenCode constructs an ADK model.LLM backed by the OpenCode Go or OpenCode Zen HTTP API
// using a portable API token (OPENCODE_API_KEY / credentials.json) — no local CLI required.
func NewOpenCode(ctx context.Context, modelName string) (model.LLM, error) {
	tier, modelID := ParseOpenCodeSpec(modelName)
	if modelID == "" || strings.EqualFold(modelID, "opencode") || strings.EqualFold(modelID, "opencode-go") {
		if tier == "go" {
			modelID = "deepseek-v4.1-flash"
		} else {
			modelID = "big-pickle"
		}
	}
	prof, mode, _ := ResolveProfile(ctx, "opencode")
	if mode == "isolated" {
		return nil, fmt.Errorf("nenhum token OpenCode vinculado a esta organização")
	}
	var apiKey, zenBaseURL string
	profileOnly := prof != nil && (len(prof.OrgPatterns) > 0 || mode == "auto_org" || mode == "manual" || mode == "override")
	if prof != nil {
		apiKey = firstNonEmpty(profileEnvValue(prof, "OPENCODE_API_KEY"), profileEnvValue(prof, "OPENCODE_ZEN_API_KEY"))
		zenBaseURL = strings.TrimRight(strings.TrimSpace(profileEnvValue(prof, "OPENCODE_BASE_URL")), "/")
	}
	if apiKey == "" && !profileOnly {
		fileKey, fileBase, _ := LoadOpenCodeCredentials()
		apiKey = fileKey
		if zenBaseURL == "" {
			zenBaseURL = fileBase
		}
	}
	if zenBaseURL == "" {
		zenBaseURL = OpenCodeDefaultBaseURL
	}
	if apiKey == "" {
		if isOpenCodeFreeModel(modelID) {
			apiKey = "public"
		} else {
			return nil, fmt.Errorf("token do OpenCode não configurado: defina OPENCODE_API_KEY ou salve seu token na IDE (barra inferior → OpenCode)")
		}
	}

	rt := lookupOpenCodeRoute(ctx, tier, modelID, apiKey, zenBaseURL)
	cleanBase := strings.TrimRight(rt.BaseURL, "/")
	httpClient := newOpenCodeHTTPClient(sessionIDFromContext(ctx), apiKey)

	switch rt.Protocol {
	case "messages":
		// Anthropic SDK appends "/v1/messages" to BaseURL.
		anthBase := strings.TrimSuffix(cleanBase, "/v1")
		return anthropicmodel.NewModel(ctx, modelID, anthropicmodel.ClientConfig{
			Backend:    anthropicmodel.BackendAnthropic,
			BaseURL:    anthBase,
			APIKey:     apiKey,
			AuthToken:  apiKey,
			HTTPClient: httpClient,
		})
	case "generate-content":
		// Split version suffix ("/v1beta" or "/v1") for genai.HTTPOptions.
		apiVersion := "v1"
		gemBase := cleanBase
		if strings.HasSuffix(gemBase, "/v1beta") {
			apiVersion = "v1beta"
			gemBase = strings.TrimSuffix(gemBase, "/v1beta")
		} else if strings.HasSuffix(gemBase, "/v1") {
			apiVersion = "v1"
			gemBase = strings.TrimSuffix(gemBase, "/v1")
		}
		headers := http.Header{}
		headers.Set("Authorization", "Bearer "+apiKey)
		return gemini.NewModel(ctx, modelID, &genai.ClientConfig{
			APIKey:     apiKey,
			Backend:    genai.BackendGeminiAPI,
			HTTPClient: httpClient,
			HTTPOptions: genai.HTTPOptions{
				BaseURL:    gemBase + "/",
				APIVersion: apiVersion,
				Headers:    headers,
			},
		})
	case "responses":
		return openaimodel.NewModel(ctx, modelID, &openaimodel.ClientConfig{
			APIKey:     apiKey,
			BaseURL:    cleanBase,
			API:        openaimodel.APIResponses,
			HTTPClient: httpClient,
		})
	default:
		return openaimodel.NewModel(ctx, modelID, &openaimodel.ClientConfig{
			APIKey:     apiKey,
			BaseURL:    cleanBase,
			API:        openaimodel.APIChatCompletions,
			HTTPClient: httpClient,
		})
	}
}

type openCodeAPIProtocol struct {
	BaseURL string `json:"base_url"`
}

type openCodeModelsResp struct {
	Data []struct {
		ID      string                         `json:"id"`
		Name    string                         `json:"name"`
		OwnedBy string                         `json:"owned_by"`
		API     map[string]openCodeAPIProtocol `json:"api"`
	} `json:"data"`
}

func fetchOpenCodeCatalog(ctx context.Context, tier, catalogBaseURL, apiKey string) ([]AgyModel, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	authBearer := strings.TrimSpace(apiKey)
	if authBearer == "" {
		authBearer = "public"
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(catalogBaseURL, "/")+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("URL inválida: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+authBearer)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var parsed openCodeModelsResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(parsed.Data))
	models := make([]AgyModel, 0, len(parsed.Data))
	routes := make(map[string]openCodeRoute, len(parsed.Data))

	// Preferred protocol order when a model advertises multiple adapters.
	protoOrder := []string{"chat-completions", "responses", "messages", "generate-content"}

	for _, item := range parsed.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		displayName := strings.TrimSpace(item.Name)
		if displayName == "" {
			displayName = id
		}
		models = append(models, AgyModel{
			Slug: id,
			Name: displayName,
		})

		for _, proto := range protoOrder {
			if cfg, ok := item.API[proto]; ok && strings.TrimSpace(cfg.BaseURL) != "" {
				routes[tier+":"+strings.ToLower(id)] = openCodeRoute{
					Protocol: proto,
					BaseURL:  strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
				}
				break
			}
		}
	}

	if len(routes) > 0 {
		ocRouteMu.Lock()
		for k, v := range routes {
			ocRouteCache[k] = v
		}
		ocRouteMu.Unlock()
	}

	sort.Slice(models, func(i, j int) bool {
		return models[i].Slug < models[j].Slug
	})
	return models, nil
}

// ProbeAndListOpenCode checks the configured OpenCode API token and fetches BOTH the
// OpenCode Go (subscription) and OpenCode Zen (pay-as-you-go/free) model catalogs over HTTPS.
func ProbeAndListOpenCode(ctx context.Context) (OpenCodeStatus, []AgyModel, []AgyModel) {
	apiKey, zenBaseURL, source := LoadOpenCodeCredentials()
	goBaseURL := resolveGoCatalogURL(zenBaseURL)

	st := OpenCodeStatus{
		Configured: apiKey != "",
		Source:     source,
		BaseURL:    zenBaseURL,
		KeyMasked:  maskAPIKey(apiKey),
	}

	var (
		goModels  []AgyModel
		zenModels []AgyModel
		goErr     error
		zenErr    error
		wg        sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		goModels, goErr = fetchOpenCodeCatalog(ctx, "go", goBaseURL, apiKey)
	}()
	go func() {
		defer wg.Done()
		zenModels, zenErr = fetchOpenCodeCatalog(ctx, "zen", zenBaseURL, apiKey)
	}()
	wg.Wait()

	if goErr != nil && zenErr != nil {
		st.Detail = fmt.Sprintf("Falha ao consultar OpenCode (Go/Zen): %v", zenErr)
		return st, nil, nil
	}

	st.GoCount = len(goModels)
	st.ZenCount = len(zenModels)
	st.ModelCount = st.GoCount + st.ZenCount

	if st.Configured {
		origin := "salvo localmente"
		if source == "env" {
			origin = "via OPENCODE_API_KEY"
		}
		st.Detail = fmt.Sprintf("Token %s (%s) · Go: %d · Zen: %d modelos", origin, st.KeyMasked, st.GoCount, st.ZenCount)
	} else {
		st.Detail = fmt.Sprintf("Sem token (somente -free · Go: %d · Zen: %d modelos no catálogo)", st.GoCount, st.ZenCount)
	}
	return st, goModels, zenModels
}
