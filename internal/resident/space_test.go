package resident

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/sandboxx"
)

type countingSandbox struct {
	runs int
}

func (c *countingSandbox) Probe() sandboxx.ProbeResult {
	return sandboxx.ProbeResult{Available: true, Mechanism: "test", OS: runtime.GOOS}
}

func (c *countingSandbox) Run(context.Context, sandboxx.Options) (sandboxx.Result, error) {
	c.runs++
	return sandboxx.Result{Mechanism: "test"}, nil
}

func TestIsolatedBrowserAndDesktopStayRefused(t *testing.T) {
	profile := `C:\Users\pessoa\AppData\Local\Google\Chrome\User Data`
	if runtime.GOOS != "windows" {
		profile = "/home/pessoa/.config/google-chrome"
	}
	for _, name := range []string{"browser_open", "desktop_click", "desktop_type"} {
		for _, origin := range []Origin{OriginLocal, OriginRemote, OriginRoutine, OriginSubagent} {
			dec := Decide(Call{
				Name:   name,
				Origin: origin,
				Args: map[string]any{
					"url":     "https://example.com",
					"profile": profile,
					"x":       12,
					"y":       40,
				},
			})
			if dec.Allow || dec.Ask {
				t.Fatalf("%s/%s deveria recusar sem pedir: %+v", name, origin, dec)
			}
			if strings.Contains(dec.Reason, profile) {
				t.Fatalf("motivo ecoou o perfil: %s", dec.Reason)
			}
		}
	}
	ok, reason := IsolatedBrowserAvailable()
	if ok || !strings.Contains(reason, "recusada") {
		t.Fatalf("browser isolado deveria estar desligado: %v %s", ok, reason)
	}
	ok, reason = IsolatedDesktopAvailable()
	if ok || !strings.Contains(reason, "recusada") {
		t.Fatalf("desktop isolado deveria estar desligado: %v %s", ok, reason)
	}

	sb := &countingSandbox{}
	broker := NewBroker(BrokerConfig{
		Workspace: t.TempDir(),
		Origin:    OriginLocal,
		Audit:     NewFileAuditLogger(filepath.Join(t.TempDir(), "audit.jsonl")),
		Sandbox:   sb,
		ApproveDetailed: func(context.Context, ApprovalRequest) bool {
			t.Fatal("não há aprovação para um espaço que não sobe")
			return true
		},
	})
	for _, name := range []string{"browser_open", "desktop_click"} {
		_, err := broker.Execute(context.Background(), name, map[string]any{"profile": profile, "url": "https://example.com"})
		if err == nil {
			t.Fatalf("%s deveria falhar", name)
		}
		if strings.Contains(err.Error(), profile) {
			t.Fatalf("erro ecoou o perfil: %v", err)
		}
	}
	if sb.runs != 0 {
		t.Fatalf("sandbox rodou %d vezes sem espaço isolado", sb.runs)
	}
}

type recordingSurface struct {
	opens  []string
	clicks [][2]int
	texts  []string
}

func (r *recordingSurface) OpenURL(_ context.Context, rawURL string) error {
	r.opens = append(r.opens, rawURL)
	return nil
}

func (r *recordingSurface) Click(_ context.Context, x, y int) error {
	r.clicks = append(r.clicks, [2]int{x, y})
	return nil
}

func (r *recordingSurface) TypeText(_ context.Context, text string) error {
	r.texts = append(r.texts, text)
	return nil
}

type spyVault struct {
	gets int
}

func (s *spyVault) Available() (bool, string) { return true, "teste" }
func (s *spyVault) Set(string, string) error  { return nil }
func (s *spyVault) Get(string) (string, error) {
	s.gets++
	return "segredo-do-cofre", nil
}
func (s *spyVault) Delete(string) error          { return nil }
func (s *spyVault) ListNames() ([]string, error) { return []string{"github_ro"}, nil }

func TestExperimentalUnisolatedOffKeepsRefusal(t *testing.T) {
	surface := &recordingSurface{}
	vault := &spyVault{}
	broker := NewBroker(BrokerConfig{
		Workspace:              t.TempDir(),
		Origin:                 OriginLocal,
		Audit:                  NewFileAuditLogger(filepath.Join(t.TempDir(), "audit.jsonl")),
		ExperimentalUnisolated: false,
		Surface:                surface,
		Vault:                  vault,
		PermissionMode:         func(string) string { return "full_access" },
		ApproveDetailed: func(context.Context, ApprovalRequest) bool {
			t.Fatal("opção desligada não pede confirmação")
			return true
		},
	})
	_, err := broker.Execute(context.Background(), "browser_open", map[string]any{"url": "https://example.com"})
	if err == nil {
		t.Fatal("flag desligada deveria recusar")
	}
	if len(surface.opens) != 0 || vault.gets != 0 {
		t.Fatalf("efeito indevido opens=%d vault=%d", len(surface.opens), vault.gets)
	}
}

