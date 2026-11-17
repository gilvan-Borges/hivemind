package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"google.golang.org/adk/v2/agent"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/shellx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func newRunCmd() *cobra.Command {
	var jiraKey, sessionID string
	var yes bool
	cmd := &cobra.Command{
		Use:   "run [objetivo]",
		Short: "Executa um objetivo com o time, mostrando a saída de cada agente",
		Example: `  hivemind run "adicionar um endpoint de healthcheck"
  hivemind run --jira PROJ-123
  hivemind run "ajuste o texto" --jira PROJ-123 --session 1a2b3c4d5e6f`,
		RunE: func(cmd *cobra.Command, args []string) error {
			objective := strings.TrimSpace(strings.Join(args, " "))
			_, err := executeRun(cmd, objective, jiraKey, sessionID, yes)
			return err
		},
	}
	cmd.Flags().StringVar(&jiraKey, "jira", "", "Usa a issue do Jira Cloud como objetivo")
	cmd.Flags().StringVar(&sessionID, "session", "", "Retoma uma sessão existente")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirma comandos de shell perigosos sem perguntar")
	return cmd
}

func executeRun(cmd *cobra.Command, objective, jiraKey, sessionID string, yes bool) (orchestrator.Result, error) {
	dir, project, err := loadProject(cmd)
	if err != nil {
		return orchestrator.Result{}, err
	}
	squad, err := selectedTeam(cmd, project)
	if err != nil {
		return orchestrator.Result{}, err
	}
	confirm := confirmer(cmd, project.Settings, yes)
	runner := &orchestrator.Runner{
		Workspace: dir,
		Project:   project,
		Team:      squad,
		Git:       gitx.Local{},
		Confirm:   confirm,
		Out:       cmd.OutOrStdout(),
		Err:       cmd.ErrOrStderr(),
		Streaming: agent.StreamingModeSSE,
	}
	return runner.Run(cmd.Context(), orchestrator.Request{
		Objective: objective,
		JiraKey:   jiraKey,
		SessionID: sessionID,
	})
}

func confirmer(cmd *cobra.Command, settings team.Settings, yes bool) shellx.Confirmer {
	return func(command string) bool {
		if yes {
			return true
		}
		if settings.ConfirmShell != nil && !*settings.ConfirmShell {
			fmt.Fprintf(cmd.ErrOrStderr(), "comando perigoso recusado pela configuração: %s\n", command)
			return false
		}
		if !isTTY(cmd.InOrStdin()) {
			fmt.Fprintf(cmd.ErrOrStderr(), "comando perigoso recusado fora de um terminal: %s\n", command)
			return false
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Executar comando perigoso?\n  %s\n[s/N]: ", command)
		line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		if err != nil && err != io.EOF {
			return false
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "s", "sim", "y", "yes":
			return true
		default:
			return false
		}
	}
}

func interactive(cmd *cobra.Command, sessionID string) error {
	dir, project, err := loadProject(cmd)
	if err != nil {
		if !isTTY(cmd.InOrStdin()) {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Não há time neste projeto.")
		ask := newAsker(cmd.InOrStdin(), cmd.OutOrStdout())
		ok, askErr := ask.yesNo("Criar o time padrão agora", true)
		if askErr != nil {
			return askErr
		}
		if !ok {
			return err
		}
		create := newTeamCreate()
		create.SetIn(cmd.InOrStdin())
		create.SetOut(cmd.OutOrStdout())
		create.SetErr(cmd.ErrOrStderr())
		create.SetContext(cmd.Context())
		create.Flags().Set("name", "padrao")
		if err := create.RunE(cmd, nil); err != nil {
			return err
		}
		dir, project, err = loadProject(cmd)
		if err != nil {
			return err
		}
	}
	squad, err := selectedTeam(cmd, project)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "HiveMind — sessão interativa\nTime: %s\nWorkspace: %s\nDigite um objetivo, /ajuda ou /sair.\n", squad.Name, dir)
	scanner := bufio.NewScanner(cmd.InOrStdin())
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	current := sessionID
	for {
		if err := cmd.Context().Err(); err != nil {
			return err
		}
		fmt.Fprint(out, "hivemind> ")
		if !scanner.Scan() {
			fmt.Fprintln(out)
			return scanner.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "":
			continue
		case line == "/sair" || line == "/exit" || line == "/quit":
			fmt.Fprintln(out, "Até logo.")
			return nil
		case line == "/ajuda" || line == "/help":
			fmt.Fprintln(out, "Objetivo em texto livre executa o time.\n/jira PROJ-123   usa a issue como objetivo\n/time            mostra o time ativo\n/sessoes        lista sessões\n/sair            encerra")
		case line == "/time":
			for _, a := range squad.Agents {
				fmt.Fprintf(out, "- %s (%s, %s)\n", a.Name, a.Role, a.Model)
			}
		case line == "/sessoes":
			if err := printSessions(cmd, dir); err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
			}
		case strings.HasPrefix(line, "/jira "):
			key := strings.TrimSpace(strings.TrimPrefix(line, "/jira "))
			result, err := executeRun(cmd, "", key, current, false)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "erro:", err)
				continue
			}
			current = result.SessionID
		default:
			result, err := executeRun(cmd, line, "", current, false)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "erro:", err)
				if result.SessionID != "" {
					current = result.SessionID
				}
				continue
			}
			current = result.SessionID
		}
	}
}
