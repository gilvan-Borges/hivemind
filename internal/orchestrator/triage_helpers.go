package orchestrator

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/skillx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/tools"
)

func appendTextToFirstPart(msg *genai.Content, extra string) *genai.Content {
	if msg == nil || len(msg.Parts) == 0 || msg.Parts[0] == nil || extra == "" {
		return msg
	}
	clonedParts := make([]*genai.Part, len(msg.Parts))
	copy(clonedParts, msg.Parts)
	firstCopy := *clonedParts[0]
	firstCopy.Text += extra
	clonedParts[0] = &firstCopy
	return &genai.Content{Role: msg.Role, Parts: clonedParts}
}

func (r *Runner) buildTriageBaseTools(toolRoot, jiraKey string, sddDocs, sddJira, isAutoReview bool) ([]tool.Tool, error) {
	if sddDocs {
		return tools.ForRole(team.RoleProjectManager, tools.Deps{
			Root:                 r.Workspace,
			Settings:             r.Project.Settings,
			DisableGitWrite:      true,
			DocsOnlyWrite:        true,
			ReviewRequestedInObj: r.reviewRequested,
			Confirm:              r.Confirm,
			Approve:              r.Approve,
			Git:                  r.Git,
			GitHub:               githubx.Disabled{Reason: "fase SDD de docs não opera GitHub"},
			Jira:                 nil,
			JiraKey:              jiraKey,
			JiraProject:          r.Project.Settings.JiraProject,
			BaseBranch:           r.Project.Settings.BaseBranch,
			MCP:                  r.MCP,
		})
	}
	if sddJira {
		return tools.ForRole(team.RoleProjectManager, tools.Deps{
			Root:                 r.Workspace,
			Settings:             r.Project.Settings,
			DisableGitWrite:      true,
			DocsOnlyWrite:        true,
			ReviewRequestedInObj: r.reviewRequested,
			Confirm:              r.Confirm,
			Approve:              r.Approve,
			Git:                  r.Git,
			GitHub:               githubx.Disabled{Reason: "fase SDD de backlog não opera GitHub"},
			Jira:                 r.Jira,
			JiraKey:              jiraKey,
			JiraProject:          r.Project.Settings.JiraProject,
			BaseBranch:           r.Project.Settings.BaseBranch,
			MCP:                  r.MCP,
		})
	}
	return tools.ForRole(team.RoleProjectManager, tools.Deps{
		Root:                   toolRoot,
		Settings:               r.Project.Settings,
		DisableGitWrite:        true,
		DisableMerge:           isAutoReview,
		ReviewCommentOnly:      isAutoReview && r.Project.Settings.AutoReview.EffectiveReviewAction() == "comment_only",
		AllowReadOnlyTestShell: isAutoReview && r.Project.Settings.AutoReview.EffectiveRunTests(),
		NoShell:                isAutoReview && !r.Project.Settings.AutoReview.EffectiveRunTests(),
		DocsOnlyWrite:          false,
		ReviewRequestedInObj:   r.reviewRequested || isAutoReview,
		Allowlist:              r.Project.Settings.ShellAllowlist,
		Confirm:                r.Confirm,
		Approve:                r.Approve,
		Git:                    r.Git,
		GitHub:                 r.GitHub,
		Jira:                   r.Jira,
		JiraKey:                jiraKey,
		JiraProject:            r.Project.Settings.JiraProject,
		BaseBranch:             r.Project.Settings.BaseBranch,
		MCP:                    r.MCP,
	})
}

