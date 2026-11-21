package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gilvan-Borges/HiveMind/internal/skillx"
)

func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Documentação compacta e stubs híbridos embutidos nesta versão do HiveMind",
	}

	var listJSON bool
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "Lista os tópicos embutidos alinhados à versão instalada",
		RunE: func(cmd *cobra.Command, _ []string) error {
			topics := skillx.ListBuiltinTopics()
			if listJSON {
				type summaryItem struct {
					Name    string `json:"name"`
					Title   string `json:"title"`
					Summary string `json:"summary"`
					Version string `json:"version"`
					Commit  string `json:"commit"`
				}
				items := make([]summaryItem, 0, len(topics))
				for _, t := range topics {
					items = append(items, summaryItem{
						Name:    t.Name,
						Title:   t.Title,
						Summary: t.Summary,
						Version: t.Version,
						Commit:  t.Commit,
					})
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(items)
			}
			for _, t := range topics {
				fmt.Fprintf(cmd.OutOrStdout(), "%-16s %s (%s)\n", t.Name, t.Title, t.Version)
			}
			return nil
		},
	}
	listCmd.Flags().BoolVar(&listJSON, "json", false, "Emite saída estruturada em JSON")

	var getJSON bool
	getCmd := &cobra.Command{
		Use:   "get <topic>",
		Short: "Exibe o stub híbrido completo de um tópico",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			topic, ok := skillx.GetBuiltinTopic(args[0])
			if !ok {
				var names []string
				for _, t := range skillx.ListBuiltinTopics() {
					names = append(names, t.Name)
				}
				return fmt.Errorf("tópico %q não encontrado (disponíveis: %s)", args[0], strings.Join(names, ", "))
			}
			if getJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(topic)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "<!-- hivemind %s (%s) -->\n%s", topic.Version, topic.Commit, topic.Body)
			return nil
		},
	}
	getCmd.Flags().BoolVar(&getJSON, "json", false, "Emite saída estruturada em JSON")

	cmd.AddCommand(listCmd, getCmd)
	return cmd
}
