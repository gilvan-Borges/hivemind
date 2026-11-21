package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
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
func (f *fakeJira) CreateIssue(_ context.Context, projectKey, _, summary, _, _ string) (jirax.Issue, error) {
	f.mu.Lock()
	f.subs = append(f.subs, summary)
	idx := len(f.subs)
	f.mu.Unlock()
	if projectKey == "" {
		projectKey = "PROJ"
	}
	return jirax.Issue{Key: fmt.Sprintf("%s-%d", projectKey, 100+idx), Summary: summary}, nil
}
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

	squad := team.FullSquadTeam("padrao", "", "scripted", "gemini", 2)
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
	a := filepath.Join(workspace.WorktreePath(repo, "dev_1"), "a.txt")
	b := filepath.Join(workspace.WorktreePath(repo, "dev_2"), "b.txt")
	for _, path := range []string{a, b} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "olá\n" {
			t.Fatalf("%s = %q %v\nsaída:\n%s", path, data, err, out.String())
		}
	}
	deliveryWT := workspace.WorktreePath(repo, "_integracao")
	for _, rel := range []string{"a.txt", "b.txt"} {
		data, err := os.ReadFile(filepath.Join(deliveryWT, rel))
		if err != nil || string(data) != "olá\n" {
			t.Fatalf("consolidated %s = %q %v", rel, data, err)
		}
	}
	// Developers must NOT create commits in their draft worktrees; only Chief/Architect commits on deliveryWT.
	devLog, err := g.Run(ctx, workspace.WorktreePath(repo, "dev_1"), "log", "-1", "--pretty=%s")
	if err != nil || strings.Contains(devLog, "PROJ-1") {
		t.Fatalf("dev worktree should not have committed PROJ-1: %q %v", devLog, err)
	}
	logDelivery, err := g.Run(ctx, deliveryWT, "log", "-1", "--pretty=%s")
	if err != nil || !strings.Contains(logDelivery, "PROJ-1") {
		t.Fatalf("expected Chief/Architect commit on deliveryWT, got %q %v", logDelivery, err)
	}
	if len(jira.subs) != 1 || jira.subs[0] != "Implementar healthcheck" {
		t.Fatalf("subtarefas = %#v", jira.subs)
	}
	if len(jira.comments) != 1 || !strings.Contains(jira.comments[0], result.SessionID) || !strings.Contains(jira.comments[0], "hivemind/proj-1/entrega") {
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

func TestDeliveryIsBlockedWithoutQAApproval(t *testing.T) {
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

	squad := team.DefaultTeam("padrao", "", "scripted", "gemini", 1)
	project := team.File{
		Active: squad.Name,
		Settings: team.Settings{
			AutoCommit:   true,
			AutoPR:       false,
			MaxQALoops:   1,
			BaseBranch:   "main",
			ConfirmShell: boolPtr(true),
		},
		Teams: []team.Team{squad},
	}
	project.Normalize()

	models := map[string]model.LLM{
		"chefe": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
			if call == 1 {
				return modelx.FunctionCall("delegate_to_squad", "d1", map[string]any{
					"plan":      "gravar a.txt",
					"dev_tasks": []string{"gravar a.txt"},
				})
			}
			return modelx.Text("delegado")
		}},
		"dev": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
			if call == 1 {
				return modelx.FunctionCall("write_file", "w1", map[string]any{"path": "a.txt", "content": "não aprovado\n"})
			}
			return modelx.Text("a.txt pronto")
		}},
		"qa": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
			if call == 1 {
				return modelx.FunctionCall("qa_request_changes", "q1", map[string]any{"notes": "falta teste"})
			}
			return modelx.Text("reprovado")
		}},
	}

	store, err := sessionx.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	runner := &Runner{
		Workspace:    repo,
		Project:      project,
		Team:         squad,
		Git:          g,
		Sessions:     store,
		Models:       models,
		Confirm:      func(string) bool { return false },
		Out:          &out,
		Err:          &out,
		Streaming:    agent.StreamingModeNone,
		EnableTriage: true,
	}
	result, err := runner.Run(ctx, Request{Objective: "gravar a.txt"})
	if err != nil {
		t.Fatalf("run: %v\nsaída:\n%s", err, out.String())
	}
	if !result.PendingQA {
		t.Fatalf("expected PendingQA=true; saída:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "PENDENTE") {
		t.Fatalf("mensagem de pendência ausente:\n%s", out.String())
	}
	deliveryWT := workspace.WorktreePath(repo, "_integracao")
	logDelivery, err := g.Run(ctx, deliveryWT, "log", "-1", "--pretty=%s")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logDelivery, "gravar a.txt") {
		t.Fatalf("entrega rejeitada não pode ser commitada: %q", logDelivery)
	}
	status, err := g.Run(ctx, deliveryWT, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "a.txt") {
		t.Fatalf("alteração deveria permanecer na worktree de entrega: %q", status)
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
	squad := team.FullSquadTeam("ide", "", "scripted", "gemini", 2)
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
	squad := team.FullSquadTeam("ide-plan", "", "scripted", "gemini", 2)
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

func TestSDDGatesDocsThenJiraThenSquad(t *testing.T) {
	dir := t.TempDir()
	squad := team.FullSquadTeam("sdd", "", "scripted", "gemini", 2)
	project := team.File{
		Active: squad.Name,
		Settings: team.Settings{
			MaxQALoops:  1,
			BaseBranch:  "main",
			JiraEnabled: true,
			JiraProject: "PROJ",
		},
		Teams: []team.Team{squad},
	}
	project.Normalize()

	type captured struct{ kind, gate string }
	collect := func(out *[]captured) func(Event) {
		return func(ev Event) {
			if ev.Kind == "sdd_gate" {
				*out = append(*out, captured{ev.Kind, ev.Gate})
			}
		}
	}

	// Gate 1 — Fase SDD de docs: Chefe grava spec via write_doc e chama sdd_docs_ready.
	store1, err := sessionx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var docsGates []captured
	devRanDuringDocs := false
	docsRunner := &Runner{
		Workspace:          dir,
		Project:            project,
		Team:               squad,
		Jira:               &fakeJira{},
		Sessions:           store1,
		EnableTriage:       true,
		EnableClosingChief: true,
		Out:                &strings.Builder{},
		Err:                &strings.Builder{},
		Streaming:          agent.StreamingModeNone,
		OnEvent:            collect(&docsGates),
		Models: map[string]model.LLM{
			"pm": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				switch call {
				case 1:
					return modelx.FunctionCall("write_doc", "w1", map[string]any{
						"path":    "specs/05-tarefas.md",
						"content": "# Backlog SDD\n- Epic 1: API de pedidos\n- Task: expor POST /pedidos",
					})
				case 2:
					return modelx.FunctionCall("sdd_docs_ready", "g1", map[string]any{
						"summary": "Specs SDD gravadas: 01-spec, 02-arquitetura, 05-tarefas.",
					})
				}
				return modelx.Text("Docs SDD prontos para aprovação.")
			}},
			"dev_1": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
				devRanDuringDocs = true
				return modelx.Text("erro")
			}},
		},
	}
	docsRes, err := docsRunner.Run(context.Background(), Request{Objective: "novo projeto do zero", RunMode: "plan:sdd"})
	if err != nil {
		t.Fatalf("sdd docs fase falhou: %v", err)
	}
	if devRanDuringDocs {
		t.Fatal("nenhum dev pode rodar na fase SDD de docs")
	}
	if len(docsGates) != 1 || docsGates[0].gate != "docs" {
		t.Fatalf("esperado 1 evento sdd_gate (docs), veio %v", docsGates)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "specs", "05-tarefas.md")); statErr != nil {
		t.Fatalf("specs/05-tarefas.md deveria existir: %v", statErr)
	}

	// Gate 2 — Fase SDD de backlog: Chefe cria Epic no Jira e registra as ondas com delegate_to_squad.
	store2, err := sessionx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var jiraGates []captured
	jira := &fakeJira{}
	jiraRunner := &Runner{
		Workspace:          dir,
		Project:            project,
		Team:               squad,
		Jira:               jira,
		Sessions:           store2,
		EnableTriage:       true,
		EnableClosingChief: true,
		Out:                &strings.Builder{},
		Err:                &strings.Builder{},
		Streaming:          agent.StreamingModeNone,
		OnEvent:            collect(&jiraGates),
		Models: map[string]model.LLM{
			"pm": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				switch call {
				case 1:
					return modelx.FunctionCall("jira_create_issue", "j1", map[string]any{
						"type":        "Epic",
						"summary":     "Epic 1: API de pedidos",
						"description": "Espelha specs/05-tarefas.md",
					})
				case 2:
					return modelx.FunctionCall("delegate_to_squad", "d1", map[string]any{
						"plan":      "Ondas SDD: 1) API de pedidos 2) Front",
						"dev_tasks": []any{"onda 1: internal/pedidos/**", "onda 2: web/pedidos/**"},
					})
				}
				return modelx.Text("Backlog Jira pronto para aprovação.")
			}},
		},
	}
	jiraRes, err := jiraRunner.Run(context.Background(), Request{
		Objective: "Gate 1 aprovado: crie o backlog no Jira.",
		SessionID: docsRes.SessionID,
		RunMode:   "plan:sdd-jira",
	})
	if err != nil {
		t.Fatalf("sdd jira fase falhou: %v", err)
	}
	if len(jiraGates) != 1 || jiraGates[0].gate != "jira" {
		t.Fatalf("esperado 1 evento sdd_gate (jira), veio %v", jiraGates)
	}
	jira.mu.Lock()
	created := len(jira.subs)
	jira.mu.Unlock()
	if created == 0 {
		t.Fatal("esperado ao menos 1 issue criada no Jira fake")
	}
	_ = jiraRes

	// Gate 2 aprovado — Squad executa as ondas registradas.
	store3, err := sessionx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var dev1SawSlice, dev2SawSlice bool
	squadRunner := &Runner{
		Workspace:          dir,
		Project:            project,
		Team:               squad,
		Jira:               &fakeJira{},
		Sessions:           store3,
		EnableTriage:       false,
		EnableClosingChief: true,
		Out:                &strings.Builder{},
		Err:                &strings.Builder{},
		Streaming:          agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"pm": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
				return modelx.Text("Resumo executivo final.")
			}},
			"tech_lead": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
				return modelx.Text("Guardrails ok.")
			}},
			"dev_1": &modelx.Scripted{Fn: func(req *model.LLMRequest, _ int) *model.LLMResponse {
				if req.Config != nil && req.Config.SystemInstruction != nil {
					for _, p := range req.Config.SystemInstruction.Parts {
						if strings.Contains(p.Text, "onda 1: internal/pedidos/**") {
							dev1SawSlice = true
						}
					}
				}
				return modelx.Text("api pronta")
			}},
			"dev_2": &modelx.Scripted{Fn: func(req *model.LLMRequest, _ int) *model.LLMResponse {
				if req.Config != nil && req.Config.SystemInstruction != nil {
					for _, p := range req.Config.SystemInstruction.Parts {
						if strings.Contains(p.Text, "onda 2: web/pedidos/**") {
							dev2SawSlice = true
						}
					}
				}
				return modelx.Text("front pronto")
			}},
			"qa": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("qa_approve", "qa-ok", map[string]any{"note": "critérios de aceite verificados"})
				}
				return modelx.Text("ok")
			}},
		},
	}
	if _, err := squadRunner.Run(context.Background(), Request{
		Objective: "Gate 2 aprovado: disparem o Squad.",
		SessionID: jiraRes.SessionID,
		RunMode:   "squad",
	}); err != nil {
		t.Fatalf("squad pós-gate falhou: %v", err)
	}
	if !dev1SawSlice || !dev2SawSlice {
		t.Fatalf("esperado devs vendo as ondas SDD: dev1=%v dev2=%v", dev1SawSlice, dev2SawSlice)
	}

	// Regressão: plan:sdd-jira sem docs pendentes deve recusar.
	store4, err := sessionx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	orphan := &Runner{
		Workspace: dir,
		Project:   project,
		Team:      squad,
		Jira:      &fakeJira{},
		Sessions:  store4,
		Out:       &strings.Builder{},
		Err:       &strings.Builder{},
		Streaming: agent.StreamingModeNone,
	}
	if _, err := orphan.Run(context.Background(), Request{Objective: "sem docs", RunMode: "plan:sdd-jira"}); err == nil {
		t.Fatal("plan:sdd-jira sem docs pendentes deveria falhar")
	}
}

