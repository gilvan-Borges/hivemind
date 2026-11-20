package orchestrator

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

const (
	// maxPreloadedPRDiffBytes caps the total unified diff patches injected into Turn 1
	// (~6k-7k tokens), enough to fit 100% of small/medium PRs in a single turn.
	maxPreloadedPRDiffBytes = 26000
	// maxSingleFilePreloadPatchBytes caps a single file's patch in the pre-hydrated dossier.
	maxSingleFilePreloadPatchBytes = 4500
	// defaultMaxReviewExploreCalls caps repository exploration calls during a PR review
	// so the reviewer never spirals into a 40-turn / 2M-token repo crawl.
	defaultMaxReviewExploreCalls = 6
	// defaultMaxTriageExploreCalls caps repository exploration calls by the Triage Chief
	// before it must call delegate_to_squad (OpenAI Codex / Swarm fast-router pattern).
	defaultMaxTriageExploreCalls = 6
)

var (
	prNumberRegex   = regexp.MustCompile(`(?i)\b(?:pr|pull\s+request)\s*#(\d+)\b`)
	jiraKeyInTextRe = regexp.MustCompile(`\b([A-Z][A-Z0-9]{1,9}-\d+)\b`)
)

// extractReviewPRNumber extracts the PR number (#N) from a review objective prompt.
func extractReviewPRNumber(objective string) int {
	text := strings.TrimSpace(objective)
	if idx := strings.LastIndex(text, "[Objetivo do Usuário]"); idx >= 0 {
		text = text[idx+len("[Objetivo do Usuário]"):]
	}
	m := prNumberRegex.FindStringSubmatch(text)
	if len(m) < 2 {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// isGeneratedOrLockfile reports whether a file path is a lockfile, minified bundle,
// snapshot, or generated artifact whose diff should not consume Turn-1 prompt tokens.
func isGeneratedOrLockfile(filename string) bool {
	low := strings.ToLower(filepath.ToSlash(strings.TrimSpace(filename)))
	base := filepath.Base(low)
	switch base {
	case "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb", "bun.lock",
		"go.sum", "cargo.lock", "composer.lock", "poetry.lock", "uv.lock", "gemfile.lock":
		return true
	}
	for _, suf := range []string{
		".min.js", ".min.css", ".bundle.js", ".map", ".snap",
		".pb.go", "_gen.go", ".gen.ts", ".gen.js", ".generated.ts",
	} {
		if strings.HasSuffix(low, suf) {
			return true
		}
	}
	for _, dir := range []string{"dist/", "build/", "node_modules/", "vendor/", ".next/", "coverage/"} {
		if strings.HasPrefix(low, dir) || strings.Contains(low, "/"+dir) {
			return true
		}
	}
	return false
}

// buildPreHydratedPRDossier fetches PR metadata, reviews, comments, CI status, and
// prioritized diff patches in parallel in Go (<400ms, 0 tool-call round-trips) and
// formats a Turn-1 review dossier. It also reports whether the PR is authored by the
// currently authenticated GitHub user (isOwnPR) and whether every non-generated file's
// patch fit completely inside the dossier (allPatchesComplete).
func buildPreHydratedPRDossier(parentCtx context.Context, gh githubx.Client, prNum int) (dossier string, isOwnPR bool, allPatchesComplete bool) {
	if gh == nil || !gh.Enabled() || prNum <= 0 {
		return "", false, false
	}
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	var (
		wg       sync.WaitGroup
		prDetail githubx.PullRequestDetail
		prErr    error
		files    []githubx.PRFile
		filesErr error
		me       string
	)

	wg.Add(3)
	go func() {
		defer wg.Done()
		prDetail, prErr = gh.GetPullRequest(ctx, prNum)
		if prErr == nil && len(prDetail.IssueComments) == 0 {
			if extra, err := gh.ListIssueComments(ctx, prNum); err == nil {
				prDetail.IssueComments = extra
			}
		}
	}()
	go func() {
		defer wg.Done()
		files, filesErr = gh.ListPullRequestFiles(ctx, prNum)
	}()
	go func() {
		defer wg.Done()
		me, _ = gh.CurrentUser(ctx)
	}()
	wg.Wait()

	if prErr != nil || prDetail.Number == 0 {
		return "", false, false
	}

	meTrim := strings.TrimSpace(me)
	isOwnPR = meTrim != "" && strings.EqualFold(strings.TrimSpace(prDetail.Author), meTrim)

	ciState := ""
	if ref := strings.TrimSpace(prDetail.HeadRef); ref != "" {
		if st, err := gh.CIStatus(ctx, ref); err == nil {
			ciState = strings.TrimSpace(st)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n\n[Dossiê Pré-Carregado do PR #%d (Zero-Roundtrip — NÃO chame github_get_pr nem github_pr_files para este PR)]\n", prDetail.Number)
	roleTag := "PR DE TERCEIRO (você é o revisor técnico)"
	if isOwnPR {
		roleTag = "NOSSO PR (autor = usuário autenticado; NÃO emita github_review_pr neste PR — atenda feedbacks via delegate_to_squad)"
	}
	fmt.Fprintf(&b, "- **Título:** %s\n", strings.TrimSpace(prDetail.Title))
	fmt.Fprintf(&b, "- **Autor:** @%s | **Papel:** %s\n", prDetail.Author, roleTag)
	fmt.Fprintf(&b, "- **Branches:** `%s` -> `%s` | **Estado:** %s\n", prDetail.HeadRef, prDetail.BaseRef, prDetail.State)
	if ciState != "" {
		fmt.Fprintf(&b, "- **Status CI (`github_ci_status`):** %s\n", ciState)
	}
	if len(prDetail.RequestedReviewers) > 0 {
		fmt.Fprintf(&b, "- **Revisores solicitados:** %s\n", strings.Join(prDetail.RequestedReviewers, ", "))
	}
	if body := strings.TrimSpace(prDetail.Body); body != "" {
		fmt.Fprintf(&b, "\n### Descrição do PR\n%s\n", strx.TruncateBytes(body, 2200, "\n…"))
	}

	if len(prDetail.Reviews) > 0 {
		b.WriteString("\n### Revisões Formais Anteriores\n")
		start := 0
		if len(prDetail.Reviews) > 8 {
			start = len(prDetail.Reviews) - 8
		}
		for _, rv := range prDetail.Reviews[start:] {
			body := strx.TruncateBytes(strings.TrimSpace(rv.Body), 450, "…")
			if body != "" {
				fmt.Fprintf(&b, "- @%s [%s] (%s): %s\n", rv.Author, rv.State, rv.SubmittedAt, body)
			} else {
				fmt.Fprintf(&b, "- @%s [%s] (%s)\n", rv.Author, rv.State, rv.SubmittedAt)
			}
		}
	}

	if len(prDetail.ReviewComments) > 0 {
		b.WriteString("\n### Comentários Inline no Código\n")
		start := 0
		if len(prDetail.ReviewComments) > 15 {
			start = len(prDetail.ReviewComments) - 15
		}
		for _, c := range prDetail.ReviewComments[start:] {
			loc := c.Path
			if c.Line > 0 {
				loc = fmt.Sprintf("%s:%d", c.Path, c.Line)
			}
			fmt.Fprintf(&b, "- [comment_id=%d] @%s em `%s`: %s\n",
				c.ID, c.Author, loc, strx.TruncateBytes(strings.TrimSpace(c.Body), 450, "…"))
		}
	}

	if len(prDetail.IssueComments) > 0 {
		b.WriteString("\n### Comentários Gerais Recentes\n")
		start := 0
		if len(prDetail.IssueComments) > 8 {
			start = len(prDetail.IssueComments) - 8
		}
		for _, c := range prDetail.IssueComments[start:] {
			fmt.Fprintf(&b, "- @%s (%s): %s\n",
				c.Author, c.CreatedAt, strx.TruncateBytes(strings.TrimSpace(c.Body), 500, "…"))
		}
	}

	if filesErr != nil || len(files) == 0 {
		return b.String(), isOwnPR, false
	}

	totalAdd, totalDel := 0, 0
	for _, f := range files {
		totalAdd += f.Additions
		totalDel += f.Deletions
	}
	fmt.Fprintf(&b, "\n### Arquivos Alterados (%d arquivo(s), +%d -%d)\n", len(files), totalAdd, totalDel)
	for _, f := range files {
		noiseTag := ""
		if isGeneratedOrLockfile(f.Filename) {
			noiseTag = " (lockfile/gerado — patch omitido)"
		}
		fmt.Fprintf(&b, "- `%s` [%s] (+%d -%d)%s\n", f.Filename, f.Status, f.Additions, f.Deletions, noiseTag)
	}

	// Order files so primary production code comes before tests/docs, and lockfiles are skipped.
	type scoredFile struct {
		file  githubx.PRFile
		score int
	}
	var reviewable []scoredFile
	for _, f := range files {
		if isGeneratedOrLockfile(f.Filename) || strings.TrimSpace(f.Patch) == "" {
			continue
		}
		low := strings.ToLower(f.Filename)
		sc := 0
		switch {
		case strings.HasSuffix(low, ".md") || strings.HasSuffix(low, ".txt"):
			sc = 2
		case strings.Contains(low, "test") || strings.Contains(low, "spec."):
			sc = 1
		default:
			sc = 0
		}
		reviewable = append(reviewable, scoredFile{file: f, score: sc})
	}
	sort.SliceStable(reviewable, func(i, j int) bool {
		return reviewable[i].score < reviewable[j].score
	})

	b.WriteString("\n### Diff Unificado Pré-Carregado\n")
	usedBytes := 0
	allPatchesComplete = true
	for _, item := range reviewable {
		f := item.file
		patch := strings.TrimSpace(f.Patch)
		remaining := maxPreloadedPRDiffBytes - usedBytes
		if remaining <= 300 {
			allPatchesComplete = false
			fmt.Fprintf(&b, "\n=== %s [%s] (+%d -%d) [PATCH OMITIDO POR ORÇAMENTO — use github_pr_diff(number=%d, file=%q) SOMENTE se precisar auditar este arquivo] ===\n",
				f.Filename, f.Status, f.Additions, f.Deletions, prNum, f.Filename)
			continue
		}
		capBytes := maxSingleFilePreloadPatchBytes
		if len(reviewable) <= 5 {
			capBytes = 9000
		}
		if capBytes > remaining {
			capBytes = remaining
		}
		if len(patch) > capBytes {
			allPatchesComplete = false
			truncated := strx.TruncateBytes(patch, capBytes, fmt.Sprintf("\n... [patch truncado; passe file=%q em github_pr_diff apenas se precisar ver o restante]", f.Filename))
			fmt.Fprintf(&b, "\n=== %s [%s] (+%d -%d) [PARCIAL] ===\n%s\n", f.Filename, f.Status, f.Additions, f.Deletions, truncated)
			usedBytes += len(truncated)
		} else {
			fmt.Fprintf(&b, "\n=== %s [%s] (+%d -%d) [PATCH COMPLETO] ===\n%s\n", f.Filename, f.Status, f.Additions, f.Deletions, patch)
			usedBytes += len(patch)
		}
	}

	return b.String(), isOwnPR, allPatchesComplete
}

// newPRReviewGuardCallbacks returns BeforeTool and AfterTool callbacks that enforce
// Anthropic-style bounded PR reviews:
// 1. Blocks redundant calls to github_get_pr / github_pr_files / github_ci_status for the preloaded PR.
// 2. Blocks calling github_pr_diff without a file filter when all patches are already preloaded.
// 3. Caps repository exploration calls (read_file, search_text, find_files, list_dir, github_pr_diff)
//    at maxExploreCalls so the reviewer submits its verdict in 1-4 turns instead of 40 turns.
// 4. Escalates (finishes the turn immediately) once github_review_pr succeeds.
func newPRReviewGuardCallbacks(preloadedPR int, allPatchesComplete bool, maxExploreCalls int) (llmagent.BeforeToolCallback, llmagent.AfterToolCallback) {
	if maxExploreCalls <= 0 {
		maxExploreCalls = defaultMaxReviewExploreCalls
	}
	var (
		mu              sync.Mutex
		exploreCount    int
		reviewSubmitted bool
	)

	before := func(_ agent.Context, tl tool.Tool, args map[string]any) (map[string]any, error) {
		if tl == nil {
			return nil, nil
		}
		name := strings.ToLower(strings.TrimSpace(tl.Name()))

		mu.Lock()
		submitted := reviewSubmitted
		mu.Unlock()
		if submitted {
			return map[string]any{
				"text": "A revisão oficial já foi enviada ao GitHub com sucesso neste turno. NÃO execute novas ferramentas — apenas entregue o resumo final curto ao usuário.",
			}, nil
		}

		// Short-circuit redundant GitHub calls when the PR dossier is already in the prompt.
		if preloadedPR > 0 {
			argNum := parseIntArg(args["number"])
			switch name {
			case "github_get_pr", "github_pr_files", "github_issue_comments":
				if argNum == 0 || argNum == preloadedPR {
					return map[string]any{
						"text": fmt.Sprintf("CACHE DO DOSSIÊ PR #%d: os metadados, revisões anteriores, comentários inline/gerais e a lista completa de arquivos já estão pré-carregados na seção [Dossiê Pré-Carregado do PR #%d] da mensagem inicial. Não gaste turnos re-buscando esses dados; analise o diff já fornecido e publique o veredito com github_review_pr.", preloadedPR, preloadedPR),
					}, nil
				}
			case "github_ci_status":
				return map[string]any{
					"text": fmt.Sprintf("CACHE DO DOSSIÊ PR #%d: o status do CI já foi consultado e incluído no cabeçalho do [Dossiê Pré-Carregado do PR #%d]. Prossiga diretamente para a análise ou github_review_pr.", preloadedPR, preloadedPR),
				}, nil
			case "github_pr_diff":
				fileFilter := strings.TrimSpace(fmt.Sprint(args["file"]))
				if fileFilter == "<nil>" {
					fileFilter = ""
				}
				if (argNum == 0 || argNum == preloadedPR) && allPatchesComplete && fileFilter == "" {
					return map[string]any{
						"text": fmt.Sprintf("CACHE DO DOSSIÊ PR #%d: 100%% dos patches de código deste PR já estão marcados como [PATCH COMPLETO] no [Dossiê Pré-Carregado do PR #%d] acima. Não chame github_pr_diff novamente; avalie o código já visível e chame github_review_pr.", preloadedPR, preloadedPR),
					}, nil
				}
			}
		}

		// Enforce bounded exploration budget for repository lookups during review.
		switch name {
		case "read_file", "search_text", "find_files", "list_dir", "github_pr_diff", "git_diff":
			mu.Lock()
			exploreCount++
			cur := exploreCount
			mu.Unlock()
			if cur > maxExploreCalls {
				return map[string]any{
					"text": fmt.Sprintf("ORÇAMENTO DE VERIFICAÇÃO DA REVISÃO ATINGIDO (%d/%d leituras/buscas): pare imediatamente de explorar novos arquivos do repositório para evitar alto consumo de tokens. Com as evidências que você já coletou e o diff do PR, chame AGORA github_review_pr com seu parecer técnico objetivo.", cur-1, maxExploreCalls),
				}, nil
			}
		}
		return nil, nil
	}

	after := func(ctx agent.Context, tl tool.Tool, _ map[string]any, result map[string]any, err error) (map[string]any, error) {
		if err != nil || tl == nil {
			return nil, nil
		}
		if strings.EqualFold(strings.TrimSpace(tl.Name()), "github_review_pr") {
			if hasErr, ok := result["error"]; ok && strings.TrimSpace(fmt.Sprint(hasErr)) != "" {
				return nil, nil
			}
			mu.Lock()
			reviewSubmitted = true
			mu.Unlock()
		}
		return nil, nil
	}

	return before, after
}

// extractJiraKeyFromObjective finds an explicit Jira issue key (e.g. "IPG-874", "VPD-57")
// inside the user's objective text while ignoring spec/decision codes (RN-018, D-179, FT-232, T-042).
func extractJiraKeyFromObjective(objective string) string {
	text := strings.TrimSpace(objective)
	if idx := strings.LastIndex(text, "[Objetivo do Usuário]"); idx >= 0 {
		text = text[idx+len("[Objetivo do Usuário]"):]
	}
	ignorePrefixes := map[string]bool{
		"D": true, "RN": true, "FT": true, "T": true, "ADR": true,
		"UTF": true, "SHA": true, "ISO": true, "HTTP": true, "CVE": true, "PR": true,
	}
	for _, m := range jiraKeyInTextRe.FindAllStringSubmatch(text, -1) {
		if len(m) < 2 {
			continue
		}
		cand := strings.ToUpper(strings.TrimSpace(m[1]))
		if dash := strings.IndexByte(cand, '-'); dash > 0 {
			pref := cand[:dash]
			if ignorePrefixes[pref] {
				continue
			}
			return cand
		}
	}
	return ""
}

func isJiraDiscoveryPrompt(objective string) bool {
	text := strings.TrimSpace(objective)
	if idx := strings.LastIndex(text, "[Objetivo do Usuário]"); idx >= 0 {
		text = text[idx+len("[Objetivo do Usuário]"):]
	}
	low := strings.ToLower(text)
	return strings.Contains(low, "jira") || strings.Contains(low, "subtask") || strings.Contains(low, "sub-task") || strings.Contains(low, "sub task")
}

// buildPreHydratedJiraDossier pre-fetches in Go (<400ms, 0 LLM tool calls) the target Jira issue,
// all of its subtasks / child tasks (via inline subtasks + JQL `parent = KEY`), and highlights
// the first pending subtask/task so the Chief immediately knows what to scope and delegate
// without looping across 30 files or repeating jira_get/jira_search calls.
func buildPreHydratedJiraDossier(parentCtx context.Context, jc jirax.Client, explicitKey, defaultProject, objective string) (dossier string, resolvedKey string) {
	if jc == nil || !jc.Enabled() {
		return "", ""
	}
	ctx, cancel := context.WithTimeout(parentCtx, 8*time.Second)
	defer cancel()

	key := strings.ToUpper(strings.TrimSpace(explicitKey))
	if key == "" {
		key = extractJiraKeyFromObjective(objective)
	}

	// If no specific issue key was given, but the user asked to look at Jira tasks/subtasks,
	// pre-fetch the open tasks for the project so Turn 1 already has the backlog + subtasks!
	if key == "" {
		if !isJiraDiscoveryPrompt(objective) {
			return "", ""
		}
		proj := strings.ToUpper(strings.TrimSpace(defaultProject))
		if proj == "NONE" || proj == "*" {
			proj = ""
		}
		jql := "assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC"
		if proj != "" {
			jql = fmt.Sprintf(`project = "%s" AND assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC`, proj)
		}
		openList, err := jc.Search(ctx, jql, 12)
		if err != nil || len(openList) == 0 {
			return "", ""
		}
		var b strings.Builder
		b.WriteString("\n\n[Dossiê Pré-Carregado do Jira — Tasks & Subtasks Abertas (Zero-Roundtrip)]\n")
		for i, it := range openList {
			subLabel := ""
			if len(it.Subtasks) > 0 {
				var pendingSubs []string
				for _, st := range it.Subtasks {
					if !jirax.IsDoneStatus(st.Status) {
						pendingSubs = append(pendingSubs, fmt.Sprintf("%s [%s] %s", st.Key, st.Status, st.Summary))
					}
				}
				if len(pendingSubs) > 0 {
					subLabel = fmt.Sprintf(" -> Subtasks pendentes: %s", strings.Join(pendingSubs, " | "))
				}
			}
			fmt.Fprintf(&b, "%d. **%s** [%s] — %s%s\n", i+1, it.Key, it.Status, it.Summary, subLabel)
		}
		// Automatically expand the top open task's children if it has any.
		top := openList[0]
		if children, cErr := jc.Search(ctx, fmt.Sprintf(`parent = "%s" ORDER BY key ASC`, top.Key), 25); cErr == nil && len(children) > 0 {
			fmt.Fprintf(&b, "\n### Subtasks / Tasks Filhas de %s (%s)\n", top.Key, top.Summary)
			for _, ch := range children {
				doneTag := "PENDENTE"
				if jirax.IsDoneStatus(ch.Status) {
					doneTag = "CONCLUÍDA — IGNORAR"
				}
				fmt.Fprintf(&b, "- **%s** [%s · %s] — %s\n", ch.Key, ch.Status, doneTag, ch.Summary)
			}
		}
		return b.String(), top.Key
	}

	var (
		wg          sync.WaitGroup
		parentIssue jirax.Issue
		parentErr   error
		jqlChildren []jirax.Issue
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		parentIssue, parentErr = jc.GetIssue(ctx, key)
	}()
	go func() {
		defer wg.Done()
		jqlChildren, _ = jc.Search(ctx, fmt.Sprintf(`parent = "%s" ORDER BY key ASC`, key), 40)
	}()
	wg.Wait()

	if parentErr != nil && len(jqlChildren) == 0 {
		return "", key
	}

	// Merge inline subtasks with JQL child issues (for Epics/Stories whose children are Tasks).
	seenChild := map[string]bool{}
	var allChildren []jirax.Issue
	for _, st := range parentIssue.Subtasks {
		k := strings.ToUpper(strings.TrimSpace(st.Key))
		if k == "" || seenChild[k] {
			continue
		}
		seenChild[k] = true
		allChildren = append(allChildren, jirax.Issue{
			Key:     st.Key,
			Summary: st.Summary,
			Status:  st.Status,
		})
	}
	for _, ch := range jqlChildren {
		k := strings.ToUpper(strings.TrimSpace(ch.Key))
		if k == "" || seenChild[k] {
			// Enrich description if already in list from inline subtasks
			for i := range allChildren {
				if strings.EqualFold(allChildren[i].Key, k) && allChildren[i].Description == "" {
					allChildren[i].Description = ch.Description
				}
			}
			continue
		}
		seenChild[k] = true
		allChildren = append(allChildren, ch)
	}

	var pendingChildren, doneChildren []jirax.Issue
	for _, ch := range allChildren {
		if jirax.IsDoneStatus(ch.Status) {
			doneChildren = append(doneChildren, ch)
		} else {
			pendingChildren = append(pendingChildren, ch)
		}
	}

	// If the 1st pending child has no description yet (came from inline subtasks), fetch it once.
	if len(pendingChildren) > 0 && pendingChildren[0].Description == "" {
		if fullChild, err := jc.GetIssue(ctx, pendingChildren[0].Key); err == nil && fullChild.Description != "" {
			pendingChildren[0].Description = fullChild.Description
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n\n[Dossiê Pré-Carregado do Jira — %s (Zero-Roundtrip — NÃO chame jira_get nem jira_search para %s)]\n", key, key)
	if parentIssue.Key != "" {
		typeTag := parentIssue.IssueType
		if typeTag == "" {
			typeTag = "Issue"
		}
		fmt.Fprintf(&b, "- **Chave:** `%s` (%s) | **Status:** `%s`\n", parentIssue.Key, typeTag, parentIssue.Status)
		fmt.Fprintf(&b, "- **Título:** %s\n", parentIssue.Summary)
		if parentIssue.ParentKey != "" {
			fmt.Fprintf(&b, "- **Issue Pai:** `%s` — %s\n", parentIssue.ParentKey, parentIssue.ParentSummary)
		}
		if desc := strings.TrimSpace(parentIssue.Description); desc != "" {
			fmt.Fprintf(&b, "- **Descrição:**\n%s\n", strx.TruncateBytes(desc, 2200, "\n…"))
		}
	}

	if len(allChildren) > 0 {
		fmt.Fprintf(&b, "\n### Subtasks / Tasks Filhas de %s (%d pendente(s), %d concluída(s))\n",
			key, len(pendingChildren), len(doneChildren))
		if len(pendingChildren) > 0 {
			b.WriteString("**PENDENTES / EM ANDAMENTO (Foco de execução imediata):**\n")
			for i, ch := range pendingChildren {
				focusTag := ""
				if i == 0 {
					focusTag = " 👈 **[PRÓXIMA SUBTASK ALVO RECOMENDADA]**"
				}
				fmt.Fprintf(&b, "%d. **%s** [%s] — %s%s\n", i+1, ch.Key, ch.Status, ch.Summary, focusTag)
				if i < 2 && strings.TrimSpace(ch.Description) != "" {
					fmt.Fprintf(&b, "   Descrição de %s: %s\n", ch.Key, strx.TruncateBytes(strings.TrimSpace(ch.Description), 1000, "…"))
				}
			}
		}
		if len(doneChildren) > 0 {
			b.WriteString("**JÁ CONCLUÍDAS (NÃO refazer):**\n")
			for _, ch := range doneChildren {
				fmt.Fprintf(&b, "- ~%s~ [%s] — %s\n", ch.Key, ch.Status, ch.Summary)
			}
		}
		if len(pendingChildren) > 0 {
			fmt.Fprintf(&b, "\n**DIRETRIZ OPENAI CODEX PARA %s:** Foque na próxima subtask pendente (`%s — %s`, ou até 2-3 subtasks pendentes independentes desta lista), faça no máximo 1 turno rápido de leitura cirúrgica (3 a 5 arquivos) e chame **OBRIGATORIAMENTE `delegate_to_squad`** para criar a worktree isolada e colocar o(s) Desenvolvedor(es) para implementar agora.\n",
				key, pendingChildren[0].Key, pendingChildren[0].Summary)
		}
	}

	return b.String(), key
}

// newChiefToolStrippingModelCallback returns a BeforeModelCallback that physically removes
// exploration tools (read_file, search_text, find_files, list_dir, jira_get, jira_search,
// github_pr_diff, github_get_pr, github_pr_files) from req.Config.Tools once the Chief has
// already executed maxExploreCalls exploration tool calls (or maxExploreTurns model turns
// with tool calls). This makes an 18-turn exploration spiral physically impossible across
// every LLM provider (AGY, OpenRouter, Gemini, Claude, OpenAI): once exploration tools are
// removed from the tool declarations, the model MUST either call delegate_to_squad /
// github_review_pr or emit its final answer immediately.
func newChiefToolStrippingModelCallback(maxExploreCalls, maxExploreTurns int, isReview bool) llmagent.BeforeModelCallback {
	if maxExploreCalls <= 0 {
		maxExploreCalls = 8
	}
	if maxExploreTurns <= 0 {
		maxExploreTurns = 3
	}
	exploreNames := map[string]bool{
		"read_file":             true,
		"search_text":           true,
		"find_files":            true,
		"list_dir":              true,
		"git_diff":              true,
		"git_status":            true,
		"jira_get":              true,
		"jira_search":           true,
		"github_get_pr":         true,
		"github_pr_files":       true,
		"github_pr_diff":        true,
		"github_issue_comments": true,
		"github_ci_status":      true,
		"github_list_prs":       true,
	}

	return func(_ agent.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
		if req == nil || req.Config == nil || len(req.Config.Tools) == 0 {
			return nil, nil
		}
		// Count exploration calls made in the current turn (after the last plain-text user prompt).
		startIdx := 0
		for i := len(req.Contents) - 1; i >= 0; i-- {
			c := req.Contents[i]
			if c == nil || c.Role != "user" {
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
				startIdx = i
				break
			}
		}

		exploreCalls := 0
		exploreTurns := 0
		for i := startIdx; i < len(req.Contents); i++ {
			c := req.Contents[i]
			if c == nil {
				continue
			}
			turnHadExplore := false
			for _, p := range c.Parts {
				if p != nil && p.FunctionCall != nil {
					name := strings.ToLower(strings.TrimSpace(p.FunctionCall.Name))
					if exploreNames[name] {
						exploreCalls++
						turnHadExplore = true
					}
				}
			}
			if turnHadExplore {
				exploreTurns++
			}
		}

		if exploreCalls < maxExploreCalls && exploreTurns < maxExploreTurns {
			return nil, nil
		}

		// Physically strip exploration tools from req.Config.Tools so the LLM cannot make another read/search turn.
		var filteredTools []*genai.Tool
		for _, t := range req.Config.Tools {
			if t == nil {
				continue
			}
			var keptDecls []*genai.FunctionDeclaration
			for _, d := range t.FunctionDeclarations {
				if d == nil {
					continue
				}
				if exploreNames[strings.ToLower(strings.TrimSpace(d.Name))] {
					continue
				}
				keptDecls = append(keptDecls, d)
			}
			if len(keptDecls) > 0 {
				tCopy := *t
				tCopy.FunctionDeclarations = keptDecls
				filteredTools = append(filteredTools, &tCopy)
			} else if len(t.FunctionDeclarations) == 0 {
				filteredTools = append(filteredTools, t)
			}
		}
		cfgCopy := *req.Config
		cfgCopy.Tools = filteredTools
		if cfgCopy.SystemInstruction != nil && len(cfgCopy.SystemInstruction.Parts) > 0 {
			directive := "\n\n[GATE DE EFICIÊNCIA ATIVO: orçamento de leitura/exploração da triagem concluído. As ferramentas read_file/search_text/find_files foram desligadas neste turno. Chame AGORA delegate_to_squad (preenchendo plan, context_brief e dev_tasks para criar a worktree e iniciar o Dev) ou conclua sua resposta imediatamente.]"
			if isReview {
				directive = "\n\n[GATE DE EFICIÊNCIA ATIVO: orçamento de leitura da revisão concluído. As ferramentas read_file/search_text/github_pr_diff foram desligadas neste turno. Chame AGORA github_review_pr com seu parecer técnico final e encerre.]"
			}
			sysParts := make([]*genai.Part, len(cfgCopy.SystemInstruction.Parts))
			copy(sysParts, cfgCopy.SystemInstruction.Parts)
			lastIdx := len(sysParts) - 1
			if sysParts[lastIdx] != nil {
				pCopy := *sysParts[lastIdx]
				pCopy.Text += directive
				sysParts[lastIdx] = &pCopy
			}
			cfgCopy.SystemInstruction = &genai.Content{
				Role:  cfgCopy.SystemInstruction.Role,
				Parts: sysParts,
			}
		}
		req.Config = &cfgCopy
		return nil, nil
	}
}
// on the Triage Chief during implementation/planning runs:
// 1. Short-circuits redundant jira_get(preloadedJiraKey) calls when the Jira hierarchy dossier is already preloaded.
// 2. Caps repository exploration calls (read_file, search_text, find_files, list_dir, jira_get, jira_search)
//    at maxExploreCalls (default 6) so the Chief never loops for 17 minutes / 1.8M tokens without delegating.
// 3. Once delegate_to_squad is called, blocks any further exploration tool calls so the pipeline
//    immediately creates the worktree and launches the Developer agent(s).
func newChiefTriageBudgetCallbacks(preloadedJiraKey string, maxExploreCalls int) (llmagent.BeforeToolCallback, llmagent.AfterToolCallback) {
	if maxExploreCalls <= 0 {
		maxExploreCalls = defaultMaxTriageExploreCalls
	}
	cleanKey := strings.ToUpper(strings.TrimSpace(preloadedJiraKey))

	var (
		mu           sync.Mutex
		exploreCount int
		delegated    bool
	)

	before := func(_ agent.Context, tl tool.Tool, args map[string]any) (map[string]any, error) {
		if tl == nil {
			return nil, nil
		}
		name := strings.ToLower(strings.TrimSpace(tl.Name()))

		mu.Lock()
		isDelegated := delegated
		mu.Unlock()
		if isDelegated && name != "jira_transition" && name != "jira_comment" {
			return map[string]any{
				"text": "delegate_to_squad já foi registrado com sucesso neste turno. NÃO execute novas leituras ou ferramentas — encerre sua resposta imediatamente com um resumo curto de 2 linhas para o orquestrador criar a worktree e iniciar os Desenvolvedores.",
			}, nil
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
		case "read_file", "search_text", "find_files", "list_dir", "jira_get", "jira_search", "git_diff", "git_status":
			mu.Lock()
			exploreCount++
			cur := exploreCount
			mu.Unlock()
			if cur > maxExploreCalls {
				return map[string]any{
					"text": fmt.Sprintf("ORÇAMENTO DE TRIAGEM DO CHEFE ATINGIDO (%d/%d leituras/buscas): PARE IMEDIATAMENTE de abrir novos arquivos na fase de triagem. No padrão OpenAI Codex/Swarm, o Chefe apenas mapeia o escopo em 1-2 turnos e delega a implementação para a worktree do Desenvolvedor. Chame AGORA delegate_to_squad preenchendo 'plan', 'context_brief' (com os contratos/arquivos que você já viu) e 'dev_tasks' (focado na task/subtask pendente) para criar a worktree e iniciar o Dev.", cur-1, maxExploreCalls),
				}, nil
			}
		}
		return nil, nil
	}

	after := func(_ agent.Context, tl tool.Tool, _ map[string]any, result map[string]any, err error) (map[string]any, error) {
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
		}
		return nil, nil
	}

	return before, after
}
