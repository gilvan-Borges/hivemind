package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

type fakeJira struct {
	mu       sync.Mutex
	subs     []string
	comments []string
}

func (f *fakeJira) Enabled() bool { return true }
func (f *fakeJira) GetIssue(context.Context, string) (jirax.Issue, error) {
	return jirax.Issue{Key: "PROJ-1", Summary: "Healthcheck", Description: "expor /health", Status: "A fazer"}, nil
}
func (f *fakeJira) Search(context.Context, string, int) ([]jirax.Issue, error) { return nil, nil }
func (f *fakeJira) CreateSubtask(_ context.Context, _, summary, _ string) (jirax.Issue, error) {
	f.mu.Lock()
	f.subs = append(f.subs, summary)
	f.mu.Unlock()
	return jirax.Issue{Key: "PROJ-124", Summary: summary}, nil
}
func (f *fakeJira) Comment(_ context.Context, _, body string) error {
	f.mu.Lock()
	f.comments = append(f.comments, body)
	f.mu.Unlock()
	return nil
}
func (f *fakeJira) Transition(context.Context, string, string) error { return nil }

func TestPipelineWithMocks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não está instalado")
	}
	repo := t.TempDir()
	g := gitx.Local{}
	ctx := context.Background()
	mustGit(t, g, ctx, repo, "init")
	mustGit(t, g, ctx, repo, "config", "user.email", "hivemind@example.com")
	mustGit(t, g, ctx, repo, "config", "user.name", "HiveMind Test")
	mustGit(t, g, ctx, repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("oi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, g, ctx, repo, "add", "README.md")
	mustGit(t, g, ctx, repo, "commit", "-m", "inicial")

	squad := team.DefaultTeam("padrao", "", "scripted", "gemini", 2)
	project := team.File{
		Active: squad.Name,
		Settings: team.Settings{
			JiraEnabled:  true,
			AutoCommit:   true,
			AutoPR:       false,
			MaxQALoops:   3,
			BaseBranch:   "main",
			ConfirmShell: boolPtr(true),
		},
		Teams: []team.Team{squad},
	}
	project.Normalize()

	jira := &fakeJira{}
	var traces sync.Map
	modelOf := func(name string, fn func(req *model.LLMRequest, call int) *model.LLMResponse) *modelx.Scripted {
		return &modelx.Scripted{Fn: func(req *model.LLMRequest, call int) *model.LLMResponse {
			traces.Store(name+":"+itoa(call), true)
			return fn(req, call)
		}}
	}
	models := map[string]model.LLM{
		"pm": modelOf("pm", func(_ *model.LLMRequest, call int) *model.LLMResponse {
			if call == 1 {
				return modelx.FunctionCall("jira_create_subtask", "c1", map[string]any{
					"summary":     "Implementar healthcheck",
					"description": "gravar arquivo",
				})
			}
			return modelx.Text("Plano: gravar a saudação em arquivo.")
		}),
		"tech_lead": modelOf("tech_lead", func(*model.LLMRequest, int) *model.LLMResponse {
			return modelx.Text("dev_1 grava a.txt e dev_2 grava b.txt")
		}),
		"architect": modelOf("architect", func(*model.LLMRequest, int) *model.LLMResponse {
			return modelx.Text("Arquivos de texto na raiz de cada worktree.")
		}),
		"dev_1": modelOf("dev_1", func(_ *model.LLMRequest, call int) *model.LLMResponse {
			if call == 1 {
				return modelx.FunctionCall("write_file", "w1", map[string]any{"path": "a.txt", "content": "olá\n"})
			}
			return modelx.Text("a.txt pronto")
		}),
		"dev_2": modelOf("dev_2", func(_ *model.LLMRequest, call int) *model.LLMResponse {
			if call == 1 {
				return modelx.FunctionCall("write_file", "w2", map[string]any{"path": "b.txt", "content": "olá\n"})
			}
			return modelx.Text("b.txt pronto")
		}),
		"qa": modelOf("qa", func(_ *model.LLMRequest, call int) *model.LLMResponse {
			switch call {
			case 1:
				return modelx.FunctionCall("qa_request_changes", "q1", map[string]any{"notes": "confira a saudação"})
			case 2:
				return modelx.Text("reprovado")
			case 3:
				return modelx.FunctionCall("qa_approve", "q2", map[string]any{"note": "ok"})
			default:
				return modelx.Text("aprovado")
			}
		}),
	}

	store, err := sessionx.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	runner := &Runner{
		Workspace: repo,
		Project:   project,
		Team:      squad,
		Git:       g,
		Jira:      jira,
		Sessions:  store,
		Models:    models,
		Confirm:   func(string) bool { return false },
		Out:       &out,
		Err:       &out,
		Streaming: agent.StreamingModeNone,
	}
	result, err := runner.Run(ctx, Request{Objective: "gravar saudação", JiraKey: "PROJ-1"})
	if err != nil {
		t.Fatalf("run: %v\nsaída:\n%s\ntraces:%v", err, out.String(), traceKeys(&traces))
	}
	a := filepath.Join(repo, ".hivemind", "worktrees", "dev_1", "a.txt")
	b := filepath.Join(repo, ".hivemind", "worktrees", "dev_2", "b.txt")
	for _, path := range []string{a, b} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "olá\n" {
			t.Fatalf("%s = %q %v\nsaída:\n%s", path, data, err, out.String())
		}
	}
	logA, err := g.Run(ctx, filepath.Join(repo, ".hivemind", "worktrees", "dev_1"), "log", "-1", "--pretty=%s")
	if err != nil || !strings.Contains(logA, "PROJ-1") {
		t.Fatalf("commit = %q %v", logA, err)
	}
	if len(jira.subs) != 1 || jira.subs[0] != "Implementar healthcheck" {
		t.Fatalf("subtarefas = %#v", jira.subs)
	}
	if len(jira.comments) != 1 || !strings.Contains(jira.comments[0], result.SessionID) || !strings.Contains(jira.comments[0], "hivemind/proj-1/dev_1") {
		t.Fatalf("comentário = %#v", jira.comments)
	}
	if !strings.Contains(out.String(), "Gerente de projeto") || !strings.Contains(out.String(), "ferramenta write_file") {
		t.Fatalf("saída do time:\n%s", out.String())
	}

	reopened, err := sessionx.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(ctx, &session.GetRequest{AppName: sessionx.AppName, UserID: sessionx.UserID, SessionID: result.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	escalated := false
	for i := 0; i < got.Session.Events().Len(); i++ {
		ev := got.Session.Events().At(i)
		if ev != nil && ev.Actions.Escalate {
			escalated = true
		}
	}
	if !escalated {
		t.Fatalf("QA não encerrou o loop\nsaída:\n%s\ntraces:%v", out.String(), traceKeys(&traces))
	}
	firstEvents := got.Session.Events().Len()

	text := func(*model.LLMRequest, int) *model.LLMResponse { return modelx.Text("seguimos") }
	project.Settings.MaxQALoops = 1
	second := &Runner{
		Workspace: repo,
		Project:   project,
		Team:      squad,
		Git:       g,
		Jira:      jira,
		Sessions:  reopened,
		Models: map[string]model.LLM{
			"pm":        &modelx.Scripted{Fn: text},
			"tech_lead": &modelx.Scripted{Fn: text},
			"architect": &modelx.Scripted{Fn: text},
			"dev_1":     &modelx.Scripted{Fn: text},
			"dev_2":     &modelx.Scripted{Fn: text},
			"qa": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("qa_approve", "q3", map[string]any{"note": "ainda ok"})
				}
				return modelx.Text("aprovado de novo")
			}},
		},
		Confirm:   func(string) bool { return false },
		Out:       &out,
		Err:       &out,
		Streaming: agent.StreamingModeNone,
	}
	resumed, err := second.Run(ctx, Request{Objective: "continuar", JiraKey: "PROJ-1", SessionID: result.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.SessionID != result.SessionID {
		t.Fatalf("sessão mudou: %s", resumed.SessionID)
	}
	again, err := reopened.Get(ctx, &session.GetRequest{AppName: sessionx.AppName, UserID: sessionx.UserID, SessionID: result.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if again.Session.Events().Len() <= firstEvents {
		t.Fatalf("retomada não acrescentou eventos: %d <= %d", again.Session.Events().Len(), firstEvents)
	}
}

func mustGit(t *testing.T, g gitx.Local, ctx context.Context, dir string, args ...string) {
	t.Helper()
	if _, err := g.Run(ctx, dir, args...); err != nil {
		t.Fatal(err)
	}
}

func boolPtr(v bool) *bool { return &v }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func traceKeys(m *sync.Map) []string {
	var out []string
	m.Range(func(k, _ any) bool {
		out = append(out, k.(string))
		return true
	})
	return out
}

func TestTriageDirectAnswerAndDelegationWithSlicesAndClosingChief(t *testing.T) {
	dir := t.TempDir()
	squad := team.DefaultTeam("ide", "", "scripted", "gemini", 2)
	project := team.File{
		Active: squad.Name,
		Settings: team.Settings{
			MaxQALoops:   2,
			BaseBranch:   "main",
			ConfirmShell: boolPtr(true),
		},
		Teams: []team.Team{squad},
	}
	project.Normalize()

	store, err := sessionx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Case 1: Triage Chief answers a read-only question directly without invoking Devs or QA.
	devCalled := false
	directRunner := &Runner{
		Workspace:          dir,
		Project:            project,
		Team:               squad,
		Sessions:           store,
		EnableTriage:       true,
		EnableClosingChief: true,
		Out:                &strings.Builder{},
		Err:                &strings.Builder{},
		Streaming:          agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"pm": &modelx.Scripted{Fn: func(req *model.LLMRequest, _ int) *model.LLMResponse {
				// Verify multimodal attachment arrives on the Chief's request.
				hasImage := false
				for _, c := range req.Contents {
					for _, p := range c.Parts {
						if p.InlineData != nil && p.InlineData.MIMEType == "image/png" {
							hasImage = true
						}
					}
				}
				if !hasImage {
					t.Errorf("expected image attachment in Chief request")
				}
				return modelx.Text("Analisei o print: basta alinhar o botão.")
			}},
			"dev_1": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
				devCalled = true
				return modelx.Text("não deveria rodar")
			}},
		},
	}

	res1, err := directRunner.Run(context.Background(), Request{
		Objective: "analise este print sem editar nada",
		Attachments: []Attachment{{
			Name:     "tela.png",
			MIMEType: "image/png",
			Data:     []byte{0x89, 0x50, 0x4E, 0x47},
		}},
	})
	if err != nil {
		t.Fatalf("direct triage run failed: %v", err)
	}
	if devCalled {
		t.Fatal("expected devs NOT to run on direct triage answer")
	}
	if !strings.Contains(res1.Summary, "Analisei o print") {
		t.Fatalf("unexpected direct summary: %q", res1.Summary)
	}

	// Case 2: Triage Chief calls delegate_to_squad with per-dev slices -> Dev1 & Dev2 receive slices -> QA approves -> Closing Chief summarizes.
	var dev1SawSlice, dev2SawSlice bool
	var events []Event
	var evMu sync.Mutex
	pmTurn := 0
	fullRunner := &Runner{
		Workspace:          dir,
		Project:            project,
		Team:               squad,
		Sessions:           store,
		EnableTriage:       true,
		EnableClosingChief: true,
		Out:                &strings.Builder{},
		Err:                &strings.Builder{},
		Streaming:          agent.StreamingModeNone,
		OnEvent: func(ev Event) {
			evMu.Lock()
			events = append(events, ev)
			evMu.Unlock()
		},
		Models: map[string]model.LLM{
			"pm": &modelx.Scripted{Fn: func(_ *model.LLMRequest, _ int) *model.LLMResponse {
				pmTurn++
				switch pmTurn {
				case 1:
					return modelx.FunctionCall("delegate_to_squad", "d1", map[string]any{
						"plan":      "Dividir front e back",
						"dev_tasks": []any{"cuidar apenas de ui.ts", "cuidar apenas de api.go"},
					})
				case 2:
					return modelx.Text("Acionei o time.")
				default:
					return modelx.Text("Resumo final do Chefe (Fecho): tudo entregue e revisado.")
				}
			}},
			"tech_lead": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
				return modelx.Text("Guardrails definidos.")
			}},
			"dev_1": &modelx.Scripted{Fn: func(req *model.LLMRequest, _ int) *model.LLMResponse {
				if req.Config != nil && req.Config.SystemInstruction != nil {
					for _, p := range req.Config.SystemInstruction.Parts {
						if strings.Contains(p.Text, "cuidar apenas de ui.ts") {
							dev1SawSlice = true
						}
					}
				}
				return modelx.Text("ui.ts concluído")
			}},
			"dev_2": &modelx.Scripted{Fn: func(req *model.LLMRequest, _ int) *model.LLMResponse {
				if req.Config != nil && req.Config.SystemInstruction != nil {
					for _, p := range req.Config.SystemInstruction.Parts {
						if strings.Contains(p.Text, "cuidar apenas de api.go") {
							dev2SawSlice = true
						}
					}
				}
				return modelx.Text("api.go concluído")
			}},
			"qa": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("qa_approve", "qa1", map[string]any{"note": "testes verdes"})
				}
				return modelx.Text("QA aprovado.")
			}},
		},
	}

	res2, err := fullRunner.Run(context.Background(), Request{Objective: "implementar front e back"})
	if err != nil {
		t.Fatalf("delegated triage run failed: %v", err)
	}
	if !dev1SawSlice || !dev2SawSlice {
		t.Fatalf("expected per-dev slices injected: dev1=%v dev2=%v", dev1SawSlice, dev2SawSlice)
	}
	if !strings.Contains(res2.Summary, "Resumo final do Chefe (Fecho)") {
		t.Fatalf("expected Closing Chief summary, got %q", res2.Summary)
	}
	sawHandoff := false
	for _, ev := range events {
		if ev.Kind == "handoff" {
			sawHandoff = true
		}
	}
	if !sawHandoff {
		t.Fatal("expected handoff events emitted for IDE")
	}
}

