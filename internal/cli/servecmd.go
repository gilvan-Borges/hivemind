package cli

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/gilvan-Borges/HiveMind/internal/browserx"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/remote"
)

// newServeCmd builds the `hivemind serve` subcommand (Marco 3 da
// SPEC_APP_MOBILE_NATIVO.md). The Remote Bridge now runs IN PROCESS on the
// root-module runtime (internal/remote): no more locating or spawning
// hivemind-ide.exe (Spec 09 §3.3 — 100% headless).
func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve [workspace]",
		Short: "Mantém o Remote Bridge (App Mobile) rodando sem a IDE",
		Long: `Roda o Remote Bridge em segundo plano no próprio binário hivemind para o
App Mobile continuar comandando os trabalhos pelo celular — sem a janela da
IDE e sem depender do binário hivemind-ide.

O QR de pareamento é exibido no terminal. Aprovações de comandos perigosos e
ações protegidas chegam no celular (approval_required) e são resolvidas pelo
app. Pressione Ctrl+C para encerrar.

O argumento opcional [workspace] registra o projeto inicial (padrão: diretório atual).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workspace := ""
			if len(args) == 1 {
				if abs, err := filepath.Abs(args[0]); err == nil {
					if info, statErr := os.Stat(abs); statErr == nil && info.IsDir() {
						workspace = abs
					} else {
						return fmt.Errorf("workspace inválido: %s", args[0])
					}
				}
			} else if cwd, err := os.Getwd(); err == nil {
				workspace = cwd
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()

			opts := remote.HeadlessOptions{
				Deps: func(hub *remote.Hub) remote.Deps {
					host := newServeHost(hub)
					browserx.SetChatHandler(func(text string) (string, error) {
						if host.RunSteer("lane-browser", text) {
							return "instrução enviada para a execução", nil
						}
						if err := host.RunStart(cmd.Context(), "lane-browser", workspace, "", text, "", "", "auto", nil); err != nil {
							return "", err
						}
						return "turno iniciado no hivemind serve", nil
					})
					if _, err := browserx.Listen(cmd.Context()); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "aviso: ponte do Chrome: %s\n", err.Error())
					}
					return host
				},
				Banner: func() string {
					return "Modo CLI: runs do time com aprovações no celular; diffs/PRs/issues/auto-review chegam com o bootstrap unificado."
				},
				OnExit: modelx.FlushAIUsage,
			}
			if err := remote.RunHeadless(ctx, workspace, opts); err != nil {
				return err
			}
			return nil
		},
	}
	return cmd
}