func TestExperimentalUnisolatedAsksOutsideFullAccess(t *testing.T) {
	profile := `C:\Users\pessoa\AppData\Local\Google\Chrome\User Data`
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	surface := &recordingSurface{}
	vault := &spyVault{}
	asked := 0
	broker := NewBroker(BrokerConfig{
		Workspace:              t.TempDir(),
		Origin:                 OriginLocal,
		Audit:                  NewFileAuditLogger(auditPath),
		ExperimentalUnisolated: true,
		Surface:                surface,
		Vault:                  vault,
		PermissionMode:         func(string) string { return "governed" },
		ApproveDetailed: func(_ context.Context, req ApprovalRequest) bool {
			asked++
			if !strings.Contains(req.Prompt, "SEM isolamento") {
				t.Fatalf("cartão sem aviso: %s", req.Prompt)
			}
			if strings.Contains(req.Prompt, profile) {
				t.Fatalf("cartão ecoou o perfil: %s", req.Prompt)
			}
			return true
		},
	})
	_, err := broker.Execute(context.Background(), "browser_open", map[string]any{
		"url":     "https://example.com/docs",
		"profile": profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if asked != 1 || len(surface.opens) != 1 || surface.opens[0] != "https://example.com/docs" {
		t.Fatalf("asked=%d opens=%v", asked, surface.opens)
	}
	_, err = broker.Execute(context.Background(), "desktop_click", map[string]any{"x": 12, "y": 40, "profile": profile})
	if err != nil {
		t.Fatal(err)
	}
	_, err = broker.Execute(context.Background(), "desktop_type", map[string]any{"text": "oi"})
	if err != nil {
		t.Fatal(err)
	}
	if vault.gets != 0 {
		t.Fatalf("cofre lido %d vezes", vault.gets)
	}
	raw, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, profile) || strings.Contains(text, "segredo-do-cofre") {
		t.Fatalf("auditoria vazou perfil ou segredo: %s", text)
	}
	for _, tool := range []string{"browser_open", "desktop_click", "desktop_type"} {
		if !strings.Contains(text, tool) || !strings.Contains(text, "unisolated") {
			t.Fatalf("auditoria de %s: %s", tool, text)
		}
	}
}

func TestExperimentalUnisolatedFullAccessSkipsConfirm(t *testing.T) {
	surface := &recordingSurface{}
	broker := NewBroker(BrokerConfig{
		Workspace:              t.TempDir(),
		Origin:                 OriginRemote,
		Audit:                  NewFileAuditLogger(filepath.Join(t.TempDir(), "audit.jsonl")),
		ExperimentalUnisolated: true,
		Surface:                surface,
		PermissionMode:         func(string) string { return "full_access" },
		ApproveDetailed: func(context.Context, ApprovalRequest) bool {
			t.Fatal("Full Acesso não pede cartão")
			return false
		},
	})
	if _, err := broker.Execute(context.Background(), "desktop_type", map[string]any{"text": "nota"}); err != nil {
		t.Fatal(err)
	}
	if len(surface.texts) != 1 || surface.texts[0] != "nota" {
		t.Fatalf("texto: %#v", surface.texts)
	}
}

func TestExperimentalUnisolatedRefusesVaultAndBadURL(t *testing.T) {
	surface := &recordingSurface{}
	vault := &spyVault{}
	broker := NewBroker(BrokerConfig{
		Workspace:              t.TempDir(),
		Origin:                 OriginLocal,
		Audit:                  NewFileAuditLogger(filepath.Join(t.TempDir(), "audit.jsonl")),
		ExperimentalUnisolated: true,
		Surface:                surface,
		Vault:                  vault,
		PermissionMode:         func(string) string { return "full_access" },
	})
	for _, args := range []map[string]any{
		{"url": "https://user:segredo@example.com"},
		{"url": "https://example.com", "credential_name": "github_ro"},
		{"url": "file:///etc/passwd"},
		{"text": "x", "secret_name": "cofre"},
	} {
		name := "browser_open"
		if _, ok := args["text"]; ok {
			name = "desktop_type"
		}
		if _, err := broker.Execute(context.Background(), name, args); err == nil {
			t.Fatalf("%s deveria recusar %#v", name, args)
		}
	}
	if len(surface.opens) != 0 || len(surface.texts) != 0 || vault.gets != 0 {
		t.Fatalf("efeito indevido opens=%v texts=%v vault=%d", surface.opens, surface.texts, vault.gets)
	}
}

func TestExperimentalUnisolatedConfigDefaultsOff(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExperimentalUnisolated {
		t.Fatal("padrão deveria ser desligado")
	}
	cfg.ExperimentalUnisolated = true
	if err := SaveConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(dir)
	if err != nil || !loaded.ExperimentalUnisolated {
		t.Fatalf("não persistiu: %+v %v", loaded, err)
	}
}
