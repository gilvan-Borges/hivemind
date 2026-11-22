package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"iter"
	"strings"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/agent/workflowagents/loopagent"
	"google.golang.org/adk/v2/agent/workflowagents/parallelagent"
	"google.golang.org/adk/v2/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"

	"github.com/gilvan-Borges/HiveMind/internal/contextx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/names"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

type devTurnBudget struct {
	calls       int
	explore     int
	writes      int
	inputTokens int64
}

var devExploreTools = map[string]bool{
	"read_file": true, "search_text": true, "find_files": true, "list_dir": true,
	"git_status": true, "git_diff": true, "git_log": true,
	"github_list_prs": true, "github_pr_files": true, "github_get_pr": true, "github_pr_diff": true,
	"jira_get": true, "jira_search": true,
}

var devWriteTools = map[string]bool{
	"edit_file": true, "write_file": true, "write_doc": true,
}

func (r *Runner) resetDevBudgets() {
	r.devBudgets = map[string]*devTurnBudget{}
}

// checkRunBudget enforces the run-wide token and USD ceilings on the event
// stream. Tokens were already gated here; the USD ceiling closes the gap
// where a QA loop on an expensive model burns dollars while staying under
// max_tokens (cost is measured per call, but the budget is only enforced now).
func (r *Runner) checkRunBudget() error {
	if tot := r.currentTotalTokens(); r.Project.Settings.MaxTokens > 0 && tot > r.Project.Settings.MaxTokens {
		return fmt.Errorf("limite de tokens por execução atingido (%d > %d tokens)", tot, r.Project.Settings.MaxTokens)
	}
	if maxMicro := r.Project.Settings.EffectiveMaxRunCostUSD(); maxMicro > 0 {
		r.costMu.Lock()
		spent := r.turnCostMicroUSD
		r.costMu.Unlock()
		if spent > maxMicro {
			return fmt.Errorf("orçamento de custo por execução atingido (%s > %s) — reduza max_qa_loops, troque para um modelo mais barato ou ajuste max_run_cost_usd",
				jev.FormatMicroUSD(spent), jev.FormatMicroUSD(maxMicro))
		}
	}
	return nil
}

// checkDevGuard enforces per-dev performance budgets on the event stream.
func (r *Runner) checkDevGuard(ev *session.Event, labels map[string]string) error {
	if ev == nil || ev.Partial || ev.Author == "" {
		return nil
	}
	label := labels[ev.Author]
	stage := inferStage(ev.Author, label)
	if stage == "qa" {
		r.resetDevBudgets()
		return nil
	}
	if stage != "dev" {
		return nil
	}
	if r.devBudgets == nil {
		r.devBudgets = map[string]*devTurnBudget{}
	}
	author := strings.ToLower(strings.TrimSpace(ev.Author))
	b := r.devBudgets[author]
	if b == nil {
		b = &devTurnBudget{}
		r.devBudgets[author] = b
	}

	if u := ev.LLMResponse.UsageMetadata; u != nil && u.PromptTokenCount > 0 {
		b.inputTokens += int64(u.PromptTokenCount)
	}
	if ev.LLMResponse.Content != nil {
		for _, part := range ev.LLMResponse.Content.Parts {
			if part == nil || part.FunctionCall == nil {
				continue
			}
			name := strings.ToLower(strings.TrimSpace(part.FunctionCall.Name))
			b.calls++
			if devExploreTools[name] {
				b.explore++
			}
			if devWriteTools[name] {
				b.writes++
				b.explore = 0
			}
		}
	}

	if maxCalls := r.Project.Settings.EffectiveMaxDevCalls(); b.calls > maxCalls {
		return fmt.Errorf("desempenho do dev %q: %d chamadas de LLM nesta rodada (limite %d) sem concluir a entrega — execução interrompida para economizar tokens. Divida a tarefa em fatias menores, detalhe os arquivos exatos no plano do Chefe ou use um modelo mais rápido para os devs.", ev.Author, b.calls, maxCalls)
	}
	if maxExplore := r.Project.Settings.EffectiveMaxDevExploreCalls(); b.explore >= maxExplore && b.writes == 0 {
		return fmt.Errorf("desempenho do dev %q: %d buscas/leituras seguidas sem editar NENHUM arquivo — padrão de loop exploratório detectado; execução interrompida antes de queimar mais tokens. A fatia provavelmente está grande ou ambígua demais: reduza o escopo no dev_tasks ou especifique os arquivos/trechos exatos no plano do Chefe.", ev.Author, b.explore)
	}
	if maxInput := r.Project.Settings.EffectiveMaxDevInputTokens(); b.inputTokens > maxInput {
		return fmt.Errorf("orçamento do dev %q: %d tokens de entrada nesta rodada (limite %d) sem concluir a entrega — execução interrompida. Reduza o escopo da fatia ou troque o modelo do dev por um mais barato/rápido.", ev.Author, b.inputTokens, maxInput)
	}
	return nil
}

