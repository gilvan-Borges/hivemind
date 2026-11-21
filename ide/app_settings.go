package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// GetInitialState loads the initial workspace and recent list without blocking on agy.
func (a *App) GetInitialState() InitialState {
	dir := a.initialDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	proj, _ := a.OpenProject(dir)
	recent := idecore.LoadRecentProjects("")

	a.mu.Lock()
	agy := a.agyStatus
	models := append([]modelx.AgyModel(nil), a.modelsCache...)
	ocStatus := a.openCodeStatus
	ocGoModels := append([]modelx.AgyModel(nil), a.openCodeGoModels...)
	ocModels := append([]modelx.AgyModel(nil), a.openCodeModels...)
	a.mu.Unlock()

	return InitialState{
		InitialWorkspace: dir,
		InitialProject:   proj,
		RecentProjects:   recent,
		AgyStatus:        agy,
		Models:           models,
		OpenCodeStatus:   ocStatus,
		OpenCodeGoModels: ocGoModels,
		OpenCodeModels:   ocModels,
		GoVersion:        runtime.Version(),
		OS:               runtime.GOOS,
	}
}

// BootstrapOpenCode probes the OpenCode Go & Zen HTTP APIs using the configured token
// (OPENCODE_API_KEY or user credentials.json) and fetches both live model catalogs.
func (a *App) BootstrapOpenCode(force bool) OpenCodeBootstrap {
	a.mu.Lock()
	if !force && !a.openCodeCachedAt.IsZero() && time.Since(a.openCodeCachedAt) < 5*time.Minute && (len(a.openCodeGoModels) > 0 || len(a.openCodeModels) > 0) {
		out := OpenCodeBootstrap{
			Status:   a.openCodeStatus,
			GoModels: append([]modelx.AgyModel(nil), a.openCodeGoModels...),
			Models:   append([]modelx.AgyModel(nil), a.openCodeModels...),
		}
		a.mu.Unlock()
		return out
	}
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	status, goModels, zenModels := modelx.ProbeAndListOpenCode(ctx)

	a.mu.Lock()
	a.openCodeStatus = status
	a.openCodeGoModels = goModels
	a.openCodeModels = zenModels
	a.openCodeCachedAt = time.Now()
	a.mu.Unlock()

	return OpenCodeBootstrap{
		Status:   status,
		GoModels: goModels,
		Models:   zenModels,
	}
}

// SaveOpenCodeConfig stores or clears the OpenCode Zen API token in the user's local
// credentials.json (never inside the project Git repo) and refreshes the model catalog.
func (a *App) SaveOpenCodeConfig(apiKey, baseURL string) (OpenCodeBootstrap, error) {
	if err := modelx.SaveOpenCodeCredentials(apiKey, baseURL); err != nil {
		return OpenCodeBootstrap{}, err
	}
	return a.BootstrapOpenCode(true), nil
}

// BootstrapAgy probes agy and fetches its models in a single process invocation,
// caching the result for 5 minutes unless force is true.
func (a *App) BootstrapAgy(force bool) AgyBootstrap {
	a.mu.Lock()
	if !force && !a.agyCachedAt.IsZero() && time.Since(a.agyCachedAt) < 5*time.Minute && a.agyStatus.Found {
		out := AgyBootstrap{
			Status: a.agyStatus,
			Models: append([]modelx.AgyModel(nil), a.modelsCache...),
		}
		a.mu.Unlock()
		return out
	}
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	status, models := modelx.ProbeAndListAgy(ctx)

	a.mu.Lock()
	a.agyStatus = status
	a.modelsCache = models
	a.agyCachedAt = time.Now()
	a.mu.Unlock()

	return AgyBootstrap{
		Status: status,
		Models: models,
	}
}

// ListModels fetches available models using the cached agy bootstrap.
func (a *App) ListModels() ([]modelx.AgyModel, error) {
	boot := a.BootstrapAgy(false)
	if !boot.Status.LoggedIn && len(boot.Models) == 0 {
		return nil, fmt.Errorf("%s", boot.Status.Detail)
	}
	return boot.Models, nil
}

// DoctorAgy forces a fresh `agy` check and refreshes the model cache.
func (a *App) DoctorAgy() modelx.AgyStatus {
	return a.BootstrapAgy(true).Status
}