func TestPlanApprovalPreservesChiefPlanAndSlicesAcrossStores(t *testing.T) {
	dir := t.TempDir()
	squad := team.DefaultTeam("ide-plan", "", "scripted", "gemini", 2)
	project := team.File{
		Active: squad.Name,
		Settings: team.Settings{
			MaxQALoops:   1,
			BaseBranch:   "main",
			ConfirmShell: boolPtr(true),
		},
		Teams: []team.Team{squad},
	}
	project.Normalize()

	store1, err := sessionx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Step 1: Run in "plan" mode -> Chief delegates plan + slices, execution pauses before devs.
	devRanDuringPlan := false
	pmPlanCalls := 0
	planRunner := &Runner{
		Workspace:          dir,
		Project:            project,
		Team:               squad,
		Sessions:           store1,
		EnableTriage:       true,
		EnableClosingChief: true,
		Out:                &strings.Builder{},
		Err:                &strings.Builder{},
		Streaming:          agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"pm": &modelx.Scripted{Fn: func(_ *model.LLMRequest, _ int) *model.LLMResponse {
				pmPlanCalls++
				if pmPlanCalls == 1 {
					return modelx.FunctionCall("delegate_to_squad", "p1", map[string]any{
						"plan":      "Plano arquitetural aprovado pelo humano",
						"dev_tasks": []any{"fatia exclusiva frontend.ts", "fatia exclusiva backend.go"},
					})
				}
				return modelx.Text("Plano pronto para revisão.")
			}},
			"dev_1": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
				devRanDuringPlan = true
				return modelx.Text("erro")
			}},
		},
	}
	planRes, err := planRunner.Run(context.Background(), Request{
		Objective: "criar módulo de autenticação",
		RunMode:   "plan",
	})
	if err != nil {
		t.Fatalf("plan mode failed: %v", err)
	}
	if devRanDuringPlan {
		t.Fatal("devs must not run during plan mode")
	}

	// Step 2: Re-open session store (simulating a new Wails RPC turn) and execute approved plan in "squad" mode.
	store2, err := sessionx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var dev1SawPlan, dev1SawSlice, dev2SawSlice, architectReRan bool
	squadRunner := &Runner{
		Workspace:          dir,
		Project:            project,
		Team:               squad,
		Sessions:           store2,
		EnableTriage:       false,
		EnableClosingChief: true,
		Out:                &strings.Builder{},
		Err:                &strings.Builder{},
		Streaming:          agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"pm": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
				return modelx.Text("Resumo executivo final.")
			}},
			"architect": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
				architectReRan = true
				return modelx.Text("não deveria reexecutar arquiteto")
			}},
			"tech_lead": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
				return modelx.Text("Guardrails ok.")
			}},
			"dev_1": &modelx.Scripted{Fn: func(req *model.LLMRequest, _ int) *model.LLMResponse {
				if req.Config != nil && req.Config.SystemInstruction != nil {
					for _, p := range req.Config.SystemInstruction.Parts {
						if strings.Contains(p.Text, "Plano arquitetural aprovado pelo humano") {
							dev1SawPlan = true
						}
						if strings.Contains(p.Text, "fatia exclusiva frontend.ts") {
							dev1SawSlice = true
						}
					}
				}
				return modelx.Text("frontend pronto")
			}},
			"dev_2": &modelx.Scripted{Fn: func(req *model.LLMRequest, _ int) *model.LLMResponse {
				if req.Config != nil && req.Config.SystemInstruction != nil {
					for _, p := range req.Config.SystemInstruction.Parts {
						if strings.Contains(p.Text, "fatia exclusiva backend.go") {
							dev2SawSlice = true
						}
					}
				}
				return modelx.Text("backend pronto")
			}},
			"qa": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("qa_approve", "qa-ok", map[string]any{"note": "aprovado"})
				}
				return modelx.Text("ok")
			}},
		},
	}

	_, err = squadRunner.Run(context.Background(), Request{
		Objective: "Plano aprovado. Prossigam.",
		SessionID: planRes.SessionID,
		RunMode:   "squad",
	})
	if err != nil {
		t.Fatalf("approved squad run failed: %v", err)
	}
	if architectReRan {
		t.Fatal("expected approved chief_plan to skip redundant PM/Architect planning")
	}
	if !dev1SawPlan || !dev1SawSlice || !dev2SawSlice {
		t.Fatalf("expected approved plan and slices preserved: plan=%v slice1=%v slice2=%v", dev1SawPlan, dev1SawSlice, dev2SawSlice)
	}
}

