package modelx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
		ID:        "agy-pessoal",
		Provider:  "agy",
		Name:      "AGY Pessoal",
		AuthMode:  "live",
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

	// Acme has no AGY org pattern. The personal default must NOT be used once any
	// AGY profile is org-scoped — that would hand Vibetex's sibling account to Acme.
	ctxA := WithWorkspace(context.Background(), wsAcme)
	pAgyA, modeA, _ := ResolveProfile(ctxA, "agy")
	if pAgyA != nil || modeA != "isolated" {
		t.Fatalf("expected agy isolated for acme, got %+v (%s)", pAgyA, modeA)
	}
	if gotKey := resolveEnvForContext(ctxA, "openrouter", "OPENROUTER_API_KEY"); gotKey != "sk-or-acme-0987654321" {
		t.Fatalf("unexpected openrouter key for acme: %q", gotKey)
	}

	// Secrets must be masked in overview (no prefix of the raw key).
	ov := ListProfilesOverview(wsVibetex)
	for _, s := range ov.Profiles {
		if s.ID != "or-vibetex" {
			continue
		}
		masked := s.EnvMasked["OPENROUTER_API_KEY"]
		if masked == "" || masked == "sk-or-vibetex-1234567890" || strings.Contains(masked, "sk-or") || strings.Contains(masked, "1234567890") {
			t.Fatalf("summary leaked OpenRouter key material: %q", masked)
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

	// A third org must not inherit env or the legacy api-keys store.
	t.Setenv("OPENROUTER_API_KEY", "sk-or-env-SHOULD-NOT-LEAK-999")
	wsOther := filepath.Join(tmp, "org3-app")
	gitDir := filepath.Join(wsOther, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[remote \"origin\"]\n\turl = git@github.com:org3/app.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctxO := WithWorkspace(context.Background(), wsOther)
	if got := resolveEnvForContext(ctxO, "openrouter", "OPENROUTER_API_KEY"); got != "" {
		t.Fatalf("org3 inherited an OpenRouter key: %q", got)
	}
	if _, mode, _ := ResolveProfile(ctxO, "openrouter"); mode != "isolated" {
		t.Fatalf("org3 openrouter mode = %s", mode)
	}

	// ADC directories are per profile and ignore a shared CLOUDSDK_CONFIG.
	if _, err := SaveProviderProfile(SaveProfileRequest{
		ID:          "agy-acme",
		Provider:    "agy",
		Name:        "AGY Acme",
		AuthMode:    "adc",
		OrgPatterns: []string{"acme-corp"},
		Env:         map[string]string{"CLOUDSDK_CONFIG": filepath.Join(tmp, "shared-adc")},
	}); err != nil {
		t.Fatal(err)
	}
	pAcme, modeAcme, _ := ResolveProfile(ctxA, "agy")
	if pAcme == nil || pAcme.ID != "agy-acme" || modeAcme != "auto_org" {
		t.Fatalf("expected agy-acme auto_org, got %+v (%s)", pAcme, modeAcme)
	}
	dirV := pAgyV.Env["CLOUDSDK_CONFIG"]
	dirA := pAcme.Env["CLOUDSDK_CONFIG"]
	if dirV == "" || dirA == "" || dirV == dirA || strings.Contains(dirA, "shared-adc") {
		t.Fatalf("ADC dirs not isolated: vibetex=%q acme=%q", dirV, dirA)
	}
	escaped, err := AgyADCConfigDir("../../etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(filepath.Clean(escaped), "..") || !strings.Contains(escaped, "agy-profiles") {
		t.Fatalf("profile id escaped config root: %s", escaped)
	}
}
