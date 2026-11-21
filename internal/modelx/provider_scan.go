package modelx

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// ProviderScanAccount represents a single discovered account, key, or CLI session
// for a provider (strictly secret-safe; raw tokens are never returned).
type ProviderScanAccount struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name"`
	Source     string `json:"source"` // "pool" | "global" | "env" | "cli"
	Active     bool   `json:"active"`
	Ready      bool   `json:"ready"`
	MaskedHint string `json:"maskedHint,omitempty"`
}

// ProviderScanItem describes the discovery and connectivity status of one LLM provider.
type ProviderScanItem struct {
	ID           string                `json:"id"`
	Name         string                `json:"name"`
	Found        bool                  `json:"found"`
	Connected    bool                  `json:"connected"`
	StatusLabel  string                `json:"statusLabel"` // "Conectado" | "Parcial" | "Desconectado" | "Não instalado"
	Source       string                `json:"source,omitempty"`
	Path         string                `json:"path,omitempty"`
	Detail       string                `json:"detail"`
	ModelCount   int                   `json:"modelCount"`
	ModelsSample []string              `json:"modelsSample,omitempty"`
	Accounts     []ProviderScanAccount `json:"accounts"`
}

// ProvidersScanReport aggregates a full parallel scan across every supported provider
// and all saved accounts (Antigravity Google pool, OpenCode, OpenRouter, Anthropic,
// OpenAI/Grok, Google Gemini/Vertex, and Cursor CLI).
type ProvidersScanReport struct {
	ScannedAt          string             `json:"scannedAt"`
	TotalProviders     int                `json:"totalProviders"`
	ConnectedProviders int                `json:"connectedProviders"`
	TotalAccounts      int                `json:"totalAccounts"`
	Providers          []ProviderScanItem `json:"providers"`
}

type providerScanDeps struct {
	listProfiles    func(workspace string) ProfilesOverview
	storedKeys      func() map[string]string
	getenv          func(string) string
	probeAgy        func(context.Context) (AgyStatus, []AgyModel)
	probeOpenCode   func(context.Context) (OpenCodeStatus, []AgyModel, []AgyModel)
	probeOpenRouter func(context.Context) ([]OpenRouterModel, error)
	probeORAuth     func(context.Context) error
	probeCursor     func(context.Context) ([]AgyModel, CursorStatus, error)
	probeTypeSafe   func(context.Context) ([]string, error)
}

func defaultProviderScanDeps() providerScanDeps {
	return providerScanDeps{
		listProfiles:    ListProfilesOverview,
		storedKeys:      StoredAPIKeysStatus,
		getenv:          os.Getenv,
		probeAgy:        ProbeAndListAgy,
		probeOpenCode:   ProbeAndListOpenCode,
		probeOpenRouter: OpenRouterModels,
		probeORAuth:     ProbeOpenRouterAuth,
		probeCursor:     ListCursorModels,
		probeTypeSafe:   ProbeTypeSafeJev,
	}
}

// ScanAllProviders scans every supported LLM provider and saved account in parallel,
// replacing the legacy Antigravity-only doctor check.
func ScanAllProviders(ctx context.Context, workspace string) ProvidersScanReport {
	return scanAllProvidersWithDeps(ctx, workspace, defaultProviderScanDeps())
}

