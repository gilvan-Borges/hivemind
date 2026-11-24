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

func TestVisiblePageTextDropsScriptAndStaysShort(t *testing.T) {
	raw := "<html><script>segredo interno</script><body><p>Havrus resultado</p>" + strings.Repeat("<p>x</p>", 8000) + "</body></html>"
	got := visiblePageText(raw)
	if strings.Contains(got, "segredo interno") {
		t.Fatal("script entrou no texto")
	}
	if !strings.Contains(got, "Havrus resultado") {
		t.Fatal(got)
	}
	if len(got) > 6*1024+8 {
		t.Fatalf("texto longo demais: %d", len(got))
	}
}

func TestChromeTabAsksAndRefusesTheVault(t *testing.T) {
	ok := DecideWithPolicy(Call{
		Name:   "chrome_tab",
		Origin: OriginLocal,
		Args:   map[string]any{"action": "navigate", "url": "https://www.google.com/search?q=havrus"},
	}, Policy{})
	if ok.Allow || !ok.Ask {
		t.Fatalf("deveria pedir aprovação: %+v", ok)
	}
	blocked := DecideWithPolicy(Call{
		Name:   "chrome_tab",
		Origin: OriginLocal,
		Args:   map[string]any{"action": "navigate", "url": "https://www.google.com", "credential_name": "cofre"},
	}, Policy{})
	if blocked.Allow || blocked.Ask {
		t.Fatalf("cofre deveria recusar: %+v", blocked)
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
