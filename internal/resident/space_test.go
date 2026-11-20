package resident

import (
	"context"
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