func scanAllProvidersWithDeps(ctx context.Context, workspace string, deps providerScanDeps) ProvidersScanReport {
	if ctx == nil {
		ctx = context.Background()
	}
	overview := deps.listProfiles(workspace)
	stored := deps.storedKeys()
	if stored == nil {
		stored = map[string]string{}
	}

	profilesByProv := map[string][]ProviderProfileSummary{}
	for _, p := range overview.Profiles {
		k := strings.ToLower(strings.TrimSpace(p.Provider))
		profilesByProv[k] = append(profilesByProv[k], p)
	}

	// Parallel active probes for CLI/HTTP providers.
	var (
		agySt       AgyStatus
		agyModels   []AgyModel
		ocSt        OpenCodeStatus
		ocGoModels  []AgyModel
		ocZenModels []AgyModel
		orModels    []OpenRouterModel
		orAuthErr   error
		orChecked   bool
		curModels   []AgyModel
		curSt       CursorStatus
		tsModels    []string
		tsErr       error
		tsChecked   bool
	)

	hasORCred := strings.TrimSpace(deps.getenv("OPENROUTER_API_KEY")) != "" ||
		stored["OPENROUTER_API_KEY"] != "" ||
		hasReadyProfile(profilesByProv["openrouter"])

	hasTSCred := strings.TrimSpace(deps.getenv("TYPESAFE_API_KEY")) != "" ||
		stored["TYPESAFE_API_KEY"] != "" ||
		hasReadyProfile(profilesByProv["typesafe"])

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		pCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		agySt, agyModels = deps.probeAgy(pCtx)
	}()
	go func() {
		defer wg.Done()
		pCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		ocSt, ocGoModels, ocZenModels = deps.probeOpenCode(pCtx)
	}()
	go func() {
		defer wg.Done()
		pCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		curModels, curSt, _ = deps.probeCursor(pCtx)
	}()
	if hasORCred {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			orChecked = true
			orAuthErr = deps.probeORAuth(pCtx)
			if m, err := deps.probeOpenRouter(pCtx); err == nil {
				orModels = m
			}
		}()
	}
	if hasTSCred && deps.probeTypeSafe != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			tsChecked = true
			tsModels, tsErr = deps.probeTypeSafe(pCtx)
		}()
	}
	wg.Wait()

	items := []ProviderScanItem{
		buildTypeSafeScanItem(tsChecked, tsErr, tsModels, profilesByProv["typesafe"], stored, deps.getenv),
		buildAgyScanItem(agySt, agyModels, profilesByProv["agy"], overview.Resolved["agy"]),
		buildOpenCodeScanItem(ocSt, ocGoModels, ocZenModels, profilesByProv["opencode"], stored, deps.getenv),
		buildOpenRouterScanItem(orChecked, orAuthErr, orModels, profilesByProv["openrouter"], stored, deps.getenv),
		buildAPIKeyProviderScanItem("anthropic", "Anthropic (Claude)", []string{"ANTHROPIC_API_KEY"}, profilesByProv["anthropic"], stored, deps.getenv),
		buildAPIKeyProviderScanItem("openai", "OpenAI / Grok", []string{"OPENAI_API_KEY"}, profilesByProv["openai"], stored, deps.getenv),
		buildGeminiScanItem(profilesByProv["gemini"], stored, deps.getenv),
		buildCursorScanItem(curSt, curModels, profilesByProv["cursor"], stored, deps.getenv),
	}

	report := ProvidersScanReport{
		ScannedAt:      time.Now().UTC().Format(time.RFC3339),
		TotalProviders: len(items),
		Providers:      items,
	}
	for _, it := range items {
		if it.Connected {
			report.ConnectedProviders++
		}
		report.TotalAccounts += len(it.Accounts)
	}
	return report
}

func hasReadyProfile(list []ProviderProfileSummary) bool {
	for _, p := range list {
		if p.Ready {
			return true
		}
	}
	return false
}

