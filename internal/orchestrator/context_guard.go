package orchestrator

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/tool"

	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// agentContextWindow resolves the pinned agent's context window from the Jev
// catalog so in-turn compaction scales with the model actually serving the
// call (Spec 02 §3.3). Unknown models fall back to contextx.DefaultContextWindow.
func agentContextWindow(a team.Agent) int {
	prov := team.NormalizeProvider(a.Provider, a.Model)
	mod := strings.TrimSpace(a.Model)
	if prov == "" || mod == "" {
		return 0
	}
	if entry, ok := jev.DefaultStaticCatalog().Lookup(prov, mod); ok && entry.ContextLength > 0 {
		return int(entry.ContextLength)
	}
	lowMod := strings.ToLower(mod)
	if prov == "agy" && (strings.Contains(lowMod, "gemini") || strings.Contains(lowMod, "argon") || strings.Contains(lowMod, "carbon")) {
		return 1_000_000
	}
	if prov == "agy" || prov == "jev" {
		return 200_000
	}
	return 0
}

func (r *Runner) dynamicAgentContextWindow(adkName string, a team.Agent) func() int {
	return func() int {
		if r != nil {
			prov, mod := r.resolveAuthorRoute(adkName)
			if prov != "" && mod != "" {
				if r.Catalog != nil {
					if entry, ok := r.Catalog.Snapshot().Lookup(prov, mod); ok && entry.ContextLength > 0 {
						return int(entry.ContextLength)
					}
				}
				if w := agentContextWindow(team.Agent{Provider: prov, Model: mod}); w > 0 {
					return w
				}
			}
		}
		return agentContextWindow(a)
	}
}

// newRepeatReadGuardCallback returns a BeforeToolCallback that prevents an agent
// from entering a re-read death spiral (calling read_file on the exact same
// file/range 3+ times, or shifting line ranges on the same file 4+ times without
// modifying the file in between, or repeating the same github_pr_diff / search_text).
// Any edit_file or write_file on that path resets its read counter immediately.
func newRepeatReadGuardCallback() llmagent.BeforeToolCallback {
	return newRepeatReadGuardCallbackWithRound(nil)
}

// newRepeatReadGuardCallbackWithRound behaves like newRepeatReadGuardCallback and
// also resets all per-round inspection counters whenever roundFn() advances (e.g.
// when the pipeline moves to a new Dev->QA iteration after qa_request_changes).
func newRepeatReadGuardCallbackWithRound(roundFn func() int) llmagent.BeforeToolCallback {
	var mu sync.Mutex
	reads := make(map[string]int)
	fileTotalReads := make(map[string]int)
	diffCalls := make(map[string]int)
	searchCalls := make(map[string]int)
	gitInspectCalls := make(map[string]int)
	lastRound := -1
	if roundFn != nil {
		lastRound = roundFn()
	}

	normPath := func(raw any) string {
		s := strings.TrimSpace(fmt.Sprint(raw))
		if s == "" || s == "<nil>" {
			return ""
		}
		return strings.ToLower(filepath.ToSlash(filepath.Clean(s)))
	}

	return func(_ agent.Context, tl tool.Tool, args map[string]any) (map[string]any, error) {
		if tl == nil {
			return nil, nil
		}
		if roundFn != nil {
			cur := roundFn()
			mu.Lock()
			if cur != lastRound {
				clear(reads)
				clear(fileTotalReads)
				clear(diffCalls)
				clear(searchCalls)
				clear(gitInspectCalls)
				lastRound = cur
			}
			mu.Unlock()
		}
		name := strings.ToLower(strings.TrimSpace(tl.Name()))
		switch name {
		case "edit_file", "write_file", "write_doc", "git_commit":
			p := normPath(args["path"])
			mu.Lock()
			if p != "" {
				prefix := p + "::"
				for k := range reads {
					if strings.HasPrefix(k, prefix) {
						delete(reads, k)
					}
				}
				delete(fileTotalReads, p)
			}
			clear(gitInspectCalls)
			mu.Unlock()
			return nil, nil

		case "git_status", "git_diff":
			pathArg := normPath(args["path"])
			stagedArg := strings.ToLower(strings.TrimSpace(fmt.Sprint(args["staged"])))
			key := fmt.Sprintf("%s::%s::%s", name, pathArg, stagedArg)
			mu.Lock()
			gitInspectCalls[key]++
			n := gitInspectCalls[key]
			mu.Unlock()
			if n >= 2 {
				return map[string]any{
					"text": fmt.Sprintf("AVISO ANTI-LOOP: você já executou %s neste turno sem nenhuma edição intermediária na worktree (o resultado permanece idêntico). NÃO chame %s novamente — avance imediatamente para o próximo passo (delegate_to_squad na triagem, edit_file/write_file no dev, qa_approve/qa_request_changes no QA ou resumo final no fecho).", name, name),
				}, nil
			}
			return nil, nil

		case "read_file":
			p := normPath(args["path"])
			if p == "" {
				return nil, nil
			}
			start := parseIntArg(args["start_line"])
			end := parseIntArg(args["end_line"])
			key := fmt.Sprintf("%s::%d-%d", p, start, end)

			mu.Lock()
			reads[key]++
			n := reads[key]
			fileTotalReads[p]++
			totalFileN := fileTotalReads[p]
			mu.Unlock()

			if n >= 3 || totalFileN >= 5 {
				return map[string]any{
					"text": fmt.Sprintf("AVISO ANTI-LOOP: você já leu %s (%d-%d) %d vezes neste turno sem editar o arquivo. NÃO releia novamente (nem deslocando poucas linhas) — use as informações já obtidas (e o dossiê [chief_context_brief] / [chief_plan]) e avance agora para edit_file/write_file ou conclua a tarefa.", args["path"], start, end, max(n-1, totalFileN-1)),
				}, nil
			}

		case "github_pr_diff":
			num := parseIntArg(args["number"])
			file := normPath(args["file"])
			key := fmt.Sprintf("%d::%s", num, file)
			mu.Lock()
			diffCalls[key]++
			n := diffCalls[key]
			mu.Unlock()
			if n >= 2 {
				return map[string]any{
					"text": fmt.Sprintf("AVISO ANTI-LOOP: você já consultou github_pr_diff para PR #%d (%s) neste turno. NÃO chame github_pr_diff repetidamente para o mesmo arquivo — use o diff já recebido e prossiga para a decisão final.", num, file),
				}, nil
			}

		case "read_skill":
			n := strings.ToLower(strings.TrimSpace(fmt.Sprint(args["name"])))
			mu.Lock()
			searchCalls["read_skill::"+n]++
			calls := searchCalls["read_skill::"+n]
			mu.Unlock()
			if calls >= 2 {
				return map[string]any{
					"text": "AVISO ANTI-LOOP: você já carregou as diretrizes desta skill neste turno. NÃO recarregue — as diretrizes completas já estão no seu contexto; aplique-as e avance para a próxima etapa da tarefa.",
				}, nil
			}
			return nil, nil

		case "search_text", "find_files", "list_dir", "github_list_prs", "github_pr_files":
			q := strings.ToLower(strings.TrimSpace(fmt.Sprint(args["query"])))
			pat := strings.ToLower(strings.TrimSpace(fmt.Sprint(args["pattern"])))
			num := parseIntArg(args["number"])
			p := normPath(args["path"])
			g := strings.ToLower(strings.TrimSpace(fmt.Sprint(args["glob"])))
			key := fmt.Sprintf("%s::%s::%s::%d::%s::%s", name, q, pat, num, p, g)
			mu.Lock()
			searchCalls[key]++
			n := searchCalls[key]
			mu.Unlock()
			if n >= 2 {
				return map[string]any{
					"text": fmt.Sprintf("AVISO ANTI-LOOP: você já executou %s com os mesmos parâmetros neste turno. NÃO repita a mesma busca/listagem — avance imediatamente com os resultados já obtidos (write_file/edit_file ou delegate_to_squad).", name),
				}, nil
			}
		}
		return nil, nil
	}
}

