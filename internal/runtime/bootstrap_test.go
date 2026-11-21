package runtime

import (
	"context"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/mcpx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestMCPPoolAcquireAndRelease(t *testing.T) {
	p := NewMCPPool()
	starts := 0
	p.startFn = func(_ context.Context, _ string) *mcpx.Host {
		starts++
		return &mcpx.Host{}
	}

	ctx := context.Background()
	ws := t.TempDir()
	h1 := p.Acquire(ctx, ws)
	h2 := p.Acquire(ctx, ws)
	if h1 == nil || h1 != h2 || starts != 1 {
		t.Fatalf("expected pooled reuse on same workspace, starts=%d h1=%p h2=%p", starts, h1, h2)
	}

	p.ReleaseWorkspace(ws)
	h3 := p.Acquire(ctx, ws)
	if starts != 2 || h3 == h1 {
		t.Fatalf("expected fresh host after ReleaseWorkspace, starts=%d", starts)
	}
	p.CloseAll()
}

func TestBuildRunnerWiresUnifiedDefaults(t *testing.T) {
	t.Setenv("HIVEMIND_DATA_DIR", t.TempDir())
	ws := t.TempDir()
	r, cleanup := BuildRunner(context.Background(), RunnerOptions{
		Workspace:      ws,
		DisableMCP:     true,
		SkipSeedSkills: true,
		Project:        team.File{Version: 1},
		Team: team.Team{
			Name: "unified",
			Agents: []team.Agent{
				{Name: "chefe", Role: team.RoleArchitect, Model: "gemini-flash-latest"},
				{Name: "dev", Role: team.RoleDeveloper, Model: "gemini-flash-latest"},
				{Name: "qa", Role: team.RoleQA, Model: "gemini-flash-latest"},
			},
		},
		EnableTriage: true,
	})
	defer cleanup()

	if r == nil || r.NewModel == nil || r.Git == nil || r.Workspace != ws {
		t.Fatalf("unexpected BuildRunner result: %#v", r)
	}
}