// ScanAllProviders scans every supported LLM provider and saved account in parallel
// (Antigravity Google pool, OpenCode, OpenRouter, Anthropic, OpenAI/Grok, Gemini/Vertex,
// and Cursor CLI) and refreshes cached IDE bootstrap statuses.
func (a *App) ScanAllProviders(workspace string) modelx.ProvidersScanReport {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	report := modelx.ScanAllProviders(ctx, workspace)
	// Refresh Jev health summary quietly in background after scan.
	hs := modelx.DefaultJevHealthStore()
	hs.SyncCredentialFingerprints(modelx.JevCredentialFingerprints())
	a.emitProviderHealth(hs.Summary(a.currentJevPolicy(), modelx.JevCredentialPresence()), false)
	return report
}

// GetAPIKeysStatus returns the masked persisted LLM API keys/settings
// (env vars always take precedence at call time; this shows the saved store).
func (a *App) GetAPIKeysStatus() map[string]string {
	return modelx.StoredAPIKeysStatus()
}

// SaveAPIKeys persists LLM API keys/settings in the user config dir
// (outside the repo) and returns the new masked status. Empty values clear.
func (a *App) SaveAPIKeys(entries map[string]string) (map[string]string, error) {
	if entries != nil {
		_, hasOCKey := entries["OPENCODE_API_KEY"]
		_, hasOCBase := entries["OPENCODE_BASE_URL"]
		if hasOCKey || hasOCBase {
			curKey, curBase, _ := modelx.LoadOpenCodeCredentials()
			newKey := curKey
			newBase := curBase
			if hasOCKey {
				newKey = strings.TrimSpace(entries["OPENCODE_API_KEY"])
			}
			if hasOCBase {
				newBase = strings.TrimSpace(entries["OPENCODE_BASE_URL"])
			}
			if err := modelx.SaveOpenCodeCredentials(newKey, newBase); err != nil {
				return nil, err
			}
		}
	}
	if _, err := modelx.SaveStoredAPIKeys(entries); err != nil {
		return nil, err
	}
	// Re-read unified status including mirrored OpenCode credentials.
	return modelx.StoredAPIKeysStatus(), nil
}

// GetProviderProfilesOverview returns detected workspace org info, saved multi-account
// provider profiles (secrets masked), and resolved profile per provider.
func (a *App) GetProviderProfilesOverview(workspace string) modelx.ProfilesOverview {
	return modelx.ListProfilesOverview(workspace)
}

// SaveProviderProfile creates or updates a named account/profile for agy or any LLM provider.
func (a *App) SaveProviderProfile(req modelx.SaveProfileRequest) (modelx.ProfilesOverview, error) {
	return modelx.SaveProviderProfile(req)
}

// DeleteProviderProfile removes a saved provider profile by ID.
func (a *App) DeleteProviderProfile(profileID, workspace string) (modelx.ProfilesOverview, error) {
	return modelx.DeleteProviderProfile(profileID, workspace)
}

// BindProviderWorkspace pins or unpins a workspace+provider to a profile ("", "auto", "global", or profileID).
func (a *App) BindProviderWorkspace(workspace, provider, profileID string) (modelx.ProfilesOverview, error) {
	return modelx.BindProviderWorkspace(workspace, provider, profileID)
}

// LoginAgyProfile opens an interactive terminal window running `gcloud auth application-default login`
// inside the isolated CLOUDSDK_CONFIG directory for the given AGY profile ID.
func (a *App) LoginAgyProfile(profileID string) (string, error) {
	return modelx.LaunchAgyADCLogin(profileID)
}

// ConnectNewAgyGoogleAccount backs up the current Antigravity Google OAuth session into its
// isolated profile folder, clears the live slot, opens the browser Google OAuth login flow via
// `agy`, and waits for the user to paste the authorization code (`4/0A...`) in the modal.
func (a *App) ConnectNewAgyGoogleAccount(workspace string) (modelx.ProfilesOverview, error) {
	return modelx.ConnectNewAgyGoogleAccount(workspace)
}

// SubmitAgyOAuthCode completes the Google OAuth flow by sending the authorization code (`4/0A...`)
// copied from `antigravity.google/oauth-callback` to the waiting `agy` process, then saves the
// new Google account by its email (`id_token_email`) into its isolated vault folder.
func (a *App) SubmitAgyOAuthCode(code, workspace string) (modelx.ProfilesOverview, error) {
	return modelx.SubmitAgyOAuthCode(code, workspace)
}

