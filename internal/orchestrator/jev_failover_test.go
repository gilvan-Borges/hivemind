package orchestrator

import (
	"context"
	"errors"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

type errLLM struct {
	name  string
	calls atomic.Int32
	fn    func(ctx context.Context, req *model.LLMRequest, call int) (*model.LLMResponse, error)
}

func (e *errLLM) Name() string { return e.name }
func (e *errLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		n := int(e.calls.Add(1))
		resp, err := e.fn(ctx, req, n)
		if err != nil {
			yield(nil, err)
			return
		}
		yield(resp, nil)
	}
}

func newTwoProviderTestEnv(t *testing.T, autoEscalateUSD float64) (*jev.CatalogStore, *jev.HealthStore, jev.Policy) {
	t.Helper()
	pol := jev.DefaultPolicy()
	pol.Enabled = true
	pol.AutoEscalateUSD = autoEscalateUSD
	cat := jev.NewCatalogStore(filepath.Join(t.TempDir(), "catalog.json"), nil)
	cat.MergeSource("openrouter", []jev.ModelEntry{
		{
			Provider:           "openrouter",
			Model:              "cheap/a",
			ContextLength:      128_000,
			PromptMicroUSD:     0,
			CompletionMicroUSD: 0,
			LatencyP50Ms:       100,
			Quality:            0.96,
			Free:               true,
			Capabilities:       jev.Capabilities{Tools: true},
			DataClass:          jev.DataClassHTTPAPI,
			Tier:               jev.TierCheap,
		},
		{
			Provider:           "openrouter",
			Model:              "cheap/b",
			ContextLength:      128_000,
			PromptMicroUSD:     0,
			CompletionMicroUSD: 0,
			LatencyP50Ms:       150,
			Quality:            0.90,
			Free:               true,
			Capabilities:       jev.Capabilities{Tools: true},
			DataClass:          jev.DataClassHTTPAPI,
			Tier:               jev.TierCheap,
		},
	})
	cat.MergeSource("opencode", []jev.ModelEntry{
		{
			Provider:           "opencode",
			Model:              "opencode-go:deepseek-v4.1-flash",
			ContextLength:      128_000,
			PromptMicroUSD:     0.12,
			CompletionMicroUSD: 0.25,
			Capabilities:       jev.Capabilities{Tools: true},
			DataClass:          jev.DataClassHTTPAPI,
			Tier:               jev.TierCheap,
		},
	})
	hs := jev.NewHealthStore(filepath.Join(t.TempDir(), "health.json"), pol.Cooldown)
	return cat, hs, pol
}

func TestJevFailover429And402AndTimeout(t *testing.T) {
	cases := []struct {
		name              string
		err               error
		wantSecondModel   string
		wantOpenRouterOff bool // 402 opens whole openrouter provider; 429 opens only cheap/a
	}{
		{
			name:              "429 rate limit opens model only",
			err:               errors.New("HTTP 429 Too Many Requests"),
			wantSecondModel:   "cheap/b",
			wantOpenRouterOff: false,
		},
		{
			name:              "402 quota opens whole openrouter provider",
			err:               errors.New("OpenRouter respondeu 402: créditos insuficientes"),
			wantSecondModel:   "opencode-go:deepseek-v4.1-flash",
			wantOpenRouterOff: true,
		},
		{
			name:              "timeout outage opens model and fails over",
			err:               context.DeadlineExceeded,
			wantSecondModel:   "cheap/b",
			wantOpenRouterOff: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat, hs, pol := newTwoProviderTestEnv(t, 0.02)
			var built []string
			var events []Event

			factory := NewJevModelFactory(JevFactoryOptions{
				Policy:      &pol,
				Catalog:     cat,
				HealthStore: hs,
				Creds:       func() map[string]bool { return map[string]bool{"openrouter": true, "opencode": true} },
				BuildModel: func(_ context.Context, prov, mod string) (model.LLM, error) {
					built = append(built, prov+"/"+mod)
					if mod == "cheap/a" {
						return &errLLM{name: mod, fn: func(context.Context, *model.LLMRequest, int) (*model.LLMResponse, error) {
							return nil, tc.err
						}}, nil
					}
					return &errLLM{name: mod, fn: func(context.Context, *model.LLMRequest, int) (*model.LLMResponse, error) {
						return modelx.Text("ok de " + mod), nil
					}}, nil
				},
				OnEvent: func(ev Event) { events = append(events, ev) },
			})

			llm, err := factory(context.Background(), team.Agent{Name: "dev", Role: team.RoleDeveloper, Provider: "jev", Model: "auto"})
			if err != nil {
				t.Fatalf("factory error: %v", err)
			}
			var gotText string
			for r, gErr := range llm.GenerateContent(context.Background(), &model.LLMRequest{
				Contents: []*genai.Content{genai.NewContentFromText("oi", genai.RoleUser)},
			}, false) {
				if gErr != nil {
					t.Fatalf("expected failover recovery, got error: %v", gErr)
				}
				if r != nil && r.Content != nil && len(r.Content.Parts) > 0 {
					gotText = r.Content.Parts[0].Text
				}
			}
			if gotText != "ok de "+tc.wantSecondModel {
				t.Fatalf("got %q, want %q (built=%v)", gotText, "ok de "+tc.wantSecondModel, built)
			}
			var sawFailover bool
			for _, ev := range events {
				if ev.Kind == "provider_failover" && ev.FromModel == "cheap/a" && ev.Model == tc.wantSecondModel {
					sawFailover = true
				}
			}
			if !sawFailover {
				t.Fatalf("expected provider_failover event to %s, got %+v", tc.wantSecondModel, events)
			}
			snap := hs.Snapshot(nil, nil)
			if tc.wantOpenRouterOff && snap.Circuits["openrouter"].Circuit != jev.CircuitOpen {
				t.Fatalf("expected openrouter provider circuit open on 402, got %+v", snap.Circuits["openrouter"])
			}
		})
	}
}

