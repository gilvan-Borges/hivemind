package modelx

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestScanAllProvidersAggregatesPoolGlobalEnvAndRedactsSecrets(t *testing.T) {
	deps := providerScanDeps{
		listProfiles: func(string) ProfilesOverview {
			return ProfilesOverview{
				Profiles: []ProviderProfileSummary{
					{
						ID:           "agy-1",
						Provider:     "agy",
						Name:         "dev1@example.com",
						Ready:        true,
						ConfigValues: map[string]string{"AGY_EMAIL": "dev1@example.com"},
					},
					{
						ID:           "agy-2",
						Provider:     "agy",
						Name:         "dev2@example.com",
						Ready:        true,
						ConfigValues: map[string]string{"AGY_EMAIL": "dev2@example.com"},
					},
					{
						ID:        "cur-1",
						Provider:  "cursor",
						Name:      "cursor-work",
						Ready:     true,
						EnvMasked: map[string]string{"CURSOR_API_KEY": "••••d9fa"},
					},
				},
				Resolved: map[string]ResolvedProviderInfo{
					"agy": {ProfileID: "agy-1", ProfileName: "dev1@example.com"},
				},
			}
		},
		storedKeys: func() map[string]string {
			return map[string]string{
				"OPENROUTER_API_KEY": "••••3736",
				"ANTHROPIC_API_KEY":  "••••9988",
			}
		},
		getenv: func(k string) string {
			if k == "OPENAI_API_KEY" {
				return "sk-secret-openai-value-123456"
			}
			return ""
		},
		probeAgy: func(context.Context) (AgyStatus, []AgyModel) {
			return AgyStatus{Found: true, Path: "C:\\agy.exe", LoggedIn: true, Detail: "ok"},
				[]AgyModel{{Slug: "gemini-3.8-flash-high", Name: "Gemini 3.8 Flash"}}
		},
		probeOpenCode: func(context.Context) (OpenCodeStatus, []AgyModel, []AgyModel) {
			return OpenCodeStatus{Configured: true, Source: "stored", KeyMasked: "••••QVDh", Detail: "Conectado"},
				[]AgyModel{{Slug: "deepseek-v4-pro"}},
				[]AgyModel{{Slug: "big-pickle"}}
		},
		probeORAuth: func(context.Context) error {
			return errors.New("401 unauthorized sk-or-v1-leakedtoken99999")
		},
		probeOpenRouter: func(context.Context) ([]OpenRouterModel, error) {
			return nil, nil
		},
		probeCursor: func(context.Context) ([]AgyModel, CursorStatus, error) {
			return []AgyModel{{Slug: "auto"}}, CursorStatus{Found: true, HasKey: true, Path: "C:\\agent.exe"}, nil
		},
	}

	report := scanAllProvidersWithDeps(context.Background(), "", deps)
	if report.TotalProviders != 8 {
		t.Fatalf("TotalProviders = %d, want 8", report.TotalProviders)
	}
	// Connected: agy, opencode, anthropic, openai, cursor (5); openrouter failed auth (Parcial); gemini & typesafe disconnected.
	if report.ConnectedProviders != 5 {
		t.Fatalf("ConnectedProviders = %d, want 5 (%+v)", report.ConnectedProviders, report.Providers)
	}
	if report.TotalAccounts < 7 {
		t.Fatalf("TotalAccounts = %d, expected at least 7", report.TotalAccounts)
	}

	byID := map[string]ProviderScanItem{}
	for _, it := range report.Providers {
		byID[it.ID] = it
		if strings.Contains(it.Detail, "sk-secret-openai-value-123456") || strings.Contains(it.Detail, "leakedtoken99999") {
			t.Fatalf("raw secret leaked in Detail of %s: %q", it.ID, it.Detail)
		}
		for _, acc := range it.Accounts {
			if strings.Contains(acc.MaskedHint, "sk-secret-openai-value-123456") {
				t.Fatalf("raw secret leaked in MaskedHint of %s: %q", it.ID, acc.MaskedHint)
			}
		}
	}

	if len(byID["agy"].Accounts) != 2 || !byID["agy"].Connected {
		t.Fatalf("agy item unexpected: %+v", byID["agy"])
	}
	if byID["opencode"].ModelCount != 2 || !byID["opencode"].Connected {
		t.Fatalf("opencode item unexpected: %+v", byID["opencode"])
	}
	if byID["openrouter"].Connected || byID["openrouter"].StatusLabel != "Parcial" {
		t.Fatalf("openrouter with auth error should be Parcial/not connected: %+v", byID["openrouter"])
	}
	if byID["gemini"].Connected || byID["gemini"].StatusLabel != "Desconectado" {
		t.Fatalf("gemini should be Desconectado: %+v", byID["gemini"])
	}
	if !byID["cursor"].Connected || byID["cursor"].ModelCount != 1 {
		t.Fatalf("cursor should be Connected with 1 model: %+v", byID["cursor"])
	}
}