func TestAutoPauseForApprovalOnLargeTask(t *testing.T) {
	dir := t.TempDir()
	squad := team.FullSquadTeam("auto-pause", "", "scripted", "gemini", 1)
	project := team.File{Active: squad.Name, Settings: team.Settings{MaxQALoops: 1, BaseBranch: "main"}, Teams: []team.Team{squad}}
	project.Normalize()

	store, err := sessionx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sawPlanReady bool
	devRan := false
	r := &Runner{
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
			if ev.Kind == "plan_ready" {
				sawPlanReady = true
			}
		},
		Models: map[string]model.LLM{
			"pm": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("delegate_to_squad", "d1", map[string]any{
						"plan":               "Refatoração ampla e arriscada do módulo de billing",
						"dev_tasks":          []any{"fatia 1: internal/billing/core/**"},
						"pause_for_approval": true,
					})
				}
				return modelx.Text("Tarefa grande: pausando para sua aprovação.")
			}},
			"dev_1": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
				devRan = true
				return modelx.Text("erro")
			}},
		},
	}
	res, err := r.Run(context.Background(), Request{Objective: "refatorar todo o módulo de billing", RunMode: "auto"})
	if err != nil {
		t.Fatalf("auto pause falhou: %v", err)
	}
	if devRan {
		t.Fatal("dev não pode rodar antes da aprovação quando o Chefe pede pausa")
	}
	if !sawPlanReady {
		t.Fatal("esperado card plan_ready quando o Chefe pede pausa no modo Auto")
	}

	// Aprovação: retoma a sessão em modo squad e o dev executa a fatia aprovada.
	store2, err := sessionx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var devSawSlice bool
	r2 := &Runner{
		Workspace:          dir,
		Project:            project,
		Team:               squad,
		Sessions:           store2,
		EnableClosingChief: true,
		Out:                &strings.Builder{},
		Err:                &strings.Builder{},
		Streaming:          agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"pm": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
				return modelx.Text("Resumo executivo.")
			}},
			"tech_lead": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
				return modelx.Text("Guardrails ok.")
			}},
			"dev_1": &modelx.Scripted{Fn: func(req *model.LLMRequest, _ int) *model.LLMResponse {
				if req.Config != nil && req.Config.SystemInstruction != nil {
					for _, p := range req.Config.SystemInstruction.Parts {
						if strings.Contains(p.Text, "fatia 1: internal/billing/core/**") {
							devSawSlice = true
						}
					}
				}
				return modelx.Text("billing refatorado")
			}},
			"qa": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("qa_approve", "qa-ok", map[string]any{"note": "ok"})
				}
				return modelx.Text("ok")
			}},
		},
	}
	if _, err := r2.Run(context.Background(), Request{
		Objective: "Plano aprovado. Prossigam.",
		SessionID: res.SessionID,
		RunMode:   "squad",
	}); err != nil {
		t.Fatalf("squad pós-aprovação falhou: %v", err)
	}
	if !devSawSlice {
		t.Fatal("dev deveria receber a fatia aprovada após retomar em squad")
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

	squad := team.FullSquadTeam("direto", "", "scripted", "gemini", 1)
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

type fakeGitHub struct {
	mu                 sync.Mutex
	me                 string
	prs                map[int]*githubx.PullRequestDetail
	comments           map[int][]string
	requestedReviewers map[int][]string
	merged             map[int]bool
}

func (f *fakeGitHub) Enabled() bool                               { return true }
func (f *fakeGitHub) CurrentUser(context.Context) (string, error) { return f.me, nil }
func (f *fakeGitHub) CreatePullRequest(_ context.Context, req githubx.CreatePR) (githubx.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	num := len(f.prs) + 1
	if f.prs == nil {
		f.prs = map[int]*githubx.PullRequestDetail{}
	}
	f.prs[num] = &githubx.PullRequestDetail{
		Number:  num,
		Title:   req.Title,
		Body:    req.Body,
		URL:     fmt.Sprintf("https://github.com/acme/app/pull/%d", num),
		State:   "open",
		Author:  f.me,
		HeadRef: req.Head,
		BaseRef: req.Base,
	}
	return githubx.PullRequest{Number: num, Title: req.Title, URL: f.prs[num].URL}, nil
}
func (f *fakeGitHub) FindOpenPullRequestByHead(_ context.Context, head string) (*githubx.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, pr := range f.prs {
		if pr.State == "open" && pr.HeadRef == head {
			return &githubx.PullRequest{Number: pr.Number, Title: pr.Title, URL: pr.URL}, nil
		}
	}
	return nil, nil
}
func (f *fakeGitHub) GetPullRequest(_ context.Context, number int) (githubx.PullRequestDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr, ok := f.prs[number]
	if !ok {
		return githubx.PullRequestDetail{}, fmt.Errorf("PR #%d não encontrado", number)
	}
	cp := *pr
	cp.RequestedReviewers = append([]string(nil), pr.RequestedReviewers...)
	cp.Reviews = append([]githubx.PRReview(nil), pr.Reviews...)
	return cp, nil
}
func (f *fakeGitHub) RequestReviewers(_ context.Context, number int, reviewers []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.requestedReviewers == nil {
		f.requestedReviewers = map[int][]string{}
	}
	f.requestedReviewers[number] = append(f.requestedReviewers[number], reviewers...)
	if pr, ok := f.prs[number]; ok {
		pr.RequestedReviewers = append([]string(nil), reviewers...)
	}
	return nil
}
func (f *fakeGitHub) SubmitReview(_ context.Context, number int, event, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pr, ok := f.prs[number]; ok {
		pr.Reviews = append(pr.Reviews, githubx.PRReview{Author: f.me, State: event, Body: body})
	}
	return nil
}
func (f *fakeGitHub) MergePullRequest(_ context.Context, number int, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.merged == nil {
		f.merged = map[int]bool{}
	}
	f.merged[number] = true
	if pr, ok := f.prs[number]; ok {
		pr.Merged = true
		pr.State = "closed"
	}
	return nil
}
func (f *fakeGitHub) CommentOnIssue(_ context.Context, number int, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.comments == nil {
		f.comments = map[int][]string{}
	}
	f.comments[number] = append(f.comments[number], body)
	return nil
}
func (f *fakeGitHub) ListPullRequests(_ context.Context, _ string, _ int) ([]githubx.PullRequestSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]githubx.PullRequestSummary, 0, len(f.prs))
	for _, pr := range f.prs {
		out = append(out, githubx.PullRequestSummary{
			Number:             pr.Number,
			Title:              pr.Title,
			URL:                pr.URL,
			State:              pr.State,
			Author:             pr.Author,
			HeadRef:            pr.HeadRef,
			BaseRef:            pr.BaseRef,
			RequestedReviewers: append([]string(nil), pr.RequestedReviewers...),
		})
	}
	return out, nil
}
func (f *fakeGitHub) ListPullRequestFiles(context.Context, int) ([]githubx.PRFile, error) {
	return nil, nil
}
func (f *fakeGitHub) ListPullRequestComments(_ context.Context, number int) ([]githubx.Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pr, ok := f.prs[number]; ok {
		return append([]githubx.Comment(nil), pr.ReviewComments...), nil
	}
	return nil, nil
}
func (f *fakeGitHub) ReplyToReviewComment(_ context.Context, number int, _ int64, body string) error {
	return f.CommentOnIssue(context.Background(), number, body)
}
func (f *fakeGitHub) ClosePullRequest(_ context.Context, number int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pr, ok := f.prs[number]; ok {
		pr.State = "closed"
	}
	return nil
}
func (f *fakeGitHub) GetIssue(_ context.Context, number int) (githubx.IssueDetail, error) {
	return githubx.IssueDetail{Number: number, State: "open", Author: f.me}, nil
}
func (f *fakeGitHub) CloseIssue(context.Context, int) error { return nil }
func (f *fakeGitHub) ListIssueComments(context.Context, int) ([]githubx.Comment, error) {
	return nil, nil
}
func (f *fakeGitHub) CIStatus(context.Context, string) (string, error) { return "success", nil }