func TestJevFailoverInvalidAuthKeyAtBuildAndGenerate(t *testing.T) {
	cat, hs, pol := newTwoProviderTestEnv(t, 0.02)
	var events []Event
	factory := NewJevModelFactory(JevFactoryOptions{
		Policy:      &pol,
		Catalog:     cat,
		HealthStore: hs,
		Creds:       func() map[string]bool { return map[string]bool{"openrouter": true, "opencode": true} },
		BuildModel: func(_ context.Context, prov, mod string) (model.LLM, error) {
			if prov == "openrouter" {
				return nil, errors.New("OpenRouter rejeitou a chave (HTTP 401 Unauthorized) sk-invalid999999")
			}
			return &errLLM{name: mod, fn: func(context.Context, *model.LLMRequest, int) (*model.LLMResponse, error) {
				return modelx.Text("recuperado em opencode"), nil
			}}, nil
		},
		OnEvent: func(ev Event) { events = append(events, ev) },
	})

	// Even with explicit team.yaml pin on openrouter, invalid auth key fails over to opencode.
	llm, err := factory(context.Background(), team.Agent{Name: "dev", Role: team.RoleDeveloper, Provider: "openrouter", Model: "cheap/a"})
	if err != nil {
		t.Fatalf("expected failover on 401 at build time, got %v", err)
	}
	if !strings.Contains(llm.Name(), "opencode") {
		t.Fatalf("expected opencode after openrouter 401, got %s", llm.Name())
	}
	for _, ev := range events {
		if strings.Contains(ev.Text, "sk-invalid999999") {
			t.Fatalf("secret leaked in event: %+v", ev)
		}
	}
}

