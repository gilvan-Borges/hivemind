package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func newJiraCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jira",
		Short: "Consulta o Jira Cloud configurado nas variáveis de ambiente",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "search <jql>",
		Short: "Pesquisa issues com JQL",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := jiraClient(cmd)
			if err != nil {
				return err
			}
			issues, err := client.Search(cmd.Context(), strings.Join(args, " "), 20)
			if err != nil {
				return err
			}
			if len(issues) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Nenhuma issue.")
				return nil
			}
			for _, issue := range issues {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", issue.Key, issue.Summary)
			}
			return nil
		},
	}, &cobra.Command{
		Use:   "show <chave>",
		Short: "Mostra uma issue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := jiraClient(cmd)
			if err != nil {
				return err
			}
			issue, err := client.GetIssue(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s [%s]\n%s\n\n%s\n", issue.Key, issue.Status, issue.Summary, issue.Description)
			return nil
		},
	})
	return cmd
}

func jiraClient(cmd *cobra.Command) (jirax.Client, error) {
	dir, err := workspaceOf(cmd)
	if err != nil {
		return nil, err
	}
	if f, loadErr := team.Load(dir); loadErr == nil && !f.Settings.JiraEnabled {
		return nil, fmt.Errorf("Jira está desativado em .hivemind/team.yaml")
	}
	client := jirax.FromEnv()
	if !client.Enabled() {
		return nil, fmt.Errorf("defina JIRA_BASE_URL, JIRA_EMAIL e JIRA_API_TOKEN")
	}
	return client, nil
}

func newGitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "git",
		Short: "Atalhos de git no workspace",
	}
	g := gitx.Local{}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "git status --short --branch",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := workspaceOf(cmd)
			if err != nil {
				return err
			}
			out, err := g.Status(cmd.Context(), dir)
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return err
		},
	}, &cobra.Command{
		Use:   "diff",
		Short: "git diff --stat HEAD",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := workspaceOf(cmd)
			if err != nil {
				return err
			}
			out, err := g.Diff(cmd.Context(), dir)
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return err
		},
	}, &cobra.Command{
		Use:   "branch",
		Short: "Lista branches locais",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := workspaceOf(cmd)
			if err != nil {
				return err
			}
			out, err := g.Run(cmd.Context(), dir, "branch")
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return err
		},
	})
	return cmd
}

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Mostra o que está configurado, sem revelar segredos",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := workspaceOf(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Workspace: %s\n", dir)
			fmt.Fprintf(out, "Git: %s\n", simNao(gitx.Local{}.IsRepo(cmd.Context(), dir)))
			if _, err := exec.LookPath("gh"); err == nil {
				fmt.Fprintln(out, "gh: encontrado")
			} else {
				fmt.Fprintln(out, "gh: ausente")
			}
			agy := modelx.ProbeAgy(cmd.Context())
			if agy.Found {
				fmt.Fprintf(out, "agy: %s (%s)\n", agy.Detail, agy.Path)
			} else {
				fmt.Fprintf(out, "agy: %s\n", agy.Detail)
			}
			if f, err := team.Load(dir); err != nil {
				fmt.Fprintln(out, "Time: ausente")
			} else {
				fmt.Fprintf(out, "Time ativo: %s (%d times)\n", f.Active, len(f.Teams))
				fmt.Fprintf(out, "Jira na configuração: %s\n", simNao(f.Settings.JiraEnabled))
				fmt.Fprintf(out, "Auto-commit: %s | auto-pr: %s\n", simNao(f.Settings.AutoCommit), simNao(f.Settings.AutoPR))
			}
			for _, key := range []string{"GOOGLE_API_KEY", "GEMINI_API_KEY", "GOOGLE_GENAI_USE_VERTEXAI", "GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENROUTER_API_KEY", "OPENROUTER_BASE_URL", "JIRA_BASE_URL", "JIRA_EMAIL", "JIRA_API_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
				fmt.Fprintf(out, "%s: %s\n", key, presente(os.Getenv(key)))
			}
			probe, _ := cmd.Flags().GetBool("probe")
			if probe {
				text, err := modelx.ProbeAgyPrompt(cmd.Context())
				if err != nil {
					fmt.Fprintf(out, "agy probe: %s\n", err)
					return err
				}
				fmt.Fprintf(out, "agy probe: %s\n", text)
			}
			return nil
		},
	}
	cmd.Flags().Bool("probe", false, `Executa o mesmo caminho do agy (stdin UTF-8 e --json-schema em arquivo) com "responda apenas OK"`)
	return cmd
}

func presente(v string) string {
	if strings.TrimSpace(v) == "" {
		return "ausente"
	}
	return "definida"
}