func TestPRReviewCycleHomologAutoMergeAndMainApprovalGate(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não está instalado")
	}
	ctx := context.Background()
	g := gitx.Local{}

	// Create bare origin + local clone so git push works end-to-end.
	origin := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, g, ctx, origin, "init", "--bare")

	repo := t.TempDir()
	mustGit(t, g, ctx, repo, "init")
	mustGit(t, g, ctx, repo, "config", "user.email", "hivemind@example.com")
	mustGit(t, g, ctx, repo, "config", "user.name", "HiveMind Test")
	mustGit(t, g, ctx, repo, "config", "commit.gpgsign", "false")
	mustGit(t, g, ctx, repo, "remote", "add", "origin", origin)
	_ = os.WriteFile(filepath.Join(repo, "auth.go"), []byte("package auth\n"), 0o644)
	mustGit(t, g, ctx, repo, "add", "auth.go")
	mustGit(t, g, ctx, repo, "commit", "-m", "init")
	mustGit(t, g, ctx, repo, "branch", "hivemind/pr-1/entrega", "HEAD")

	gh := &fakeGitHub{
		me: "gilvan",
		prs: map[int]*githubx.PullRequestDetail{
			1: {
				Number:  1,
				Title:   "feat: login",
				URL:     "https://github.com/acme/app/pull/1",
				State:   "open",
				Author:  "gilvan",
				HeadRef: "hivemind/pr-1/entrega",
				BaseRef: "homolog",
				Reviews: []githubx.PRReview{
					{Author: "fulano", State: "CHANGES_REQUESTED", Body: "validar expiração do token"},
				},
			},
			2: {
				Number:  2,
				Title:   "release: main",
				URL:     "https://github.com/acme/app/pull/2",
				State:   "open",
				Author:  "gilvan",
				HeadRef: "hivemind/pr-2/entrega",
				BaseRef: "main",
				Reviews: []githubx.PRReview{
					{Author: "fulano", State: "APPROVED", Body: "LGTM"},
				},
			},
		},
	}

	squad := team.FullSquadTeam("gov", "", "scripted", "gemini", 1)
	project := team.File{
		Active:   squad.Name,
		Settings: team.DefaultSettings(),
		Teams:    []team.Team{squad},
	}
	project.Normalize()

	// Step 1: PR #1 has CHANGES_REQUESTED from fulano -> Chief reads PR #1, dispatches Squad ->
	// Dev tries git commit via shell (blocked!), writes fix -> QA approves -> Closing Chief commits, pushes, comments, and re-requests fulano!
	var devCommitBlocked bool
	pmStep := 0
	r1 := &Runner{
		Workspace:          repo,
		Project:            project,
		Team:               squad,
		Git:                g,
		GitHub:             gh,
		EnableTriage:       true,
		EnableClosingChief: true,
		Out:                &strings.Builder{},
		Err:                &strings.Builder{},
		Streaming:          agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"pm": &modelx.Scripted{Fn: func(_ *model.LLMRequest, _ int) *model.LLMResponse {
				pmStep++
				switch pmStep {
				case 1:
					return modelx.FunctionCall("github_get_pr", "gp1", map[string]any{"number": 1})
				case 2:
					return modelx.FunctionCall("delegate_to_squad", "del1", map[string]any{
						"plan":      "Validar expiração do token em auth.go",
						"dev_tasks": []any{"atualizar auth.go"},
						"pr_number": 1,
						"pr_branch": "hivemind/pr-1/entrega",
						"reviewers": []any{"fulano"},
					})
				case 3:
					return modelx.Text("Despachado para a equipe.")
				case 4:
					return modelx.FunctionCall("git_commit", "gc1", map[string]any{"message": "fix: validar expiração conforme revisão"})
				case 5:
					return modelx.FunctionCall("git_push", "gpush1", map[string]any{})
				case 6:
					return modelx.FunctionCall("github_comment_pr", "gcom1", map[string]any{"number": 1, "body": "Ajustada a validação de expiração; solicitando nova revisão."})
				case 7:
					return modelx.FunctionCall("github_request_review", "greq1", map[string]any{"number": 1, "reviewers": []any{"fulano"}})
				default:
					return modelx.Text("Mudanças commitadas, comentadas no PR #1 e revisão solicitada novamente ao fulano.")
				}
			}},
			"tech_lead": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse { return modelx.Text("ok") }},
			"dev_1": &modelx.Scripted{Fn: func(req *model.LLMRequest, call int) *model.LLMResponse {
				switch call {
				case 1:
					return modelx.FunctionCall("run_shell", "sh1", map[string]any{"command": `git commit -m "tentativa indevida"`})
				case 2:
					for _, c := range req.Contents {
						for _, p := range c.Parts {
							if p.FunctionResponse != nil && strings.Contains(fmt.Sprint(p.FunctionResponse.Response), "apenas o Chefe ou o Arquiteto") {
								devCommitBlocked = true
							}
						}
					}
					return modelx.FunctionCall("write_file", "wf1", map[string]any{"path": "auth.go", "content": "package auth\nfunc Valid() bool { return true }\n"})
				default:
					return modelx.Text("auth.go ajustado")
				}
			}},
			"qa": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("qa_approve", "qa1", map[string]any{"note": "testes ok"})
				}
				return modelx.Text("aprovado")
			}},
		},
	}

	if _, err := r1.Run(ctx, Request{Objective: "verifique o PR 1 que fulano revisou e pediu mudança"}); err != nil {
		t.Fatalf("PR review fix cycle failed: %v", err)
	}
	if !devCommitBlocked {
		t.Fatal("expected developer git commit via run_shell to be blocked by governance")
	}
	if len(gh.comments[1]) == 0 || len(gh.requestedReviewers[1]) == 0 {
		t.Fatalf("expected PR #1 comment and re-requested reviewer fulano: comments=%v reviewers=%v", gh.comments[1], gh.requestedReviewers[1])
	}

	// Step 2: Fulano now approves PR #1 (target = homolog) -> Chief merges automatically WITHOUT asking user permission!
	gh.prs[1].RequestedReviewers = nil
	gh.prs[1].Reviews = append(gh.prs[1].Reviews, githubx.PRReview{Author: "fulano", State: "APPROVED", Body: "Perfeito!"})
	askedUserForHomolog := false
	rHomolog := &Runner{
		Workspace:    repo,
		Project:      project,
		Team:         squad,
		Git:          g,
		GitHub:       gh,
		EnableTriage: true,
		Approve: func(string) bool {
			askedUserForHomolog = true
			return false
		},
		Out:       &strings.Builder{},
		Err:       &strings.Builder{},
		Streaming: agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"pm": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("github_merge_pr", "m1", map[string]any{"number": 1})
				}
				return modelx.Text("PR #1 mesclado em homolog.")
			}},
		},
	}
	if _, err := rHomolog.Run(ctx, Request{Objective: "fulano aprovou o PR 1, mescle"}); err != nil {
		t.Fatalf("homolog merge failed: %v", err)
	}
	if askedUserForHomolog || !gh.merged[1] {
		t.Fatalf("expected homolog merge without user prompt: asked=%v merged=%v", askedUserForHomolog, gh.merged[1])
	}

	// Step 3A: PR #2 targets main -> must ask user permission; when denied, merge MUST NOT happen!
	askedUserForMain := false
	rMainDenied := &Runner{
		Workspace:    repo,
		Project:      project,
		Team:         squad,
		Git:          g,
		GitHub:       gh,
		EnableTriage: true,
		Approve: func(string) bool {
			askedUserForMain = true
			return false
		},
		Out:       &strings.Builder{},
		Err:       &strings.Builder{},
		Streaming: agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"pm": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("github_merge_pr", "m2", map[string]any{"number": 2})
				}
				return modelx.Text("Aguardando sua permissão para mesclar na main.")
			}},
		},
	}
	if _, err := rMainDenied.Run(ctx, Request{Objective: "mesclar PR 2 na main"}); err != nil {
		t.Fatal(err)
	}
	if !askedUserForMain || gh.merged[2] {
		t.Fatalf("expected main merge blocked until user approves: asked=%v merged=%v", askedUserForMain, gh.merged[2])
	}

	// Step 3B: With full_access permission mode in Settings, main merge proceeds without prompting!
	fullProject := project
	fullProject.Settings.PermissionMode = team.PermissionFullAccess
	promptedInFullAccess := false
	rMainFull := &Runner{
		Workspace:    repo,
		Project:      fullProject,
		Team:         squad,
		Git:          g,
		GitHub:       gh,
		EnableTriage: true,
		Approve: func(string) bool {
			promptedInFullAccess = true
			return false
		},
		Out:       &strings.Builder{},
		Err:       &strings.Builder{},
		Streaming: agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"pm": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("github_merge_pr", "m3", map[string]any{"number": 2})
				}
				return modelx.Text("PR #2 mesclado na main com full_access.")
			}},
		},
	}
	if _, err := rMainFull.Run(ctx, Request{Objective: "mesclar PR 2 na main"}); err != nil {
		t.Fatal(err)
	}
	if promptedInFullAccess || !gh.merged[2] {
		t.Fatalf("expected full_access to merge main without prompt: prompted=%v merged=%v", promptedInFullAccess, gh.merged[2])
	}
}