func TestJevIncompatibleModel400FailsOverOnce(t *testing.T) {
	cat, hs, pol := newTwoProviderTestEnv(t, 0.02)
	var calls atomic.Int32
	var events []Event
	const leaked = "openrouter/particle-ai/particle-1:free"
	const secret = "sk-or-v1-secretkeyvalue"
	factory := NewJevModelFactory(JevFactoryOptions{
		Policy:      &pol,
		Catalog:     cat,
		HealthStore: hs,
		Creds:       func() map[string]bool { return map[string]bool{"openrouter": true, "opencode": true} },
		BuildModel: func(_ context.Context, _, mod string) (model.LLM, error) {
			if mod == "cheap/a" {
				return &errLLM{name: mod, fn: func(context.Context, *model.LLMRequest, int) (*model.LLMResponse, error) {
					calls.Add(1)
					return nil, errors.New("cursor/" + leaked + ": modelo incompatível com o provedor Cursor: OpenAI API error: 400 Bad Request: model \"" + leaked + "\" is not a Cursor model " + secret)
				}}, nil
			}
			return &errLLM{name: mod, fn: func(context.Context, *model.LLMRequest, int) (*model.LLMResponse, error) {
				return modelx.Text("ok de " + mod), nil
			}}, nil
		},
		OnEvent: func(ev Event) { events = append(events, ev) },
	})
	llm, err := factory(context.Background(), team.Agent{Name: "dev", Role: team.RoleDeveloper, Provider: "jev", Model: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	var gotText string
	var gotErr error
	for r, gErr := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("oi", genai.RoleUser)},
	}, false) {
		if gErr != nil {
			gotErr = gErr
		}
		if r != nil && r.Content != nil && len(r.Content.Parts) > 0 {
			gotText = r.Content.Parts[0].Text
		}
	}
	if gotErr != nil {
		t.Fatalf("400 de modelo incompatível deveria ir ao próximo candidato, veio %v", gotErr)
	}
	if gotText != "ok de cheap/b" {
		t.Fatalf("texto = %q, built events=%+v", gotText, events)
	}
	if calls.Load() != 1 {
		t.Fatalf("o modelo incompatível foi chamado %d vezes; a mesma requisição não pode repetir", calls.Load())
	}
	snap := hs.Snapshot(nil, nil)
	if snap.Circuits["openrouter"].Circuit == jev.CircuitOpen {
		t.Fatal("400 de um modelo não pode abrir o circuito do provedor inteiro")
	}
	if snap.ModelCircuits[jevKey("openrouter", "cheap/a")].Circuit != jev.CircuitOpen {
		t.Fatalf("circuito do modelo: %+v", snap.ModelCircuits)
	}
	for _, ev := range events {
		if strings.Contains(ev.Text, secret) {
			t.Fatalf("evento vazou a chave: %+v", ev)
		}
	}
}

func jevKey(provider, model string) string {
	return strings.ToLower(provider) + "\x00" + model
}

func TestJev400PolicyDoesNotFailover(t *testing.T) {
	cat, hs, pol := newTwoProviderTestEnv(t, 0.02)
	buildCalls := 0
	factory := NewJevModelFactory(JevFactoryOptions{
		Policy:      &pol,
		Catalog:     cat,
		HealthStore: hs,
		Creds:       func() map[string]bool { return map[string]bool{"openrouter": true, "opencode": true} },
		BuildModel: func(_ context.Context, _, mod string) (model.LLM, error) {
			buildCalls++
			return &errLLM{name: mod, fn: func(context.Context, *model.LLMRequest, int) (*model.LLMResponse, error) {
				return nil, errors.New("HTTP 400 Bad Request: invalid_argument")
			}}, nil
		},
	})
	llm, err := factory(context.Background(), team.Agent{Name: "dev", Role: team.RoleDeveloper, Provider: "jev", Model: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	var gotErr error
	for _, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("oi", genai.RoleUser)},
	}, false) {
		if err != nil {
			gotErr = err
		}
	}
	if gotErr == nil {
		t.Fatal("expected 400 error to propagate")
	}
	if buildCalls != 1 {
		t.Fatalf("400 must NOT trigger failover, got %d build calls", buildCalls)
	}
	if hs.Snapshot(nil, nil).Circuits["openrouter"].Circuit == jev.CircuitOpen {
		t.Fatal("400 must NOT open circuit")
	}
}