func (r *Runner) newSDDDocsReadyTool(chief team.Agent) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "sdd_docs_ready",
		Description: "Encerra a fase SDD de Discovery & Docs: registra o resumo da especificação gravada e pausa no Gate 1 para o usuário aprovar antes de qualquer criação no Jira ou código.",
	}, func(tctx agent.Context, args struct {
		Summary string `json:"summary" jsonschema:"Resumo executivo: artefatos SDD gravados (specs/docs/ADRs), principais decisões e próximos passos."`
	}) (delegateResult, error) {
		text := strings.TrimSpace(args.Summary)
		if text == "" {
			return delegateResult{}, fmt.Errorf("informe o resumo dos docs SDD gravados")
		}
		r.setStateChecked(tctx.State(), "sdd_docs_pending", "1")
		r.setStateChecked(tctx.State(), "chief_plan", text)
		if actions := tctx.Actions(); actions != nil {
			if actions.StateDelta == nil {
				actions.StateDelta = map[string]any{}
			}
			actions.StateDelta["sdd_docs_pending"] = "1"
			actions.StateDelta["chief_plan"] = text
		}
		if r.OnEvent != nil {
			r.OnEvent(Event{
				Kind:   "sdd_gate",
				Stage:  "chief",
				Author: chief.Name,
				Gate:   "docs",
				Label:  "Gate 1 · Docs & Arquitetura SDD prontos",
				Text:   text,
			})
		}
		return delegateResult{Status: "docs SDD registrados; aguardando aprovação do usuário no Gate 1 (Criar Tasks no Jira)"}, nil
	})
}

