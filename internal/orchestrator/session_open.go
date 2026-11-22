package orchestrator

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/skillx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// isResumeSquadPrompt returns true when the user sends a short continuation prompt
// to resume an interrupted/failed Dev/QA squad run without re-running the Triage Chief.
func isResumeSquadPrompt(raw string) bool {
	text := strings.TrimSpace(raw)
	if idx := strings.LastIndex(text, "[Objetivo do Usuário]"); idx >= 0 {
		text = strings.TrimSpace(text[idx+len("[Objetivo do Usuário]"):])
	}
	if idx := strings.Index(text, "\n\nIssue "); idx > 0 {
		text = strings.TrimSpace(text[:idx])
	}
	low := strings.ToLower(strings.TrimSpace(text))
	low = strings.Trim(low, " \t\r\n.!?,;:")
	if low == "" || len(low) > 120 {
		return false
	}
	switch low {
	case "continua", "continue", "continuar", "pode continuar", "podes continuar",
		"prosseguir", "prossiga", "retomar", "retome", "segue", "seguir", "vai", "bora",
		"tenta de novo", "tentar de novo", "tente novamente", "rodar dev", "roda o dev",
		"continue da onde parou", "continua de onde parou", "continuar de onde parou",
		"ok", "aprovar", "aprovado", "go", "resume", "manda ver", "pode seguir":
		return true
	}
	for _, prefix := range []string{
		"continua ", "continue ", "continuar ", "pode continuar", "retomar ", "retome ",
		"prossiga", "prosseguir", "tenta de novo", "tente novamente", "segue ",
		"contnu", "contina", "conitn",
	} {
		if strings.HasPrefix(low, prefix) {
			return true
		}
	}
	// Fuzzy match for short single-word continuation typos (e.g. "contnuue", "continaur", "conitnue", "prossiga").
	if !strings.ContainsAny(low, " \t\n") && len(low) >= 5 && len(low) <= 16 {
		collapsed := collapseRepeatedRunes(low)
		for _, target := range []string{"continua", "continue", "continuar", "prosseguir", "prossiga", "retomar", "retome"} {
			if levenshteinASCII(low, target) <= 2 || levenshteinASCII(collapsed, target) <= 2 {
				return true
			}
		}
	}
	return false
}

func collapseRepeatedRunes(s string) string {
	var b strings.Builder
	var prev rune
	for i, r := range s {
		if i > 0 && r == prev {
			continue
		}
		prev = r
		b.WriteRune(r)
	}
	return b.String()
}

