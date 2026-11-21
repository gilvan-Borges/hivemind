package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"google.golang.org/adk/v2/agent"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/mcpx"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/shellx"
	"github.com/gilvan-Borges/HiveMind/internal/skillx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func newRunCmd() *cobra.Command {
	var jiraKey, sessionID, permissionMode, runMode string
	var yes, fullAccess bool
	var maxTokens int64
	var raceCount int
	cmd := &cobra.Command{
		Use:   "run [objetivo]",
		Short: "Executa um objetivo com o time, mostrando a saída de cada agente",
		Example: `  hivemind run "adicionar um endpoint de healthcheck"
  hivemind run --jira PROJ-123
  hivemind run --max-tokens 600000 "implementar o fluxo de login"
  hivemind run --full-access "corrigir revisão do PR #1"
  hivemind run --race 3 "refatorar parser mantendo testes verdes"
  hivemind run "ajuste o texto" --jira PROJ-123 --session 1a2b3c4d5e6f`,
		RunE: func(cmd *cobra.Command, args []string) error {
			objective := strings.TrimSpace(strings.Join(args, " "))
			overrideMode := strings.TrimSpace(permissionMode)
			if fullAccess {
				overrideMode = team.PermissionFullAccess
			}
			effectiveMode := strings.TrimSpace(runMode)
			if raceCount > 0 {
				effectiveMode = fmt.Sprintf("race:%d", raceCount)
			}
			_, err := executeRunDetailed(cmd, objective, jiraKey, sessionID, yes, overrideMode, maxTokens, effectiveMode)
			return err
		},
	}
	cmd.Flags().StringVar(&jiraKey, "jira", "", "Usa a issue do Jira Cloud como objetivo")
	cmd.Flags().StringVar(&sessionID, "session", "", "Retoma uma sessão existente")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirma comandos de shell perigosos e ações protegidas sem perguntar")
	cmd.Flags().BoolVar(&fullAccess, "full-access", false, "Concede acesso e permissão total nesta execução (estilo Claude Code / Codex)")
	cmd.Flags().StringVar(&permissionMode, "permission-mode", "", "Sobrescreve o modo de permissão nesta execução: governed, auto_edit ou full_access")
	cmd.Flags().StringVar(&runMode, "mode", "", "Modo de execução: auto, direct, plan, squad ou race[:N]")
	cmd.Flags().IntVar(&raceCount, "race", 0, "Executa N candidatos paralelos em worktrees isoladas (2..4, padrão 2)")
	if fl := cmd.Flags().Lookup("race"); fl != nil {
		fl.NoOptDefVal = "2"
	}
	cmd.Flags().Int64Var(&maxTokens, "max-tokens", -1, "Limite de tokens desta execução (0 = sem limite; -1 = usa settings.max_tokens do time)")
	return cmd
}

func executeRun(cmd *cobra.Command, objective, jiraKey, sessionID string, yes bool) (orchestrator.Result, error) {
	return executeRunWithMode(cmd, objective, jiraKey, sessionID, yes, "", -1)
}

func executeRunWithMode(cmd *cobra.Command, objective, jiraKey, sessionID string, yes bool, permissionOverride string, maxTokens int64) (orchestrator.Result, error) {
	return executeRunDetailed(cmd, objective, jiraKey, sessionID, yes, permissionOverride, maxTokens, "")
}

func executeRunDetailed(cmd *cobra.Command, objective, jiraKey, sessionID string, yes bool, permissionOverride string, maxTokens int64, runMode string) (orchestrator.Result, error) {
	dir, project, err := loadProject(cmd)
	if err != nil {
		return orchestrator.Result{}, err
	}
	if permissionOverride != "" {
		project.Settings.PermissionMode = team.NormalizePermissionMode(permissionOverride)
	}
	if maxTokens >= 0 {
		project.Settings.MaxTokens = maxTokens
	}
	squad, err := selectedTeam(cmd, project)
	if err != nil {
		return orchestrator.Result{}, err
	}
	var promptMu sync.Mutex
	var sharedReader *bufio.Reader
	if isTTY(cmd.InOrStdin()) {
		sharedReader = bufio.NewReader(cmd.InOrStdin())
	}
	confirm := confirmerWithReader(cmd, project.Settings, yes, sharedReader, &promptMu)
	approve := approverWithReader(cmd, project.Settings, yes, sharedReader, &promptMu)
	nativeRoot := skillx.UserSkillsDir()
	if _, err := skillx.SeedProductSkills(nativeRoot); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "aviso: skills nativas: %v\n", err)
	}
	mcpHost := mcpx.Start(cmd.Context(), dir)
	defer mcpHost.Close()
	runner := &orchestrator.Runner{
		Workspace:        dir,
		Project:          project,
		Team:             squad,
		Git:              gitx.Local{},
		Confirm:          confirm,
		Approve:          approve,
		MCP:              mcpHost,
		NativeSkills:     skillx.LoadNative(nativeRoot),
		NativeSkillsRoot: nativeRoot,
		Out:              cmd.OutOrStdout(),
		Err:              cmd.ErrOrStderr(),
		Streaming:        agent.StreamingModeSSE,
		OnEvent: func(ev orchestrator.Event) {
			if ev.Kind == "model_route" && ev.Text != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "[jev] %s\n", ev.Text)
			}
		},
	}
	runner.NewModel = orchestrator.NewJevModelFactory(orchestrator.JevFactoryOptions{
		Workspace: dir,
		Approve:   approve,
		OnEvent:   runner.OnEvent,
	})
	res, runErr := runner.Run(cmd.Context(), orchestrator.Request{
		Objective: objective,
		JiraKey:   jiraKey,
		SessionID: sessionID,
		RunMode:   runMode,
	})
	return res, runErr
}

func approverWithReader(cmd *cobra.Command, settings team.Settings, yes bool, sharedReader *bufio.Reader, sharedMu *sync.Mutex) func(string) bool {
	var localMu sync.Mutex
	mu := sharedMu
	if mu == nil {
		mu = &localMu
	}
	var reader *bufio.Reader
	return func(prompt string) bool {
		if yes || settings.IsFullAccess() {
			return true
		}
		if !isTTY(cmd.InOrStdin()) {
			fmt.Fprintf(cmd.ErrOrStderr(), "ação protegida recusada fora de um terminal: %s\n", prompt)
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		r := sharedReader
		if r == nil {
			if reader == nil {
				reader = bufio.NewReader(cmd.InOrStdin())
			}
			r = reader
		}
		fmt.Fprintf(cmd.OutOrStdout(), "\nPermissão solicitada pelo Chefe:\n  %s\n[s/N]: ", prompt)
		line, err := r.ReadString('\n')
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

func confirmer(cmd *cobra.Command, settings team.Settings, yes bool) shellx.Confirmer {
	return confirmerWithReader(cmd, settings, yes, nil, nil)
}

func confirmerWithReader(cmd *cobra.Command, settings team.Settings, yes bool, sharedReader *bufio.Reader, sharedMu *sync.Mutex) shellx.Confirmer {
	var localMu sync.Mutex
	mu := sharedMu
	if mu == nil {
		mu = &localMu
	}
	var reader *bufio.Reader
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
		mu.Lock()
		defer mu.Unlock()
		r := sharedReader
		if r == nil {
			if reader == nil {
				reader = bufio.NewReader(cmd.InOrStdin())
			}
			r = reader
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Executar comando perigoso?\n  %s\n[s/N]: ", command)
		line, err := r.ReadString('\n')
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
