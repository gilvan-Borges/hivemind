package resident

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/skillx"
)

func TestPhase3CronIsStructuredAndFiresOnce(t *testing.T) {
	dir := t.TempDir()
	if _, err := SaveRoutine(dir, Routine{
		Expr:     "0 9 * * *",
		Timezone: "UTC",
		Text:     "revisar o dia",
		Enabled:  true,
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "routines.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "command") || strings.Contains(string(raw), "sh -c") {
		t.Fatalf("rotina gravou comando: %s", raw)
	}
	info, err := os.Stat(filepath.Join(dir, "routines.json"))
	if err != nil {
		t.Fatal(err)
	}
	// No Windows o Go reporta 0666 mesmo com WriteFile(..., 0600). A criação não muda.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("routines.json deveria ser 0600, veio %o", info.Mode().Perm())
	}
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	got, ok, err := ClaimDue(dir, now)
	if err != nil || !ok || got.Text != "revisar o dia" {
		t.Fatalf("primeira cobrança = (%+v, %v, %v)", got, ok, err)
	}
	if _, ok, err = ClaimDue(dir, now); err != nil || ok {
		t.Fatalf("segunda cobrança no mesmo instante deveria ser vazia (ok=%v err=%v)", ok, err)
	}
	if _, err := SaveRoutine(dir, Routine{Expr: "* * * * *", Text: "sh -c echo x", Enabled: true}); err == nil {
		t.Fatal("rotina com shell deveria falhar")
	}
	dec := Decide(Call{
		Name:   "cron_add",
		Origin: OriginLocal,
		Args:   map[string]any{"expr": "* * * * *", "text": "ok", "command": "sh -c echo"},
	})
	if dec.Allow || dec.Ask {
		t.Fatalf("cron com comando deveria falhar fechado: %+v", dec)
	}
}

func TestPhase3NoSystemSchedulerInSource(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if path == "os/exec" {
				t.Fatalf("%s importa os/exec", name)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			switch id.Name {
			case "schtasks", "crontab":
				t.Errorf("%s chama %s", name, id.Name)
			}
			return true
		})
	}
}

