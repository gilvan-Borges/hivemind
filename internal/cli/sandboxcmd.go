package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gilvan-Borges/HiveMind/internal/sandboxx"
)

func newSandboxExecCmd() *cobra.Command {
	var (
		writeRoots   []string
		readRoots    []string
		timeout      time.Duration
		allowNetwork bool
	)
	cmd := &cobra.Command{
		Use:    "sandbox-exec [flags] -- <comando> [args...]",
		Short:  "Executa um comando dentro da sandbox obrigatória do Hive (falha fechada)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := sandboxx.ValidateArgv(args); err != nil {
				return err
			}
			ws, err := workspaceOf(cmd)
			if err != nil {
				return err
			}
			res, runErr := sandboxx.NewOSRunner().Run(cmd.Context(), sandboxx.Options{
				Argv:         args,
				WorkDir:      ws,
				ReadRoots:    readRoots,
				WriteRoots:   writeRoots,
				AllowNetwork: allowNetwork,
				Timeout:      timeout,
			})
			if strings.TrimSpace(res.Stdout) != "" {
				fmt.Fprint(cmd.OutOrStdout(), res.Stdout)
			}
			if strings.TrimSpace(res.Stderr) != "" {
				fmt.Fprint(cmd.ErrOrStderr(), res.Stderr)
			}
			return runErr
		},
	}
	cmd.Flags().StringSliceVar(&readRoots, "read-root", nil, "Diretório adicional somente leitura na sandbox")
	cmd.Flags().StringSliceVar(&writeRoots, "write-root", nil, "Diretório autorizado para escrita na sandbox")
	cmd.Flags().DurationVar(&timeout, "timeout", sandboxx.DefaultTimeout, "Tempo limite de execução na sandbox")
	cmd.Flags().BoolVar(&allowNetwork, "allow-network", false, "Habilita rede (se suportado pela política)")
	return cmd
}