// CancelAgyOAuthLogin cancels an in-progress Google OAuth login and restores the previous account.
func (a *App) CancelAgyOAuthLogin(workspace string) modelx.ProfilesOverview {
	return modelx.CancelAgyOAuthLogin(workspace)
}

// ActivateAgyAccount switches the live Antigravity Google OAuth session to the saved isolated
// session of profileID (preserving the previous account's session in its own folder).
func (a *App) ActivateAgyAccount(profileID, workspace string) (modelx.ProfilesOverview, error) {
	if err := modelx.ActivateAgyProfileSession(profileID); err != nil {
		return modelx.ListProfilesOverview(workspace), err
	}
	return modelx.ListProfilesOverview(workspace), nil
}

// ListOpenRouterModels returns the full public OpenRouter catalog (cached ~30min).
func (a *App) ListOpenRouterModels() ([]modelx.OpenRouterModel, error) {
	return modelx.OpenRouterModels(a.ctx)
}

// CursorCLIStatus reports the official Cursor CLI without revealing the API key.
func (a *App) CursorCLIStatus() modelx.CursorStatus {
	return modelx.ProbeCursor()
}

// ListCursorModels returns auto plus whatever `agent models` lists for this key.
func (a *App) ListCursorModels() ([]modelx.AgyModel, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	models, _, err := modelx.ListCursorModels(ctx)
	return models, err
}

func (a *App) JiraStatus() jirax.ConnectorStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return jirax.CheckConnection(ctx)
}

// JiraStatusForWorkspace returns Jira status including auto-discovery from workspace/parent MCP configs.
func (a *App) JiraStatusForWorkspace(workspace string) jirax.ConnectorStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return jirax.CheckConnectionForWorkspace(ctx, workspace)
}

// ListJiraProjects fetches available Jira Cloud projects dynamically for the active credentials.
func (a *App) ListJiraProjects() ([]jirax.Project, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	return jirax.ListProjects(ctx)
}

// ListJiraProjectsForWorkspace fetches Jira Cloud projects using workspace/MCP-aware credentials.
func (a *App) ListJiraProjectsForWorkspace(workspace string) ([]jirax.Project, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	return jirax.ListProjectsForWorkspace(ctx, workspace)
}

// ImportJiraFromMCP imports Jira credentials from an existing mcp_config.json (workspace, parent, or user home)
// into the user's native jira-connector.json and returns the live connection status.
func (a *App) ImportJiraFromMCP(workspace string) (jirax.ConnectorStatus, error) {
	if _, err := jirax.ImportFromMCP(workspace); err != nil {
		return jirax.ConnectorStatus{}, err
	}
	st := a.JiraStatusForWorkspace(workspace)
	if st.Connected {
		idecore.EnableProjectJira(workspace, st.DefaultProject)
	}
	return st, nil
}

// SaveJiraConfig stores (or clears, when clear=true) the native Jira Cloud
// credentials outside project repos, returning the refreshed connection status.
func (a *App) SaveJiraConfig(baseURL, email, token, defaultProject string, clear bool) (jirax.ConnectorStatus, error) {
	if err := jirax.SaveCredentials(baseURL, email, token, defaultProject, clear); err != nil {
		return jirax.ConnectorStatus{}, err
	}
	return a.JiraStatus(), nil
}

// SaveJiraProfile creates or updates a named Jira Cloud profile (with optional org patterns
// and optional workspace pinning) and returns refreshed status for bindWorkspace.
func (a *App) SaveJiraProfile(profile jirax.JiraProfile, bindWorkspace string) (jirax.ConnectorStatus, error) {
	if err := jirax.SaveProfile(profile, bindWorkspace); err != nil {
		return jirax.ConnectorStatus{}, err
	}
	st := a.JiraStatusForWorkspace(bindWorkspace)
	if st.Connected && strings.TrimSpace(bindWorkspace) != "" {
		idecore.EnableProjectJira(bindWorkspace, st.DefaultProject)
	}
	return st, nil
}

// DeleteJiraProfile removes a Jira Cloud profile by ID and returns refreshed status for workspace.
func (a *App) DeleteJiraProfile(profileID, workspace string) (jirax.ConnectorStatus, error) {
	if err := jirax.DeleteProfile(profileID); err != nil {
		return jirax.ConnectorStatus{}, err
	}
	return a.JiraStatusForWorkspace(workspace), nil
}