func TestLeanTeamDynamicDevScaling(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não instalado")
	}
	ctx := context.Background()
	repo := t.TempDir()
	g := gitx.Local{}
	mustGit(t, g, ctx, repo, "init", "-b", "main")
	mustGit(t, g, ctx, repo, "config", "user.name", "Teste")
	mustGit(t, g, ctx, repo, "config", "user.email", "teste@local")
	mustGit(t, g, ctx, repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("lean\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, g, ctx, repo, "add", "README.md")
	mustGit(t, g, ctx, repo, "commit", "-m", "inicial")

	// Lean 3-agent team: chefe (architect), dev (developer), qa (qa).
	squad := team.DefaultTeam("principal", "", "scripted", "gemini", 1)
	if len(squad.Agents) != 3 {
		t.Fatalf("expected 3 default agents (chefe, dev, qa), got %d: %+v", len(squad.Agents), squad.Agents)
	}
	if err := squad.ValidateForRun(); err != nil {
		t.Fatalf("lean 3-agent team should validate: %v", err)
	}
	project := team.File{
		Active: squad.Name,
		Settings: team.Settings{
			AutoCommit:     false,
			AutoPR:         false,
			MaxQALoops:     2,
			MaxDynamicDevs: 5,
			BaseBranch:     "main",
		},
		Teams: []team.Team{squad},
	}
	project.Normalize()

	var scaledDevs []string
	r := &Runner{
		Workspace:          repo,
		Project:            project,
		Team:               squad,
		Git:                g,
		GitHub:             githubx.Disabled{Reason: "off"},
		Jira:               jirax.Disabled{Reason: "off"},
		EnableTriage:       true,
		EnableClosingChief: true,
		Out:                &strings.Builder{},
		Err:                &strings.Builder{},
		Streaming:          agent.StreamingModeNone,
		OnEvent: func(ev Event) {
			if ev.Kind == "devs_scaled" {
				scaledDevs = append([]string(nil), ev.Devs...)
			}
		},
		Models: map[string]model.LLM{
			"chefe": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("delegate_to_squad", "d1", map[string]any{
						"plan": "Entrega grande dividida em 3 módulos independentes.",
						"dev_tasks": []any{
							"Criar mod1.txt",
							"Criar mod2.txt",
							"Criar mod3.txt",
						},
					})
				}
				return modelx.Text("Entrega com 3 devs dinâmicos finalizada.")
			}},
			"dev_1": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("write_file", "w1", map[string]any{"path": "mod1.txt", "content": "1\n"})
				}
				return modelx.Text("mod1 pronto")
			}},
			"dev_2": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("write_file", "w2", map[string]any{"path": "mod2.txt", "content": "2\n"})
				}
				return modelx.Text("mod2 pronto")
			}},
			"dev_3": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("write_file", "w3", map[string]any{"path": "mod3.txt", "content": "3\n"})
				}
				return modelx.Text("mod3 pronto")
			}},
			"qa": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("qa_approve", "q1", map[string]any{"note": "3 módulos ok"})
				}
				return modelx.Text("aprovado")
			}},
		},
	}

	if _, err := r.Run(ctx, Request{Objective: "implementar 3 módulos paralelos"}); err != nil {
		t.Fatal(err)
	}
	if len(scaledDevs) != 3 || scaledDevs[0] != "dev_1" || scaledDevs[1] != "dev_2" || scaledDevs[2] != "dev_3" {
		t.Fatalf("expected 3 dynamically scaled devs [dev_1 dev_2 dev_3], got %v", scaledDevs)
	}
	for _, fn := range []string{"mod1.txt", "mod2.txt", "mod3.txt"} {
		if _, err := os.Stat(filepath.Join(r.deliveryRoot, fn)); err != nil {
			t.Fatalf("expected %s integrated in delivery worktree %s: %v", fn, r.deliveryRoot, err)
		}
	}
}