func buildAgyScanItem(st AgyStatus, models []AgyModel, profiles []ProviderProfileSummary, resolved ResolvedProviderInfo) ProviderScanItem {
	accounts := make([]ProviderScanAccount, 0, len(profiles)+1)
	for idx, p := range profiles {
		email := strings.TrimSpace(p.ConfigValues["AGY_EMAIL"])
		if email == "" {
			email = p.Name
		}
		active := (resolved.ProfileID != "" && resolved.ProfileID == p.ID) || (resolved.ProfileID == "" && idx == 0)
		accounts = append(accounts, ProviderScanAccount{
			ID:         p.ID,
			Name:       email,
			Source:     "pool",
			Active:     active,
			Ready:      p.Ready,
			MaskedHint: "Pasta isolada salva",
		})
	}
	if len(accounts) == 0 && st.LoggedIn {
		accounts = append(accounts, ProviderScanAccount{
			Name:       "Sessão local Antigravity",
			Source:     "cli",
			Active:     true,
			Ready:      true,
			MaskedHint: "Google Pro",
		})
	}

	sample := make([]string, 0, len(models))
	for _, m := range models {
		if m.Slug != "" {
			sample = append(sample, m.Slug)
		}
	}

	connected := st.LoggedIn || hasReadyProfile(profiles)
	statusLabel := "Desconectado"
	switch {
	case connected:
		statusLabel = "Conectado"
	case !st.Found && len(profiles) == 0:
		statusLabel = "Não instalado"
	case st.Found:
		statusLabel = "Parcial"
	}

	source := ""
	switch {
	case len(profiles) > 0:
		source = fmt.Sprintf("Pool (%d conta(s) Google)", len(profiles))
	case st.LoggedIn:
		source = "CLI local (agy)"
	case st.Found:
		source = "CLI instalado"
	}

	detail := strings.TrimSpace(st.Detail)
	if detail == "" {
		if connected {
			detail = fmt.Sprintf("%d conta(s) Google pronta(s)", len(accounts))
		} else {
			detail = "Faça login com uma conta Google no Pool para conectar."
		}
	}

	return ProviderScanItem{
		ID:           "agy",
		Name:         "Antigravity (Google)",
		Found:        st.Found || len(profiles) > 0,
		Connected:    connected,
		StatusLabel:  statusLabel,
		Source:       source,
		Path:         st.Path,
		Detail:       detail,
		ModelCount:   len(models),
		ModelsSample: sample,
		Accounts:     accounts,
	}
}

func buildOpenCodeScanItem(st OpenCodeStatus, goModels, zenModels []AgyModel, profiles []ProviderProfileSummary, stored map[string]string, getenv func(string) string) ProviderScanItem {
	accounts := collectPoolAccounts(profiles, "OPENCODE_API_KEY")
	envKey := strings.TrimSpace(getenv("OPENCODE_API_KEY"))
	if envKey == "" {
		envKey = strings.TrimSpace(getenv("OPENCODE_ZEN_API_KEY"))
	}
	if envKey != "" {
		accounts = append(accounts, ProviderScanAccount{
			Name:       "Variável OPENCODE_API_KEY",
			Source:     "env",
			Active:     len(profiles) == 0,
			Ready:      true,
			MaskedHint: MaskKey(envKey),
		})
	} else if st.Configured && st.Source != "env" {
		accounts = append(accounts, ProviderScanAccount{
			Name:       "Token global OpenCode",
			Source:     "global",
			Active:     len(profiles) == 0,
			Ready:      true,
			MaskedHint: st.KeyMasked,
		})
	} else if stored["OPENCODE_API_KEY"] != "" {
		accounts = append(accounts, ProviderScanAccount{
			Name:       "Token global OpenCode",
			Source:     "global",
			Active:     len(profiles) == 0,
			Ready:      true,
			MaskedHint: stored["OPENCODE_API_KEY"],
		})
	}

	sample := make([]string, 0, len(goModels)+len(zenModels))
	for _, m := range goModels {
		if m.Slug != "" {
			sample = append(sample, "opencode-go:"+m.Slug)
		}
	}
	for _, m := range zenModels {
		if m.Slug != "" {
			sample = append(sample, "opencode:"+m.Slug)
		}
	}

	connected := st.Configured || len(accounts) > 0
	statusLabel := "Desconectado"
	if connected {
		statusLabel = "Conectado"
	} else if len(sample) > 0 {
		statusLabel = "Parcial"
	}

	source := summarizeSources(accounts)
	if source == "" && len(sample) > 0 {
		source = "Modelos gratuitos"
	}
	detail := fmt.Sprintf("Go (%d) + Zen (%d)", len(goModels), len(zenModels))
	if st.Detail != "" {
		detail = st.Detail + " · " + detail
	} else if !connected {
		detail = "Sem token salvo (apenas modelos -free liberados) · " + detail
	}

	return ProviderScanItem{
		ID:           "opencode",
		Name:         "OpenCode Go & Zen",
		Found:        connected || len(sample) > 0,
		Connected:    connected,
		StatusLabel:  statusLabel,
		Source:       source,
		Detail:       detail,
		ModelCount:   len(sample),
		ModelsSample: capSlice(sample, 40),
		Accounts:     accounts,
	}
}

