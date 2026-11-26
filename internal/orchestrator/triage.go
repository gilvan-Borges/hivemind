package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"

	"github.com/gilvan-Borges/HiveMind/internal/contextx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/names"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/ui"
	"github.com/gilvan-Borges/HiveMind/internal/vaultx"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

type delegateArgs struct {
	Plan             string     `json:"plan" jsonschema:"Plano técnico detalhado para a equipe executar."`
	ContextBrief     string     `json:"context_brief,omitempty" jsonschema:"Dossiê técnico com os pontos importantes que você JÁ leu (regras RN exatas, assinaturas de classes/interfaces/métodos, campos de DTO, códigos HTTP/erro, rotas, caminhos de arquivos e faixas de linha) para o Dev e o QA consultarem direto sem reler spec.md/plan.md/tasks.md inteiros."`
	DevTasks         []string   `json:"dev_tasks" jsonschema:"Lista dinâmica de 1 até max_dynamic_devs subtarefas SEM sobreposição de arquivos (1 item para tarefas focadas; 2+ itens quando realmente paralelizáveis)."`
	OwnedPaths       [][]string `json:"owned_paths,omitempty" jsonschema:"Opcional (recomendado para 2+ dev_tasks): lista de prefixos/pastas exclusivos para cada dev na mesma ordem de dev_tasks, ex.: [[\"internal/api/\"], [\"ide/frontend/\"]]. Quando disjuntos, todos os devs compartilham 1 única worktree da conversa com trava de posse e memória compartilhada."`
	Sequential       bool       `json:"sequential,omitempty" jsonschema:"Use true quando 2+ dev_tasks precisarem editar o mesmo serviço/arquivos sem colisão (roda Wave 1 -> Wave 2 na mesma worktree da conversa)."`
	PRNumber         int        `json:"pr_number,omitempty" jsonschema:"Número do PR existente quando a tarefa é corrigir mudanças solicitadas em revisão."`
	PRBranch         string     `json:"pr_branch,omitempty" jsonschema:"Branch headRef do PR existente, quando houver."`
	Reviewers        []string   `json:"reviewers,omitempty" jsonschema:"Logins dos revisores para pedir nova revisão após corrigir (ex: quem pediu mudança)."`
	Skills           []string   `json:"skills,omitempty" jsonschema:"Opcional: nomes exatos das skills do catálogo .agents/skills do projeto que a equipe deve seguir (apenas as relevantes para a tarefa; vazio significa nenhuma)."`
	NativeSkills     []string   `json:"native_skills,omitempty" jsonschema:"Opcional: nomes das skills NATIVAS da IDE que a equipe deve seguir. Use quando a nativa parecer mais atual ou melhor que a versão do projeto; se houver conflito de nome, o usuário será consultado para autorizar."`
	PauseForApproval bool       `json:"pause_for_approval,omitempty" jsonschema:"No modo Auto: use true quando a tarefa for grande, arriscada ou de escopo amplo e você julgar que o usuário deve revisar e aprovar o plano ANTES de disparar o Squad. false (padrão) executa direto."`
}

type delegateResult struct {
	Status string `json:"status"`
}

