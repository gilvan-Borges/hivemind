package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/workerx"
)

type fakeExternalWorkerAdapter struct {
	provider   string
	writeFile  string
	writeBody  string
	askPrompt  string
	emitFiles  []string
	seenReq    workerx.DispatchRequest
	askAllowed bool
}

func (f *fakeExternalWorkerAdapter) Provider() string { return f.provider }

func (f *fakeExternalWorkerAdapter) Execute(ctx context.Context, req workerx.DispatchRequest, cb workerx.Callbacks) (workerx.DoneEvent, error) {
	f.seenReq = req
	if cb.OnHeartbeat != nil {
		cb.OnHeartbeat(workerx.HeartbeatEvent{DispatchID: req.DispatchID, TaskID: req.TaskID, Message: "working"})
	}
	if f.askPrompt != "" && cb.OnAsk != nil {
		resp, err := cb.OnAsk(ctx, workerx.AskRequest{DispatchID: req.DispatchID, TaskID: req.TaskID, Question: f.askPrompt})
		if err != nil {
			return workerx.DoneEvent{}, err
		}
		f.askAllowed = resp.Approved
		if !resp.Approved {
			return workerx.DoneEvent{DispatchID: req.DispatchID, TaskID: req.TaskID, Outcome: "aborted", Summary: "negado"}, nil
		}
	}
	if f.writeFile != "" {
		abs := filepath.Join(req.WorktreeDir, filepath.FromSlash(f.writeFile))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return workerx.DoneEvent{}, err
		}
		if err := os.WriteFile(abs, []byte(f.writeBody), 0o644); err != nil {
			return workerx.DoneEvent{}, err
		}
	}
	return workerx.DoneEvent{
		DispatchID:    req.DispatchID,
		TaskID:        req.TaskID,
		Outcome:       "success",
		Summary:       "external worker concluído",
		FilesModified: f.emitFiles,
	}, nil
}

func initOrchestratorWorkerRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	g := gitx.Local{}
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "hivemind@example.com"},
		{"config", "user.name", "HiveMind"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := g.Run(ctx, dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(dir, "pkg", "api"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "pkg", "api", "service.go"), []byte("package api\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "pkg", "web"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "pkg", "web", "app.ts"), []byte("export {};\n"), 0o644)
	_, _ = g.Run(ctx, dir, "add", ".")
	_, _ = g.Run(ctx, dir, "commit", "-m", "initial")
	return dir
}

func TestExternalWorkerDevEndToEndAndPostHocGuard(t *testing.T) {
	t.Setenv("HIVEMIND_DATA_DIR", t.TempDir())
	repo := initOrchestratorWorkerRepo(t)

	squad := team.Team{
		Name: "ext-squad",
		Agents: []team.Agent{
			{Name: "pm", Role: team.RoleProjectManager, Provider: "scripted", Model: "pm"},
			{Name: "dev_ext", Role: team.RoleDeveloper, Provider: "cursor", Model: "auto"},
			{Name: "qa", Role: team.RoleQA, Provider: team.RoleQA},
		},
	}
	// Keep explicit QA provider/model valid on team validation.
	squad.Agents[2].Provider = "scripted"
	squad.Agents[2].Model = "qa"

	proj := team.File{
		Active:   squad.Name,
		Settings: team.DefaultSettings(),
		Teams:    []team.Team{squad},
	}
	proj.Settings.AutoCommit = true
	proj.Settings.AutoPR = false
	proj.Settings.BaseBranch = "main"
	proj.Normalize()

	extAdapter := &fakeExternalWorkerAdapter{
		provider:  "cursor",
		writeFile: "pkg/api/service.go",
		writeBody: "package api\nfunc Hello() string { return \"cursor\" }\n",
		askPrompt: "Aplicar alteração em service.go?",
		emitFiles: []string{"pkg/api/service.go"},
	}
	reg := workerx.NewAdapterRegistry()
	reg.Register(extAdapter)

	var pmStep int
	var events []Event
	r := &Runner{
		Workspace:          repo,
		Project:            proj,
		Team:               squad,
		Git:                gitx.Local{},
		GitHub:             githubx.Disabled{Reason: "off"},
		Jira:               jirax.Disabled{Reason: "off"},
		ExternalWorkers:    reg,
		EnableTriage:       true,
		EnableClosingChief: false,
		Approve:            func(string) bool { return true },
		OnEvent:            func(ev Event) { events = append(events, ev) },
		Out:                &strings.Builder{},
		Err:                &strings.Builder{},
		Streaming:          agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"pm": &modelx.Scripted{Fn: func(_ *model.LLMRequest, _ int) *model.LLMResponse {
				pmStep++
				if pmStep == 1 {
					return modelx.FunctionCall("delegate_to_squad", "del1", map[string]any{
						"plan":        "Atualizar service.go via worker Cursor",
						"dev_tasks":   []any{"Atualizar pkg/api/service.go"},
						"owned_paths": [][]any{{"pkg/api/**"}},
					})
				}
				return modelx.Text("delegado")
			}},
			"qa": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("qa_approve", "qa1", map[string]any{
						"command":   "go test ./pkg/api",
						"exit_code": 0,
						"evidence":  "ok pkg/api 0.1s",
						"note":      "worker cursor validado",
					})
				}
				return modelx.Text("aprovado")
			}},
		},
	}

	res, err := r.Run(context.Background(), Request{Objective: "atualizar service.go via worker cursor"})
	if err != nil {
		t.Fatalf("Runner.Run failed: %v", err)
	}
	if !extAdapter.askAllowed || extAdapter.seenReq.WorktreeDir == repo {
		t.Fatalf("expected isolated worktree and HITL ask approval, got askAllowed=%v wt=%s repo=%s", extAdapter.askAllowed, extAdapter.seenReq.WorktreeDir, repo)
	}
	if res.PendingQA {
		t.Fatal("expected QA approval and integration commit")
	}
	if _, statErr := os.Stat(extAdapter.seenReq.WorktreeDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected scratch dev worktree %s to be cleaned after delivery commit, stat=%v", extAdapter.seenReq.WorktreeDir, statErr)
	}

	// Verify RunExternalWorkerDev rejects out-of-owned_paths / sensitive writes pós-hoc.
	guardWT, err := r.Git.EnsureWorktree(context.Background(), repo, "dev_ext_guard", "hivemind/dev-ext-guard")
	if err != nil {
		t.Fatalf("EnsureWorktree for guard test failed: %v", err)
	}
	badAdapter := &fakeExternalWorkerAdapter{
		provider:  "cursor",
		writeFile: ".env",
		writeBody: "SECRET=leak\n",
	}
	badEnv := devEnv{
		Agent:      team.Agent{Name: "dev_ext", Role: team.RoleDeveloper, Provider: "cursor"},
		ADK:        "dev_ext",
		Root:       guardWT,
		Branch:     "hivemind/dev-ext-guard",
		OwnedPaths: []string{"pkg/api/**"},
	}
	_, guardRes, badErr := r.RunExternalWorkerDev(context.Background(), badEnv, badAdapter, "test guard")
	if badErr == nil || guardRes.Passed() {
		t.Fatalf("expected RunExternalWorkerDev to reject .env edit pós-hoc, got err=%v guard=%+v", badErr, guardRes)
	}
	if _, statErr := os.Stat(filepath.Join(guardWT, ".env")); !os.IsNotExist(statErr) {
		t.Fatal("expected disallowed .env to be reverted automatically")
	}
}
