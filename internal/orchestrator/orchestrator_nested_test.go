package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func initNestedOrchestratorRepo(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	g := gitx.Local{}
	ctx := context.Background()
	if _, err := g.Run(ctx, dir, "init", "-b", "main"); err != nil {
		t.Fatalf("git init %s: %v", dir, err)
	}
	_, _ = g.Run(ctx, dir, "config", "user.email", "test@hivemind.local")
	_, _ = g.Run(ctx, dir, "config", "user.name", "HiveMind Test")
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := g.Run(ctx, dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(ctx, dir, "commit", "-m", "init"); err != nil {
		t.Fatal(err)
	}
}

func TestCompositeWorktreeMountsNestedRepoForDevAndCommitsSeparately(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	root := t.TempDir()
	initNestedOrchestratorRepo(t, root, map[string]string{
		".gitignore":     "app-mobile/\n",
		"api/Order.java": "class Order {}\n",
	})
	mobileDir := filepath.Join(root, "app-mobile")
	initNestedOrchestratorRepo(t, mobileDir, map[string]string{
		"lib/order_execution_page.dart": "class ExecutionPage {}\n",
	})

	squad := team.FullSquadTeam("gov", "", "scripted", "gemini", 1)
	project := team.File{
		Active:   squad.Name,
		Settings: team.DefaultSettings(),
		Teams:    []team.Team{squad},
	}
	project.Settings.AutoCommit = true
	project.Settings.AutoPR = false
	project.Settings.BaseBranch = "main"
	project.Normalize()

	var devSawMobile bool
	r := &Runner{
		Workspace:          root,
		Project:            project,
		Team:               squad,
		Git:                gitx.Local{},
		GitHub:             githubx.Disabled{Reason: "off"},
		Jira:               jirax.Disabled{Reason: "off"},
		EnableTriage:       false,
		EnableClosingChief: false,
		Out:                &strings.Builder{},
		Err:                &strings.Builder{},
		Streaming:          agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"pm":        &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse { return modelx.Text("plano pm") }},
			"tech_lead": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse { return modelx.Text("plano tech") }},
			"architect": &modelx.Scripted{Fn: func(*model.LLMRequest, int) *model.LLMResponse { return modelx.Text("plano arq") }},
			"dev_1": &modelx.Scripted{Fn: func(req *model.LLMRequest, call int) *model.LLMResponse {
				switch call {
				case 1:
					return modelx.FunctionCall("read_file", "rf1", map[string]any{"path": "app-mobile/lib/order_execution_page.dart"})
				case 2:
					for _, c := range req.Contents {
						for _, p := range c.Parts {
							if p.FunctionResponse != nil && strings.Contains(strings.TrimSpace(p.FunctionResponse.Name), "read_file") && strings.Contains(fmt.Sprint(p.FunctionResponse.Response), "ExecutionPage") {
								devSawMobile = true
							}
						}
					}
					return modelx.FunctionCall("write_file", "wf1", map[string]any{
						"path":    "app-mobile/lib/order_execution_page.dart",
						"content": "class ExecutionPage { final bool fixed = true; }\n",
					})
				default:
					return modelx.Text("app-mobile atualizado")
				}
			}},
			"qa": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("qa_approve", "qa1", map[string]any{
						"command":   "flutter test",
						"exit_code": 0,
						"evidence":  "All tests passed!",
						"note":      "app-mobile validado",
					})
				}
				return modelx.Text("aprovado")
			}},
		},
	}

	res, err := r.Run(context.Background(), Request{Objective: "atualizar order_execution_page.dart em app-mobile"})
	if err != nil {
		t.Fatalf("Runner.Run failed: %v", err)
	}
	if !devSawMobile {
		t.Fatal("expected dev inside conversation worktree to read app-mobile/lib/order_execution_page.dart")
	}
	if len(res.NestedDeliveries) != 1 || res.NestedDeliveries[0].RelPath != "app-mobile" {
		t.Fatalf("NestedDeliveries unexpected: %+v", res.NestedDeliveries)
	}
	if res.PendingQA {
		t.Fatalf("expected QA to approve delivery, got PendingQA=true (summary=%q)", res.Summary)
	}
	g := gitx.Local{}
	wtSt, _ := g.Run(context.Background(), res.NestedDeliveries[0].WorktreeDir, "status")
	wtFile, _ := os.ReadFile(filepath.Join(res.NestedDeliveries[0].WorktreeDir, "lib", "order_execution_page.dart"))
	if !gitx.HasCommitsAhead(context.Background(), g, mobileDir, res.NestedDeliveries[0].Branch, "main") {
		branches, _ := g.Run(context.Background(), mobileDir, "branch", "-a")
		t.Fatalf("expected commit inside nested app-mobile branch %q (wtDir=%s wtSt=%q wtFile=%q mobileBranches=%q)", res.NestedDeliveries[0].Branch, res.NestedDeliveries[0].WorktreeDir, wtSt, string(wtFile), branches)
	}
	if gitx.HasCommitsAhead(context.Background(), g, root, res.DeliveryBranch, "main") {
		t.Fatalf("root branch %q should remain clean without app-mobile files", res.DeliveryBranch)
	}
	// User's original nested repo working tree must remain untouched.
	origBytes, err := os.ReadFile(filepath.Join(mobileDir, "lib", "order_execution_page.dart"))
	if err != nil || strings.Contains(string(origBytes), "fixed") {
		t.Fatalf("original app-mobile checkout should stay clean, got %q (%v)", string(origBytes), err)
	}
}
