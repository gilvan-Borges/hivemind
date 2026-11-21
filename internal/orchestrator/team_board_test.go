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

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

func TestDisjointOwnedPathsAndMatcher(t *testing.T) {
	if !DisjointOwnedPaths([][]string{{"internal/relay/"}, {"ide/frontend/src/"}}, 2) {
		t.Fatal("expected backend vs frontend paths to be disjoint")
	}
	if DisjointOwnedPaths([][]string{{"internal/"}, {"internal/relay/"}}, 2) {
		t.Fatal("expected parent/child prefixes to overlap")
	}
	if DisjointOwnedPaths([][]string{{"internal/relay/"}}, 2) {
		t.Fatal("expected missing second dev slice to return false")
	}
	if !MatchesOwnedPaths("./internal/relay/server.go", []string{"internal/relay"}) {
		t.Fatal("expected nested file to match directory prefix")
	}
	if MatchesOwnedPaths("ide/app.go", []string{"internal/relay"}) {
		t.Fatal("unexpected match outside prefix")
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não instalado")
	}
	repo := t.TempDir()
	g := gitx.Local{}
	ctx := context.Background()
	mustGit(t, g, ctx, repo, "init")
	mustGit(t, g, ctx, repo, "config", "user.email", "hivemind@example.com")
	mustGit(t, g, ctx, repo, "config", "user.name", "HiveMind Test")
	mustGit(t, g, ctx, repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, g, ctx, repo, "add", ".")
	mustGit(t, g, ctx, repo, "commit", "-m", "initial")
	return repo
}

func TestSingleDevSharesConversationWorktreePerLane(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()
	store, err := sessionx.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	squad := team.Team{
		Name: "core",
		Agents: []team.Agent{
			{Name: "chefe", Role: team.RoleProjectManager, Provider: "mock", Model: "m"},
			{Name: "dev", Role: team.RoleDeveloper, Provider: "mock", Model: "m"},
			{Name: "qa", Role: team.RoleQA, Provider: "mock", Model: "m"},
		},
	}
	proj := team.File{Teams: []team.Team{squad}}
	proj.Normalize()

	makeModels := func(fileName string) map[string]model.LLM {
		return map[string]model.LLM{
			"chefe": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("delegate_to_squad", "d1", map[string]any{
						"plan":      "Criar " + fileName,
						"dev_tasks": []any{"Criar " + fileName},
					})
				}
				return modelx.Text("ok")
			}},
			"dev": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("write_file", "w1", map[string]any{"path": fileName, "content": "ok\n"})
				}
				return modelx.Text("feito")
			}},
			"qa": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("qa_approve", "q1", map[string]any{"note": "ok"})
				}
				return modelx.Text("aprovado")
			}},
		}
	}

	r1 := &Runner{
		Workspace: repo, LaneID: "aba-1", Project: proj, Team: squad, Git: gitx.Local{},
		Sessions: store, EnableTriage: true, Streaming: agent.StreamingModeNone,
		Models: makeModels("lane1.txt"),
	}
	res1, err := r1.Run(ctx, Request{Objective: "tarefa lane 1"})
	if err != nil {
		t.Fatal(err)
	}

	r2 := &Runner{
		Workspace: repo, LaneID: "aba-2", Project: proj, Team: squad, Git: gitx.Local{},
		Sessions: store, EnableTriage: true, Streaming: agent.StreamingModeNone,
		Models: makeModels("lane2.txt"),
	}
	res2, err := r2.Run(ctx, Request{Objective: "tarefa lane 2"})
	if err != nil {
		t.Fatal(err)
	}

	if res1.WorktreeDir == "" || res2.WorktreeDir == "" || res1.WorktreeDir == res2.WorktreeDir {
		t.Fatalf("expected distinct conversation worktrees, got %q vs %q", res1.WorktreeDir, res2.WorktreeDir)
	}
	if _, err := os.Stat(workspace.WorktreePath(repo, "dev")); !os.IsNotExist(err) {
		t.Fatalf("single dev must share conversation worktree instead of creating dev clone; stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(res1.WorktreeDir, "lane1.txt")); err != nil {
		t.Fatalf("missing lane1.txt in %s: %v", res1.WorktreeDir, err)
	}
	if _, err := os.Stat(filepath.Join(res2.WorktreeDir, "lane2.txt")); err != nil {
		t.Fatalf("missing lane2.txt in %s: %v", res2.WorktreeDir, err)
	}
}

