package modelx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMultiOrgProviderProfilesResolution(t *testing.T) {
	tmp := t.TempDir()
	prev := profilesFilePathFn
	profilesFilePathFn = func() (string, error) {
		return filepath.Join(tmp, "provider-profiles.json"), nil
	}
	t.Cleanup(func() { profilesFilePathFn = prev })

	// Create 3 org workspaces (vibetex with git remote, acme with git remote, personal without git remote)
	wsVibetex := filepath.Join(tmp, "vibetex-app")
	wsAcme := filepath.Join(tmp, "acme-api")
	wsPersonal := filepath.Join(tmp, "sandbox-pessoal")
	for _, pair := range [][2]string{
		{wsVibetex, "git@github.com:vibetex/app.git"},
		{wsAcme, "https://github.com/acme-corp/api.git"},
	} {
		gitDir := filepath.Join(pair[0], ".git")
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		cfg := "[remote \"origin\"]\n\turl = " + pair[1] + "\n"
		if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(wsPersonal, 0o755); err != nil {
		t.Fatal(err)
	}

	// Two AGY profiles: Personal (default) + Vibetex (ADC isolated)
	if _, err := SaveProviderProfile(SaveProfileRequest{
		ID:       "agy-pessoal",
		Provider: "agy",
		Name:     "AGY Pessoal",
		AuthMode: "live",
		IsDefault: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveProviderProfile(SaveProfileRequest{
		ID:          "agy-vibetex",
		Provider:    "agy",
		Name:        "AGY Vibetex",
		AuthMode:    "adc",
		OrgPatterns: []string{"vibetex"},
		Env: map[string]string{
			"GOOGLE_CLOUD_PROJECT": "vibetex-ai-prod",
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Two OpenRouter keys: Vibetex vs Acme
	if _, err := SaveProviderProfile(SaveProfileRequest{
		ID:          "or-vibetex",
		Provider:    "openrouter",
		Name:        "OpenRouter Vibetex",
		OrgPatterns: []string{"vibetex"},
		Env:         map[string]string{"OPENROUTER_API_KEY": "sk-or-vibetex-1234567890"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveProviderProfile(SaveProfileRequest{
		ID:          "or-acme",
		Provider:    "openrouter",
		Name:        "OpenRouter Acme",
		OrgPatterns: []string{"acme-corp"},
		Env:         map[string]string{"OPENROUTER_API_KEY": "sk-or-acme-0987654321"},
	}); err != nil {
		t.Fatal(err)
	}

	// Auto-detect vibetex workspace -> picks agy-vibetex & or-vibetex
	ctxV := WithWorkspace(context.Background(), wsVibetex)
	pAgyV, modeV, _ := ResolveProfile(ctxV, "agy")
	if pAgyV == nil || pAgyV.ID != "agy-vibetex" || modeV != "auto_org" {
		t.Fatalf("expected agy-vibetex auto_org, got %+v (%s)", pAgyV, modeV)
	}
	if pAgyV.Env["AGY_ADC_AUTH"] != "true" || pAgyV.Env["CLOUDSDK_CONFIG"] == "" {
		t.Fatalf("expected isolated CLOUDSDK_CONFIG & AGY_ADC_AUTH on ADC profile, got %+v", pAgyV.Env)
	}
	if gotKey := resolveEnvForContext(ctxV, "openrouter", "OPENROUTER_API_KEY"); gotKey != "sk-or-vibetex-1234567890" {
		t.Fatalf("unexpected openrouter key for vibetex: %q", gotKey)
	}

	// Auto-detect acme workspace -> picks or-acme, and default AGY Pessoal!
	ctxA := WithWorkspace(context.Background(), wsAcme)
	pAgyA, modeA, _ := ResolveProfile(ctxA, "agy")
	if pAgyA == nil || pAgyA.ID != "agy-pessoal" || modeA != "default" {
		t.Fatalf("expected agy-pessoal default for acme, got %+v (%s)", pAgyA, modeA)
	}
	if gotKey := resolveEnvForContext(ctxA, "openrouter", "OPENROUTER_API_KEY"); gotKey != "sk-or-acme-0987654321" {
		t.Fatalf("unexpected openrouter key for acme: %q", gotKey)
	}

	// Secrets must be masked in overview
	ov := ListProfilesOverview(wsVibetex)
	for _, s := range ov.Profiles {
		if s.ID == "or-vibetex" && s.EnvMasked["OPENROUTER_API_KEY"] == "sk-or-vibetex-1234567890" {
			t.Fatal("summary leaked raw OpenRouter key")
		}
	}
	if ov.Resolved["agy"].ProfileID != "agy-vibetex" || ov.Identity.GitOrg != "vibetex" {
		t.Fatalf("unexpected overview resolution: %+v", ov)
	}

	// Manual workspace override beats auto_org
	if _, err := BindProviderWorkspace(wsVibetex, "agy", "agy-pessoal"); err != nil {
		t.Fatal(err)
	}
	pPinned, modePinned, _ := ResolveProfile(ctxV, "agy")
	if pPinned == nil || pPinned.ID != "agy-pessoal" || modePinned != "manual" {
		t.Fatalf("expected manual pin to override auto_org, got %+v (%s)", pPinned, modePinned)
	}
}
