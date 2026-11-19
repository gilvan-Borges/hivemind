package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func newTeamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team",
		Short: "Cria e seleciona times do projeto",
	}
	cmd.AddCommand(newTeamCreate(), newTeamList(), newTeamUse(), newTeamShow(), newTeamPermission())
	return cmd
}

func newTeamCreate() *cobra.Command {
	var name, description, model, provider, permissionMode string
	var devs int
	var jira, autoCommit, autoPR, worktrees bool
	var loops int
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Cria um time com os papéis do pipeline",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := workspaceOf(cmd)
			if err != nil {
				return err
			}
			existing, loadErr := team.Load(dir)
			fresh := false
			if loadErr != nil {
				if !errors.Is(loadErr, os.ErrNotExist) {
					return loadErr
				}
				fresh = true
				existing = team.File{Settings: team.DefaultSettings()}
			}
			if !cmd.Flags().Changed("model") && !cmd.Flags().Changed("provider") {
				provider, model = modelx.DefaultProviderModel()
			} else {
				if strings.TrimSpace(model) == "" {
					model = "gemini-flash-latest"
				}
				if strings.TrimSpace(provider) == "" {
					provider = team.NormalizeProvider("", model)
				}
			}
			if name == "" && isTTY(cmd.InOrStdin()) {
				ask := newAsker(cmd.InOrStdin(), cmd.OutOrStdout())
				fmt.Fprintln(cmd.OutOrStdout(), "Assistente de criação de time")
				name, err = ask.text("Nome do time", "padrao")
				if err != nil {
					return err
				}
				description, err = ask.text("Descrição", description)
				if err != nil {
					return err
				}
				model, err = ask.text("Modelo padrão", model)
				if err != nil {
					return err
				}
				provider, err = ask.text("Provedor (agy, gemini, vertex, anthropic, openai, openrouter)", provider)
				if err != nil {
					return err
				}
				devs, err = ask.integer("Quantos desenvolvedores", devs)
				if err != nil {
					return err
				}
				jira, err = ask.yesNo("Habilitar Jira", jira)
				if err != nil {
					return err
				}
				autoCommit, err = ask.yesNo("Commit automático ao final", autoCommit)
				if err != nil {
					return err
				}
				autoPR, err = ask.yesNo("Abrir pull request ao final", autoPR)
				if err != nil {
					return err
				}
				loops, err = ask.integer("Máximo de voltas do QA", loops)
				if err != nil {
					return err
				}
			}
			if strings.TrimSpace(name) == "" {
				name = "padrao"
			}
			created := team.DefaultTeam(name, description, model, provider, devs)
			if err := created.ValidateForRun(); err != nil {
				return err
			}
			team.UpsertTeam(&existing, created)
			if fresh {
				wt := worktrees
				existing.Settings.Worktrees = &wt
				existing.Settings.PermissionMode = team.NormalizePermissionMode(permissionMode)
				existing.Settings.JiraEnabled = jira
				existing.Settings.AutoCommit = autoCommit
				existing.Settings.AutoPR = autoPR
				existing.Settings.MaxQALoops = loops
			}
			if err := team.Save(dir, existing); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Time %q salvo em .hivemind/team.yaml (%d agentes).\n", created.Name, len(created.Agents))
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Nome do time")
	cmd.Flags().StringVar(&description, "description", "", "Descrição")
	cmd.Flags().StringVar(&model, "model", "", "Modelo. Vazio usa agy quando o CLI está no PATH e não há GOOGLE_API_KEY")
	cmd.Flags().StringVar(&provider, "provider", "", "Provedor: agy, gemini, vertex, anthropic, openai ou openrouter. Vazio segue o modelo")
	cmd.Flags().IntVar(&devs, "devs", 1, "Quantidade de desenvolvedores")
	cmd.Flags().BoolVar(&worktrees, "worktrees", true, "Trabalhar sempre em worktree separada")
	cmd.Flags().StringVar(&permissionMode, "permission-mode", team.PermissionGoverned, "Modo de permissão: governed, auto_edit ou full_access")
	cmd.Flags().BoolVar(&jira, "jira", true, "Habilitar ferramentas Jira neste projeto")
	cmd.Flags().BoolVar(&autoCommit, "auto-commit", true, "Commit automático pelo Chefe/Arquiteto na worktree de entrega")
	cmd.Flags().BoolVar(&autoPR, "auto-pr", true, "Abrir pull request automaticamente a partir da worktree de entrega")
	cmd.Flags().IntVar(&loops, "max-qa-loops", 3, "Máximo de voltas do QA")
	return cmd
}

func newTeamPermission() *cobra.Command {
	return &cobra.Command{
		Use:   "permission [governed|auto_edit|full_access]",
		Short: "Exibe ou altera o nível de autonomia e permissão do projeto",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, f, err := loadProject(cmd)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "Modo de permissão atual: %s\n", f.Settings.EffectivePermissionMode())
				return nil
			}
			mode := team.NormalizePermissionMode(args[0])
			f.Settings.PermissionMode = mode
			if err := team.Save(dir, f); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Modo de permissão atualizado para: %s\n", mode)
			return nil
		},
	}
}

func newTeamList() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Lista os times do projeto",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, f, err := loadProject(cmd)
			if err != nil {
				return err
			}
			for _, t := range f.Teams {
				mark := " "
				if t.Name == f.Active {
					mark = "*"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%d agentes)\n", mark, t.Name, len(t.Agents))
			}
			return nil
		},
	}
}

func newTeamUse() *cobra.Command {
	return &cobra.Command{
		Use:   "use <nome>",
		Short: "Define o time ativo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, f, err := loadProject(cmd)
			if err != nil {
				return err
			}
			if _, err := f.ActiveTeam(args[0]); err != nil {
				return err
			}
			f.Active = args[0]
			if err := team.Save(dir, f); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Time ativo: %s\n", args[0])
			return nil
		},
	}
}

func newTeamShow() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Mostra o time ativo e a configuração",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, f, err := loadProject(cmd)
			if err != nil {
				return err
			}
			t, err := selectedTeam(cmd, f)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Time: %s\n", t.Name)
			if t.Description != "" {
				fmt.Fprintf(out, "Descrição: %s\n", t.Description)
			}
			wtOn := f.Settings.Worktrees == nil || *f.Settings.Worktrees
			fmt.Fprintf(out, "Permissão: %s | worktrees: %s | Jira: %s | auto-commit: %s | auto-pr: %s | voltas do QA: %d | base: %s | homolog: %s\n",
				f.Settings.EffectivePermissionMode(), simNao(wtOn), simNao(f.Settings.JiraEnabled), simNao(f.Settings.AutoCommit), simNao(f.Settings.AutoPR), f.Settings.MaxQALoops, f.Settings.BaseBranch, f.Settings.HomologBranch)
			for _, a := range t.Agents {
				fmt.Fprintf(out, "- %s  papel=%s  provedor=%s  modelo=%s\n", a.Name, a.Role, a.Provider, a.Model)
			}
			return nil
		},
	}
}

func simNao(v bool) string {
	if v {
		return "sim"
	}
	return "não"
}