func isLightOperationalObjective(raw string) bool {
	text := strings.TrimSpace(raw)
	if idx := strings.LastIndex(text, "[Objetivo do Usuário]"); idx >= 0 {
		text = strings.TrimSpace(text[idx+len("[Objetivo do Usuário]"):])
	}
	if len(text) == 0 || len(text) > 360 {
		return false
	}
	low := strings.ToLower(text)
	for _, heavy := range []string{
		"resolver issue", "resolva a issue", "resolver task", "resolver tarefa", "resolver ipg",
		"revisar pr", "reavaliar pr", "revisar automaticamente", "atender feedback",
		"implement", "refator", "crie o arquivo", "criar arquivo", "edite o código",
		"editar o código", "corrija o bug", "corrigir o bug", "desenvolv", "escreva o teste",
		"adicione o endpoint", "criar componente", "migração", "fazer uma task", "pegar uma task",
		"pegue uma task", "fazer a task", "executar a task", "worktree", "onda ", "fase ",
		"descrição:", "critérios de aceite",
	} {
		if strings.Contains(low, heavy) {
			return false
		}
	}
	for _, kw := range []string{
		"mude o status", "mudar status", "mudar o status", "atualize o status", "atualizar status",
		"mover para", "mova para", "transicionar", "transição",
		"comentar no jira", "comente no jira", "comentar na task", "comente na task",
		"comentar no pr", "comente no pr", "comentar na issue",
		"qual o status", "conferir status", "verificar status", "listar tasks", "liste as tasks",
	} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

func (r *Runner) resolveLightJevAgent(chief team.Agent) (team.Agent, bool) {
	cProv := team.NormalizeProvider(chief.Provider, chief.Model)
	cLow := strings.ToLower(strings.TrimSpace(chief.Model))
	if cProv == "jev" || strings.HasPrefix(cLow, "jev:") || strings.HasSuffix(cLow, ":auto") {
		return chief, true
	}
	for _, ag := range r.Team.Agents {
		p := team.NormalizeProvider(ag.Provider, ag.Model)
		m := strings.ToLower(strings.TrimSpace(ag.Model))
		if p == "jev" || strings.HasPrefix(m, "jev:") || (p != "cursor" && strings.HasSuffix(m, ":auto")) {
			routed := chief
			routed.Provider = ag.Provider
			routed.Model = ag.Model
			return routed, true
		}
	}
	creds := modelx.JevCredentialPresence()
	routed := chief
	routed.Provider = "jev"
	if creds["openrouter"] {
		routed.Model = "jev:openrouter"
	} else {
		routed.Model = "jev:auto"
	}
	return routed, true
}

func (r *Runner) resolveTriageChief() team.Agent {
	chief := r.primaryChief()
	if r.autoReviewMode {
		if arm := strings.TrimSpace(r.Project.Settings.AutoReview.Model); arm != "" {
			chief.Model = arm
			chief.Provider = team.NormalizeProvider("", arm)
			return chief
		}
	}
	if r.reviewRequested {
		if rm := strings.TrimSpace(r.Project.Settings.ReviewModel); rm != "" {
			chief.Model = rm
			chief.Provider = team.NormalizeProvider("", rm)
		}
		return chief
	}
	if isLightOperationalObjective(r.objectiveText) {
		if light, ok := r.resolveLightJevAgent(chief); ok {
			return light
		}
	}
	return chief
}

func (r *Runner) runTriageChief(ctx context.Context, sess session.Session, msg *genai.Content, jiraKey string, mode agent.StreamingMode, runMode string) (bool, string, error) {
	chief := r.resolveTriageChief()

	sddDocs := runMode == "plan:sdd"
	sddJira := runMode == "plan:sdd-jira"
	isAutoReview := runMode == "auto-review"

	var (
		preloadedPR       int
		preloadedHeadRef  string
		preloadedOwnPR    bool
		preloadedAllDiffs bool
		preloadedJiraKey  string
	)
	reviewGH, reviewRepoSlug := r.resolveReviewGitHubAndRepo(ctx, r.objectiveText)
	if (r.reviewRequested || isAutoReview) && !sddDocs && !sddJira && reviewGH != nil && reviewGH.Enabled() {
		if prNum := extractReviewPRNumber(r.objectiveText); prNum > 0 {
			dossier, headRef, isOwn, allComplete := buildPreHydratedPRDossierWithHead(ctx, reviewGH, prNum)
			if dossier != "" {
				preloadedPR = prNum
				preloadedHeadRef = headRef
				preloadedOwnPR = isOwn
				preloadedAllDiffs = allComplete
				msg = appendTextToFirstPart(msg, dossier)
			}
		}
	} else if !sddDocs && !sddJira && r.Jira != nil && r.Jira.Enabled() {
		jiraDossier, resKey := buildPreHydratedJiraDossier(ctx, r.Jira, jiraKey, r.Project.Settings.JiraProject, r.objectiveText)
		if jiraDossier != "" {
			preloadedJiraKey = resKey
			if jiraKey == "" && resKey != "" {
				jiraKey = resKey
				r.issueKey = resKey
			}
			msg = appendTextToFirstPart(msg, jiraDossier)
		}
	}

	toolRoot := r.Workspace
	if strings.TrimSpace(r.ReviewRoot) != "" && (isAutoReview || r.reviewRequested) {
		toolRoot = r.ReviewRoot
	} else if (r.reviewRequested || isAutoReview) && preloadedPR > 0 && !preloadedOwnPR && strings.TrimSpace(r.Workspace) != "" {
		if wtPath, wtCleanup, wtErr := prepareEphemeralReviewWorktree(
			ctx,
			r.Git,
			r.Workspace,
			reviewRepoSlug,
			preloadedPR,
			preloadedHeadRef,
			r.conversationSlot(),
		); wtErr == nil && wtPath != "" {
			defer wtCleanup()
			toolRoot = wtPath
			msg = appendTextToFirstPart(msg, fmt.Sprintf(
				"\n- **Worktree Efêmera do PR #%d pronta:** `%s` (checkout destacado do commit exato do PR via `pull/%d/head`; `read_file`, `search_text` e `run_shell` já apontam para esta worktree — rode os testes reais dos módulos alterados aqui e NUNCA diga que a branch do PR não consta no clone local).\n",
				preloadedPR, wtPath, preloadedPR,
			))
		}
	} else if !sddDocs && !sddJira && !isAutoReview {
		if wtPath, effBranch, remoteBranch, wtErr := r.ensureConversationWorktree(ctx, jiraKey, preloadedPR, preloadedHeadRef, preloadedOwnPR, runMode); wtErr == nil && wtPath != "" {
			toolRoot = wtPath
			r.deliveryRoot = wtPath
			if effBranch != "" {
				r.deliveryBranch = effBranch
			}
			pushHint := ""
			if remoteBranch != "" && effBranch != remoteBranch {
				pushHint = fmt.Sprintf(" Para enviar commits ao PR, use `git push origin HEAD:%s`.", remoteBranch)
			}
			prHint := ""
			if preloadedPR > 0 {
				prHint = fmt.Sprintf(" (espelhando o PR #%d)", preloadedPR)
			}
			msg = appendTextToFirstPart(msg, fmt.Sprintf(
				"\n- **Worktree Isolada desta Conversa pronta:** `%s` (branch `%s`%s). Todas as ferramentas (`read_file`, `write_file`, `edit_file`, `run_shell`, `git_*`) já operam exclusivamente dentro desta worktree isolada. **NUNCA** faça `cd`, `git checkout` ou `git merge` na pasta principal do workspace (`%s`) — resolva edições, conflitos e testes aqui dentro sem interferir em conversas paralelas.%s\n",
				wtPath, effBranch, prHint, r.Workspace, pushHint,
			))
		} else if r.resumedSession && r.Git != nil && strings.TrimSpace(r.Workspace) != "" {
			if slotWT := workspace.WorktreePath(r.Workspace, r.conversationSlot()); r.Git.IsRepo(ctx, slotWT) {
				toolRoot = slotWT
				r.deliveryRoot = slotWT
			}
		}
	} else if r.resumedSession && r.Git != nil && strings.TrimSpace(r.Workspace) != "" {
		if slotWT := workspace.WorktreePath(r.Workspace, r.conversationSlot()); r.Git.IsRepo(ctx, slotWT) {
			toolRoot = slotWT
		}
	}
	if !sddDocs && !sddJira && !isAutoReview && strings.TrimSpace(toolRoot) != "" {
		if vSum, vErr := vaultx.GenerateWorktreeVault(vaultx.Options{
			Root:      toolRoot,
			Branch:    r.deliveryBranch,
			LaneSlot:  r.conversationSlot(),
			Objective: r.objectiveText,
			JiraKey:   jiraKey,
			PRNumber:  preloadedPR,
		}); vErr == nil && vSum.PromptBanner != "" {
			msg = appendTextToFirstPart(msg, vSum.PromptBanner)
		}
	}
	triageStep := "triage_chief"
	if isLightOperationalObjective(r.objectiveText) && !sddDocs && !sddJira && !isAutoReview {
		triageStep = "light_ops"
	}
	triageCtx := modelx.WithAgentDir(ctx, toolRoot)
	triageCtx = withJevRouteHint(triageCtx, jevRouteHint{
		RunMode:      runMode,
		Step:         triageStep,
		ObjectiveLen: len(r.objectiveText),
		Objective:    r.objectiveText,
		InputTokens:  jev.EstimateFirstStepInputTokens(len(r.objectiveText), len(r.teamMemorySnippet()), true),
		HasImages:    r.hasImages || hasImageContent(msg),
		NeedsTools:   true,
		QAStall:      r.qaStallCount,
		Workspace:    r.Workspace,
		WorkDir:      toolRoot,
		RepoKey:      r.jevRepo,
		Author:       chief.Name,
		Label:        roleLabel(chief.Role) + " · " + chief.Name,
		Stage:        "chief",
		Git:          r.Git,
		Approve:      r.Approve,
		OnEvent:      r.wrapRouteEvent(r.OnEvent),
	})
	llm, err := r.modelFor(triageCtx, chief)
	if err != nil {
		return false, "", fmt.Errorf("modelo de %s: %w", chief.Name, err)
	}

	isDirect := runMode == "direct"
	isAuto := runMode == "" || runMode == "auto"
	allTools, err := r.buildTriageBaseToolsWithGitHub(toolRoot, jiraKey, sddDocs, sddJira, isAutoReview, reviewGH, isDirect || isAuto)
	if err != nil {
		return false, "", err
	}

	baseDevs := r.Team.ByRole(team.RoleDeveloper)
	maxDevs := r.maxAllowedDevs()

	var delegateMu sync.Mutex
	var delegated bool
	var planCardEmitted bool

	if sddDocs {
		docsReady, err := r.newSDDDocsReadyTool(chief)
		if err != nil {
			return false, "", err
		}
		allTools = append(allTools, docsReady)
	}

	if runMode != "direct" && runMode != "auto-review" && !sddDocs {
		delegateTool, err := r.newTriageDelegateTool(chief, sess, runMode, maxDevs, baseDevs, &delegateMu, &delegated, &planCardEmitted)
		if err != nil {
			return false, "", err
		}
		allTools = append(allTools, delegateTool)
	}

	triageInstr := r.buildTriageInstructions(ctx, chief, runMode, maxDevs)

	adkName := names.ADK(chief.Name + "_triage")
	// O Chefe também pode pesquisar com subagente efêmero (contexto limpo) sem
	// queimar seu próprio orçamento de exploração na triagem.
	if sp, pErr := r.newSpawnSubagentTool(chief, adkName, toolRoot, isDirect); pErr == nil {
		allTools = append(allTools, sp)
	}
	var beforeToolCBs []llmagent.BeforeToolCallback
	var afterToolCBs []llmagent.AfterToolCallback
	if runMode == "direct" {
		// Modo Direto: sem tetos artificiais de chamadas, tokens ou exploração.
	} else if (r.reviewRequested || isAutoReview) && !preloadedOwnPR {
		beforeToolCBs = append(beforeToolCBs, newRepeatReadGuardCallback())
		revBefore, revAfter := newPRReviewGuardCallbacks(preloadedPR, preloadedAllDiffs, defaultMaxReviewExploreCalls)
		beforeToolCBs = append([]llmagent.BeforeToolCallback{revBefore}, beforeToolCBs...)
		afterToolCBs = append(afterToolCBs, revAfter)
	} else if !sddDocs && !sddJira {
		beforeToolCBs = append(beforeToolCBs, newRepeatReadGuardCallback())
		if !isAuto {
			triBefore, triAfter := newChiefTriageBudgetCallbacks(preloadedJiraKey, defaultMaxTriageExploreCalls)
			beforeToolCBs = append([]llmagent.BeforeToolCallback{triBefore}, beforeToolCBs...)
			afterToolCBs = append(afterToolCBs, triAfter)
		}
	}
	keepInTurn := r.Project.Settings.EffectiveInTurnKeepToolResults()
	if (r.reviewRequested || isAutoReview) && keepInTurn < 6 {
		keepInTurn = 6
	}
	if ctxWin := agentContextWindow(chief); (runMode == "direct" || isAuto) && (ctxWin >= 200_000 || ctxWin == 0) && keepInTurn < 18 {
		keepInTurn = 18
	}
	triageBeforeModelCBs := []llmagent.BeforeModelCallback{
		r.newTriageTurnIsolationCallback(sess, toolRoot, runMode, &delegateMu, &delegated),
		contextx.NewCompactionCallback(contextx.Budget{
			ContextWindow:   agentContextWindow(chief),
			ContextWindowFn: r.dynamicAgentContextWindow(chief.Name, chief),
			KeepRecent:      keepInTurn,
		}),
	}
	triageAgent, err := llmagent.New(llmagent.Config{
		Name:                     adkName,
		Description:              "Triagem e coordenação do Chefe HiveMind.",
		Model:                    llm,
		Mode:                     llmagent.ModeChat,
		IncludeContents:          llmagent.IncludeContentsDefault,
		DisallowTransferToParent: true,
		DisallowTransferToPeers:  true,
		Tools:                    allTools,
		Instruction:              triageInstr,
		BeforeModelCallbacks:     triageBeforeModelCBs,
		BeforeToolCallbacks:      beforeToolCBs,
		AfterToolCallbacks:       afterToolCBs,
	})
	if err != nil {
		return false, "", err
	}

	triageRunner, err := runner.New(runner.Config{
		AppName:        sessionx.AppName,
		Agent:          triageAgent,
		SessionService: r.Sessions,
	})
	if err != nil {
		return false, "", err
	}

	labels := map[string]string{
		adkName: roleLabel(chief.Role) + " (Chefe) · " + chief.Name,
	}
	if r.OnEvent != nil {
		r.OnEvent(Event{Kind: "stage", Stage: "chief", Author: adkName, Label: labels[adkName]})
	}
	printer := &ui.Printer{Out: r.Out, Labels: labels}
	var summary string
	maxLLMCalls := r.Project.Settings.MaxLLMCalls
	if runMode == "direct" {
		maxLLMCalls = 0
	}
	for ev, err := range triageRunner.Run(ctx, sessionx.UserID, sess.ID(), msg, agent.RunConfig{
		StreamingMode: mode,
		MaxLLMCalls:   maxLLMCalls,
	}) {
		if err != nil {
			return false, "", err
		}
		printer.Event(ev)
		if ev != nil {
			r.recordUsage(ev, labels)
			if r.OnEvent != nil {
				r.emitEvent(ev, labels)
			}
			if runMode != "direct" {
				tokenLimit := r.Project.Settings.MaxTokens
				if isAutoReview {
					tokenLimit = r.Project.Settings.AutoReview.EffectiveMaxTokensPerPR(tokenLimit)
				}
				if tokenLimit > 0 && r.turnTotalTokens > tokenLimit {
					return false, "", fmt.Errorf("limite de tokens por execução atingido (%d > %d tokens)", r.turnTotalTokens, tokenLimit)
				}
			}
		}
		if ev != nil && !ev.Partial && ev.LLMResponse.Content != nil {
			for _, part := range ev.LLMResponse.Content.Parts {
				if part != nil && part.Text != "" && !part.Thought {
					summary = strings.TrimSpace(part.Text)
				}
			}
		}
	}
	delegateMu.Lock()
	wasDelegated := delegated
	wasPlanCard := planCardEmitted
	delegateMu.Unlock()
	if !wasDelegated && r.resumedSession && runMode != "direct" && runMode != "auto-review" && !sddDocs {
		if restored := r.tryRestorePlanAfterTriageAbort(sess); restored {
			wasDelegated = true
		}
	}
	if wasDelegated {
		r.sealHandoff(sess.ID())
		stateVals := map[string]any{}
		if r.chiefPlan != "" {
			stateVals["chief_plan"] = r.chiefPlan
		}
		if r.chiefContextBrief != "" {
			stateVals["chief_context_brief"] = r.chiefContextBrief
		}
		if len(r.devSlices) > 0 {
			stateVals["dev_slices_joined"] = strings.Join(r.devSlices, "\n|||\n")
		}
		if len(r.devOwnedPaths) > 0 {
			stateVals["dev_owned_paths_json"] = marshalOwnedPaths(r.devOwnedPaths)
		}
		if r.devSequential {
			stateVals["dev_sequential"] = "1"
		}
		if len(stateVals) > 0 {
			r.updateStateChecked(sess, stateVals)
		}
	}
	if runMode == "plan" && !wasPlanCard && strings.TrimSpace(summary) != "" && r.OnEvent != nil {
		r.OnEvent(Event{
			Kind:   "plan_ready",
			Stage:  "chief",
			Author: chief.Name,
			Label:  "Plano pronto para aprovação",
			Text:   strings.TrimSpace(summary),
		})
	}
	return wasDelegated, summary, nil
}

// newTriageTurnIsolationCallback prevents two major pathologies in runTriageChief:
//  1. Post-delegation loop: once delegate_to_squad has been called, it immediately terminates
//     the triage turn with a TurnComplete LLMResponse instead of making another 75k-token LLM
//     call where the Chief hallucinates that the Squad already ran and loops on git_status/git_diff.
//  2. Stale session history pollution: when a session is resumed (IncludeContentsDefault), ADK
//     populates req.Contents with raw FunctionCall/FunctionResponse events from earlier runs
//     (including old delegate_to_squad and Developer tool calls). Slicing req.Contents to the
//     current user turn and injecting a clean <system-reminder> with prior_session_context +
//     current worktree git status keeps triage fast (~3k tokens instead of 75k) and prevents
//     the Chief from mistaking itself for the Closing Chief.
func (r *Runner) newTriageTurnIsolationCallback(
	sess session.Session,
	toolRoot string,
	runMode string,
	delegateMu *sync.Mutex,
	delegated *bool,
) llmagent.BeforeModelCallback {
	var postDelegateModelCalls int
	return func(ctx agent.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
		if delegateMu != nil && delegated != nil {
			delegateMu.Lock()
			done := *delegated
			if done {
				postDelegateModelCalls++
			}
			callsAfterDelegate := postDelegateModelCalls
			delegateMu.Unlock()
			// Allow at most 1 final text summarization call after delegate_to_squad;
			// if the model attempted another tool call after delegating (callsAfterDelegate > 1),
			// terminate the triage turn immediately so it can never loop on git_status/git_diff.
			if done && callsAfterDelegate > 1 {
				summaryText := "Plano técnico e dossiê de handoff registrados — iniciando execução do Squad na worktree da conversa."
				if runMode == "plan" || r.pauseForApproval {
					summaryText = "Plano técnico registrado — aguardando aprovação do usuário para iniciar o Squad."
				} else if runMode == "plan:sdd-jira" {
					summaryText = "Backlog Jira e ondas de desenvolvimento registrados — aguardando aprovação no Gate 2."
				}
				return &model.LLMResponse{
					Content:      &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{Text: summaryText}}},
					TurnComplete: true,
				}, nil
			}
		}
		if req == nil || len(req.Contents) == 0 {
			return nil, nil
		}

		// Find the index of the latest real user prompt (a RoleUser Content with text and no FunctionResponse).
		lastUserIdx := -1
		for i := len(req.Contents) - 1; i >= 0; i-- {
			c := req.Contents[i]
			if c == nil || c.Role != genai.RoleUser {
				continue
			}
			hasText := false
			hasFuncResp := false
			for _, p := range c.Parts {
				if p == nil {
					continue
				}
				if p.FunctionResponse != nil {
					hasFuncResp = true
				}
				if strings.TrimSpace(p.Text) != "" {
					hasText = true
				}
			}
			if hasText && !hasFuncResp {
				lastUserIdx = i
				break
			}
		}
		if lastUserIdx > 0 {
			req.Contents = append([]*genai.Content(nil), req.Contents[lastUserIdx:]...)
		}

		if !r.resumedSession || sess == nil {
			return nil, nil
		}
		var b strings.Builder
		if rawPrior, err := sess.State().Get("prior_session_context"); err == nil && rawPrior != nil {
			if prior := strings.TrimSpace(fmt.Sprint(rawPrior)); prior != "" {
				fmt.Fprintf(&b, "[Contexto Resumido da Sessão Anterior]\n%s\n\n", prior)
			}
		}
		if rawPlan, err := sess.State().Get("chief_plan"); err == nil && rawPlan != nil {
			if plan := strings.TrimSpace(fmt.Sprint(rawPlan)); plan != "" {
				fmt.Fprintf(&b, "[Plano Técnico Anterior da Sessão (reutilize ou complemente e chame delegate_to_squad)]\n%s\n\n", plan)
			}
		}
		if r.Git != nil && strings.TrimSpace(toolRoot) != "" && r.Git.IsRepo(ctx, toolRoot) {
			if st, stErr := r.Git.Status(ctx, toolRoot); stErr == nil && strings.TrimSpace(st) != "" {
				fmt.Fprintf(&b, "[Arquivos Já Criados/Modificados na Worktree da Conversa (%s)]\n%s\n(ATENÇÃO: a entrega ainda NÃO foi aprovada pelo QA. Se ainda faltam itens ou validação do QA, chame IMEDIATAMENTE delegate_to_squad para o Desenvolvedor e o QA concluírem na worktree — NÃO fique rodando git_status/git_diff em loop na triagem.)\n\n", toolRoot, strings.TrimSpace(st))
			}
		}
		if rem := strings.TrimSpace(b.String()); rem != "" {
			appendSystemReminder(req, rem)
		}
		return nil, nil
	}
}