// newDevExploreBudgetCallbacks cria uma dupla BeforeTool/AfterTool em 2 camadas para o Dev:
// 1. Controla a exploração inicial (avisa ao aproximar-se de softBlockAt antes da 1ª edição e faz
//    soft-block redirecionando para write_file/edit_file antes do hard-abort de maxExplore).
// 2. Controla o orçamento total de chamadas da rodada (maxCalls): avisa em warnWrapCallsAt e faz
//    soft-wrapup em softWrapCallsAt para que o Dev encerre o turno com resumo e entregue a
//    worktree ao QA em vez de estourar o hard-abort após já ter escrito código.
func newDevExploreBudgetCallbacks(maxExplore int, roundFn func() int, maxCallsOpt ...int) (llmagent.BeforeToolCallback, llmagent.AfterToolCallback) {
	if maxExplore <= 0 {
		maxExplore = 40
	}
	maxCalls := 120
	if len(maxCallsOpt) > 0 && maxCallsOpt[0] > 0 {
		maxCalls = maxCallsOpt[0]
	}
	softBlockAt := maxExplore - 12
	if softBlockAt > 12 {
		softBlockAt = 12
	}
	if softBlockAt > maxExplore-2 {
		softBlockAt = maxExplore - 2
	}
	if softBlockAt < 6 {
		if maxExplore-1 < 6 {
			softBlockAt = maxExplore - 1
		} else {
			softBlockAt = 6
		}
	}
	if softBlockAt < 1 {
		softBlockAt = 1
	}
	softWarnAt := softBlockAt - 8
	if softWarnAt < 3 {
		softWarnAt = 3
	}
	if softWarnAt > softBlockAt {
		softWarnAt = softBlockAt
	}

	softWrapCallsAt := maxCalls - 14
	if maxCalls <= 25 {
		softWrapCallsAt = maxCalls - 4
	}
	if softWrapCallsAt < 4 {
		softWrapCallsAt = maxCalls - 1
	}
	warnWrapCallsAt := softWrapCallsAt - 12
	if warnWrapCallsAt < softWrapCallsAt/2 {
		warnWrapCallsAt = softWrapCallsAt / 2
	}

	initRound := 0
	if roundFn != nil {
		initRound = roundFn()
	}

	var (
		mu           sync.Mutex
		seenRound    = initRound
		exploreCalls int
		totalCalls   int
		writes       int
	)

	syncRoundLocked := func() {
		if roundFn == nil {
			return
		}
		if cur := roundFn(); cur != seenRound {
			seenRound = cur
			exploreCalls = 0
			totalCalls = 0
			writes = 0
		}
	}

	before := func(_ agent.Context, tl tool.Tool, _ map[string]any) (map[string]any, error) {
		if tl == nil {
			return nil, nil
		}
		name := strings.ToLower(strings.TrimSpace(tl.Name()))
		mu.Lock()
		syncRoundLocked()
		totalCalls++
		curTotal := totalCalls
		curWrites := writes
		mu.Unlock()

		// Ferramentas de fechamento/publicação (commit, push, merge de PR, revisão e transição Jira)
		// nunca podem ser bloqueadas pelo teto de wrap-up logo na linha de chegada — só recebem
		// uma trava ampla de segurança contra loop infinito (maxCalls + 10).
		isClosingAction := isClosingPublishTool(name) || name == "jira_transition" || name == "jira_comment"
		if isClosingAction {
			if curTotal <= maxCalls+10 {
				return nil, nil
			}
		}

		// Se o orçamento total da rodada chegou ao teto de wrap-up (e o dev já escreveu arquivos
		// ou está tentando mais uma leitura/busca), bloqueia novas ferramentas para forçar o
		// encerramento limpo do turno e passagem imediata para o QA.
		if curTotal > softWrapCallsAt && (curWrites > 0 || !devWriteTools[name]) {
			return map[string]any{
				"text": fmt.Sprintf("ORÇAMENTO DE FERRAMENTAS DA RODADA CONCLUÍDO (%d/%d chamadas utilizadas, %d escrita(s) aplicadas na worktree): PARE de chamar ferramentas de exploração/leitura agora. Se já terminou, conclua apenas com fechamento (commit/push/merge/Jira se aplicável) ou responda IMEDIATAMENTE com o resumo final.", curTotal-1, maxCalls, curWrites),
			}, nil
		}

		if devWriteTools[name] {
			mu.Lock()
			writes++
			exploreCalls = 0
			mu.Unlock()
			return nil, nil
		}
		if devExploreTools[name] {
			mu.Lock()
			var blocked bool
			var cur int
			if writes == 0 {
				exploreCalls++
				cur = exploreCalls
				blocked = cur > softBlockAt
			}
			mu.Unlock()
			if blocked {
				return map[string]any{
					"text": fmt.Sprintf("ORÇAMENTO DE EXPLORAÇÃO DO DEV ATINGIDO (%d leituras/buscas seguidas sem editar nenhum arquivo): PARE de explorar novos arquivos agora. Você já tem contexto suficiente em [chief_plan], [chief_context_brief] e nas leituras anteriores. Comece IMEDIATAMENTE a implementar código usando write_file (arquivos novos) ou edit_file (arquivos existentes) — assim que criar ou editar o primeiro arquivo, leituras pontuais voltam a ser liberadas e o limite rígido (%d) não interromperá a execução.", cur-1, maxExplore),
				}, nil
			}
		}
		return nil, nil
	}

	after := func(_ agent.Context, tl tool.Tool, _ map[string]any, result map[string]any, err error) (map[string]any, error) {
		if err != nil || tl == nil || result == nil {
			return nil, nil
		}
		name := strings.ToLower(strings.TrimSpace(tl.Name()))
		mu.Lock()
		syncRoundLocked()
		shouldWarnExplore := devExploreTools[name] && writes == 0 && exploreCalls >= softWarnAt && exploreCalls <= softBlockAt
		curExplore := exploreCalls
		shouldWarnTotal := totalCalls >= warnWrapCallsAt && totalCalls <= softWrapCallsAt
		curTotal := totalCalls
		mu.Unlock()
		if !shouldWarnExplore && !shouldWarnTotal {
			return nil, nil
		}
		var notices []string
		if shouldWarnExplore {
			notices = append(notices, fmt.Sprintf("[AVISO DE ORÇAMENTO DO DEV: %d/%d leituras antes da 1ª edição — pare de explorar amplamente e comece a criar/editar arquivos com write_file/edit_file já no próximo turno.]", curExplore, softBlockAt))
		}
		if shouldWarnTotal {
			notices = append(notices, fmt.Sprintf("[AVISO DE ENCERRAMENTO DA RODADA: %d/%d chamadas de ferramentas utilizadas nesta rodada — conclua a edição atual imediatamente e encerre o turno com seu resumo final para entregar a worktree ao QA.]", curTotal, softWrapCallsAt))
		}
		warn := strings.Join(notices, "\n")
		out := make(map[string]any, len(result)+1)
		for k, v := range result {
			out[k] = v
		}
		if txt, ok := result["text"].(string); ok {
			out["text"] = txt + "\n\n" + warn
		} else {
			out["budget_warning"] = warn
		}
		return out, nil
	}

	return before, after
}