func TestRoleTargetedSkillsInjection(t *testing.T) {
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
	for name, body := range map[string]string{
		"chief-only": "# Chief Only\nResumo chief.\n\nCORPO_EXCLUSIVO_CHIEF",
		"dev-only":   "# Dev Only\nResumo dev.\n\nCORPO_EXCLUSIVO_DEV",
		"qa-only":    "# QA Only\nResumo qa.\n\nCORPO_EXCLUSIVO_QA",
		"everyone":   "# Everyone\nResumo geral.\n\nCORPO_PARA_TODOS",
	} {
		dir := filepath.Join(repo, ".agents", "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustGit(t, g, ctx, repo, "add", ".")
	mustGit(t, g, ctx, repo, "commit", "-m", "inicial")

	// Shared team memory must reach the pipeline agents through the state
	// handoff (IncludeContentsNone), not only the triage Chief.
	if err := os.MkdirAll(filepath.Join(repo, ".hivemind"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".hivemind", "MEMORY.md"), []byte("# Mem\n\nMEMORIA_MARCADOR: use JDK 21.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	squad := team.Team{
		Name: "principal",
		Agents: []team.Agent{
			{Name: "chefe", Role: team.RoleArchitect, Provider: "scripted", Model: "scripted"},
			{Name: "dev", Role: team.RoleDeveloper, Provider: "scripted", Model: "scripted"},
			{Name: "qa", Role: team.RoleQA, Provider: "scripted", Model: "scripted"},
		},
	}
	project := team.File{
		Active: squad.Name,
		Settings: team.Settings{
			MaxQALoops: 2,
			BaseBranch: "main",
			SkillAssignments: []team.SkillAssignment{
				{Name: "chief-only", Source: "project", Roles: []string{team.SkillTargetChief}},
				{Name: "dev-only", Source: "project", Roles: []string{team.SkillTargetDev}},
				{Name: "qa-only", Source: "project", Roles: []string{team.SkillTargetQA}},
				{Name: "everyone", Source: "project", Roles: []string{team.SkillTargetAll}},
			},
		},
		Teams: []team.Team{squad},
	}
	project.Normalize()

	var chiefInst, chiefUserMsg, devInst, qaInst string
	extractSystem := func(req *model.LLMRequest) string {
		if req == nil || req.Config == nil || req.Config.SystemInstruction == nil {
			return ""
		}
		var sb strings.Builder
		for _, p := range req.Config.SystemInstruction.Parts {
			if p != nil {
				sb.WriteString(p.Text)
			}
		}
		return sb.String()
	}
	extractUser := func(req *model.LLMRequest) string {
		if req == nil {
			return ""
		}
		var sb strings.Builder
		for _, c := range req.Contents {
			if c == nil || c.Role != genai.RoleUser {
				continue
			}
			for _, p := range c.Parts {
				if p != nil && p.FunctionResponse == nil {
					sb.WriteString(p.Text)
				}
			}
		}
		return sb.String()
	}

	r := &Runner{
		Workspace:          repo,
		Project:            project,
		Team:               squad,
		Git:                g,
		GitHub:             githubx.Disabled{Reason: "test"},
		Jira:               jirax.Disabled{Reason: "test"},
		EnableTriage:       true,
		EnableClosingChief: false,
		Out:                &strings.Builder{},
		Err:                &strings.Builder{},
		Streaming:          agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"chefe": &modelx.Scripted{Fn: func(req *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					chiefInst = extractSystem(req)
					chiefUserMsg = extractUser(req)
					return modelx.FunctionCall("delegate_to_squad", "d1", map[string]any{
						"plan":      "Executar com skills por papel",
						"dev_tasks": []any{"Alterar a.txt"},
					})
				}
				return modelx.Text("ok")
			}},
			"dev": &modelx.Scripted{Fn: func(req *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					devInst = extractSystem(req)
					return modelx.FunctionCall("write_file", "w1", map[string]any{"path": "a.txt", "content": "1\n"})
				}
				return modelx.Text("dev ok")
			}},
			"qa": &modelx.Scripted{Fn: func(req *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					qaInst = extractSystem(req)
					return modelx.FunctionCall("qa_approve", "q1", map[string]any{"note": "ok"})
				}
				return modelx.Text("qa ok")
			}},
		},
	}

	if _, err := r.Run(ctx, Request{Objective: "testar skills por papel"}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(chiefInst, "CORPO_EXCLUSIVO_CHIEF") || !strings.Contains(chiefInst, "CORPO_PARA_TODOS") {
		t.Fatalf("expected chief-only and everyone in chief instruction, got: %s", chiefInst)
	}
	if strings.Contains(chiefInst, "CORPO_EXCLUSIVO_DEV") || strings.Contains(chiefInst, "CORPO_EXCLUSIVO_QA") {
		t.Fatalf("did not expect dev-only or qa-only body in chief instruction")
	}
	if !strings.Contains(devInst, "CORPO_EXCLUSIVO_DEV") || !strings.Contains(devInst, "CORPO_PARA_TODOS") {
		t.Fatalf("expected dev-only and everyone in dev instruction, got: %s", devInst)
	}
	if strings.Contains(devInst, "CORPO_EXCLUSIVO_CHIEF") || strings.Contains(devInst, "CORPO_EXCLUSIVO_QA") {
		t.Fatalf("did not expect chief-only or qa-only in dev instruction")
	}
	if !strings.Contains(qaInst, "CORPO_EXCLUSIVO_QA") || !strings.Contains(qaInst, "CORPO_PARA_TODOS") {
		t.Fatalf("expected qa-only and everyone in qa instruction, got: %s", qaInst)
	}
	if strings.Contains(qaInst, "CORPO_EXCLUSIVO_CHIEF") || strings.Contains(qaInst, "CORPO_EXCLUSIVO_DEV") {
		t.Fatalf("did not expect chief-only or dev-only in qa instruction")
	}
	if !strings.Contains(devInst, "MEMORIA_MARCADOR") || !strings.Contains(qaInst, "MEMORIA_MARCADOR") {
		t.Fatalf("team memory should reach dev and QA through state, dev=%q qa=%q", devInst, qaInst)
	}
}

