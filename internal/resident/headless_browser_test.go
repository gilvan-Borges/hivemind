package resident

import (
	"strings"
	"testing"
)

func TestBrowserHeadlessAsksOnAllowlistAndRefusesTheRest(t *testing.T) {
	ok := DecideWithPolicy(Call{
		Name:   "browser_headless",
		Origin: OriginLocal,
		Args:   map[string]any{"url": "https://example.com/docs", "profile": "C:/Users/alguem/Chrome"},
	}, Policy{AllowedHosts: []string{"example.com"}})
	if ok.Allow || !ok.Ask {
		t.Fatalf("allowlist deveria pedir aprovação: %+v", ok)
	}

	blocked := DecideWithPolicy(Call{
		Name:   "browser_headless",
		Origin: OriginLocal,
		Args:   map[string]any{"url": "https://example.com/docs"},
	}, Policy{})
	if blocked.Allow || blocked.Ask {
		t.Fatalf("sem allowlist deveria recusar: %+v", blocked)
	}
}

func TestHeadlessArgsUseAFreshProfile(t *testing.T) {
	args := headlessArgs(`C:\temp\hivemind-headless-1`, "https://example.com/docs")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--headless=new") || !strings.Contains(joined, "--dump-dom") {
		t.Fatalf("não é headless: %s", joined)
	}
	if !strings.Contains(joined, `--user-data-dir=C:\temp\hivemind-headless-1`) {
		t.Fatalf("perfil temporário ausente: %s", joined)
	}
	if strings.Contains(strings.ToLower(joined), "user data") || strings.Contains(joined, "profile-directory") {
		t.Fatalf("args tocam o perfil da pessoa: %s", joined)
	}
	if args[len(args)-1] != "https://example.com/docs" {
		t.Fatalf("url: %v", args)
	}
}