func (r *Runner) newTriageDelegateTool(
	chief team.Agent,
	sess session.Session,
	runMode string,
	maxDevs int,
	baseDevs []team.Agent,
	delegateMu *sync.Mutex,
	delegated *bool,
	planCardEmitted *bool,
) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "delegate_to_squad",
		Description: fmt.Sprintf("Registra o plano técnico e dispara dinamicamente de 1 até %d desenvolvedores paralelos em worktrees separadas (Dev(s) → QA → Chefe).", maxDevs),
	}, func(tctx agent.Context, args delegateArgs) (delegateResult, error) {
		delegateMu.Lock()
		defer delegateMu.Unlock()
		*delegated = true
		r.chiefPlan = strings.TrimSpace(args.Plan)
		r.chiefContextBrief = strings.TrimSpace(args.ContextBrief)
		if args.PauseForApproval && (runMode == "" || runMode == "auto") {
			r.pauseForApproval = true
		}

		cleaned := make([]string, 0, len(args.DevTasks))
		seenTasks := make(map[string]bool, len(args.DevTasks))
		for _, s := range args.DevTasks {
			trimmed := strings.TrimSpace(s)
			if trimmed == "" {
				continue
			}
			key := strings.ToLower(trimmed)
			if seenTasks[key] {
				continue
			}
			seenTasks[key] = true
			cleaned = append(cleaned, trimmed)
		}
		if len(cleaned) > maxDevs && maxDevs > 0 {
			tail := strings.Join(cleaned[maxDevs-1:], "\n")
			cleaned = append(cleaned[:maxDevs-1], tail)
		}
		r.devSlices = cleaned
		r.devOwnedPaths = alignOwnedPaths(args.OwnedPaths, len(cleaned))
		r.devSequential = args.Sequential
		r.prNumber = args.PRNumber
		r.prBranch = strings.TrimSpace(args.PRBranch)
		r.prReviewers = append([]string(nil), args.Reviewers...)
		r.selectedSkills = skillx.Match(r.skills, args.Skills)
		r.selectedNative = skillx.Match(r.NativeSkills, args.NativeSkills)

		for _, raw := range args.Skills {
			if skillx.Find(r.selectedSkills, raw) != nil {
				continue
			}
			if hit := skillx.Find(r.NativeSkills, raw); hit != nil && skillx.Find(r.selectedNative, raw) == nil {
				r.selectedNative = append(r.selectedNative, *hit)
				fmt.Fprintf(r.Err, "aviso: skill %q não existe no projeto; usando a nativa da IDE\n", raw)
			}
		}
		for _, raw := range args.NativeSkills {
			if skillx.Find(r.selectedNative, raw) != nil {
				continue
			}
			if hit := skillx.Find(r.skills, raw); hit != nil {
				r.selectedSkills = append(r.selectedSkills, *hit)
				fmt.Fprintf(r.Err, "aviso: skill nativa %q não existe na IDE; usando a versão do projeto\n", raw)
			}
		}
		if len(r.selectedNative) > 0 {
			keptNative := make([]skillx.Skill, 0, len(r.selectedNative))
			for _, ns := range r.selectedNative {
				if skillx.Find(r.skills, ns.Name) == nil {
					keptNative = append(keptNative, ns)
					continue
				}
				prompt := fmt.Sprintf("O Chefe considera a skill nativa da IDE `%s` mais atual que a versão do projeto. Autorizar o uso da versão nativa da IDE? (Recuse para manter a versão do projeto.)", ns.Name)
				if r.Approve != nil && !r.Approve(prompt) {
					if skillx.Find(r.selectedSkills, ns.Name) == nil {
						if ps := skillx.Find(r.skills, ns.Name); ps != nil {
							r.selectedSkills = append(r.selectedSkills, *ps)
						}
					}
					continue
				}
				keptNative = append(keptNative, ns)
				r.selectedSkills = filterOutSkill(r.selectedSkills, ns.Name)
			}
			r.selectedNative = keptNative
		}
		if len(args.Skills) > 0 && len(r.selectedSkills) == 0 && len(r.selectedNative) == 0 {
			fmt.Fprintf(r.Err, "aviso: skills pedidas pelo Chefe não existem nos catálogos (%s e nativas da IDE): %s\n", skillx.SkillsDir, strings.Join(args.Skills, ", "))
		}
		if runMode == "plan:sdd-jira" {
			for _, n := range []string{"sdd-dev-executor", "sdd-qa-verifier"} {
				if hit := skillx.Find(r.NativeSkills, n); hit != nil &&
					skillx.Find(r.selectedNative, n) == nil && skillx.Find(r.selectedSkills, n) == nil {
					r.selectedNative = append(r.selectedNative, *hit)
				}
			}
		}

		skillNames := skillx.Names(r.selectedSkills)
		nativeNames := skillx.Names(r.selectedNative)
		r.sealHandoff(sess.ID())
		joinedSlices := strings.Join(r.devSlices, "\n|||\n")
		ownedJSON := marshalOwnedPaths(r.devOwnedPaths)
		seqFlag := boolFlag(r.devSequential)
		stateUpdates := map[string]any{
			"chief_plan":             r.chiefPlan,
			"chief_context_brief":    r.chiefContextBrief,
			"dev_slices_joined":      joinedSlices,
			"dev_owned_paths_json":   ownedJSON,
			"dev_sequential":         seqFlag,
			"pr_number":              strconv.Itoa(r.prNumber),
			"pr_branch":              r.prBranch,
			"pr_reviewers":           strings.Join(r.prReviewers, ","),
			"selected_skills":        strings.Join(skillNames, "\n"),
			"selected_native_skills": strings.Join(nativeNames, "\n"),
		}
		for k, v := range stateUpdates {
			r.setStateChecked(tctx.State(), k, v)
		}
		if actions := tctx.Actions(); actions != nil {
			if actions.StateDelta == nil {
				actions.StateDelta = map[string]any{}
			}
			for k, v := range stateUpdates {
				actions.StateDelta[k] = v
			}
		}
		r.updateStateChecked(sess, stateUpdates)
		r.sealHandoff(sess.ID())

		resolved := r.resolveDynamicDevs()
		if r.OnEvent != nil {
			var hb strings.Builder
			if r.chiefPlan != "" {
				hb.WriteString(r.chiefPlan)
			}
			if r.prNumber > 0 {
				fmt.Fprintf(&hb, "\n- **PR Alvo**: #%d (%s)", r.prNumber, r.prBranch)
			}
			if skillLabel := skillSelectionLabel(skillNames, nativeNames); skillLabel != "" {
				fmt.Fprintf(&hb, "\n- **Skills selecionadas**: %s", skillLabel)
			}
			for i, slice := range r.devSlices {
				dn := fmt.Sprintf("dev_%d", i+1)
				if i < len(resolved) {
					dn = resolved[i].Name
				} else if len(r.devSlices) == 1 && len(baseDevs) > 0 {
					dn = baseDevs[0].Name
				}
				if strings.TrimSpace(slice) != "" {
					fmt.Fprintf(&hb, "\n- **%s**: %s", dn, strings.TrimSpace(slice))
				}
			}
			devCountLabel := fmt.Sprintf("%d Desenvolvedor", len(resolved))
			if len(resolved) > 1 {
				devCountLabel = fmt.Sprintf("%d Desenvolvedores em paralelo", len(resolved))
			}
			switch {
			case runMode == "plan:sdd-jira":
				r.OnEvent(Event{
					Kind:   "sdd_gate",
					Stage:  "chief",
					Author: chief.Name,
					Gate:   "jira",
					Label:  fmt.Sprintf("Gate 2 · Backlog Jira criado (%s)", devCountLabel),
					Text:   strings.TrimSpace(hb.String()),
				})
			case runMode == "plan" || r.pauseForApproval:
				*planCardEmitted = true
				r.OnEvent(Event{
					Kind:   "plan_ready",
					Stage:  "chief",
					Author: chief.Name,
					Label:  fmt.Sprintf("Plano pronto para aprovação (%s)", devCountLabel),
					Text:   strings.TrimSpace(hb.String()),
				})
			default:
				r.OnEvent(Event{
					Kind:   "handoff",
					Stage:  "dev",
					Author: chief.Name,
					Label:  fmt.Sprintf("Chefe delegou → %s", devCountLabel),
					Text:   strings.TrimSpace(hb.String()),
				})
			}
		}
		if runMode == "plan" {
			return delegateResult{Status: fmt.Sprintf("plano registrado (%d dev(s) previstos); aguardando aprovação do usuário", len(resolved))}, nil
		}
		if runMode == "plan:sdd-jira" {
			return delegateResult{Status: fmt.Sprintf("backlog Jira registrado e %d onda(s) de desenvolvimento definidas; aguardando aprovação do usuário para disparar o Squad", len(resolved))}, nil
		}
		if r.pauseForApproval {
			return delegateResult{Status: fmt.Sprintf("plano registrado com pausa para aprovação (%d dev(s) previstos); o usuário revisará antes de disparar o Squad", len(resolved))}, nil
		}
		return delegateResult{Status: fmt.Sprintf("delegado para %d desenvolvedor(es) e QA em worktree separada", len(resolved))}, nil
	})
}