func TestUsageSummaryLine(t *testing.T) {
	cases := []struct {
		name   string
		result Result
		want   string
	}{
		{
			name:   "sem tokens não gera linha",
			result: Result{},
			want:   "",
		},
		{
			name:   "formata entrada, saída e total",
			result: Result{InputTokens: 1032816, OutputTokens: 70062, TotalTokens: 1102878},
			want:   "Tokens: ↑1032816 entrada · ↓70062 saída · 1102878 total",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := usageSummaryLine(tc.result); got != tc.want {
				t.Fatalf("usageSummaryLine = %q, queria %q", got, tc.want)
			}
		})
	}
}

func TestPrintUsageSummaryWritesNothingWhenEmpty(t *testing.T) {
	var buf strings.Builder
	(&Runner{}).printUsageSummary(Result{}, &buf, "")
	if buf.Len() != 0 {
		t.Fatalf("sem tokens não deveria imprimir nada; saída = %q", buf.String())
	}
	(&Runner{}).printUsageSummary(Result{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, &buf, "")
	if !strings.Contains(buf.String(), "↑10 entrada") || !strings.Contains(buf.String(), "15 total") {
		t.Fatalf("resumo incompleto: %q", buf.String())
	}
}

// TestTeamMemorySnippetInjectsSharedMemory valida o achado #7 do dogfooding:
// o CLI passa a ler .hivemind/MEMORY.md em todo run, como a IDE já fazia.
func TestTeamMemorySnippetInjectsSharedMemory(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{Workspace: dir}

	// Primeiro run: arquivo não existe — cria o starter e não injeta nada.
	if got := r.teamMemorySnippet(); got != "" {
		t.Fatalf("primeiro run não deveria injetar nada; got = %q", got)
	}
	memPath := workspace.MemoryPath(dir)
	starter, err := os.ReadFile(memPath)
	if err != nil || !strings.Contains(string(starter), "Memória Compartilhada") {
		t.Fatalf("starter não foi criado: %v", err)
	}

	// Conhecimento caro do run anterior (ex.: o JDK 21 da T1) fica disponível.
	note := "Testes do core: rode .\\gradlew.bat com JDK 21 (o JDK 25 padrão quebra o Gradle 8.x)."
	if err := os.WriteFile(memPath, []byte("# Mem\n\n"+note), 0o600); err != nil {
		t.Fatal(err)
	}
	got := r.teamMemorySnippet()
	if !strings.Contains(got, "[Memória Compartilhada do Time") || !strings.Contains(got, note) {
		t.Fatalf("snippet não contém a memória: %q", got)
	}

	// Arquivo vazio não injeta lixo.
	if err := os.WriteFile(memPath, []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := r.teamMemorySnippet(); got != "" {
		t.Fatalf("memória vazia deveria injetar nada; got = %q", got)
	}
}

func TestClosingChiefBlockedWithoutQAApproval(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não instalado")
	}
	ctx := context.Background()
	repo := t.TempDir()
	g := gitx.Local{}
	mustGit(t, g, ctx, repo, "init", "-b", "main")
	mustGit(t, g, ctx, repo, "config", "user.name", "Teste")
	mustGit(t, g, ctx, repo, "config", "user.email", "teste@local")
	mustGit(t, g, ctx, repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, g, ctx, repo, "add", "README.md")
	mustGit(t, g, ctx, repo, "commit", "-m", "inicial")

	squad := team.DefaultTeam("principal", "", "scripted", "gemini", 1)
	project := team.File{
		Active: squad.Name,
		Settings: team.Settings{
			AutoCommit: true,
			AutoPR:     false,
			MaxQALoops: 1,
			BaseBranch: "main",
		},
		Teams: []team.Team{squad},
	}
	project.Normalize()

	chiefCalls := 0
	r := &Runner{
		Workspace:          repo,
		Project:            project,
		Team:               squad,
		Git:                g,
		GitHub:             githubx.Disabled{Reason: "off"},
		Jira:               jirax.Disabled{Reason: "off"},
		EnableTriage:       true,
		EnableClosingChief: true,
		Out:                &strings.Builder{},
		Err:                &strings.Builder{},
		Streaming:          agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"chefe": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				chiefCalls++
				switch chiefCalls {
				case 1:
					return modelx.FunctionCall("delegate_to_squad", "d1", map[string]any{
						"plan":      "Criar unapproved.txt",
						"dev_tasks": []any{"Criar unapproved.txt"},
					})
				case 2:
					// Triage finish text
					return modelx.Text("delegado")
				case 3:
					// Closing chief attempts to commit even though QA never approved!
					return modelx.FunctionCall("git_commit", "c1", map[string]any{
						"message": "tentativa indevida sem QA",
					})
				default:
					return modelx.Text("Resumo: QA não aprovou, commit bloqueado.")
				}
			}},
			"dev": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("write_file", "w1", map[string]any{"path": "unapproved.txt", "content": "nao aprovado\n"})
				}
				return modelx.Text("dev pronto")
			}},
			"qa": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("qa_request_changes", "q1", map[string]any{"notes": "faltam testes"})
				}
				return modelx.Text("reprovado")
			}},
		},
	}

	res, err := r.Run(ctx, Request{Objective: "criar unapproved.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.PendingQA {
		t.Fatalf("expected PendingQA=true when QA rejects, got %+v", res)
	}
	logOut, err := g.Status(ctx, r.deliveryRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logOut, "unapproved.txt") {
		t.Fatalf("expected unapproved.txt to remain uncommitted in delivery worktree, git status=%q", logOut)
	}
}

func TestResumedSessionPreservesPriorContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não instalado")
	}
	ctx := context.Background()
	repo := t.TempDir()
	g := gitx.Local{}
	mustGit(t, g, ctx, repo, "init", "-b", "main")
	mustGit(t, g, ctx, repo, "config", "user.name", "Teste")
	mustGit(t, g, ctx, repo, "config", "user.email", "teste@local")
	mustGit(t, g, ctx, repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, g, ctx, repo, "add", "README.md")
	mustGit(t, g, ctx, repo, "commit", "-m", "inicial")

	squad := team.DefaultTeam("principal", "", "scripted", "gemini", 1)
	project := team.File{
		Active: squad.Name,
		Settings: team.Settings{
			AutoCommit: false,
			AutoPR:     false,
			MaxQALoops: 1,
			BaseBranch: "main",
		},
		Teams: []team.Team{squad},
	}
	project.Normalize()

	extractSystem := func(req *model.LLMRequest) string {
		if req == nil || req.Config == nil || req.Config.SystemInstruction == nil {
			return ""
		}
		var sb strings.Builder
		for _, p := range req.Config.SystemInstruction.Parts {
			if p != nil {
				sb.WriteString(p.Text)
			}
		}
		return sb.String()
	}

	r1 := &Runner{
		Workspace: repo,
		Project:   project,
		Team:      squad,
		Git:       g,
		GitHub:    githubx.Disabled{Reason: "off"},
		Jira:      jirax.Disabled{Reason: "off"},
		Out:       &strings.Builder{},
		Err:       &strings.Builder{},
		Streaming: agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"chefe": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
				return modelx.Text("DECISAO_TURNO_1: usar padrão repository com cache LRU")
			}},
			"dev": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
				return modelx.Text("dev turno 1 ok")
			}},
			"qa": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("qa_approve", "q1", map[string]any{"note": "ok"})
				}
				return modelx.Text("aprovado")
			}},
		},
	}
	res1, err := r1.Run(ctx, Request{Objective: "REQUISITO_TURNO_1: criar endpoint /v1/users"})
	if err != nil {
		t.Fatal(err)
	}

	var devTurn2System string
	r2 := &Runner{
		Workspace: repo,
		Project:   project,
		Team:      squad,
		Git:       g,
		GitHub:    githubx.Disabled{Reason: "off"},
		Jira:      jirax.Disabled{Reason: "off"},
		Out:       &strings.Builder{},
		Err:       &strings.Builder{},
		Streaming: agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"chefe": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse {
				return modelx.Text("plano turno 2")
			}},
			"dev": &modelx.Scripted{Fn: func(req *model.LLMRequest, _ int) *model.LLMResponse {
				devTurn2System = extractSystem(req)
				return modelx.Text("dev turno 2 ok")
			}},
			"qa": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("qa_approve", "q2", map[string]any{"note": "ok"})
				}
				return modelx.Text("aprovado")
			}},
		},
	}
	if _, err := r2.Run(ctx, Request{SessionID: res1.SessionID, Objective: "aplique o mesmo padrão no segundo endpoint"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(devTurn2System, "[prior_session_context]") || !strings.Contains(devTurn2System, "DECISAO_TURNO_1") {
		t.Fatalf("expected resumed dev to receive prior_session_context with turn 1 decision, got: %q", devTurn2System)
	}
}