func TestPhase3SkillHashKeepsProjectAndStaleOut(t *testing.T) {
	base := t.TempDir()
	native := t.TempDir()
	writeSkill(t, native, "revisada", "corpo revisado da skill nativa")
	writeSkill(t, native, "stale", "corpo que mudou")
	if _, err := ReviewSkill(base, native, "revisada"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReviewSkill(base, native, "stale"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(native, "stale", "SKILL.md"), []byte("corpo novo sem revisão"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Disabled skills stay out even with a matching hash.
	writeSkill(t, native, "untangle-codebase", "nao usar")
	if err := skillx.SetSkillEnabled(native, "untangle-codebase", false); err != nil {
		t.Fatal(err)
	}
	if _, err := ReviewSkill(base, native, "untangle-codebase"); err != nil {
		t.Fatal(err)
	}

	block := SkillBlock(native, base)
	if !strings.Contains(block, "revisada") || !strings.Contains(block, "corpo revisado") {
		t.Fatalf("skill revisada deveria entrar: %s", block)
	}
	if strings.Contains(block, "stale") || strings.Contains(block, "corpo novo") {
		t.Fatalf("hash diferente deveria ficar de fora: %s", block)
	}
	if strings.Contains(block, "untangle-codebase") || strings.Contains(block, "nao usar") {
		t.Fatalf("skill desligada deveria ficar de fora: %s", block)
	}

	ws := t.TempDir()
	writeSkill(t, filepath.Join(ws, ".agents", "skills"), "do-projeto", "skill de projeto nao entra")
	scripted := &modelx.Scripted{Fn: func(req *model.LLMRequest, _ int) *model.LLMResponse {
		return modelx.Text("ok")
	}}
	var prompt string
	scripted.Fn = func(req *model.LLMRequest, _ int) *model.LLMResponse {
		prompt = flattenReq(req)
		return modelx.Text("ok")
	}
	r := &Runner{
		BaseDir:   base,
		SkillsDir: native,
		Model:     scripted,
		Streaming: agent.StreamingModeNone,
	}
	if _, err := r.RunTurn(context.Background(), TurnRequest{
		Message:   "oi",
		Workspace: ws,
		Origin:    OriginLocal,
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "do-projeto") || strings.Contains(prompt, "skill de projeto") {
		t.Fatalf("skill de projeto entrou no prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "corpo revisado") {
		t.Fatalf("skill revisada não entrou no turno: %s", prompt)
	}
}

func TestPhase3SubagentCapAndRemoteApproval(t *testing.T) {
	base := t.TempDir()
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "README.md"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	blocker := &modelx.Scripted{Fn: func(req *model.LLMRequest, _ int) *model.LLMResponse {
		if strings.Contains(flattenReq(req), "segura") {
			started <- struct{}{}
			<-release
		}
		return modelx.Text("pronto")
	}}
	parent := &Runner{BaseDir: base, Model: blocker, Streaming: agent.StreamingModeNone}
	var wg sync.WaitGroup
	wg.Add(2)
	errCh := make(chan error, 3)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			_, err := parent.runSpawn(context.Background(), "segura", nil, nil)
			errCh <- err
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("subagente não chegou a rodar")
		}
	}
	if _, err := parent.runSpawn(context.Background(), "terceiro", nil, nil); err == nil {
		t.Fatal("terceiro subagente deveria recusar")
	}
	close(release)
	wg.Wait()

	asks := 0
	scripted := &modelx.Scripted{Fn: func(req *model.LLMRequest, _ int) *model.LLMResponse {
		text := flattenReq(req)
		if strings.Contains(text, "abra um subagente") && !modelx.LastIsFunctionResponse(req) {
			return modelx.FunctionCall("spawn_subagent", "s1", map[string]any{"objective": "ler o arquivo"})
		}
		if strings.Contains(text, "ler o arquivo") && !modelx.LastIsFunctionResponse(req) {
			return modelx.FunctionCall("read_file", "r1", map[string]any{"path": "README.md"})
		}
		return modelx.Text("li")
	}}
	r := &Runner{
		BaseDir:   base,
		Model:     scripted,
		Streaming: agent.StreamingModeNone,
		ApproveDetailed: func(_ context.Context, req ApprovalRequest) bool {
			asks++
			if req.ToolName == "read_file" && req.Origin != OriginSubagent {
				t.Errorf("leitura do filho com origem %s", req.Origin)
			}
			return true
		},
	}
	if _, err := r.RunTurn(context.Background(), TurnRequest{
		Message:   "abra um subagente",
		Workspace: ws,
		Origin:    OriginRemote,
	}); err != nil {
		t.Fatal(err)
	}
	if asks < 2 {
		t.Fatalf("pai remoto deveria pedir aprovação no spawn e na leitura do filho, veio %d", asks)
	}
	for _, c := range r.LastBroker.ObservedCalls() {
		if c.Name == "spawn_subagent" && c.Origin != OriginRemote {
			t.Fatalf("spawn carimbou origem %s", c.Origin)
		}
	}
}

func TestPhase3HandoffShowsPermissionMode(t *testing.T) {
	var called int
	var prompt string
	r := &Runner{
		BaseDir:        t.TempDir(),
		Streaming:      agent.StreamingModeNone,
		PermissionMode: func(string) string { return "full_access" },
		SquadHandoff: func(_ context.Context, h SquadHandoff) (string, error) {
			called++
			if h.PermissionMode != "full_access" {
				t.Fatalf("permission_mode = %q", h.PermissionMode)
			}
			if strings.Contains(h.Objective, "sh -c") {
				t.Fatalf("objetivo chegou com shell: %q", h.Objective)
			}
			return "abc", nil
		},
		ApproveDetailed: func(_ context.Context, req ApprovalRequest) bool {
			prompt = req.Prompt
			return true
		},
		Model: &modelx.Scripted{Fn: func(req *model.LLMRequest, call int) *model.LLMResponse {
			if call == 1 {
				return modelx.FunctionCall("delegate_to_hive_team", "h1", map[string]any{
					"objective": "ajuste o parser",
					"facts":     []string{"assinatura Foo()"},
				})
			}
			return modelx.Text("entregue")
		}},
	}
	if _, err := r.RunTurn(context.Background(), TurnRequest{Message: "passe ao time", Origin: OriginLocal, Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("handoff chamado %d vezes", called)
	}
	if !strings.Contains(prompt, "full_access") {
		t.Fatalf("cartão sem permission_mode: %s", prompt)
	}

	denied := NewBroker(BrokerConfig{
		Workspace:       t.TempDir(),
		Origin:          OriginLocal,
		Phase3:          true,
		BaseDir:         t.TempDir(),
		Audit:           NewFileAuditLogger(filepath.Join(t.TempDir(), "audit.jsonl")),
		SquadHandoff:    r.SquadHandoff,
		ApproveDetailed: func(context.Context, ApprovalRequest) bool { return false },
	})
	called = 0
	if _, err := denied.Execute(context.Background(), "delegate_to_hive_team", map[string]any{"objective": "de novo"}); err == nil {
		t.Fatal("negativa deveria recusar o handoff")
	}
	if called != 0 {
		t.Fatal("handoff correu sem aprovação")
	}
}

func TestPhase3RoutineManifestNarrows(t *testing.T) {
	dec := DecideWithPolicy(Call{
		Name:         "net_fetch",
		Origin:       OriginRoutine,
		AllowedTiers: []Tier{TierRead},
		Args:         map[string]any{"url": "https://example.com"},
	}, Policy{AllowedHosts: []string{"example.com"}})
	if dec.Allow || dec.Ask {
		t.Fatalf("manifesto read não pode liberar rede: %+v", dec)
	}
}

func writeSkill(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func flattenReq(req *model.LLMRequest) string {
	if req == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range req.Contents {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			if p != nil && p.Text != "" {
				b.WriteString(p.Text)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}