func levenshteinASCII(a, b string) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func (r *Runner) openSession(ctx context.Context, id, objective, key, runMode string) (session.Session, error) {
	if id != "" {
		r.resumedSession = true
		got, err := r.Sessions.Get(ctx, &session.GetRequest{AppName: sessionx.AppName, UserID: sessionx.UserID, SessionID: id})
		if err != nil {
			return nil, fmt.Errorf("retomar sessão %s: %w", id, err)
		}
		pendingFlag, _ := got.Session.State().Get("plan_pending_approval")
		isPendingPlan := strings.TrimSpace(fmt.Sprint(pendingFlag)) == "1"
		docsFlag, _ := got.Session.State().Get("sdd_docs_pending")
		isDocsPending := strings.TrimSpace(fmt.Sprint(docsFlag)) == "1"
		qaApprovedFlag, _ := got.Session.State().Get("qa_approved")
		isQAApproved := strings.TrimSpace(fmt.Sprint(qaApprovedFlag)) == "1"
		existingPlanRaw, _ := got.Session.State().Get("chief_plan")
		hasExistingPlan := existingPlanRaw != nil && strings.TrimSpace(fmt.Sprint(existingPlanRaw)) != ""
		if !hasExistingPlan && !isQAApproved {
			if pkg, ok := loadPersistedHandoff(id); ok {
				existingPlanRaw = pkg.Objective
				hasExistingPlan = true
				r.setStateChecked(got.Session.State(), "chief_plan", pkg.Objective)
				if len(pkg.Facts) > 0 {
					r.setStateChecked(got.Session.State(), "chief_context_brief", strings.Join(pkg.Facts, "\n"))
				}
			}
		}

		if runMode == "plan:sdd-jira" && !isDocsPending {
			return nil, fmt.Errorf("não há docs SDD pendentes de aprovação nesta sessão; execute primeiro o modo Planejar · Projeto SDD (plan:sdd) e aprove o Gate 1 (Docs & Arquitetura)")
		}
		isResume := isResumeSquadPrompt(objective)
		shouldRestorePlan := hasExistingPlan && ((runMode == "squad" && (isPendingPlan || !isQAApproved)) ||
			(isResume && (isPendingPlan || !isQAApproved)))

		if shouldRestorePlan {
			r.chiefPlan = strings.TrimSpace(fmt.Sprint(existingPlanRaw))
			if prevBrief, err := got.Session.State().Get("chief_context_brief"); err == nil && prevBrief != nil {
				r.chiefContextBrief = strings.TrimSpace(fmt.Sprint(prevBrief))
			}
			if prevSlices, err := got.Session.State().Get("dev_slices_joined"); err == nil && prevSlices != nil {
				if raw := strings.TrimSpace(fmt.Sprint(prevSlices)); raw != "" {
					r.devSlices = strings.Split(raw, "\n|||\n")
				}
			}
			if prevOwned, err := got.Session.State().Get("dev_owned_paths_json"); err == nil && prevOwned != nil {
				r.devOwnedPaths = unmarshalOwnedPaths(strings.TrimSpace(fmt.Sprint(prevOwned)))
			}
			if prevSeq, err := got.Session.State().Get("dev_sequential"); err == nil && prevSeq != nil {
				r.devSequential = strings.TrimSpace(fmt.Sprint(prevSeq)) == "1"
			}
			if prevPR, err := got.Session.State().Get("pr_number"); err == nil && prevPR != nil {
				r.prNumber, _ = strconv.Atoi(strings.TrimSpace(fmt.Sprint(prevPR)))
			}
			if prevBranch, err := got.Session.State().Get("pr_branch"); err == nil && prevBranch != nil {
				r.prBranch = strings.TrimSpace(fmt.Sprint(prevBranch))
			}
			if prevRevs, err := got.Session.State().Get("pr_reviewers"); err == nil && prevRevs != nil {
				if raw := strings.TrimSpace(fmt.Sprint(prevRevs)); raw != "" {
					r.prReviewers = strings.Split(raw, ",")
				}
			}
			if prevSkills, err := got.Session.State().Get("selected_skills"); err == nil && prevSkills != nil {
				if raw := strings.TrimSpace(fmt.Sprint(prevSkills)); raw != "" {
					r.selectedSkills = skillx.Match(r.skills, strings.Split(raw, "\n"))
				}
			}
			if prevNative, err := got.Session.State().Get("selected_native_skills"); err == nil && prevNative != nil {
				if raw := strings.TrimSpace(fmt.Sprint(prevNative)); raw != "" {
					r.selectedNative = skillx.Match(r.NativeSkills, strings.Split(raw, "\n"))
				}
			}
			if isResume {
				if prevObj, err := got.Session.State().Get("objective"); err == nil && prevObj != nil {
					if origObj := strings.TrimSpace(fmt.Sprint(prevObj)); origObj != "" && !isResumeSquadPrompt(origObj) {
						objective = origObj
						r.objectiveText = origObj
					}
				}
			}
		}
		if err := sessionx.SetObjective(got.Session, objective, key); err != nil {
			return nil, err
		}
		priorCtx := summarizePriorSessionContext(got.Session)
		keepPlan := (shouldRestorePlan && r.chiefPlan != "") ||
			(hasExistingPlan && !isQAApproved) ||
			(runMode == "plan:sdd") ||
			(runMode == "plan:sdd-jira" && isDocsPending)
		resetMap := map[string]any{
			"prior_session_context": priorCtx,
			"qa_feedback":           "",
			"qa_report":             "",
			"qa_approval_note":      "",
			"qa_approved":           "",
			"integrate_status":      "",
			"plan_pending_approval": "",
		}
		if runMode == "plan:sdd" || runMode == "plan:sdd-jira" {
			resetMap["sdd_stage"] = map[bool]string{true: "docs", false: "backlog"}[runMode == "plan:sdd"]
			if runMode == "plan:sdd" {
				resetMap["sdd_docs_pending"] = ""
			}
		} else {
			resetMap["sdd_docs_pending"] = ""
			resetMap["sdd_stage"] = ""
		}
		if !keepPlan {
			resetMap["chief_plan"] = ""
			resetMap["chief_context_brief"] = ""
			resetMap["dev_slices_joined"] = ""
			resetMap["dev_owned_paths_json"] = ""
			resetMap["dev_sequential"] = ""
			resetMap["pr_number"] = ""
			resetMap["pr_branch"] = ""
			resetMap["pr_reviewers"] = ""
			resetMap["selected_skills"] = ""
			resetMap["selected_native_skills"] = ""
		}
		r.updateStateChecked(got.Session, resetMap)
		if err := sessionx.CompactOldToolOutputs(got.Session, 10); err != nil && r.Err != nil {
			fmt.Fprintf(r.Err, "aviso: falha ao compactar histórico da sessão retomada: %v\n", err)
		}
		return got.Session, nil
	}
	created, err := r.Sessions.Create(ctx, &session.CreateRequest{
		AppName: sessionx.AppName,
		UserID:  sessionx.UserID,
		State: map[string]any{
			"objective":             objective,
			"jira_key":              key,
			"prior_session_context": "",
			"qa_feedback":           "",
			"qa_report":             "",
			"qa_approval_note":      "",
			"qa_approved":           "",
			"integrate_status":      "",
			"chief_plan":            "",
			"chief_context_brief":   "",
			"dev_slices_joined":     "",
			"dev_owned_paths_json":  "",
			"dev_sequential":        "",
			"pr_number":             "",
			"pr_branch":             "",
			"pr_reviewers":          "",
			"plan_pending_approval": "",
			"sdd_stage":             "",
			"sdd_docs_pending":      "",
		},
	})
	if err != nil {
		return nil, err
	}
	return created.Session, nil
}

