package idecore

import (
	"context"
	"path/filepath"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/loopx"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/remote"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// HeadlessBridgeOptions assembles the IDE-side remote.Deps for the headless
// bridge (`hivemind-ide --serve-bridge`): full SessionManager,
// ResidentManager and AutoReviewManager services bound to the runtime Hub.
// The generic runtime (remote.RunHeadless) lives in the root module and is
// shared with the CLI `hivemind serve` host.
func HeadlessBridgeOptions() remote.HeadlessOptions {
	return remote.HeadlessOptions{
		Deps: func(hub *remote.Hub) remote.Deps {
			sessions := NewSessionManager(hub.HandleLaneEvent)
			residentMgr := NewResidentManager(ResidentManagerOptions{
				Sessions: sessions,
			})
			autoReview := NewAutoReviewManager(sessions, hub.HandleAutoReviewStatus)
			return &Adapter{Sessions: sessions, Resident: residentMgr, AutoReview: autoReview, Hub: hub}
		},
		RegisterProject: func(ctx context.Context, hub *remote.Hub, workspace string) {
			registerProjectWithStack(ctx, hub, workspace)
		},
		Banner: func() string {
			return "Serviços: sessões completas, agente residente e auto-review."
		},
		OnExit: modelx.FlushAIUsage,
	}
}

// registerProjectWithStack registers the starting workspace with the IDE's
// stack detector (richer than the generic headless registrar).
func registerProjectWithStack(ctx context.Context, hub *remote.Hub, workspace string) {
	_, _ = loopx.PauseRunning(workspace)
	name := filepath.Base(workspace)
	branch := ""
	g := gitx.Local{}
	if g.IsRepo(ctx, workspace) {
		if b, err := g.CurrentBranch(ctx, workspace); err == nil {
			branch = b
		}
	}
	stack := DetectStack(workspace).Summary
	activeTeam := ""
	if cfg, err := team.Load(workspace); err == nil {
		activeTeam = cfg.Active
	}
	hub.RegisterProject(workspace, name, branch, stack, activeTeam)
}
