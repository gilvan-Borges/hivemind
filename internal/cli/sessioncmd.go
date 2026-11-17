package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"google.golang.org/adk/v2/session"

	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
)

func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Lista e retoma sessões gravadas em .hivemind/sessions",
	}
	cmd.AddCommand(newSessionList(), newSessionShow(), newSessionResume())
	return cmd
}

func newSessionList() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Lista as sessões do projeto",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := workspaceOf(cmd)
			if err != nil {
				return err
			}
			return printSessions(cmd, dir)
		},
	}
}

func printSessions(cmd *cobra.Command, dir string) error {
	store, err := sessionx.Open(dir)
	if err != nil {
		return err
	}
	metas, err := store.ListMeta()
	if err != nil {
		return err
	}
	if len(metas) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Nenhuma sessão.")
		return nil
	}
	for _, m := range metas {
		obj := strings.ReplaceAll(m.Objective, "\n", " ")
		if len(obj) > 80 {
			obj = obj[:80] + "…"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s  %s\n", m.ID, m.UpdatedAt.Local().Format("2006-01-02 15:04"), m.JiraKey, obj)
	}
	return nil
}

func newSessionShow() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Mostra o objetivo e os autores de uma sessão",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := workspaceOf(cmd)
			if err != nil {
				return err
			}
			store, err := sessionx.Open(dir)
			if err != nil {
				return err
			}
			got, err := store.Get(cmd.Context(), &session.GetRequest{
				AppName:   sessionx.AppName,
				UserID:    sessionx.UserID,
				SessionID: args[0],
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			obj, _ := got.Session.State().Get("objective")
			fmt.Fprintf(out, "Sessão %s\nObjetivo: %v\nEventos: %d\n", got.Session.ID(), obj, got.Session.Events().Len())
			for i := 0; i < got.Session.Events().Len(); i++ {
				ev := got.Session.Events().At(i)
				if ev == nil {
					continue
				}
				text := ""
				if ev.LLMResponse.Content != nil {
					for _, part := range ev.LLMResponse.Content.Parts {
						if part != nil && part.Text != "" {
							text = strings.TrimSpace(part.Text)
						}
					}
				}
				if len(text) > 120 {
					text = text[:120] + "…"
				}
				fmt.Fprintf(out, "- %s: %s\n", ev.Author, text)
			}
			return nil
		},
	}
}

func newSessionResume() *cobra.Command {
	return &cobra.Command{
		Use:   "resume <id>",
		Short: "Abre a sessão interativa continuando uma execução anterior",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return interactive(cmd, args[0])
		},
	}
}