func TestJevFailoverIdempotencyToolRunsOnce(t *testing.T) {
	runScenario := func(t *testing.T, failOnSecondCall bool) {
		t.Helper()
		cat, hs, pol := newTwoProviderTestEnv(t, 0.02)
		var toolExecutions atomic.Int32
		writeTool, err := functiontool.New(functiontool.Config{
			Name:        "write_once",
			Description: "writes once",
		}, func(_ agent.Context, args struct {
			Val string `json:"val"`
		}) (map[string]any, error) {
			toolExecutions.Add(1)
			return map[string]any{"ok": true, "val": args.Val}, nil
		})
		if err != nil {
			t.Fatal(err)
		}

		factory := NewJevModelFactory(JevFactoryOptions{
			Policy:      &pol,
			Catalog:     cat,
			HealthStore: hs,
			Creds:       func() map[string]bool { return map[string]bool{"openrouter": true, "opencode": true} },
			BuildModel: func(_ context.Context, _, mod string) (model.LLM, error) {
				if mod == "cheap/a" {
					return &errLLM{name: mod, fn: func(_ context.Context, req *model.LLMRequest, call int) (*model.LLMResponse, error) {
						if !failOnSecondCall {
							return nil, errors.New("HTTP 429 Too Many Requests")
						}
						if call == 1 {
							return modelx.FunctionCall("write_once", "call_1", map[string]any{"val": "abc"}), nil
						}
						return nil, errors.New("HTTP 429 Too Many Requests")
					}}, nil
				}
				return &errLLM{name: mod, fn: func(_ context.Context, req *model.LLMRequest, call int) (*model.LLMResponse, error) {
					if modelx.LastIsFunctionResponse(req) {
						return modelx.Text("ferramenta já concluída"), nil
					}
					if call == 1 {
						return modelx.FunctionCall("write_once", "call_2", map[string]any{"val": "abc"}), nil
					}
					return modelx.Text("concluído"), nil
				}}, nil
			},
		})

		llm, err := factory(context.Background(), team.Agent{Name: "dev", Role: team.RoleDeveloper, Provider: "jev", Model: "auto"})
		if err != nil {
			t.Fatal(err)
		}
		ag, err := llmagent.New(llmagent.Config{
			Name:  "dev",
			Model: llm,
			Tools: []tool.Tool{writeTool},
		})
		if err != nil {
			t.Fatal(err)
		}
		store, err := sessionx.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		sess, err := store.Create(context.Background(), &session.CreateRequest{AppName: sessionx.AppName, UserID: sessionx.UserID})
		if err != nil {
			t.Fatal(err)
		}
		r, err := runner.New(runner.Config{AppName: sessionx.AppName, Agent: ag, SessionService: store})
		if err != nil {
			t.Fatal(err)
		}
		for _, rErr := range r.Run(context.Background(), sessionx.UserID, sess.Session.ID(), genai.NewContentFromText("execute", genai.RoleUser), agent.RunConfig{}) {
			if rErr != nil {
				t.Fatalf("runner failed: %v", rErr)
			}
		}
		if got := toolExecutions.Load(); got != 1 {
			t.Fatalf("tool must execute exactly ONCE, executed %d times", got)
		}
	}

	t.Run("fail before tool call", func(t *testing.T) { runScenario(t, false) })
	t.Run("fail after tool call on next turn with FunctionResponse in session", func(t *testing.T) { runScenario(t, true) })
}

func TestJevFailoverBudgetGateBlocksExpensiveWithoutApproval(t *testing.T) {
	pol := jev.DefaultPolicy()
	pol.Enabled = true
	pol.AutoEscalateUSD = 0.02
	cat := jev.NewCatalogStore(filepath.Join(t.TempDir(), "catalog.json"), nil)
	cat.MergeSource("openrouter", []jev.ModelEntry{
		{
			Provider:           "openrouter",
			Model:              "cheap/a",
			ContextLength:      128_000,
			PromptMicroUSD:     0.05,
			CompletionMicroUSD: 0.10,
			Capabilities:       jev.Capabilities{Tools: true},
			DataClass:          jev.DataClassHTTPAPI,
			Tier:               jev.TierCheap,
		},
		{
			Provider:           "openrouter",
			Model:              "expensive/strong",
			ContextLength:      200_000,
			PromptMicroUSD:     10.0,
			CompletionMicroUSD: 50.0,
			Capabilities:       jev.Capabilities{Tools: true},
			DataClass:          jev.DataClassHTTPAPI,
			Tier:               jev.TierCheap,
		},
	})
	hs := jev.NewHealthStore(filepath.Join(t.TempDir(), "health.json"), pol.Cooldown)

	var expensiveCalls atomic.Int32
	var events []Event
	approved := false
	factory := NewJevModelFactory(JevFactoryOptions{
		Policy:      &pol,
		Catalog:     cat,
		HealthStore: hs,
		Creds:       func() map[string]bool { return map[string]bool{"openrouter": true} },
		ApproveGate: func(gate, _ string) bool {
			if gate != "budget" {
				t.Fatalf("expected budget gate, got %q", gate)
			}
			return approved
		},
		BuildModel: func(_ context.Context, _, mod string) (model.LLM, error) {
			if mod == "cheap/a" {
				return &errLLM{name: mod, fn: func(context.Context, *model.LLMRequest, int) (*model.LLMResponse, error) {
					return nil, errors.New("HTTP 429 Too Many Requests")
				}}, nil
			}
			return &errLLM{name: mod, fn: func(context.Context, *model.LLMRequest, int) (*model.LLMResponse, error) {
				expensiveCalls.Add(1)
				return modelx.Text("ok caro"), nil
			}}, nil
		},
		OnEvent: func(ev Event) { events = append(events, ev) },
	})

	ctx := withJevRouteHint(context.Background(), jevRouteHint{Step: "dev_report", InputTokens: 50_000, NeedsTools: true})
	llm, err := factory(ctx, team.Agent{Name: "dev", Role: team.RoleDeveloper, Provider: "jev", Model: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	var gotErr error
	for _, gErr := range llm.GenerateContent(ctx, &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("oi", genai.RoleUser)},
	}, false) {
		if gErr != nil {
			gotErr = gErr
		}
	}
	if gotErr == nil {
		t.Fatal("expected error when budget escalation is denied")
	}
	if expensiveCalls.Load() != 0 {
		t.Fatalf("expensive model must have 0 calls without approval, got %d", expensiveCalls.Load())
	}
	var sawExhausted bool
	for _, ev := range events {
		if ev.Kind == "provider_exhausted" {
			sawExhausted = true
		}
	}
	if !sawExhausted {
		t.Fatalf("expected provider_exhausted event, got %+v", events)
	}
}

