package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gilvan-Borges/HiveMind/internal/mcpx"
)

func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Lista e liga os servidores MCP do HiveMind",
	}
	cmd.AddCommand(newMCPList(), newMCPEnable(), newMCPDisable())
	return cmd
}

func newMCPList() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Mostra os servidores e se estão ligados",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := workspaceOf(cmd)
			if err != nil {
				return err
			}
			cfg, err := mcpx.Load(dir)
			if err != nil {
				return err
			}
			for _, view := range mcpx.Describe(cfg) {
				line := fmt.Sprintf("%s\t%s\t%s", view.Name, view.RuntimeLabel(), view.Description)
				if strings.TrimSpace(view.Detail) != "" {
					line += "\t" + view.Detail
				}
				fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			return nil
		},
	}
}

func newMCPEnable() *cobra.Command {
	var projectOnly bool
	cmd := &cobra.Command{
		Use:   "enable [nome]",
		Short: "Liga um servidor MCP na configuração",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setMCP(cmd, args[0], true, projectOnly)
		},
	}
	cmd.Flags().BoolVar(&projectOnly, "project", false, "Grava o interruptor só neste projeto (.hivemind/)")
	return cmd
}

func newMCPDisable() *cobra.Command {
	var projectOnly bool
	cmd := &cobra.Command{
		Use:   "disable [nome]",
		Short: "Desliga um servidor MCP na configuração",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setMCP(cmd, args[0], false, projectOnly)
		},
	}
	cmd.Flags().BoolVar(&projectOnly, "project", false, "Grava o interruptor só neste projeto (.hivemind/)")
	return cmd
}

func setMCP(cmd *cobra.Command, name string, enabled, projectOnly bool) error {
	dir, err := workspaceOf(cmd)
	if err != nil {
		return err
	}
	if _, err := mcpx.SetEnabled(dir, strings.TrimSpace(name), enabled, projectOnly); err != nil {
		return err
	}
	state := "desligado"
	if enabled {
		state = "ligado"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", name, state)
	return nil
}