// BindJiraWorkspace binds workspace to a profile ("", "auto", "none", or profileID).
func (a *App) BindJiraWorkspace(workspace, target string) (jirax.ConnectorStatus, error) {
	if err := jirax.BindWorkspace(workspace, target); err != nil {
		return jirax.ConnectorStatus{}, err
	}
	st := a.JiraStatusForWorkspace(workspace)
	if st.Connected && !strings.EqualFold(strings.TrimSpace(target), "none") {
		idecore.EnableProjectJira(workspace, st.DefaultProject)
	}
	return st, nil
}

// OpenExternalURL opens an http/https URL in the system default browser.
func (a *App) OpenExternalURL(rawURL string) error {
	trimmed := strings.TrimSpace(rawURL)
	if !strings.HasPrefix(trimmed, "https://") && !strings.HasPrefix(trimmed, "http://") {
		return fmt.Errorf("URL inválida: %s", rawURL)
	}
	if a.ctx == nil {
		return fmt.Errorf("contexto da janela ainda não está pronto")
	}
	wailsrt.BrowserOpenURL(a.ctx, trimmed)
	return nil
}

// GetAIUsage returns live Antigravity (/usage) and OpenCode Go quota windows (5h/weekly/monthly) plus local HiveMind usage per AI provider.
func (a *App) GetAIUsage() modelx.AIUsageReport {
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	return modelx.GetAIUsageReport(ctx)
}

// SaveAIUsageLimits stores optional 5h and 7d reference token limits for a provider and returns the updated report.
func (a *App) SaveAIUsageLimits(provider string, fiveHourTokens, weeklyTokens int64) (modelx.AIUsageReport, error) {
	if err := modelx.SaveAIUsageLimits(provider, fiveHourTokens, weeklyTokens); err != nil {
		return modelx.AIUsageReport{}, err
	}
	return a.GetAIUsage(), nil
}

// GetJevPolicy returns the local Jev policy. The bridge has no route for this.
func (a *App) GetJevPolicy() jev.Policy {
	return a.currentJevPolicy()
}

// SaveJevPolicy writes policy.json from the local IDE only.
func (a *App) SaveJevPolicy(pol jev.Policy) (jev.Policy, error) {
	if err := jev.SavePolicy(pol); err != nil {
		return jev.Policy{}, err
	}
	return a.currentJevPolicy(), nil
}

func (a *App) currentJevPolicy() jev.Policy {
	pol, err := jev.LoadPolicy()
	if err != nil {
		return jev.DefaultPolicy()
	}
	return pol
}

func (a *App) emitProviderHealth(views []jev.ProviderHealthView, force bool) {
	if a.ctx != nil && !a.smokeCheck {
		wailsrt.EventsEmit(a.ctx, "hivemind:provider-health", views)
	}
	if a.mobile != nil && a.mobile.hub != nil {
		a.mobile.hub.HandleProviderHealth(views, force)
	}
}

func (a *App) startJevHealthMonitor(parent context.Context) {
	monCtx, cancel := context.WithCancel(parent)
	a.healthMonitorCancel = cancel

	hs := modelx.DefaultJevHealthStore()
	hs.SetOnChange(func(opened, allClosed bool) {
		views := hs.Summary(a.currentJevPolicy(), modelx.JevCredentialPresence())
		a.emitProviderHealth(views, opened || allClosed)
	})

	go func() {
		a.emitProviderHealth(a.GetProviderHealth(), false)
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-monCtx.Done():
				return
			case <-ticker.C:
				probeCtx, cancelProbe := context.WithTimeout(monCtx, 15*time.Second)
				views := modelx.ProbeAllProvidersHealth(probeCtx, hs, a.currentJevPolicy())
				cancelProbe()
				a.emitProviderHealth(views, false)
			}
		}
	}()
}

// GetProviderHealth returns the current Jev circuit breaker and daily spend snapshot for every known provider.
func (a *App) GetProviderHealth() []jev.ProviderHealthView {
	hs := modelx.DefaultJevHealthStore()
	hs.SyncCredentialFingerprints(modelx.JevCredentialFingerprints())
	return hs.Summary(a.currentJevPolicy(), modelx.JevCredentialPresence())
}

// ProbeProviders runs recovery probes for degraded/quota-tracked providers and broadcasts the updated health state.
func (a *App) ProbeProviders() []jev.ProviderHealthView {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	hs := modelx.DefaultJevHealthStore()
	views := modelx.ProbeAllProvidersHealth(ctx, hs, a.currentJevPolicy())
	a.emitProviderHealth(views, true)
	return views
}