func buildOpenRouterScanItem(checked bool, authErr error, models []OpenRouterModel, profiles []ProviderProfileSummary, stored map[string]string, getenv func(string) string) ProviderScanItem {
	accounts := collectPoolAccounts(profiles, "OPENROUTER_API_KEY")
	if envKey := strings.TrimSpace(getenv("OPENROUTER_API_KEY")); envKey != "" {
		accounts = append(accounts, ProviderScanAccount{
			Name:       "Variável OPENROUTER_API_KEY",
			Source:     "env",
			Active:     len(profiles) == 0,
			Ready:      true,
			MaskedHint: MaskKey(envKey),
		})
	} else if stored["OPENROUTER_API_KEY"] != "" {
		accounts = append(accounts, ProviderScanAccount{
			Name:       "Chave global OpenRouter",
			Source:     "global",
			Active:     len(profiles) == 0,
			Ready:      true,
			MaskedHint: stored["OPENROUTER_API_KEY"],
		})
	}

	sample := make([]string, 0, min(len(models), 30))
	for i := 0; i < len(models) && i < 30; i++ {
		if models[i].ID != "" {
			sample = append(sample, models[i].ID)
		}
	}

	found := len(accounts) > 0
	connected := found && (!checked || authErr == nil)
	statusLabel := "Desconectado"
	detail := "Nenhuma chave OpenRouter configurada."
	if connected {
		statusLabel = "Conectado"
		if len(models) > 0 {
			detail = fmt.Sprintf("Autenticação válida · %d modelo(s) no catálogo", len(models))
		} else {
			detail = "Chave OpenRouter pronta"
		}
	} else if found && authErr != nil {
		statusLabel = "Parcial"
		detail = fmt.Sprintf("Chave encontrada, mas a verificação falhou: %s", sanitizeScanError(authErr))
	}

	return ProviderScanItem{
		ID:           "openrouter",
		Name:         "OpenRouter",
		Found:        found,
		Connected:    connected,
		StatusLabel:  statusLabel,
		Source:       summarizeSources(accounts),
		Detail:       detail,
		ModelCount:   len(models),
		ModelsSample: sample,
		Accounts:     accounts,
	}
}

func buildTypeSafeScanItem(checked bool, probeErr error, models []string, profiles []ProviderProfileSummary, stored map[string]string, getenv func(string) string) ProviderScanItem {
	accounts := collectPoolAccounts(profiles, "TYPESAFE_API_KEY")
	if envKey := strings.TrimSpace(getenv("TYPESAFE_API_KEY")); envKey != "" {
		accounts = append(accounts, ProviderScanAccount{
			Name:       "Variável TYPESAFE_API_KEY",
			Source:     "env",
			Active:     len(profiles) == 0,
			Ready:      true,
			MaskedHint: MaskKey(envKey),
		})
	} else if stored["TYPESAFE_API_KEY"] != "" {
		accounts = append(accounts, ProviderScanAccount{
			Name:       "Chave global TypeSafe AI",
			Source:     "global",
			Active:     len(profiles) == 0,
			Ready:      true,
			MaskedHint: stored["TYPESAFE_API_KEY"],
		})
	}

	found := len(accounts) > 0
	connected := found && (!checked || probeErr == nil)
	statusLabel := "Desconectado"
	detail := "Sem token TypeSafe AI (console.typesafe.ai/keys) — Jev usa OpenRouter/heurística."
	if connected {
		statusLabel = "Conectado"
		if len(models) > 0 {
			detail = fmt.Sprintf("Motor oficial Jev (System One) ativo · %d modelo(s)", len(models))
		} else {
			detail = "Token TypeSafe AI pronto (decisões Jev desacopladas do OpenRouter)"
		}
	} else if found && probeErr != nil {
		statusLabel = "Parcial"
		detail = fmt.Sprintf("Token encontrado, mas TypeSafe AI falhou: %s", sanitizeScanError(probeErr))
	}

	return ProviderScanItem{
		ID:           "typesafe",
		Name:         "TypeSafe AI (Jev Oficial)",
		Found:        found,
		Connected:    connected,
		StatusLabel:  statusLabel,
		Source:       summarizeSources(accounts),
		Detail:       detail,
		ModelCount:   len(models),
		ModelsSample: capSlice(models, 20),
		Accounts:     accounts,
	}
}