func TestParallelDevsShareWorktreeWithOwnedPathsAndTeamBoard(t *testing.T) {
	repo := initTestRepo(t)
	ctx := context.Background()
	store, err := sessionx.Open(repo)
	if err != nil {
		t.Fatal(err)
	}
	squad := team.Team{
		Name: "core",
		Agents: []team.Agent{
			{Name: "chefe", Role: team.RoleProjectManager, Provider: "mock", Model: "m"},
			{Name: "dev", Role: team.RoleDeveloper, Provider: "mock", Model: "m"},
			{Name: "qa", Role: team.RoleQA, Provider: "mock", Model: "m"},
		},
	}
	proj := team.File{Teams: []team.Team{squad}}
	proj.Normalize()

	var mu sync.Mutex
	var qaSawBoard bool
	var blockedResp string

	r := &Runner{
		Workspace: repo, LaneID: "unificada", Project: proj, Team: squad, Git: gitx.Local{},
		Sessions: store, EnableTriage: true, Streaming: agent.StreamingModeNone,
		Models: map[string]model.LLM{
			"chefe": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("delegate_to_squad", "d1", map[string]any{
						"plan": "Backend + Frontend em serviços separados",
						"dev_tasks": []any{
							"Criar backend/api.go",
							"Criar frontend/ui.ts",
						},
						"owned_paths": []any{
							[]any{"backend/"},
							[]any{"frontend/"},
						},
					})
				}
				return modelx.Text("delegado")
			}},
			"dev_1": &modelx.Scripted{Fn: func(req *model.LLMRequest, call int) *model.LLMResponse {
				switch call {
				case 1:
					// Tries to touch frontend/ outside owned_paths -> must be blocked deterministically.
					return modelx.FunctionCall("write_file", "b1", map[string]any{"path": "frontend/illegal.ts", "content": "nope"})
				case 2:
					// Capture the guard error text from the previous tool response.
					for _, c := range req.Contents {
						for _, p := range c.Parts {
							if p != nil && p.FunctionResponse != nil {
								mu.Lock()
								blockedResp = p.FunctionResponse.Response["error"].(string)
								mu.Unlock()
							}
						}
					}
					return modelx.FunctionCall("write_file", "b2", map[string]any{"path": "backend/api.go", "content": "package backend\n"})
				case 3:
					return modelx.FunctionCall("share_team_note", "n1", map[string]any{
						"topic":   "api",
						"content": "GET /health -> 200 OK",
					})
				default:
					return modelx.Text("backend pronto")
				}
			}},
			"dev_2": &modelx.Scripted{Fn: func(_ *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 {
					return modelx.FunctionCall("write_file", "f1", map[string]any{"path": "frontend/ui.ts", "content": "export const ok = true;\n"})
				}
				return modelx.Text("frontend pronto")
			}},
			"qa": &modelx.Scripted{Fn: func(req *model.LLMRequest, call int) *model.LLMResponse {
				if call == 1 && req != nil {
					if req.Config != nil && req.Config.SystemInstruction != nil {
						for _, p := range req.Config.SystemInstruction.Parts {
							if p != nil && strings.Contains(p.Text, "Memória Compartilhada do Time") {
								t.Errorf("TeamBoard dynamic summary must ride trailing <system-reminder>, not SystemInstruction")
							}
						}
					}
					for _, c := range req.Contents {
						if c == nil {
							continue
						}
						for _, p := range c.Parts {
							if p != nil && strings.Contains(p.Text, "<system-reminder>") &&
								strings.Contains(p.Text, "Memória Compartilhada do Time") &&
								strings.Contains(p.Text, "GET /health -> 200 OK") {
								mu.Lock()
								qaSawBoard = true
								mu.Unlock()
							}
						}
					}
				}
				if call == 1 {
					return modelx.FunctionCall("qa_approve", "q1", map[string]any{"note": "ok"})
				}
				return modelx.Text("aprovado")
			}},
		},
	}

	res, err := r.Run(ctx, Request{Objective: "api + ui compartilhado"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(blockedResp, "posse de arquivo bloqueada") {
		t.Fatalf("expected deterministic owned_paths block error, got %q", blockedResp)
	}
	if !qaSawBoard {
		t.Fatal("expected QA instruction to receive shared TeamBoard summary and published note")
	}
	if _, err := os.Stat(filepath.Join(res.WorktreeDir, "frontend", "illegal.ts")); !os.IsNotExist(err) {
		t.Fatal("blocked cross-scope file must not exist on disk")
	}
	if _, err := os.Stat(filepath.Join(res.WorktreeDir, "backend", "api.go")); err != nil {
		t.Fatalf("expected backend/api.go in shared conversation worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.WorktreeDir, "frontend", "ui.ts")); err != nil {
		t.Fatalf("expected frontend/ui.ts in shared conversation worktree: %v", err)
	}
	for _, devSlot := range []string{"dev_1", "dev_2"} {
		if _, err := os.Stat(workspace.WorktreePath(repo, devSlot)); !os.IsNotExist(err) {
			t.Fatalf("did not expect extra worktree %s when devs share conversation worktree", devSlot)
		}
	}
}