func (r *Runner) build(ctx context.Context, devs []devEnv, key, objective string) (agent.Agent, map[string]string, error) {
	labels := map[string]string{}
	used := map[string]int{
		"pipeline_root": 1, "parallel_developers": 1, "loop_qa": 1, "sequential_qa": 1,
	}
	unique := func(name string) string {
		base := names.ADK(name)
		n := used[base]
		used[base]++
		if n == 0 {
			return base
		}
		return fmt.Sprintf("%s_%d", base, n+1)
	}
	stateKeys := []string{"objective", "jira_key", "prior_session_context", "chief_plan", "chief_context_brief", "pm_plan", "tech_lead_plan", "architect_plan", "integrate_status", "qa_feedback", "qa_report", "qa_approval_note"}

	qaRoot := r.Workspace
	if r.deliveryRoot != "" {
		qaRoot = r.deliveryRoot
	} else if r.integration != "" {
		qaRoot = r.integration
	}

	var steps []agent.Agent
	addGroup := func(output string, agents []team.Agent, root, extra string, disableGitWrite bool) error {
		for i, a := range agents {
			name := unique(a.Name)
			keyName := output
			if output == "" {
				keyName = name + "_report"
			} else if i > 0 {
				keyName = fmt.Sprintf("%s_%d", output, i+1)
			}
			stateKeys = append(stateKeys, keyName)
			ag, err := r.llm(ctx, a, name, keyName, root, extra, key, stateKeys, false, disableGitWrite)
			if err != nil {
				return err
			}
			labels[name] = roleLabel(a.Role) + " · " + a.Name
			steps = append(steps, ag)
		}
		return nil
	}
	chiefSkillGuide := r.guidanceForRole(team.SkillTargetChief, false)
	joinExtra := func(base string) string {
		if chiefSkillGuide == "" {
			return base
		}
		if base == "" {
			return chiefSkillGuide
		}
		return base + chiefSkillGuide
	}
	if r.chiefPlan != "" {
		if err := addGroup("tech_lead_plan", r.Team.ByRole(team.RoleTechLead), qaRoot, joinExtra("Refine brevemente os pontos críticos de implementação sem repetir o plano do Chefe."), true); err != nil {
			return nil, nil, err
		}
	} else {
		if err := addGroup("pm_plan", r.Team.ByRole(team.RoleProjectManager), qaRoot, joinExtra(""), true); err != nil {
			return nil, nil, err
		}
		if err := addGroup("tech_lead_plan", r.Team.ByRole(team.RoleTechLead), qaRoot, joinExtra(""), true); err != nil {
			return nil, nil, err
		}
		if err := addGroup("architect_plan", r.Team.ByRole(team.RoleArchitect), qaRoot, joinExtra(""), true); err != nil {
			return nil, nil, err
		}
	}

	var devAgents []agent.Agent
	var wtLines []string
	var devReports []string
	devSkillGuide := r.guidanceForRole(team.SkillTargetDev, true)
	for idx, dev := range devs {
		name := unique(dev.Agent.Name)
		report := "dev_" + name + "_report"
		devReports = append(devReports, report)
		devStateKeys := append([]string(nil), stateKeys...)
		scopeDesc := "worktree unificada da conversa"
		if !r.sharedDevWorktree {
			scopeDesc = "worktree isolada"
		}
		extra := fmt.Sprintf("Seu diretório de trabalho (%s) é %s.\nSua branch é %s.\nO Chefe JÁ leu a spec/plano e registrou os contratos essenciais em [chief_plan] e [chief_context_brief]. NÃO releia spec.md/plan.md/tasks.md inteiros em loop; leia apenas os arquivos de código que precisa editar/chamar (máx. 3-4 leituras por turno) e comece a implementar logo.\nREGRA DE PARADA POR RETORNO DECRESCENTE (Diminishing-Return Stop Test): se 2 buscas/leituras seguidas retornarem arquivos já conhecidos ou duplicados, PARE de explorar imediatamente e comece a criar/editar os arquivos alvo com write_file/edit_file.\nMESA REDONDA AO VIVO (Chefe ↔ Dev ↔ QA): você pode conversar em tempo real com o Chefe e o time! Se tiver qualquer dúvida sobre qual arquivo/classe editar, encontrar uma dependência ausente na branch ou bater em um erro de compilação/teste, chame `ask_chief` para o Chefe te direcionar ao vivo na hora (ou `share_team_note` para alinhar contratos com outros Devs e o QA). Quando o Chefe publicar um `[🧭 MESA REDONDA — DIRECIONAMENTO AO VIVO]` ou `<agent_message_board_notification>`, siga a orientação dele imediatamente.\nORÇAMENTO RÍGIDO: você tem até %d chamadas de LLM nesta rodada e no máximo %d leituras/buscas antes da primeira edição — se estourar, a execução inteira é interrompida. Localize os trechos com 1-2 buscas, edite e siga em frente.\nPROIBIDO ENCERRAR SEM CÓDIGO: em tarefas de implementação, nunca finalize a rodada apenas analisando ou listando pendências sem criar ou editar arquivos.\nDEPENDÊNCIA PENDENTE NA BRANCH: se uma task pré-requisito citada na issue ainda não existir na branch, NÃO pare de mãos vazias — implemente todos os contratos, eventos, serviços, consumers e testes unitários da SUA fatia de forma desacoplada e compilável e documente o ponto de acoplamento no resumo.\nPara modificar arquivos existentes, use SEMPRE edit_file (preserva bytes/encoding/CRLF e economiza tokens); reserve write_file para arquivos novos e nunca use scripts python/node via run_shell para editar texto.\nNÃO execute git commit, git push nem abra PR (apenas o Chefe ou o Arquiteto comitam na worktree da conversa após aprovação do QA).\nNão altere arquivos fora desse diretório.\nSe houver [qa_feedback] ou [qa_report], corrija prioritariamente todos os pontos apontados pelo QA e pelo Chefe.", scopeDesc, dev.Root, dev.Branch, r.Project.Settings.EffectiveMaxDevCalls(), r.Project.Settings.EffectiveMaxDevExploreCalls())
		if hint := r.nestedWorktreesPromptHint(); hint != "" {
			extra += "\n" + hint
		}
		if idx < len(r.devSlices) && strings.TrimSpace(r.devSlices[idx]) != "" {
			extra += fmt.Sprintf("\n\n[Sua Subtarefa Exclusiva atribuída pelo Chefe]\n%s\nRestrinja suas edições aos arquivos desta subtarefa para evitar conflitos com outros desenvolvedores.", strings.TrimSpace(r.devSlices[idx]))
		} else if len(devs) > 1 {
			extra += fmt.Sprintf("\n\nVocê é o desenvolvedor %d de %d (%s). Respeite a divisão de arquivos do plano para não colidir com os demais desenvolvedores.", idx+1, len(devs), dev.Agent.Name)
		}
		if len(dev.OwnedPaths) > 0 {
			extra += fmt.Sprintf("\n\n[Posse Exclusiva de Arquivos (travada pelo orquestrador)]\nVocê SÓ tem permissão para gravar/editar dentro de: `%s`. Use `share_team_note` se precisar publicar contratos de API/tipos para os colegas.", strings.Join(dev.OwnedPaths, "`, `"))
		}
		if devSkillGuide != "" {
			extra += devSkillGuide
		}
		var (
			ag  agent.Agent
			err error
		)
		if adapter, extOK := r.externalAdapterFor(dev.Agent); extOK {
			sliceText := ""
			if idx < len(r.devSlices) {
				sliceText = r.devSlices[idx]
			}
			ag, err = r.externalWorkerAgent(dev, name, report, sliceText, adapter)
		} else {
			ag, err = r.llmCore(ctx, dev.Agent, name, report, dev.Root, extra, key, devStateKeys, false, true, dev.OwnedPaths)
		}
		if err != nil {
			return nil, nil, err
		}
		labels[name] = roleLabel(dev.Agent.Role) + " · " + dev.Agent.Name
		devAgents = append(devAgents, ag)
		wtLines = append(wtLines, dev.ADK+": "+dev.Root+" ("+dev.Branch+")")
	}
	stateKeys = append(stateKeys, devReports...)

	var devsRunner agent.Agent
	var err error
	if len(devAgents) == 1 {
		devsRunner = devAgents[0]
	} else if r.devSequential {
		devsRunner, err = sequentialagent.New(sequentialagent.Config{AgentConfig: agent.Config{
			Name:        "sequential_developers",
			Description: "Desenvolvedores trabalhando em ondas sequenciais na mesma worktree da conversa.",
			SubAgents:   devAgents,
		}})
		if err != nil {
			return nil, nil, err
		}
	} else {
		devsRunner, err = parallelagent.New(parallelagent.Config{AgentConfig: agent.Config{
			Name:        "parallel_developers",
			Description: "Desenvolvedores trabalhando em paralelo com isolamento de worktree ou posse disjunta de arquivos.",
			SubAgents:   devAgents,
		}})
		if err != nil {
			return nil, nil, err
		}
	}

	qaAgents := r.Team.ByRole(team.RoleQA)
	qaExtra := "Worktree da conversa: " + qaRoot
	if !r.sharedDevWorktree {
		qaExtra = "Worktrees dos devs:\n" + strings.Join(wtLines, "\n") + "\nAs alterações foram consolidadas na worktree separada de entrega: " + qaRoot
	}
	if hint := r.nestedWorktreesPromptHint(); hint != "" {
		qaExtra += "\n" + hint
	}
	if qaSkillGuide := r.guidanceForRole(team.SkillTargetQA, true); qaSkillGuide != "" {
		qaExtra += qaSkillGuide
	}
	var qaSubs []agent.Agent
	for i, a := range qaAgents {
		name := unique(a.Name)
		report := "qa_report"
		if i > 0 {
			report = fmt.Sprintf("qa_report_%d", i+1)
		}
		stateKeys = append(stateKeys, report)
		ag, err := r.llm(ctx, a, name, report, qaRoot, qaExtra, key, stateKeys, true, true)
		if err != nil {
			return nil, nil, err
		}
		labels[name] = roleLabel(a.Role) + " · " + a.Name
		qaSubs = append(qaSubs, ag)
	}
	var qa agent.Agent
	if len(qaSubs) == 1 {
		qa = qaSubs[0]
	} else {
		qa, err = sequentialagent.New(sequentialagent.Config{AgentConfig: agent.Config{
			Name:        "sequential_qa",
			Description: "Revisores de QA em sequência.",
			SubAgents:   qaSubs,
		}})
		if err != nil {
			return nil, nil, err
		}
	}
	loops := r.Project.Settings.MaxQALoops
	if loops <= 0 {
		loops = 3
	}
	loopSubs := []agent.Agent{devsRunner}
	if !r.sharedDevWorktree {
		integrate, err := r.newIntegrateAgent(devs)
		if err != nil {
			return nil, nil, err
		}
		loopSubs = append(loopSubs, integrate)
	}
	loopSubs = append(loopSubs, qa)
	loop, err := loopagent.New(loopagent.Config{
		MaxIterations: uint(loops),
		AgentConfig: agent.Config{
			Name:        "loop_qa",
			Description: "Desenvolvedores e QA se alternam até os testes passarem.",
			SubAgents:   loopSubs,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	steps = append(steps, loop)

	if r.EnableClosingChief {
		chief := r.primaryChief()
		closingName := unique(chief.Name + "_fecho")
		closingAgent := chief
		closingAgent.Role = team.RoleProjectManager
		closingExtra := "Você é o Chefe (Fecho) encerrando a entrega na worktree separada (" + qaRoot + ", branch " + r.deliveryBranch + ") após aprovação do QA.\n" +
			"GOVERNANÇA GIT & PR:\n" +
			"- Apenas você (Chefe) ou o Arquiteto realizam commit, push e operações de Pull Request.\n"
		if r.prNumber > 0 {
			closingExtra += fmt.Sprintf("- Esta tarefa atende revisão do PR #%d (revisores: %s). Se houver alterações prontas, faça git_commit, git_push, responda ponto a ponto o que foi ajustado com github_comment_pr(number=%d) (e github_reply_review_comment para comentários inline) e peça nova revisão com github_request_review.\n",
				r.prNumber, strings.Join(r.prReviewers, ", "), r.prNumber)
		}
		closingExtra += "Revise [qa_report], [qa_approval_note], [qa_feedback] e use git_diff se necessário.\nEntregue ao usuário um resumo executivo claro em Markdown contendo:\n1. O que foi implementado/alterado;\n2. Resultado da revisão/testes do QA;\n3. Branch/PR e próximos passos."
		if chiefSkillGuide != "" {
			closingExtra += chiefSkillGuide
		}
		ag, err := r.llm(ctx, closingAgent, closingName, "closing_summary", qaRoot, closingExtra, key, stateKeys, false, false)
		if err != nil {
			return nil, nil, err
		}
		labels[closingName] = "Chefe (Fecho) · " + chief.Name
		steps = append(steps, ag)
	}

	root, err := sequentialagent.New(sequentialagent.Config{AgentConfig: agent.Config{
		Name:        "pipeline_root",
		Description: "Pipeline do time HiveMind: planejamento, implementação paralela em worktrees e QA.",
		SubAgents:   steps,
	}})
	if err != nil {
		return nil, nil, err
	}
	return root, labels, nil
}

// reminderBlock é um bloco de contexto injetável no último user content,
// identificado por um marcador estável usado para deduplicação.
type reminderBlock struct {
	marker string
	text   string
}

// newDynamicContextReminderCallback injeta o estado compartilhado do pipeline
// como <system-reminder> anexado ao último user content. Blocos de estado que
// não mudaram desde a última injeção E cujo texto atual ainda está presente no
// transcript são omitidos: sem isso, planos, dossiês e reports de todos os
// agentes eram re-enviados a cada chamada de modelo, crescendo linearmente.
func (r *Runner) newDynamicContextReminderCallback(adkName string, a team.Agent, qa bool, outputKey string, keys []string) llmagent.BeforeModelCallback {
	lastInjected := map[string]string{}
	return func(ctx agent.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
		if req == nil {
			return nil, nil
		}
		var qaFeedback string
		if rawFB, err := ctx.ReadonlyState().Get("qa_feedback"); err == nil && rawFB != nil {
			qaFeedback = strings.TrimSpace(fmt.Sprint(rawFB))
		}
		hasQAFeedback := qaFeedback != ""

		worktreeEmpty := false
		var worktreeStatus string
		if r.Git != nil && r.deliveryRoot != "" && (qa || a.Role == team.RoleDeveloper) {
			checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			st, stErr := r.compositeStatus(checkCtx)
			cancel()
			if stErr == nil {
				worktreeStatus = strings.TrimSpace(st)
				if worktreeStatus == "" {
					worktreeEmpty = true
				}
			}
		}

		// Blocos situacionais pequenos e notificações não-lidas: sempre injetados.
		var fixed strings.Builder
		if outputKey == "closing_summary" && !qaApprovedReadonly(ctx.ReadonlyState()) {
			fixed.WriteString("[GATE DE QA — BLOQUEIO ATIVO]\nO QA NÃO aprovou esta entrega (`qa_approve` não foi chamado). Ferramentas de commit, push e PR estão bloqueadas. NÃO tente commitar, dar push ou abrir/atualizar PR; apenas apresente um resumo claro do que foi tentado, do feedback do QA e do que ficou pendente na worktree.\n\n")
		}
		if a.Role == team.RoleDeveloper && hasQAFeedback {
			if worktreeEmpty {
				fixed.WriteString("[RETOMADA APÓS REPROVAÇÃO DO QA — WORKTREE VAZIA]\nATENÇÃO CRÍTICA: nenhum arquivo foi criado ou editado na rodada anterior. NÃO encerre novamente apenas com diagnóstico ou lista de pendências. Se uma dependência de outra task ainda não estiver integrada na branch, implemente os contratos, eventos, serviços, consumidores e testes da SUA tarefa de forma desacoplada e compilável AGORA usando write_file/edit_file e documente o ponto de acoplamento no resumo.\n\n")
			} else {
				fixed.WriteString("[RETOMADA APÓS REPROVAÇÃO DO QA — AÇÃO OBRIGATÓRIA]\nCorrija prioritariamente TODOS os pontos apontados em [qa_feedback] com edit_file/write_file e valide os testes antes de encerrar.\n\n")
			}
		}
		if qa && worktreeEmpty {
			fixed.WriteString("[ESTADO DA WORKTREE: VAZIA]\nNenhum arquivo foi criado ou modificado na worktree (git status vazio). Se a tarefa exige código/alterações e não há nada implementado, chame qa_request_changes IMEDIATAMENTE em 1 único turno listando os arquivos/classes concretos que o Dev precisa criar, sem gastar turnos explorando o repositório inteiro.\n\n")
		}

		// Blocos grandes (git status, state keys, TeamBoard): deduplicados.
		var dedup []reminderBlock
		if a.Role == team.RoleDeveloper && worktreeStatus != "" {
			dedup = append(dedup, reminderBlock{
				marker: "[ARQUIVOS JÁ CRIADOS/MODIFICADOS NESTA WORKTREE",
				text:   fmt.Sprintf("[ARQUIVOS JÁ CRIADOS/MODIFICADOS NESTA WORKTREE (git status)]\n%s\n(Estes arquivos JÁ estão gravados na sua worktree. Se houve troca de modelo via failover ou compactação de contexto, NÃO recomece do zero relendo plan.md/tasks.md — verifique apenas o que falta para concluir e entregue ao QA.)\n\n", worktreeStatus),
			})
		}
		for _, k := range keys {
			if a.Role == team.RoleDeveloper && hasQAFeedback && k == outputKey {
				continue
			}
			v, err := ctx.ReadonlyState().Get(k)
			if err != nil || v == nil {
				continue
			}
			text := strings.TrimSpace(fmt.Sprint(v))
			if text == "" {
				continue
			}
			dedup = append(dedup, reminderBlock{
				marker: "[" + k + "]",
				text:   fmt.Sprintf("[%s]\n%s\n\n", k, text),
			})
		}
		if r.sharedBoard != nil && (a.Role == team.RoleDeveloper || qa) {
			if notif := r.sharedBoard.UnreadNotificationFor(adkName); notif != "" {
				fmt.Fprintf(&fixed, "%s\n\n", notif)
			}
			if boardText := r.sharedBoard.SummaryFor(adkName); boardText != "" {
				marker := "[Memória Compartilhada do Time"
				if !strings.Contains(boardText, marker) {
					marker = "[Arquivos modificados pelo time"
				}
				dedup = append(dedup, reminderBlock{marker: marker, text: boardText + "\n\n"})
			}
		}

		var b strings.Builder
		b.WriteString(fixed.String())
		for _, blk := range filterReminderBlocks(req, dedup, lastInjected) {
			b.WriteString(blk.text)
		}
		if guidance := contextx.ContextWindowGuidance(req, contextx.Budget{
			ContextWindow:   agentContextWindow(a),
			ContextWindowFn: r.dynamicAgentContextWindow(adkName, a),
		}); guidance != "" {
			fmt.Fprintf(&b, "%s\n\n", guidance)
		}
		reminder := strings.TrimSpace(b.String())
		if reminder == "" {
			return nil, nil
		}
		appendSystemReminder(req, reminder)
		return nil, nil
	}
}

// filterReminderBlocks devolve apenas os blocos que mudaram desde a última
// injeção ou cujo conteúdo atual não está mais presente no transcript (por
// exemplo após compactação de contexto). Verificar o conteúdo trimado atual
// (e não apenas o marcador) evita que um bloco antigo sobrevivente à
// compactação impeça a reinjeção de uma versão mais nova.
func filterReminderBlocks(req *model.LLMRequest, blocks []reminderBlock, lastInjected map[string]string) []reminderBlock {
	var out []reminderBlock
	for _, blk := range blocks {
		sum := sha256.Sum256([]byte(blk.text))
		hash := hex.EncodeToString(sum[:8])
		needle := strings.TrimSpace(blk.text)
		if needle == "" {
			needle = blk.marker
		}
		if lastInjected[blk.marker] == hash && reminderBlockPresent(req, needle) {
			continue
		}
		lastInjected[blk.marker] = hash
		out = append(out, blk)
	}
	return out
}

// reminderBlockPresent reports whether the transcript already carries a block
// with the given marker or content needle.
func reminderBlockPresent(req *model.LLMRequest, marker string) bool {
	if req == nil || marker == "" {
		return false
	}
	for _, c := range req.Contents {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			if p != nil && strings.Contains(p.Text, marker) {
				return true
			}
		}
	}
	return false
}

func appendSystemReminder(req *model.LLMRequest, reminder string) {
	block := "<system-reminder>\n" + reminder + "\n</system-reminder>"
	if len(req.Contents) == 0 {
		req.Contents = []*genai.Content{
			{Role: genai.RoleUser, Parts: []*genai.Part{{Text: block}}},
		}
		return
	}
	lastIdx := len(req.Contents) - 1
	orig := req.Contents[lastIdx]
	if orig == nil {
		req.Contents[lastIdx] = &genai.Content{
			Role:  genai.RoleUser,
			Parts: []*genai.Part{{Text: block}},
		}
		return
	}
	contentCopy := *orig
	contentCopy.Parts = make([]*genai.Part, len(orig.Parts), len(orig.Parts)+1)
	copy(contentCopy.Parts, orig.Parts)
	contentCopy.Parts = append(contentCopy.Parts, &genai.Part{Text: block})
	sliceCopy := make([]*genai.Content, len(req.Contents))
	copy(sliceCopy, req.Contents)
	sliceCopy[lastIdx] = &contentCopy
	req.Contents = sliceCopy
}

func (r *Runner) nestedWorktreesPromptHint() string {
	if len(r.nestedWorktrees) == 0 {
		return ""
	}
	parts := make([]string, 0, len(r.nestedWorktrees))
	for _, nw := range r.nestedWorktrees {
		parts = append(parts, fmt.Sprintf("%s (branch %s)", nw.RelPath, nw.Branch))
	}
	return "Sub-repositórios Git independentes montados nesta worktree: " + strings.Join(parts, ", ") + ". Leia/edite pelos caminhos relativos normalmente; commit, push e PR por repositório serão executados após aprovação do QA."
}

func (r *Runner) compositeStatus(ctx context.Context) (string, error) {
	st, err := r.Git.Status(ctx, r.deliveryRoot)
	if err != nil {
		return "", err
	}
	if len(r.nestedWorktrees) == 0 {
		return st, nil
	}
	var b strings.Builder
	b.WriteString(st)
	for _, nw := range r.nestedWorktrees {
		subSt, subErr := r.Git.Status(ctx, nw.WorktreeDir)
		if subErr != nil || strings.TrimSpace(subSt) == "" {
			continue
		}
		b.WriteString("\n[")
		b.WriteString(nw.RelPath)
		b.WriteString("]\n")
		b.WriteString(subSt)
	}
	return b.String(), nil
}

// recordQAStall counts consecutive QA rejections without changes in the delivery worktree.
func (r *Runner) recordQAStall(parentCtx context.Context) string {
	if r.Git == nil || r.deliveryRoot == "" {
		return ""
	}
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
	defer cancel()
	status, err := r.compositeStatus(ctx)
	if err != nil {
		return ""
	}
	defer func() {
		r.qaHasLastStatus = true
		r.qaLastStatus = status
	}()
	if !r.qaHasLastStatus || status != r.qaLastStatus {
		r.qaStallCount = 0
		return ""
	}
	r.qaStallCount++
	limit := 2
	if strings.TrimSpace(status) == "" {
		limit = 1
	}
	if r.qaStallCount < limit {
		return ""
	}
	r.qaStallCount = 0
	r.qaStallFired = true
	return "O QA reprovou de novo sem nenhuma alteração nova na entrega desde a reprovação anterior; o ciclo Dev→QA foi interrompido para não queimar mais chamadas. Revise manualmente o feedback do QA e o estado da entrega."
}

func (r *Runner) newIntegrateAgent(devs []devEnv) (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        "integrate_branches",
		Description: "Consolida os arquivos das worktrees dos desenvolvedores na worktree de entrega sem criar commits de dev.",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				var devRoots []string
				for _, dev := range devs {
					if dev.Root != "" && dev.Root != r.deliveryRoot {
						devRoots = append(devRoots, dev.Root)
					}
				}
				var nestedRels []string
				for _, nw := range r.nestedWorktrees {
					nestedRels = append(nestedRels, nw.RelPath)
				}
				synced, err := gitx.SyncCompositeUncommittedToDelivery(ctx, r.Git, devRoots, r.deliveryRoot, nestedRels)
				text := "Nada novo para consolidar."
				delta := map[string]any{"integrate_status": ""}
				if err != nil {
					text = "Falha ao consolidar worktrees: " + err.Error()
					delta["integrate_status"] = text
				} else if len(synced) > 0 {
					text = "Consolidado na worktree de entrega: " + strings.Join(synced, ", ")
					delta["integrate_status"] = text
				}
				yield(&session.Event{
					Author: "integrate_branches",
					LLMResponse: model.LLMResponse{Content: &genai.Content{
						Role:  "model",
						Parts: []*genai.Part{{Text: text}},
					}},
					Actions: session.EventActions{StateDelta: delta},
				}, nil)
			}
		},
	})
}