func (r *Runner) tryRestorePlanAfterTriageAbort(sess session.Session) bool {
	if sess == nil {
		return false
	}
	qaApprovedFlag, _ := sess.State().Get("qa_approved")
	if strings.TrimSpace(fmt.Sprint(qaApprovedFlag)) == "1" {
		return false
	}
	if rawPlan, err := sess.State().Get("chief_plan"); err == nil && rawPlan != nil {
		if plan := strings.TrimSpace(fmt.Sprint(rawPlan)); plan != "" {
			r.chiefPlan = plan
		}
	}
	if rawBrief, err := sess.State().Get("chief_context_brief"); err == nil && rawBrief != nil {
		if brief := strings.TrimSpace(fmt.Sprint(rawBrief)); brief != "" {
			r.chiefContextBrief = brief
		}
	}
	if rawSlices, err := sess.State().Get("dev_slices_joined"); err == nil && rawSlices != nil {
		if joined := strings.TrimSpace(fmt.Sprint(rawSlices)); joined != "" {
			r.devSlices = strings.Split(joined, "\n|||\n")
		}
	}
	if r.chiefPlan == "" {
		if pkg, ok := loadPersistedHandoff(sess.ID()); ok {
			r.chiefPlan = strings.TrimSpace(pkg.Objective)
			if r.chiefContextBrief == "" && len(pkg.Facts) > 0 {
				r.chiefContextBrief = strings.Join(pkg.Facts, "\n")
			}
		}
	}
	if r.chiefPlan == "" {
		return false
	}
	if len(r.devSlices) == 0 {
		r.devSlices = []string{r.chiefPlan}
	}
	return true
}

