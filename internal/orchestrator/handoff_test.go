package orchestrator

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

func TestHandoffObjectiveIsCutAt4KiB(t *testing.T) {
	// internal/orchestrator/handoff.go: objetivo maior que 4 KiB é cortado.
	ws := t.TempDir()
	t.Setenv("HIVEMIND_DATA_DIR", t.TempDir())
	raw := strings.Repeat("a", handoffObjectiveMax+80) + "\nsh -c echo segredo"
	r := &Runner{Workspace: ws, chiefPlan: raw, chiefContextBrief: "fato\ncmd /c dir\nhmlan_1234567890abcdefghij"}
	r.sealHandoff("run 1")
	if len(r.chiefPlan) != handoffObjectiveMax {
		t.Fatalf("objetivo deveria ser cortado em %d, veio %d", handoffObjectiveMax, len(r.chiefPlan))
	}
	if strings.Contains(r.chiefPlan, "sh -c") || strings.Contains(r.chiefContextBrief, "cmd /c") || strings.Contains(r.chiefContextBrief, "hmlan_") {
		t.Fatalf("brief cru sobreviveu: plan=%q brief=%q", r.chiefPlan, r.chiefContextBrief)
	}
	md := filepath.Join(ws, ".hivemind", "handoff", "latest.md")
	info, err := os.Stat(md)
	if err != nil {
		t.Fatal(err)
	}
	// No Windows o Go reporta 0666 mesmo com WriteFile(..., 0600). A criação não muda.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("latest.md deveria nascer 0600, veio %o", info.Mode().Perm())
	}
	body, err := os.ReadFile(md)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "sh -c") || strings.Contains(string(body), "cmd /c") {
		t.Fatalf("dossiê ainda tem argv: %s", body)
	}
	js := filepath.Join(workspace.AppDataRoot(), "handoffs", "run_1.json")
	jinfo, err := os.Stat(js)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && jinfo.Mode().Perm() != 0o600 {
		t.Fatalf("json do handoff deveria ser 0600, veio %o", jinfo.Mode().Perm())
	}
}

func TestDeveloperSelfEdgeDoesNotCallModel(t *testing.T) {
	var got Event
	r := &Runner{OnEvent: func(ev Event) { got = ev }}
	calls := 0
	resp := r.offerHandoff("developer", "developer", "abc", func() *model.LLMResponse {
		calls++
		return &model.LLMResponse{}
	})
	if calls != 0 {
		t.Fatalf("modelo foi chamado %d vez(es)", calls)
	}
	if resp == nil || resp.Content == nil {
		t.Fatal("aresta recusada deveria devolver resposta sem chamar o modelo")
	}
	if got.Kind != "handoff_rejected" || got.FromRole != "developer" || got.ToRole != "developer" {
		t.Fatalf("evento: %+v", got)
	}
	if strings.Contains(got.Text, "/") || strings.Contains(strings.ToLower(got.Text), "cmd /c") {
		t.Fatalf("motivo vazou caminho ou argv: %q", got.Text)
	}
}

func TestQAReturnStillAllowedUntilStall(t *testing.T) {
	r := &Runner{}
	if resp := r.offerHandoff("chief", "developer", "h1", nil); resp != nil {
		t.Fatalf("primeira entrega não deveria ser recusada")
	}
	if resp := r.offerHandoff("developer", "qa", "h1", nil); resp != nil {
		t.Fatalf("developer para qa não deveria ser recusado")
	}
	if resp := r.offerHandoff("qa", "developer", "h1", nil); resp != nil {
		t.Fatalf("volta qa para developer dentro do teto não deveria ser recusada")
	}
	r.qaStallFired = true
	if resp := r.offerHandoff("qa", "chief", "h1", nil); resp == nil {
		t.Fatal("papel já visitado com o mesmo hash deveria ser recusado")
	}
}

func TestSealSanitizesDevSlices(t *testing.T) {
	r := &Runner{
		Workspace: t.TempDir(),
		chiefPlan: "plano",
		devSlices: []string{"edite a.go\ncmd /c dir\nhmlan_1234567890abcdefghij"},
	}
	t.Setenv("HIVEMIND_DATA_DIR", t.TempDir())
	r.sealHandoff("s")
	joined := strings.Join(r.devSlices, "\n")
	if strings.Contains(joined, "cmd /c") || strings.Contains(joined, "hmlan_") {
		t.Fatalf("fatia crua: %q", joined)
	}
	if !strings.Contains(joined, "edite a.go") {
		t.Fatalf("fatia perdeu o texto seguro: %q", joined)
	}
}

func TestParallelDevelopersShareTheSameHandoff(t *testing.T) {
	r := &Runner{}
	rejected := 0
	r.OnEvent = func(ev Event) {
		if ev.Kind == "handoff_rejected" {
			rejected++
		}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	guard := r.handoffModelGuard(team.RoleDeveloper)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if resp, _ := guard(nil, nil); resp != nil {
				t.Errorf("desenvolvedor paralelo foi recusado")
			}
		}()
	}
	close(start)
	wg.Wait()
	if rejected != 0 {
		t.Fatalf("handoff compartilhado foi recusado %d vez(es)", rejected)
	}
	if r.qaLoopIter != 0 {
		t.Fatalf("volta de qa não deveria contar: %d", r.qaLoopIter)
	}
}

func TestExternalHandoffDropsShellAndCaps(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HIVEMIND_DATA_DIR", t.TempDir())
	pkg := ExternalHandoff("run 1", "corrija o parser\nsh -c echo x\ncmd /c whoami", []string{
		"fato seguro",
		"powershell -Command Get-Process",
	})
	if strings.Contains(pkg.Objective, "sh -c") || strings.Contains(pkg.Objective, "cmd /c") {
		t.Fatalf("objetivo manteve shell: %q", pkg.Objective)
	}
	if strings.Contains(strings.Join(pkg.Facts, "\n"), "powershell") {
		t.Fatalf("fato manteve shell: %#v", pkg.Facts)
	}
	if !strings.Contains(pkg.Objective, "corrija o parser") || pkg.DeliveryHash == "" {
		t.Fatalf("pacote incompleto: %+v", pkg)
	}
	if err := DeliverExternalHandoff(ws, pkg); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(ws, ".hivemind", "handoff", "latest.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "sh -c") {
		t.Fatalf("arquivo de handoff manteve shell: %s", body)
	}
}
