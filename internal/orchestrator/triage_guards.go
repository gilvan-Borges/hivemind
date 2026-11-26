package orchestrator

import (
	"fmt"
	"strings"
	"sync"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/tool"
)

// newChiefTriageBudgetCallbacks keeps the Chief's exploration on a short leash
// without ever touching the request schema (Spec 02 §3.2):
// 1. Short-circuits redundant jira_get(preloadedJiraKey) calls when the Jira hierarchy dossier is already preloaded.
// 2. Caps repository exploration calls (read_file, search_text, find_files, list_dir, jira_get, jira_search)
//    at maxExploreCalls (default 6) so the Chief never loops without delegating.
// 3. Once delegate_to_squad is called, blocks any further exploration tool calls so the pipeline
//    immediately creates the worktree and launches the Developer agent(s).
func newChiefTriageBudgetCallbacks(preloadedJiraKey string, maxExploreCalls int) (llmagent.BeforeToolCallback, llmagent.AfterToolCallback) {
	if maxExploreCalls <= 0 {
		maxExploreCalls = defaultMaxTriageExploreCalls
	}
	cleanKey := strings.ToUpper(strings.TrimSpace(preloadedJiraKey))

	var (
		mu                  sync.Mutex
		exploreCount        int
		overBudgetCount     int
		postDelegateBlocked int
		delegated           bool
		directExecuting     bool
	)

	before := func(_ agent.Context, tl tool.Tool, args map[string]any) (map[string]any, error) {
		if tl == nil {
			return nil, nil
		}
		name := strings.ToLower(strings.TrimSpace(tl.Name()))

		mu.Lock()
		isDelegated := delegated
		if isDelegated && name != "jira_transition" && name != "jira_comment" {
			postDelegateBlocked++
		}
		mu.Unlock()
		if isDelegated && name != "jira_transition" && name != "jira_comment" {
			return map[string]any{
				"text": "delegate_to_squad já foi registrado com sucesso neste turno. NÃO execute novas leituras ou ferramentas — encerre sua resposta imediatamente com um resumo curto de 2 linhas para o orquestrador criar a worktree e iniciar os Desenvolvedores.",
			}, nil
		}

		// Se o Chefe no modo Auto começou a escrever/editar diretamente na worktree isolada,
		// ele escolheu o caminho Direto com subagentes (task focada/grande sem Squad); não bloqueie por limite de triagem.
		if name == "write_file" || name == "edit_file" {
			mu.Lock()
			directExecuting = true
			exploreCount = 0
			overBudgetCount = 0
			mu.Unlock()
			return nil, nil
		}

		if cleanKey != "" && name == "jira_get" {
			argKey := strings.ToUpper(strings.TrimSpace(fmt.Sprint(args["key"])))
			if argKey == "" || argKey == "<NIL>" || argKey == cleanKey {
				return map[string]any{
					"text": fmt.Sprintf("CACHE DO DOSSIÊ JIRA %s: os detalhes completos de %s e todas as suas subtasks/tasks filhas (pendentes e concluídas) já estão pré-carregados na seção [Dossiê Pré-Carregado do Jira — %s] da mensagem inicial. Não gaste turnos re-consultando %s; selecione a subtask pendente e chame delegate_to_squad.", cleanKey, cleanKey, cleanKey, cleanKey),
				}, nil
			}
		}

		switch name {
		case "read_file", "search_text", "find_files", "list_dir", "run_shell", "jira_get", "jira_search", "git_diff", "git_status", "github_get_pr", "github_list_prs", "github_pr_files", "github_pr_diff":
			mu.Lock()
			if directExecuting {
				mu.Unlock()
				return nil, nil
			}
			exploreCount++
			cur := exploreCount
			if cur > maxExploreCalls {
				overBudgetCount++
			}
			mu.Unlock()
			if cur > maxExploreCalls {
				return map[string]any{
					"text": fmt.Sprintf("ORÇAMENTO DE TRIAGEM DO CHEFE ATINGIDO (%d/%d leituras/buscas): PARE IMEDIATAMENTE de abrir novos arquivos ou rodar git_status/git_diff/run_shell na fase de triagem. Se a issue já está implementada na branch, responda diretamente ao usuário agora; caso contrário, chame AGORA delegate_to_squad preenchendo 'plan', 'context_brief' e 'dev_tasks' para o orquestrador iniciar o Dev na worktree.", cur-1, maxExploreCalls),
				}, nil
			}
		}
		return nil, nil
	}

	after := func(ctx agent.Context, tl tool.Tool, _ map[string]any, result map[string]any, err error) (map[string]any, error) {
		if err != nil || tl == nil {
			return nil, nil
		}
		if strings.EqualFold(strings.TrimSpace(tl.Name()), "delegate_to_squad") {
			if hasErr, ok := result["error"]; ok && strings.TrimSpace(fmt.Sprint(hasErr)) != "" {
				return nil, nil
			}
			mu.Lock()
			delegated = true
			mu.Unlock()
			return nil, nil
		}
		mu.Lock()
		over := overBudgetCount
		postBlocked := postDelegateBlocked
		mu.Unlock()
		if postBlocked >= 1 || over >= 2 {
			if ctx != nil && ctx.Actions() != nil {
				ctx.Actions().Escalate = true
			}
		}
		return nil, nil
	}

	return before, after
}
