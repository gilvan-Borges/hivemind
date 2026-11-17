package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Adiciona, edita e remove agentes do time ativo",
	}
	cmd.AddCommand(newAgentList(), newAgentAdd(), newAgentEdit(), newAgentRemove())
	return cmd
}

func newAgentList() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Lista os agentes do time",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, f, err := loadProject(cmd)
			if err != nil {
				return err
			}
			t, err := selectedTeam(cmd, f)
			if err != nil {
				return err
			}
			for _, a := range t.Agents {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", a.Name, a.Role, a.Provider, a.Model)
			}
			return nil
		},
	}
}

func newAgentAdd() *cobra.Command {
	var name, role, model, provider, instructions string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Adiciona um agente",
		Example: `  hivemind agent add --role dev --name dev_back --model agy:gemini-3.8-flash-medium
  hivemind agent add --role dev --model gpt-4.1 --provider openai`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, f, err := loadProject(cmd)
			if err != nil {
				return err
			}
			t, err := selectedTeam(cmd, f)
			if err != nil {
				return err
			}
			if (name == "" || role == "" || model == "") && isTTY(cmd.InOrStdin()) {
				ask := newAsker(cmd.InOrStdin(), cmd.OutOrStdout())
				if name == "" {
					name, err = ask.text("Nome", "")
					if err != nil {
						return err
					}
				}
				if role == "" {
					role, err = ask.text("Papel (pm, tech_lead, architect, dev, qa)", "dev")
					if err != nil {
						return err
					}
				}
				if model == "" {
					model, err = ask.text("Modelo (agy, agy:<slug> ou um id de outro provedor)", "agy")
					if err != nil {
						return err
					}
				}
				if provider == "" {
					provider, err = ask.text("Provedor", "")
					if err != nil {
						return err
					}
				}
				if instructions == "" {
					instructions, err = ask.text("Instruções (vazio usa o padrão do papel)", "")
					if err != nil {
						return err
					}
				}
			}
			role = team.NormalizeRole(role)
			if role == "" {
				return fmt.Errorf("papel inválido. Use pm, tech_lead, architect, dev ou qa")
			}
			if strings.TrimSpace(name) == "" || strings.TrimSpace(model) == "" {
				return fmt.Errorf("informe --name, --role e --model")
			}
			agent := team.Agent{
				Name:         strings.TrimSpace(name),
				Role:         role,
				Provider:     team.NormalizeProvider(provider, model),
				Model:        strings.TrimSpace(model),
				Instructions: instructions,
			}
			if strings.TrimSpace(agent.Instructions) == "" {
				agent.Instructions = team.DefaultInstructions(agent.Role)
			}
			replaced := false
			for i := range t.Agents {
				if t.Agents[i].Name == agent.Name {
					t.Agents[i] = agent
					replaced = true
				}
			}
			if !replaced {
				t.Agents = append(t.Agents, agent)
			}
			if err := t.Validate(); err != nil {
				return err
			}
			team.UpsertTeam(&f, t)
			if err := team.Save(dir, f); err != nil {
				return err
			}
			verb := "adicionado"
			if replaced {
				verb = "atualizado"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Agente %s %s no time %s.\n", agent.Name, verb, t.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Nome do agente")
	cmd.Flags().StringVar(&role, "role", "", "Papel: pm, tech_lead, architect, dev ou qa")
	cmd.Flags().StringVar(&model, "model", "", "Modelo deste agente")
	cmd.Flags().StringVar(&provider, "provider", "", "Provedor: agy, gemini, vertex, anthropic, openai ou openai-responses")
	cmd.Flags().StringVar(&instructions, "instructions", "", "Instruções do agente")
	return cmd
}

func newAgentEdit() *cobra.Command {
	var name, role, model, provider, instructions string
	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Edita um agente existente",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return fmt.Errorf("informe --name")
			}
			dir, f, err := loadProject(cmd)
			if err != nil {
				return err
			}
			t, err := selectedTeam(cmd, f)
			if err != nil {
				return err
			}
			found := false
			for i := range t.Agents {
				if t.Agents[i].Name != name {
					continue
				}
				found = true
				if cmd.Flags().Changed("role") {
					t.Agents[i].Role = team.NormalizeRole(role)
					if t.Agents[i].Role == "" {
						return fmt.Errorf("papel inválido")
					}
				}
				if cmd.Flags().Changed("model") {
					t.Agents[i].Model = model
				}
				if cmd.Flags().Changed("provider") {
					t.Agents[i].Provider = team.NormalizeProvider(provider, t.Agents[i].Model)
				}
				if cmd.Flags().Changed("instructions") {
					t.Agents[i].Instructions = instructions
				}
			}
			if !found {
				return fmt.Errorf("agente %q não encontrado no time %s", name, t.Name)
			}
			if err := t.Validate(); err != nil {
				return err
			}
			team.UpsertTeam(&f, t)
			if err := team.Save(dir, f); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Agente %s atualizado.\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Nome do agente")
	cmd.Flags().StringVar(&role, "role", "", "Novo papel")
	cmd.Flags().StringVar(&model, "model", "", "Novo modelo")
	cmd.Flags().StringVar(&provider, "provider", "", "Novo provedor")
	cmd.Flags().StringVar(&instructions, "instructions", "", "Novas instruções")
	return cmd
}

func newAgentRemove() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove um agente",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return fmt.Errorf("informe --name")
			}
			dir, f, err := loadProject(cmd)
			if err != nil {
				return err
			}
			t, err := selectedTeam(cmd, f)
			if err != nil {
				return err
			}
			next := make([]team.Agent, 0, len(t.Agents))
			found := false
			for _, a := range t.Agents {
				if a.Name == name {
					found = true
					continue
				}
				next = append(next, a)
			}
			if !found {
				return fmt.Errorf("agente %q não encontrado", name)
			}
			t.Agents = next
			if err := t.Validate(); err != nil {
				return err
			}
			team.UpsertTeam(&f, t)
			if err := team.Save(dir, f); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Agente %s removido. Confira se o time ainda tem todos os papéis antes de rodar.\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Nome do agente")
	return cmd
}
