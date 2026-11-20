package remote

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// RunHeadless starts the Remote Bridge without the IDE window — the backend
// of `hivemind serve`. It blocks until ctx is canceled (Ctrl+C).
func RunHeadless(ctx context.Context, workspace string) error {
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
	uri := BuildPairURI(st.URLs, cfg.Token, st.Host)
	if qr, err := QRASCII(uri); err == nil {
		fmt.Println(qr)
	}
	fmt.Printf("Token (modo manual): %s\n\n", cfg.Token)
	fmt.Println("Escaneie o QR no App Mobile (ou cole as URLs + token).")
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