func (r *Runner) buildTriageInstructions(ctx context.Context, chief team.Agent, runMode string, maxDevs int) string {
	scaleGuidance := fmt.Sprintf(`ESCALA DINÂMICA DE DESENVOLVEDORES & WORKTREE UNIFICADA POR CONVERSA (1 até %d devs):
- A conversa usa UMA ÚNICA worktree isolada compartilhada entre Chefe, Dev(s) e QA (nunca clona worktrees redundantes à toa).
- Você decide quantos desenvolvedores disparar passando de 1 até %d itens em "dev_tasks":
  • Tarefa pequena/média ou no mesmo serviço: passe 1 item em "dev_tasks" (1 dev na worktree da conversa, mais rápido e zero conflito).
  • Tarefa grande em serviços/pastas independentes (ex.: backend Go vs frontend TS): divida em 2..%d itens em "dev_tasks" E preencha "owned_paths" com os prefixos exclusivos de cada dev (ex.: [["internal/relay/"], ["ide/frontend/"]]). Todos compartilham a mesma worktree da conversa com trava determinística de escrita e memória compartilhada via share_team_note.
  • Tarefa multi-etapa tocando o mesmo módulo/build: passe sequential=true (onda sequencial na mesma worktree, zero conflito de build).
- CADA fatia deve caber em poucas edições: o dev tem orçamento rígido por rodada (%d chamadas e %d leituras/buscas antes da 1ª edição; estouro aborta a execução). Prefira fatiar objetivo e listar os caminhos exatos.`, maxDevs, maxDevs, maxDevs, r.Project.Settings.EffectiveMaxDevCalls(), r.Project.Settings.EffectiveMaxDevExploreCalls())
	if nestedRepos := gitx.DiscoverNestedRepos(ctx, r.Git, r.Workspace); len(nestedRepos) > 0 {
		rels := make([]string, 0, len(nestedRepos))
		for _, nr := range nestedRepos {
			rels = append(rels, nr.RelPath+" (branch "+nr.BaseBranch+")")
		}
		scaleGuidance += "\n- SUB-REPOSITÓRIOS GIT ANINHADOS: " + strings.Join(rels, ", ") + ". Ao delegar com delegate_to_squad, o HiveMind monta automaticamente a worktree de cada sub-repositório no mesmo caminho relativo dentro da worktree da conversa (use normalmente em owned_paths/dev_tasks; o commit/PR por repositório é tratado no fecho)."
	}

	govRules := `GOVERNANÇA GIT & PULL REQUESTS:
- Toda alteração de código roda em worktree separada via delegate_to_squad; apenas Chefe ou Arquiteto comitam e abrem PR.
- ANTES de opinar, comentar ou revisar qualquer PR, chame github_get_pr (o conector nativo GitHub retorna autoria, revisões formais, comentários inline no código e comentários gerais) e confira o campo "Papel neste PR":
  • Se o PR for NOSSO (autor = usuário autenticado): NUNCA atue como revisor nem emita parecer revisando o próprio PR. Leia todos os comentários gerais e inline deixados pelos revisores externos, faça a triagem técnica e despache para a equipe com delegate_to_squad informando pr_number, pr_branch e reviewers; ao terminar, o Chefe comita, responde ponto a ponto (github_comment_pr / github_reply_review_comment) e pede nova revisão (github_request_review).
  • Se o PR for DE TERCEIRO: só aprove/revise/comente quando houver pedido de revisão; use github_pr_files/github_pr_diff para inspecionar o diff completo e github_review_pr para enviar o parecer.
- Só mescle (github_merge_pr) APÓS aprovação do revisor: em homolog pode mesclar direto; em main a ferramenta pede permissão ao usuário.
- INVESTIGAÇÃO ECONÔMICA NO PROJETO: prefira search_text e find_files em vez de múltiplas chamadas list_dir, e use read_file com start_line/end_line ao inspecionar arquivos grandes.
- DOSSIÊ DE HANDOFF PARA O DEV (context_brief): sempre que chamar delegate_to_squad, preencha OBRIGATORIAMENTE "context_brief" com os fatos concretos que você JÁ leu (assinaturas exatas de classes/interfaces/métodos, campos de DTO, códigos HTTP/erro, regras RN críticas e caminhos de arquivos). Esse dossiê é salvo em .hivemind/handoff/ e entregue direto ao Dev e ao QA para evitar que eles releiam a spec/plano inteiros.`

	if r.reviewRequested {
		govRules += `

PROTOCOLO DE REVISÃO DIFF-FIRST & ORÇAMENTO RÍGIDO DE TOKENS (PADRÃO ANTHROPIC):
1. ZERO-ROUNDTRIP: quando a seção [Dossiê Pré-Carregado do PR #N] estiver presente na mensagem, os metadados, revisões anteriores, comentários inline/gerais, status do CI, lista de arquivos e os patches [PATCH COMPLETO] JÁ estão carregados. NÃO chame github_get_pr, github_pr_files, github_ci_status nem github_pr_diff para arquivos já marcados como [PATCH COMPLETO].
2. DIFF-FIRST: avalie diretamente os hunks do diff pré-carregado. Se o PR for de documentação/spec ou se o diff já for autossuficiente para validar a consistência, publique o parecer IMEDIATAMENTE com github_review_pr sem gastar turnos lendo o repositório.
3. VERIFICAÇÃO CIRÚRGICA (MÁX. 1 A 2 TURNOS): só consulte arquivos fora do diff quando houver risco real de quebra de contrato (assinatura de método/DTO, rota, permissão ou regra RN citada). Faça todas as buscas/leituras estritamente necessárias em 1 único lote paralelo (máx. 4-6 chamadas no total) e NUNCA releia o mesmo arquivo deslocando faixas de linha.
4. FECHAMENTO IMEDIATO: assim que concluir a verificação (no máximo no 2º ou 3º turno), chame github_review_pr (APPROVE, REQUEST_CHANGES ou COMMENT) e encerre com um resumo executivo curto.`
	}

	var triageInstr string
	switch runMode {
	case "auto-review":
		testRule := "Execução de testes desabilitada nesta configuração (faça auditoria estática focada no diff pré-carregado)."
		if r.Project.Settings.AutoReview.EffectiveRunTests() {
			testRule = "Testes habilitados em worktree isolada da branch do PR: quando o PR alterar código executável, você PODE rodar o teste afetado via run_shell e citar o resultado objetivo no parecer. Se o PR alterar apenas documentação/specs (.md), NÃO rode suite de testes."
		}
		actionRule := "Publique o parecer adequado com github_review_pr (APPROVE, REQUEST_CHANGES ou COMMENT conforme os achados técnicos)."
		if r.Project.Settings.AutoReview.EffectiveReviewAction() == "comment_only" {
			actionRule = "Modo SOMENTE COMENTAR ativo: publique obrigatoriamente com github_review_pr usando event=\"COMMENT\" (APPROVE e REQUEST_CHANGES estão bloqueados)."
		}
		triageInstr = fmt.Sprintf(`%s

Você é o Chefe da equipe (%s) operando em MODO REVISÃO AUTOMÁTICA DE FLUXO (autônomo, SEM MERGE).
REGRAS ABSOLUTAS DESTE MODO:
1. NUNCA tente mesclar o PR (github_merge_pr foi fisicamente removida; o merge é decisão humana).
2. Analise primeiro o [Dossiê Pré-Carregado do PR] já injetado na mensagem (metadados, comentários, CI e patches). Só chame github_pr_diff(file=...) para arquivos marcados como [PARCIAL] ou [PATCH OMITIDO].
3. Só faça leituras fora do diff (search_text / read_file com start_line..end_line) se houver dúvida concreta de contrato/bug, agrupando em 1 único turno paralelo (orçamento máximo de %d verificações).
4. %s
5. %s
6. Ao final, devolva um resumo curto e objetivo do veredito registrado no GitHub.
%s`,
			chief.Instructions, chief.Name, defaultMaxReviewExploreCalls, testRule, actionRule, govRules)
	case "direct":
		triageInstr = fmt.Sprintf(`%s

Você é o Chefe da equipe (%s) operando em MODO DIRETO (1 agente, sem delegar ao squad).
Inspecione arquivos/git/PRs se necessário e responda diretamente ao usuário com clareza, profundidade técnica e objetividade.
%s`,
			chief.Instructions, chief.Name, govRules)
	case "plan":
		triageInstr = fmt.Sprintf(`%s

Você é o Chefe da equipe (%s) operando em MODO PLANEJAMENTO · FEATURE / TASK GRANDE (Human-in-the-Loop).
1. Inspecione os arquivos/PRs relevantes do projeto com read_file/list_dir/git_status/github_get_pr.
2. Elabore um plano arquitetural claro (mapa de impacto, divisão de arquivos, riscos e testes) e chame delegate_to_squad preenchendo "plan" e "dev_tasks" (escolhendo dinamicamente de 1 até %d desenvolvedores conforme o tamanho da demanda).
3. Apresente um resumo objetivo do plano ao usuário informando quantos devs serão disparados e que a execução iniciará em worktree separada assim que ele aprovar.
%s
%s`,
			chief.Instructions, chief.Name, maxDevs, scaleGuidance, govRules)
	case "plan:sdd":
		triageInstr = fmt.Sprintf(`%s

Você é o Chefe da equipe (%s) operando em MODO PLANEJAMENTO SDD · PROJETO DO ZERO (Spec-Driven Development com gates — Human-in-the-Loop).

Este modo inicia um projeto do zero: NENHUM código é criado e NENHUMA issue é aberta até a documentação SDD completa ser aprovada pelo usuário.

FLUXO TRAVADO POR GATES (você NÃO pula etapa):
- ETAPA 1 — ENTREVISTA / DISCOVERY: faça rodadas objetivas de perguntas ao usuário até zerar ambiguidades materiais: problema de negócio, usuários/personas, requisitos funcionais e não funcionais (latência, throughput, consistência, disponibilidade, custo, segurança), stack preferida, integrações, regras de domínio, restrições e deploy. Pergunte em blocos curtos (máx. 5-8 perguntas por rodada) e NÃO avance com dúvida relevante em aberto. Respostas incompletas do usuário: registre como premissa explícita e sinalize.
- ETAPA 2 — DOCS & ARQUITETURA SDD: só depois do escopo confirmado, grave APENAS documentação com write_doc (não existe write_file nesta fase):
  • specs/01-spec.md — visão, escopo, NÃO-escopo, personas, requisitos funcionais/não funcionais e critérios de aceite
  • specs/02-arquitetura.md — componentes, modelo de dados, segurança e observabilidade
  • docs/adr/NNNN-titulo.md — um ADR curto (Contexto, Decisão, Consequências) para cada decisão relevante (stack, persistência, mensageria, etc.)
  • specs/03-contratos.md — contratos de API/eventos/esquemas de dados
  • specs/04-plano.md — ondas de entrega, dependências e riscos
  • specs/05-tarefas.md — backlog completo: Epics → Tasks → Subtasks, cada task com critérios de aceite e referência à spec
- Ao concluir, chame sdd_docs_ready com um resumo executivo (artefatos gravados + principais decisões) e PARE. Gate 1: o usuário aprovou os docs antes de qualquer Jira/código.

REGRAS:
- write_doc aceita SOMENTE .md em docs/, specs/, .specify/ ou README.md; tentativas de gravar código serão recusadas pela ferramenta.
- Se o usuário pedir ajustes após o gate, revise os docs e chame sdd_docs_ready novamente.
- Siga as skills nativas sdd-discovery (entrevista) e sdd-architecture-docs (geração dos docs); leia-as com read_file quando precisar de diretrizes detalhadas.`,
			chief.Instructions, chief.Name)
	case "plan:sdd-jira":
		triageInstr = fmt.Sprintf(`%s

Você é o Chefe da equipe (%s) na FASE SDD — CRIAÇÃO DO BACKLOG NO JIRA (Gate 1 já aprovado pelo usuário).

1. Leia specs/05-tarefas.md e os demais docs SDD aprovados (specs/, docs/adr/) com read_file.
2. Espelhe o backlog no Jira com jira_create_issue (Epic por épico/onda; Task ou Story com os critérios de aceite da spec na description; parent para vincular ao Epic) e jira_create_subtask quando houver quebra fina. Sem project informado, o projeto padrão do time/conector é usado.
3. Grave as chaves Jira geradas (PROJ-xxx) de volta na spec com write_doc (atualize o mapeamento tarefa→chave em specs/05-tarefas.md) para rastreabilidade.
4. Chame delegate_to_squad com "plan" (resumo das ondas de desenvolvimento extraído de specs/04-plano.md) e "dev_tasks" (1 item por onda/desenvolvedor paralelo, SEM sobreposição de arquivos) e PARE. Gate 2: o usuário aprova o backlog e dispara o Squad.
- Se o Jira não estiver configurado/habilitado, informe claramente o que falta (Conector Jira Nativo na IDE + jira_enabled no time) e NÃO invente chaves nem avance para o desenvolvimento.
%s`,
			chief.Instructions, chief.Name, scaleGuidance)
	case "squad":
		triageInstr = fmt.Sprintf(`%s

Você é o Chefe da equipe (%s) operando em MODO SQUAD.
1. Inspecione os arquivos/PRs relevantes do projeto rapidamente.
2. Monte um plano objetivo, decida quantos desenvolvedores são necessários (1 até %d) e chame OBRIGATORIAMENTE delegate_to_squad passando "plan" e "dev_tasks" (e "pr_number", "pr_branch", "reviewers" se for revisão de PR existente).
%s
%s`,
			chief.Instructions, chief.Name, maxDevs, scaleGuidance, govRules)
	default:
		triageInstr = fmt.Sprintf(`%s

Você é o Chefe da equipe (%s).
REGRA DE TRIAGEM & PROTOCOLO OPENAI CODEX / SWARM (FAST-ROUTER):
1. Se o usuário fez apenas uma pergunta conceitual, pediu revisão de PR de terceiro (github_review_pr) ou merge de PR já aprovado (github_merge_pr) SEM pedir para implementar tarefa/código: execute as ferramentas cabíveis, responda de forma clara e NÃO chame delegate_to_squad.
2. Se o usuário pediu para resolver uma issue/task do Jira (ou ir no Jira ver as tasks/subtasks e fazer uma), criar, editar, refatorar código OU atender mudanças pedidas em revisão de um PR nosso:
   • ZERO-ROUNDTRIP JIRA: se a seção [Dossiê Pré-Carregado do Jira] já estiver na mensagem, NÃO chame jira_get nem jira_search para a mesma issue — use a lista de Subtasks/Tasks Filhas já pré-carregada.
   • SELEÇÃO IMEDIATA DE SUBTASK: quando uma issue/épico tiver múltiplas subtasks ou tarefas pendentes (no Jira ou em specs/.../tasks.md), NUNCA tente ler o projeto inteiro. Escolha a PRÓXIMA subtask pendente lógica (ou até 2-3 subtasks independentes da mesma onda), faça no máximo 1 turno rápido de leitura cirúrgica (3 a 5 arquivos, orçamento rígido de %d leituras na triagem) e chame **OBRIGATORIAMENTE delegate_to_squad** passando "plan", "context_brief" e "dev_tasks".
   • WORKTREE & EXECUÇÃO: a worktree isolada e o(s) Desenvolvedor(es) SÓ são criados quando você chama delegate_to_squad. Nunca encerre a triagem de uma tarefa de implementação sem chamar delegate_to_squad!
3. AUTONOMIA PARA PAUSAR: se a tarefa for grande, arriscada (migração, refatoração ampla, mudança de contrato, dados sensíveis) ou de escopo ambíguo, chame delegate_to_squad com pause_for_approval=true — o plano será apresentado ao usuário para aprovação ANTES de disparar o Squad. Use essa autonomia com critério: tarefas pequenas e claras executam direto.
%s
%s`,
			chief.Instructions, chief.Name, defaultMaxTriageExploreCalls, scaleGuidance, govRules)
	}

	triageInstr += "\n\nTERMINAL: run_shell é o terminal desta sessão. Se o usuário pedir para rodar um comando (gh, testes, build), chame run_shell. Não responda que não existe terminal. Comandos perigosos ficam parados até o usuário aprovar. Se a lista recusar, diga o motivo da recusa."

	if catalogBlock := skillx.CatalogBlock(r.skills, r.NativeSkills); catalogBlock != "" {
		triageInstr += "\n\n" + catalogBlock +
			"SELEÇÃO DE SKILLS:\n" +
			"- Ao delegar com delegate_to_squad, avalie quais skills acima cercam ou aceleram a tarefa e passe os nomes exatos em \"skills\" (apenas as relevantes; vazio = nenhuma).\n" +
			"- Se a versão NATIVA da IDE parecer mais atual ou melhor que a versão do projeto, passe o nome em \"native_skills\"; se o projeto tiver uma skill de mesmo nome, o usuário será consultado para autorizar a substituição.\n" +
			"- Skills na seção OPCIONAIS DESLIGADAS só entram em native_skills se o usuário pediu explicitamente essa skill pelo nome.\n" +
			"- Em modo direto/consulta você mesmo pode seguir uma skill: leia o arquivo SKILL.md indicado com read_file."
	}
	if note := r.pinnedSkillsNote(); note != "" {
		triageInstr += note
	}
	if chiefGuide := r.guidanceForRole(team.SkillTargetChief, false); chiefGuide != "" {
		triageInstr += chiefGuide
	}
	switch runMode {
	case "plan:sdd":
		triageInstr += r.nativeSkillGuide("sdd-discovery", "sdd-architecture-docs")
	case "plan:sdd-jira":
		triageInstr += r.nativeSkillGuide("sdd-jira-planner")
	case "plan":
		triageInstr += r.nativeSkillGuide("plan-feature-architect")
	}
	return triageInstr
}

func (r *Runner) writeHandoffDossier(sessionID string) {
	r.sealHandoff(sessionID)
}
