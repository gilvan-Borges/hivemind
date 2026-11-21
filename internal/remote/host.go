package remote

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

const headlessProbeEvery = 60 * time.Second

// headlessProbeTicks, when set, replaces the 60s ticker (tests pass a fake clock).
var headlessProbeTicks func() <-chan time.Time

// headlessProbeFn is the same recovery probe the IDE runs. Tests may replace it.
var headlessProbeFn = func(ctx context.Context, hub *Hub) {
	if hub == nil {
		return
	}
	views := modelx.ProbeAllProvidersHealth(ctx, modelx.DefaultJevHealthStore(), jev.DefaultPolicy())
	hub.HandleProviderHealth(views, false)
}

func startHeadlessHealthProbe(ctx context.Context, hub *Hub) {
	var ticks <-chan time.Time
	stop := func() {}
	if headlessProbeTicks != nil {
		ticks = headlessProbeTicks()
	} else {
		t := time.NewTicker(headlessProbeEvery)
		ticks = t.C
		stop = t.Stop
	}
	go func() {
		defer stop()
		last := time.Now()
		headlessProbeFn(ctx, hub)
		for {
			select {
			case <-ctx.Done():
				return
			case now, ok := <-ticks:
				if !ok {
					return
				}
				if now.IsZero() {
					now = time.Now()
				}
				if now.Sub(last) < headlessProbeEvery {
					continue
				}
				last = now
				headlessProbeFn(ctx, hub)
			}
		}
	}()
}

// HeadlessOptions carries the host-specific pieces of a headless bridge run.
// The generic runtime lives here (root module); the IDE fills these with its
// SessionManager/ResidentManager/AutoReviewManager services, and the CLI
// serve command fills them with its own remote.Deps implementation.
type HeadlessOptions struct {
	// Deps builds the HTTP API services (runs, approvals, listings) bound to
	// the freshly created Hub — the hub is owned by the runtime because every
	// Deps implementation emits lane events into it.
	Deps func(hub *Hub) Deps
	// RegisterProject enriches GET /api/v1/projects for the starting workspace
	// (branch, stack, active team). Nil uses the generic registrar.
	RegisterProject func(ctx context.Context, hub *Hub, workspace string)
	// Banner prints host-specific status lines at boot (may be nil).
	Banner func() string
	// OnExit runs once when the bridge shuts down (e.g. modelx.FlushAIUsage).
	OnExit func()
}

// RunHeadless starts the Remote Bridge without the IDE window — the shared
// backend of `hivemind serve` (CLI) and `hivemind-ide --serve-bridge`. It
// blocks until ctx is canceled (Ctrl+C).
func RunHeadless(ctx context.Context, workspace string, opts HeadlessOptions) error {
	if opts.OnExit != nil {
		defer opts.OnExit()
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.Enabled = true

	hub := NewHub()
	adapter := opts.Deps(hub)

	if ws := workspace; ws != "" {
		if abs, err := filepath.Abs(ws); err == nil {
			if opts.RegisterProject != nil {
				opts.RegisterProject(ctx, hub, abs)
			} else {
				registerProjectHeadless(ctx, hub, abs, "")
			}
		}
	}

	srv := NewServer(hub, adapter, cfg)
	if err := srv.Start(); err != nil {
		return err
	}
	defer func() { _ = srv.Stop(context.Background()) }()
	startHeadlessHealthProbe(ctx, hub)

	st := srv.Status()
	fmt.Printf("\nHiveMind Remote Bridge (headless) · v%s\n", st.Version)
	fmt.Printf("Host: %s\n", st.Host)
	if opts.Banner != nil {
		fmt.Println(opts.Banner())
	}
	fmt.Println()
	if len(st.URLs) == 0 {
		fmt.Println("Aviso: nenhum IP Tailscale/LAN detectado — pareie via rede ativa.")
	} else {
		fmt.Println("URLs disponíveis:")
		for _, u := range st.URLs {
			fmt.Printf("  • %s\n", u)
		}
	}
	info, err := srv.Pairing()
	if err != nil {
		return err
	}
	if qr, err := QRASCII(info.URI); err == nil {
		fmt.Println(qr)
	}
	fmt.Printf("Oferta: %s\n", info.URI)
	fmt.Printf("Válida até %s (uso único).\n\n", time.Unix(info.Exp, 0).UTC().Format(time.RFC3339))
	fmt.Println("Escaneie o QR para parear.")
	fmt.Println("Pressione Ctrl+C para encerrar.")
	fmt.Println()

	<-ctx.Done()
	return nil
}

// registerProjectHeadless records workspace metadata (name, branch, active
// team) using only root-module dependencies. Hosts with a richer stack
// detector (the IDE) pass their own RegisterProject callback instead.
func registerProjectHeadless(ctx context.Context, hub *Hub, workspace string, stack string) {
	name := filepath.Base(workspace)
	branch := ""
	g := gitx.Local{}
	if g.IsRepo(ctx, workspace) {
		if b, err := g.CurrentBranch(ctx, workspace); err == nil {
			branch = b
		}
	}
	activeTeam := ""
	if cfg, err := team.Load(workspace); err == nil {
		activeTeam = cfg.Active
	}
	hub.RegisterProject(workspace, name, branch, stack, activeTeam)
}