// ensureConversationWorktree guarantees that EVERY conversation (regardless of
// whether it's an own PR, third-party PR, Direto, Auto, Squad, or parallel lane)
// receives its own isolated git worktree keyed by conversationSlot() so parallel
// conversations NEVER share or collide on the main workspace checkout.
func (r *Runner) ensureConversationWorktree(
	ctx context.Context,
	jiraKey string,
	preloadedPR int,
	preloadedHeadRef string,
	preloadedOwnPR bool,
	runMode string,
) (wtPath string, effBranch string, remoteBranch string, err error) {
	allowWorktrees := r.Project.Settings.Worktrees == nil || *r.Project.Settings.Worktrees
	if !allowWorktrees || r.Git == nil || strings.TrimSpace(r.Workspace) == "" {
		return "", "", "", nil
	}
	if !r.Git.IsRepo(ctx, r.Workspace) || !r.Git.HasCommit(ctx, r.Workspace) {
		return "", "", "", nil
	}

	targetBranch := strings.TrimSpace(preloadedHeadRef)
	if targetBranch == "" {
		targetBranch = strings.TrimSpace(r.prBranch)
	}
	prNum := preloadedPR
	if prNum <= 0 {
		prNum = extractReviewPRNumber(r.objectiveText)
	}
	// Resolve the PR branch automatically even when reviewRequested was false
	// (e.g. own-PR feedback/merge turns in Direto mode).
	if targetBranch == "" && prNum > 0 && r.GitHub != nil && r.GitHub.Enabled() {
		if pr, gErr := r.GitHub.GetPullRequest(ctx, prNum); gErr == nil && strings.TrimSpace(pr.HeadRef) != "" {
			targetBranch = strings.TrimSpace(pr.HeadRef)
		}
	}

	// Isolate every named conversation lane, every Direct run, and every PR turn
	// (own PR or third-party PR alike).
	if runMode != "direct" && strings.TrimSpace(r.LaneID) == "" && prNum <= 0 {
		return "", "", "", nil
	}

	_ = gitx.EnsureHiveIgnore(r.Workspace)
	slot := r.conversationSlot()
	preserveExisting := targetBranch != "" && (preloadedOwnPR || prNum > 0 || strings.TrimSpace(r.prBranch) != "")

	// When continuing the same conversation without switching to a specific PR branch,
	// reuse its existing isolated worktree so uncommitted edits stay intact.
	slotWT := workspace.WorktreePath(r.Workspace, slot)
	if !preserveExisting && r.Git.IsRepo(ctx, slotWT) {
		curBr, _ := r.Git.CurrentBranch(ctx, slotWT)
		return slotWT, strings.TrimSpace(curBr), "", nil
	}

	if targetBranch == "" {
		targetBranch = names.DeliveryBranch(jiraKey, r.objectiveText)
		if targetBranch == "" {
			targetBranch = "hivemind/" + slot
		}
	}
	wt, eff, prepErr := gitx.PrepareDeliveryWorktreeForSlot(ctx, r.Git, r.Workspace, slot, targetBranch, preserveExisting)
	if prepErr != nil {
		if r.Err != nil {
			fmt.Fprintf(r.Err, "aviso: worktree isolada da conversa (%s): %v\n", slot, prepErr)
		}
		return "", "", "", prepErr
	}
	if nested, nErr := gitx.PrepareNestedWorktrees(ctx, r.Git, r.Workspace, wt, eff, preserveExisting); nErr == nil {
		r.nestedWorktrees = nested
	}
	_, _ = gitx.HydrateWorktree(r.Workspace, wt, r.Project.Settings.SharedDirectories)
	remote := ""
	if preserveExisting {
		remote = targetBranch
	}
	return wt, eff, remote, nil
}
