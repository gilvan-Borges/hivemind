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
	return 0
}

// newRepeatReadGuardCallback returns a BeforeToolCallback that prevents an agent
// from entering a re-read death spiral (calling read_file on the exact same
// file/range 3+ times, or shifting line ranges on the same file 4+ times without
// modifying the file in between, or repeating the same github_pr_diff / search_text).
// Any edit_file or write_file on that path resets its read counter immediately.
func newRepeatReadGuardCallback() llmagent.BeforeToolCallback {
	var mu sync.Mutex
	reads := make(map[string]int)
	fileTotalReads := make(map[string]int)
	diffCalls := make(map[string]int)
	searchCalls := make(map[string]int)

	normPath := func(raw any) string {
		s := strings.TrimSpace(fmt.Sprint(raw))
		if s == "" || s == "<nil>" {
			return ""
		}
		return strings.ToLower(filepath.ToSlash(filepath.Clean(s)))
	}

	return func(_ agent.Context, tl tool.Tool, args map[string]any) (map[string]any, error) {
		if tl == nil || len(args) == 0 {
			return nil, nil
		}
		name := strings.ToLower(strings.TrimSpace(tl.Name()))
		switch name {
		case "edit_file", "write_file", "write_doc":
			p := normPath(args["path"])
			if p != "" {
				mu.Lock()
				prefix := p + "::"
				for k := range reads {
					if strings.HasPrefix(k, prefix) {
						delete(reads, k)
					}
				}
				delete(fileTotalReads, p)
				mu.Unlock()
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

		case "search_text":
			q := strings.ToLower(strings.TrimSpace(fmt.Sprint(args["query"])))
			p := normPath(args["path"])
			g := strings.ToLower(strings.TrimSpace(fmt.Sprint(args["glob"])))
			if q != "" && q != "<nil>" {
				key := fmt.Sprintf("%s::%s::%s", q, p, g)
				mu.Lock()
				searchCalls[key]++
				n := searchCalls[key]
				mu.Unlock()
				if n >= 2 {
					return map[string]any{
						"text": fmt.Sprintf("AVISO ANTI-LOOP: você já executou search_text(%q) neste mesmo escopo. NÃO repita a mesma busca — avance com os resultados já obtidos.", q),
					}, nil
				}
			}
		}
		return nil, nil
	}
}

// newDevExploreBudgetCallbacks cria uma dupla BeforeTool/AfterTool em 2 camadas para o Dev:
// avisa ao aproximar-se do limite antes da 1ª edição e faz soft-block redirecionando para
// write_file/edit_file antes que o hard-abort de maxExplore seja atingido.
func newDevExploreBudgetCallbacks(maxExplore int, roundFn func() int) (llmagent.BeforeToolCallback, llmagent.AfterToolCallback) {
	if maxExplore <= 0 {
		maxExplore = 40
	}
	softBlockAt := maxExplore - 12
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

	initRound := 0
	if roundFn != nil {
		initRound = roundFn()
	}

	var (
		mu           sync.Mutex
		seenRound    = initRound
		exploreCalls int
		writes       int
	)

	syncRoundLocked := func() {
		if roundFn == nil {
			return
		}
		if cur := roundFn(); cur != seenRound {
			seenRound = cur
			exploreCalls = 0
			writes = 0
		}
	}

	before := func(_ agent.Context, tl tool.Tool, _ map[string]any) (map[string]any, error) {
		if tl == nil {
			return nil, nil
		}
		name := strings.ToLower(strings.TrimSpace(tl.Name()))
		if devWriteTools[name] {
			mu.Lock()
			syncRoundLocked()
			writes++
			exploreCalls = 0
			mu.Unlock()
			return nil, nil
		}
		if devExploreTools[name] {
			mu.Lock()
			syncRoundLocked()
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
		if !devExploreTools[name] {
			return nil, nil
		}
		mu.Lock()
		syncRoundLocked()
		shouldWarn := writes == 0 && exploreCalls >= softWarnAt && exploreCalls <= softBlockAt
		cur := exploreCalls
		mu.Unlock()
		if !shouldWarn {
			return nil, nil
		}
		warn := fmt.Sprintf("[AVISO DE ORÇAMENTO DO DEV: %d/%d leituras antes da 1ª edição — pare de explorar amplamente e comece a criar/editar arquivos com write_file/edit_file já no próximo turno.]", cur, softBlockAt)
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