func TestWorktreesSettingDisabledWritesDirectlyToWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não está instalado")
	}
	repo := t.TempDir()
	g := gitx.Local{}
	ctx := context.Background()
	mustGit(t, g, ctx, repo, "init")
	mustGit(t, g, ctx, repo, "config", "user.email", "hivemind@example.com")
	mustGit(t, g, ctx, repo, "config", "user.name", "HiveMind Test")
	mustGit(t, g, ctx, repo, "config", "commit.gpgsign", "false")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("init\n"), 0o644)
	mustGit(t, g, ctx, repo, "add", "README.md")
	mustGit(t, g, ctx, repo, "commit", "-m", "init")

	squad := team.DefaultTeam("direto", "", "scripted", "gemini", 1)
	project := team.File{
		Active: squad.Name,
		Settings: team.Settings{
			Worktrees:    boolPtr(false),
			MaxQALoops:   1,
			BaseBranch:   "main",
			ConfirmShell: boolPtr(true),
		},
		Teams: []team.Team{squad},
	}
	project.Normalize()

	text := func(*model.LLMRequest, int) *model.LLMResponse { return modelx.Text("ok") }
	r := &Runner{
		Workspace: repo,
		Project:   project,
		Team:      squad,
		Git:       g,
		Out:       &strings.Builder{},
		Err:       &strings.Builder{},
		Streaming: agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"pm":        &modelx.Scripted{Fn: text},
			"tech_lead": &modelx.Scripted{Fn: text},
			"architect": &modelx.Scripted{Fn: text},
			"dev_1": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("write_file", "w1", map[string]any{"path": "direto.txt", "content": "no-wt\n"})
				}
				return modelx.Text("feito")
			}},
			"qa": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("qa_approve", "q1", map[string]any{"note": "ok"})
				}
				return modelx.Text("ok")
			}},
		},
	}
	if _, err := r.Run(ctx, Request{Objective: "criar direto.txt"}); err != nil {
		t.Fatalf("run without worktrees failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "direto.txt")); err != nil {
		t.Fatalf("expected direto.txt written directly in workspace when worktrees=false: %v", err)
	}
}