func TestJevCursorDirtyWorktreeRequiresApproval(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não instalado")
	}
	repo := t.TempDir()
	g := gitx.Local{}
	ctx := context.Background()
	mustGit(t, g, ctx, repo, "init")
	mustGit(t, g, ctx, repo, "config", "user.email", "jev@example.com")
	mustGit(t, g, ctx, repo, "config", "user.name", "Jev Test")
	mustGit(t, g, ctx, repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, g, ctx, repo, "add", "README.md")
	mustGit(t, g, ctx, repo, "commit", "-m", "init")

	pol := jev.DefaultPolicy()
	pol.Enabled = true
	cat := jev.NewCatalogStore(filepath.Join(t.TempDir(), "catalog.json"), nil)
	hs := jev.NewHealthStore(filepath.Join(t.TempDir(), "health.json"), pol.Cooldown)

	var secondModelBuilt bool
	var sawWorktreeGate bool
	factory := NewJevModelFactory(JevFactoryOptions{
		Workspace:   repo,
		Git:         g,
		Policy:      &pol,
		Catalog:     cat,
		HealthStore: hs,
		Creds:       func() map[string]bool { return map[string]bool{"cursor": true, "gemini": true} },
		ApproveGate: func(gate, _ string) bool {
			if gate == "worktree" {
				sawWorktreeGate = true
			}
			return false
		},
		BuildModel: func(_ context.Context, prov, mod string) (model.LLM, error) {
			if prov == "cursor" {
				return &errLLM{name: "cursor:auto", fn: func(context.Context, *model.LLMRequest, int) (*model.LLMResponse, error) {
					// Simulate Cursor CLI editing a file and then failing.
					_ = os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("partial"), 0o644)
					return nil, errors.New("o Cursor CLI falhou: exit 1")
				}}, nil
			}
			secondModelBuilt = true
			return &modelx.Scripted{}, nil
		},
	})

	rCtx := modelx.WithAgentDir(ctx, repo)
	llm, err := factory(rCtx, team.Agent{Name: "dev", Role: team.RoleDeveloper, Provider: "cursor", Model: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	var gotErr error
	for _, gErr := range llm.GenerateContent(rCtx, &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("edite", genai.RoleUser)},
	}, false) {
		if gErr != nil {
			gotErr = gErr
		}
	}
	if gotErr == nil {
		t.Fatal("expected error when dirty worktree failover is denied")
	}
	if !sawWorktreeGate {
		t.Fatal("expected worktree approval gate when Cursor dirtied worktree before failing")
	}
	if secondModelBuilt {
		t.Fatal("second writer must NOT be built without worktree approval")
	}
}

func BenchmarkJevWrapperPassThrough(b *testing.B) {
	cat := jev.NewCatalogStore(filepath.Join(b.TempDir(), "catalog.json"), nil)
	pol := jev.DefaultPolicy()
	pol.Enabled = true
	hs := jev.NewHealthStore(filepath.Join(b.TempDir(), "health.json"), pol.Cooldown)
	w := &jevModelWrapper{
		current: &modelx.Scripted{},
		choice:  jev.Choice{Provider: "gemini", Model: "gemini-flash-latest", Tier: jev.TierCheap},
		policy:  pol,
		catalog: cat,
		health:  hs,
	}
	req := &model.LLMRequest{Contents: []*genai.Content{genai.NewContentFromText("oi", genai.RoleUser)}}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, err := range w.GenerateContent(ctx, req, false) {
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}