func buildAPIKeyProviderScanItem(id, name string, envKeys []string, profiles []ProviderProfileSummary, stored map[string]string, getenv func(string) string) ProviderScanItem {
	primaryKey := envKeys[0]
	accounts := collectPoolAccounts(profiles, primaryKey)
	for _, k := range envKeys {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			accounts = append(accounts, ProviderScanAccount{
				Name:       "Variável " + k,
				Source:     "env",
				Active:     len(profiles) == 0,
				Ready:      true,
				MaskedHint: MaskKey(v),
			})
			break
		}
	}
	if len(accounts) == len(profiles) {
		for _, k := range envKeys {
			if mask := stored[k]; mask != "" {
				accounts = append(accounts, ProviderScanAccount{
					Name:       "Chave global " + k,
					Source:     "global",
					Active:     len(profiles) == 0,
					Ready:      true,
					MaskedHint: mask,
				})
				break
			}
		}
	}

	connected := len(accounts) > 0
	statusLabel := "Desconectado"
	detail := fmt.Sprintf("Nenhuma chave %s encontrada.", primaryKey)
	if connected {
		statusLabel = "Conectado"
		detail = fmt.Sprintf("%d credencial(is) detectada(s) e pronta(s) para uso.", len(accounts))
	}

	return ProviderScanItem{
		ID:          id,
		Name:        name,
		Found:       connected,
		Connected:   connected,
		StatusLabel: statusLabel,
		Source:      summarizeSources(accounts),
		Detail:      detail,
		Accounts:    accounts,
	}
}

func buildGeminiScanItem(profiles []ProviderProfileSummary, stored map[string]string, getenv func(string) string) ProviderScanItem {
	item := buildAPIKeyProviderScanItem("gemini", "Google Gemini API", []string{"GOOGLE_API_KEY", "GEMINI_API_KEY"}, profiles, stored, getenv)
	if proj := strings.TrimSpace(getenv("GOOGLE_CLOUD_PROJECT")); proj != "" {
		item.Accounts = append(item.Accounts, ProviderScanAccount{
			Name:       "Vertex AI (" + proj + ")",
			Source:     "env",
			Active:     !item.Connected,
			Ready:      true,
			MaskedHint: proj,
		})
	} else if proj := stored["GOOGLE_CLOUD_PROJECT"]; proj != "" {
		item.Accounts = append(item.Accounts, ProviderScanAccount{
			Name:       "Vertex AI (global)",
			Source:     "global",
			Active:     !item.Connected,
			Ready:      true,
			MaskedHint: proj,
		})
	}
	if len(item.Accounts) > 0 {
		item.Found = true
		item.Connected = true
		item.StatusLabel = "Conectado"
		item.Source = summarizeSources(item.Accounts)
		item.Detail = fmt.Sprintf("%d credencial(is) Gemini/Vertex detectada(s).", len(item.Accounts))
	}
	return item
}

