// Package cli is the Portuguese command line for HiveMind.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/tui"
	"github.com/gilvan-Borges/HiveMind/internal/version"
)

// Version is the CLI version printed by `hivemind version`. O valor canônico
// vem de internal/version (injetável via -ldflags -X no build oficial).
var Version = version.Version

// Execute runs the CLI with args, which do not include the program name.
func Execute(args []string) error {
	defer modelx.FlushAIUsage()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := newRoot()
	root.SetArgs(args)
	root.SetContext(ctx)
	root.SetIn(os.Stdin)
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)
	return root.Execute()
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "hivemind",
		Short: "Time de software multiagente no terminal",
		Long: `HiveMind CLI reúne um time de agentes no diretório do projeto.

Sem argumentos, abre uma sessão interativa. Use "hivemind run" para executar um objetivo do começo ao fim.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if isTTY(os.Stdin) && isTTY(os.Stdout) {
				dir, err := workspaceOf(cmd)
				if err != nil {
					return err
				}
				name, _ := cmd.Flags().GetString("team")
				return tui.Run(cmd.Context(), tui.Options{Workspace: dir, TeamName: name})
			}
			return interactive(cmd, "")
		},
	}
	root.PersistentFlags().String("workspace", "", "Pasta do projeto (padrão: diretório atual)")
	root.PersistentFlags().String("team", "", "Nome do time (padrão: o time ativo)")
	root.CompletionOptions.DisableDefaultCmd = true
	root.InitDefaultHelpFlag()
	if helpFlag := root.Flags().Lookup("help"); helpFlag != nil {
		helpFlag.Usage = "Mostra a ajuda"
	}
	root.AddCommand(newTeamCmd(), newAgentCmd(), newRunCmd(), newSessionCmd(), newJiraCmd(), newGitCmd(), newMCPCmd(), newDoctorCmd(), newServeCmd(), newSandboxExecCmd())
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Mostra a versão",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "hivemind", version.String())
			return nil
		},
	})
	root.InitDefaultHelpCmd()
	for _, child := range root.Commands() {
		if child.Name() == "help" {
			child.Short = "Mostra a ajuda de um comando"
		}
	}
	return root
}

func workspaceOf(cmd *cobra.Command) (string, error) {
	dir, err := cmd.Flags().GetString("workspace")
	if err != nil {
		return "", err
	}
	if dir == "" {
		dir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("workspace %s: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace %s não é uma pasta", abs)
	}
	return abs, nil
}

func loadProject(cmd *cobra.Command) (string, team.File, error) {
	dir, err := workspaceOf(cmd)
	if err != nil {
		return "", team.File{}, err
	}
	f, err := team.Load(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return dir, team.File{}, fmt.Errorf("não há time neste projeto. Rode: hivemind team create")
		}
		return dir, team.File{}, err
	}
	return dir, f, nil
}

func selectedTeam(cmd *cobra.Command, f team.File) (team.Team, error) {
	name, _ := cmd.Flags().GetString("team")
	return f.ActiveTeam(name)
}

func isTTY(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
