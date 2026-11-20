package remote

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
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

// RunHeadless starts the Remote Bridge without the IDE window — the backend
// of `hivemind serve`. It blocks until ctx is canceled (Ctrl+C).
func RunHeadless(ctx context.Context, workspace string) error {
	defer modelx.FlushAIUsage()
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.Enabled = true

	hub := NewHub()
	sessions := idecore.NewSessionManager(hub.HandleLaneEvent)
	residentMgr := idecore.NewResidentManager(idecore.ResidentManagerOptions{
		Sessions: sessions,
	})
	autoReview := idecore.NewAutoReviewManager(sessions, hub.HandleAutoReviewStatus)
	adapter := &Adapter{Sessions: sessions, Resident: residentMgr, AutoReview: autoReview, Hub: hub}

	if ws := workspace; ws != "" {
		if abs, err := filepath.Abs(ws); err == nil {
			registerProjectHeadless(ctx, hub, abs)
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
	fmt.Println(residentMgr.FormatHeadlessStatus())
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

func registerProjectHeadless(ctx context.Context, hub *Hub, workspace string) {
	name := filepath.Base(workspace)
	branch := ""
	g := gitx.Local{}
	if g.IsRepo(ctx, workspace) {
		if b, err := g.CurrentBranch(ctx, workspace); err == nil {
			branch = b
		}
	}
	stack := idecore.DetectStack(workspace).Summary
	activeTeam := ""
	if cfg, err := team.Load(workspace); err == nil {
		activeTeam = cfg.Active
	}
	hub.RegisterProject(workspace, name, branch, stack, activeTeam)
}
