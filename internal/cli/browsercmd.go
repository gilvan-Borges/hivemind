package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gilvan-Borges/HiveMind/internal/browserx"
	"github.com/gilvan-Borges/HiveMind/internal/resident"
	"github.com/gilvan-Borges/HiveMind/internal/skillx"
)

func newBrowserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "browser",
		Short: "Extensão Agent Hive no Chrome real do usuário",
		Long: `A extensão controla o Chrome que você já usa (sessão e cookies). O hivemind
só escuta em 127.0.0.1. O host native messaging, registrado por "browser install",
encaminha a extensão até essa porta e guarda o token fora do Chrome.

"browser serve" sobe a ponte quando a IDE não está aberta. "browser pair" mostra
o código para o fallback de Load unpacked. O status nunca imprime o token.`,
	}
	cmd.AddCommand(newBrowserInstall(), newBrowserPair(), newBrowserStatus(), newBrowserServe(), newBrowserHost())
	return cmd
}

func newBrowserInstall() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Registra o host native messaging no Chrome deste usuário",
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			msg, err := browserx.Install(exe)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), msg)
			fmt.Fprintln(cmd.OutOrStdout(), "Carregue extension/chrome (ou o zip da release) em chrome://extensions.")
			return nil
		},
	}
}

func newBrowserPair() *cobra.Command {
	return &cobra.Command{
		Use:   "pair",
		Short: "Mostra o código de pareamento para colar no side panel",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := browserx.LoadConfig()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "porta\t127.0.0.1:%d\ncódigo\t%s\n", cfg.Port, cfg.Token)
			fmt.Fprintln(cmd.OutOrStdout(), "O código é um segredo local. Não cole em chat, issue ou log.")
			return nil
		},
	}
}

func newBrowserStatus() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Mostra se a ponte local está no ar, sem o token",
		RunE: func(cmd *cobra.Command, _ []string) error {
			port, listening, extension, hosts, err := browserx.Status()
			if err != nil {
				return err
			}
			state := "parada"
			if listening {
				state = "no ar"
			}
			ext := "desconectada"
			if extension {
				ext = "conectada"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "ponte\t%s\nendereço\t127.0.0.1:%d\textensão\t%s\n", state, port, ext)
			if len(hosts) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "sites\t(nenhum; o primeiro de cada host pede aprovação)")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "sites\t%s\n", strings.Join(hosts, ", "))
			}
			return nil
		},
	}
}

func newBrowserServe() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Sobe a ponte independente do Chrome em 127.0.0.1 até Ctrl+C (sem precisar da IDE aberta)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			browserx.SetChatHandler(func(text string) (string, error) {
				msg := strings.TrimSpace(text)
				if msg == "" {
					return "", fmt.Errorf("mensagem vazia")
				}
				baseDir := resident.DefaultBaseDir()
				cfg, err := resident.LoadConfig(baseDir)
				if err != nil {
					return "", err
				}
				pol, _ := resident.LoadPolicy(baseDir)
				runner := &resident.Runner{
					BaseDir:      baseDir,
					Config:       cfg,
					Policy:       &pol,
					Audit:        resident.NewFileAuditLogger(filepath.Join(baseDir, "audit.jsonl")),
					SkillsDir:    skillx.UserSkillsDir(),
					AllowedTiers: []resident.Tier{resident.TierRead, resident.TierNetwork},
					ApproveDetailed: func(_ context.Context, req resident.ApprovalRequest) bool {
						return req.ToolName == "chrome_tab"
					},
				}
				res, err := runner.RunTurn(cmd.Context(), resident.TurnRequest{
					Message:    msg,
					Workspace:  "",
					Workstream: "chrome-extension",
					Origin:     resident.OriginLocal,
				})
				if err != nil {
					return "", err
				}
				if strings.TrimSpace(res.FinalText) == "" {
					return "Concluído.", nil
				}
				return strings.TrimSpace(res.FinalText), nil
			})
			br, err := browserx.Listen(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = br.Close() }()
			fmt.Fprintf(cmd.OutOrStdout(), "ponte do Chrome em 127.0.0.1:%d\n", br.Port())
			fmt.Fprintln(cmd.OutOrStdout(), "O token não é impresso aqui. Use hivemind browser pair se o side panel pedir o código.")
			<-cmd.Context().Done()
			return nil
		},
	}
}

func newBrowserHost() *cobra.Command {
	return &cobra.Command{
		Use:   "host",
		Short: "Modo stdio do native messaging (o Chrome chama isto)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return browserx.RunNativeHost()
		},
	}
}