func summarizePriorSessionContext(sess session.Session) string {
	if sess == nil || sess.Events() == nil || sess.Events().Len() == 0 {
		return ""
	}
	type entry struct {
		who  string
		text string
	}
	var items []entry
	for ev := range sess.Events().All() {
		if ev == nil || ev.Partial || ev.LLMResponse.Content == nil {
			continue
		}
		var parts []string
		for _, p := range ev.LLMResponse.Content.Parts {
			if p == nil || p.Thought || strings.TrimSpace(p.Text) == "" {
				continue
			}
			parts = append(parts, strings.TrimSpace(p.Text))
		}
		if len(parts) == 0 {
			continue
		}
		who := strings.TrimSpace(ev.Author)
		if ev.LLMResponse.Content.Role == genai.RoleUser || who == "" || strings.EqualFold(who, "user") {
			who = "Usuário"
		}
		joined := strings.Join(parts, "\n")
		if len(joined) > 600 {
			joined = joined[:600] + "…"
		}
		items = append(items, entry{who: who, text: joined})
	}
	if len(items) == 0 {
		return ""
	}
	const maxItems = 8
	if len(items) > maxItems {
		items = items[len(items)-maxItems:]
	}
	var b strings.Builder
	const maxTotal = 3600
	for _, it := range items {
		line := fmt.Sprintf("- %s: %s\n", it.who, it.text)
		if b.Len()+len(line) > maxTotal {
			break
		}
		b.WriteString(line)
	}
	return strings.TrimSpace(b.String())
}

// teamMemorySnippet reads .hivemind/MEMORY.md purely read-only (Spec 01 §3.3).
// When the file does not exist yet, it returns "" without creating files on disk.
func (r *Runner) teamMemorySnippet() string {
	target, err := workspace.ResolveWithOptions(r.Workspace, ".hivemind/MEMORY.md", workspace.Options{AllowMemoryRead: true})
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return ""
	}
	txt := strings.TrimSpace(string(data))
	if txt == "" {
		return ""
	}
	txt = strx.TruncateBytes(txt, 4096, "\n…")
	return "\n[Memória Compartilhada do Time (.hivemind/MEMORY.md)]\n" + txt + "\n"
}