func buildCursorScanItem(st CursorStatus, models []AgyModel, profiles []ProviderProfileSummary, stored map[string]string, getenv func(string) string) ProviderScanItem {
	accounts := collectPoolAccounts(profiles, "CURSOR_API_KEY")
	if envKey := strings.TrimSpace(getenv("CURSOR_API_KEY")); envKey != "" {
		accounts = append(accounts, ProviderScanAccount{
			Name:       "Variável CURSOR_API_KEY",
			Source:     "env",
			Active:     len(profiles) == 0,
			Ready:      true,
			MaskedHint: MaskKey(envKey),
		})
	} else if stored["CURSOR_API_KEY"] != "" {
		accounts = append(accounts, ProviderScanAccount{
			Name:       "Chave global CURSOR_API_KEY",
			Source:     "global",
			Active:     len(profiles) == 0,
			Ready:      true,
			MaskedHint: stored["CURSOR_API_KEY"],
		})
	}

	sample := make([]string, 0, len(models))
	for _, m := range models {
		if m.Slug != "" {
			sample = append(sample, m.Slug)
		}
	}

	hasKey := st.HasKey || len(accounts) > 0
	connected := st.Found && hasKey
	statusLabel := "Desconectado"
	switch {
	case connected:
		statusLabel = "Conectado"
	case !st.Found && !hasKey:
		statusLabel = "Não instalado"
	case st.Found || hasKey:
		statusLabel = "Parcial"
	}

	detail := strings.TrimSpace(st.Detail)
	if detail == "" {
		switch {
		case connected:
			detail = fmt.Sprintf("CLI Cursor pronto · %d modelo(s) disponível(is)", len(models))
		case st.Found && !hasKey:
			detail = "CLI encontrado, mas falta configurar CURSOR_API_KEY."
		case !st.Found && hasKey:
			detail = "Chave CURSOR_API_KEY salva, mas o CLI `agent` não foi encontrado no PATH."
		default:
			detail = "CLI `agent` não instalado e sem CURSOR_API_KEY configurada."
		}
	}

	return ProviderScanItem{
		ID:           "cursor",
		Name:         "Cursor CLI",
		Found:        st.Found || hasKey,
		Connected:    connected,
		StatusLabel:  statusLabel,
		Source:       summarizeSources(accounts),
		Path:         st.Path,
		Detail:       detail,
		ModelCount:   len(models),
		ModelsSample: capSlice(sample, 30),
		Accounts:     accounts,
	}
}

func collectPoolAccounts(profiles []ProviderProfileSummary, keyName string) []ProviderScanAccount {
	out := make([]ProviderScanAccount, 0, len(profiles))
	for idx, p := range profiles {
		hint := p.EnvMasked[keyName]
		if hint == "" {
			for _, v := range p.EnvMasked {
				if v != "" {
					hint = v
					break
				}
			}
		}
		out = append(out, ProviderScanAccount{
			ID:         p.ID,
			Name:       p.Name,
			Source:     "pool",
			Active:     idx == 0,
			Ready:      p.Ready,
			MaskedHint: hint,
		})
	}
	return out
}

func summarizeSources(accounts []ProviderScanAccount) string {
	if len(accounts) == 0 {
		return ""
	}
	var poolCount int
	var hasEnv, hasGlobal, hasCLI bool
	for _, a := range accounts {
		switch a.Source {
		case "pool":
			poolCount++
		case "env":
			hasEnv = true
		case "global":
			hasGlobal = true
		case "cli":
			hasCLI = true
		}
	}
	parts := make([]string, 0, 3)
	if poolCount > 0 {
		parts = append(parts, fmt.Sprintf("Pool (%d)", poolCount))
	}
	if hasGlobal {
		parts = append(parts, "Chave global")
	}
	if hasEnv {
		parts = append(parts, "Variável de ambiente")
	}
	if hasCLI {
		parts = append(parts, "CLI local")
	}
	return strings.Join(parts, " + ")
}

func capSlice(in []string, max int) []string {
	if len(in) <= max {
		return in
	}
	return in[:max]
}

func sanitizeScanError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, tok := range []string{"sk-or-v1-", "sk-ant-", "sk-", "AIza", "crsr_", "oc_", "ts_"} {
		if idx := strings.Index(msg, tok); idx >= 0 {
			msg = msg[:idx] + "[chave ocultada]"
		}
	}
	return msg
}
