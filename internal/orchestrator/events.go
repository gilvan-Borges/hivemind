package orchestrator

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/contextx"
	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/skillx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// compactionStatsCallback emits one "compact" event per pass that actually
// trimmed payloads: token savings per agent become visible on the live event
// stream instead of being silently applied. No-op passes stay silent to avoid
// one event per model call.
func (r *Runner) compactionStatsCallback(author, label string) func(contextx.Stats) {
	return func(s contextx.Stats) {
		if r.OnEvent == nil || s.Compacted <= 0 {
			return
		}
		r.OnEvent(Event{
			Kind:   "compact",
			Stage:  inferStage(author, label),
			Author: author,
			Label:  label,
			Detail: fmt.Sprintf("%d saídas antigas compactadas · ~%d tokens poupados", s.Compacted, s.TokensSaved),
			Tokens: int32(s.TokensSaved),
		})
	}
}

func (r *Runner) resetTurnCosts() {
	r.costMu.Lock()
	r.turnCostMicroUSD = 0
	r.agentCosts = nil
	r.roleCosts = nil
	r.costMu.Unlock()
}

func (r *Runner) printUsageSummary(result Result, w io.Writer, prefix string) {
	line := usageSummaryLine(result)
	if line == "" || w == nil {
		return
	}
	fmt.Fprintf(w, "%s%s\n", prefix, line)
}

func usageSummaryLine(result Result) string {
	if result.TotalTokens <= 0 && result.CostMicroUSD <= 0 {
		return ""
	}
	base := fmt.Sprintf("Tokens: ↑%d entrada · ↓%d saída · %d total", result.InputTokens, result.OutputTokens, result.TotalTokens)
	if result.CachedReadTokens > 0 && result.InputTokens > 0 {
		base += fmt.Sprintf(" · cache read: %d (%d%%)", result.CachedReadTokens, result.CachedReadTokens*100/result.InputTokens)
	}
	if result.CostMicroUSD <= 0 {
		return base
	}
	line := base + " · Custo: " + jev.FormatMicroUSD(result.CostMicroUSD)
	if breakdown := formatRoleCostBreakdown(result.CostByRole); breakdown != "" {
		line += " (" + breakdown + ")"
	}
	return line
}

func (r *Runner) recordUsage(ev *session.Event, labels map[string]string) {
	if ev == nil || ev.Partial || ev.LLMResponse.UsageMetadata == nil {
		return
	}
	u := ev.LLMResponse.UsageMetadata
	inTok := u.PromptTokenCount
	outTok := u.CandidatesTokenCount
	totTok := u.TotalTokenCount
	if totTok == 0 && (inTok > 0 || outTok > 0) {
		totTok = inTok + outTok
	}
	if inTok == 0 && outTok == 0 && totTok == 0 {
		return
	}
	var cachedRead int64
	if u.CachedContentTokenCount > 0 {
		cachedRead = int64(u.CachedContentTokenCount)
	}
	r.turnInputTokens += int64(inTok)
	r.turnOutputTokens += int64(outTok)
	r.turnTotalTokens += int64(totTok)
	r.turnCachedRead += cachedRead

	label := labels[ev.Author]
	if label == "" {
		label = ev.Author
	}

	prov, mod := r.resolveAuthorRoute(ev.Author)
	r.resolvedChoiceMu.Lock()
	disc := r.routedDiscount[strings.ToLower(strings.TrimSpace(ev.Author))]
	r.resolvedChoiceMu.Unlock()
	costMicro, costRole := r.recordCallCost(ev.Author, label, prov, mod, int64(inTok), int64(outTok), cachedRead, disc)

	if prov != "" {
		modelx.RecordAIUsageDetailedWithMeta(prov, mod, int64(inTok), int64(outTok), int64(totTok), cachedRead, 0, disc, costMicro, ev.Author, costRole)
	}

	if r.OnEvent != nil {
		cacheHitPct := int32(0)
		if inTok > 0 && cachedRead > 0 {
			cacheHitPct = int32(cachedRead * 100 / int64(inTok))
		}
		r.OnEvent(Event{
			Kind:             "usage",
			Stage:            inferStage(ev.Author, label),
			Author:           ev.Author,
			Label:            label,
			Role:             costRole,
			Provider:         prov,
			Model:            mod,
			InputTokens:      inTok,
			OutputTokens:     outTok,
			Tokens:           totTok,
			CachedReadTokens: int32(cachedRead),
			CacheHitPct:      cacheHitPct,
			CostMicroUSD:     costMicro,
		})
	}
}

func (r *Runner) emitEvent(ev *session.Event, labels map[string]string) {
	label := labels[ev.Author]
	if label == "" {
		label = ev.Author
	}
	stage := inferStage(ev.Author, label)
	r.OnEvent(Event{Kind: "stage", Stage: stage, Author: ev.Author, Label: label})

	var tokens int32
	if ev.LLMResponse.UsageMetadata != nil {
		tokens = ev.LLMResponse.UsageMetadata.TotalTokenCount
	}
	if ev.LLMResponse.Content == nil {
		return
	}
	// Emit text and tool_result parts BEFORE tool_call parts of the same LLMResponse:
	// providers like agy put FunctionCall parts before Text in the final non-partial
	// envelope, which would otherwise push tool cards before the final text event and
	// cause the UI to create a duplicate assistant bubble after the tools.
	for _, part := range ev.LLMResponse.Content.Parts {
		if part == nil || part.FunctionCall != nil {
			continue
		}
		if part.FunctionResponse != nil {
			r.OnEvent(Event{
				Kind:     "tool_result",
				Stage:    stage,
				Author:   ev.Author,
				Label:    label,
				ToolName: part.FunctionResponse.Name,
				Detail:   summarizeToolResult(part.FunctionResponse.Response),
				Tokens:   tokens,
			})
		} else if part.Text != "" {
			r.OnEvent(Event{
				Kind:    "text",
				Stage:   stage,
				Author:  ev.Author,
				Label:   label,
				Text:    part.Text,
				Partial: ev.Partial,
				Thought: part.Thought,
				Tokens:  tokens,
			})
		}
	}
	for _, part := range ev.LLMResponse.Content.Parts {
		if part == nil || part.FunctionCall == nil {
			continue
		}
		r.OnEvent(Event{
			Kind:     "tool_call",
			Stage:    stage,
			Author:   ev.Author,
			Label:    label,
			ToolName: part.FunctionCall.Name,
			Detail:   summarizeToolArgs(part.FunctionCall.Name, part.FunctionCall.Args),
			Tokens:   tokens,
		})
		if part.FunctionCall.Name == "qa_approve" {
			note := fmt.Sprint(part.FunctionCall.Args["note"])
			r.OnEvent(Event{
				Kind:   "handoff",
				Stage:  "qa",
				Author: ev.Author,
				Label:  "QA aprovou → Chefe (Fecho)",
				Text:   strings.TrimSpace(note),
			})
		} else if part.FunctionCall.Name == "qa_request_changes" {
			notes := fmt.Sprint(part.FunctionCall.Args["notes"])
			r.OnEvent(Event{
				Kind:   "handoff",
				Stage:  "dev",
				Author: ev.Author,
				Label:  "QA pediu ajustes → Devs",
				Text:   strings.TrimSpace(notes),
			})
		}
	}
}

func inferStage(author, label string) string {
	l := strings.ToLower(author + " " + label)
	switch {
	case strings.Contains(l, "fecho"):
		return "closing"
	case strings.Contains(l, "qa"):
		return "qa"
	case strings.Contains(l, "desenvolvedor") || strings.Contains(l, "dev") || strings.Contains(l, "integrate"):
		return "dev"
	default:
		return "chief"
	}
}

func summarizeToolArgs(name string, args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	switch name {
	case "read_file":
		if p, ok := args["path"]; ok && p != nil {
			s := strings.TrimSpace(fmt.Sprint(p))
			if s != "" {
				start := parseIntArg(args["start_line"])
				end := parseIntArg(args["end_line"])
				if start > 0 && end > 0 {
					s = fmt.Sprintf("%s:%d-%d", s, start, end)
				} else if start > 0 {
					s = fmt.Sprintf("%s:%d", s, start)
				}
				return strx.TruncateRunes(strings.ReplaceAll(s, "\n", " "), 120, "…")
			}
		}
	case "search_text":
		q := strings.TrimSpace(fmt.Sprint(args["query"]))
		if q != "" && q != "<nil>" {
			if g := strings.TrimSpace(fmt.Sprint(args["glob"])); g != "" && g != "<nil>" {
				q += " (" + g + ")"
			} else if p := strings.TrimSpace(fmt.Sprint(args["path"])); p != "" && p != "<nil>" {
				q += " em " + p
			}
			return strx.TruncateRunes(strings.ReplaceAll(q, "\n", " "), 120, "…")
		}
	case "find_files":
		pat := strings.TrimSpace(fmt.Sprint(args["pattern"]))
		if pat != "" && pat != "<nil>" {
			if p := strings.TrimSpace(fmt.Sprint(args["path"])); p != "" && p != "<nil>" {
				pat += " em " + p
			}
			return strx.TruncateRunes(strings.ReplaceAll(pat, "\n", " "), 120, "…")
		}
	}
	if strings.HasPrefix(name, "github_") {
		if num, ok := args["number"]; ok && num != nil {
			n := strings.TrimSpace(fmt.Sprint(num))
			if n != "" {
				if f, ok := args["file"]; ok && f != nil && strings.TrimSpace(fmt.Sprint(f)) != "" {
					return strx.TruncateRunes(fmt.Sprintf("#%s · %s", n, strings.TrimSpace(fmt.Sprint(f))), 120, "…")
				}
				return "#" + n
			}
		}
	}
	for _, key := range []string{"path", "query", "pattern", "command", "message", "title", "number", "key", "jql", "ref", "note", "notes"} {
		if v, ok := args[key]; ok && v != nil {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" {
				return strx.TruncateRunes(strings.ReplaceAll(s, "\n", " "), 120, "…")
			}
		}
	}
	return ""
}

func parseIntArg(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	default:
		return 0
	}
}

func summarizeToolResult(res map[string]any) string {
	if len(res) == 0 {
		return ""
	}
	for _, key := range []string{"text", "status", "error", "entries"} {
		if v, ok := res[key]; ok && v != nil {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" {
				return strx.TruncateRunes(s, 360, "\n…")
			}
		}
	}
	return ""
}

func buildUserContent(objective string, attachments []Attachment) *genai.Content {
	parts := make([]*genai.Part, 0, 1+len(attachments))
	parts = append(parts, &genai.Part{Text: objective})
	for _, att := range attachments {
		if len(att.Data) == 0 {
			continue
		}
		mime := strings.TrimSpace(att.MIMEType)
		if mime == "" {
			mime = "image/png"
		}
		parts = append(parts, &genai.Part{
			InlineData: &genai.Blob{
				MIMEType: mime,
				Data:     att.Data,
			},
		})
	}
	return &genai.Content{
		Role:  genai.RoleUser,
		Parts: parts,
	}
}

func roleLabel(role string) string {
	switch team.NormalizeRole(role) {
	case team.RoleProjectManager:
		return "Gerente de projeto"
	case team.RoleTechLead:
		return "Tech lead"
	case team.RoleArchitect:
		return "Arquiteto"
	case team.RoleDeveloper:
		return "Desenvolvedor"
	case team.RoleQA:
		return "QA"
	default:
		return role
	}
}

func filterOutSkill(list []skillx.Skill, name string) []skillx.Skill {
	out := make([]skillx.Skill, 0, len(list))
	for _, s := range list {
		if s.Name != name {
			out = append(out, s)
		}
	}
	return out
}

func skillSelectionLabel(project, native []string) string {
	var parts []string
	if len(project) > 0 {
		parts = append(parts, strings.Join(project, ", "))
	}
	if len(native) > 0 {
		parts = append(parts, "nativas da IDE: "+strings.Join(native, ", "))
	}
	return strings.Join(parts, " · ")
}

func (r *Runner) resolveSkillCatalogs(target string, includeDynamic bool) (project []skillx.Skill, native []skillx.Skill) {
	seenProject := map[string]bool{}
	seenNative := map[string]bool{}
	addProject := func(s skillx.Skill) {
		if !seenProject[s.Name] {
			seenProject[s.Name] = true
			project = append(project, s)
		}
	}
	addNative := func(s skillx.Skill) {
		if !seenNative[s.Name] {
			seenNative[s.Name] = true
			native = append(native, s)
		}
	}
	for _, a := range r.Project.Settings.SkillsForTarget(target) {
		switch strings.ToLower(strings.TrimSpace(a.Source)) {
		case "native":
			if hit := skillx.Find(r.NativeSkills, a.Name); hit != nil {
				addNative(*hit)
			}
		case "project":
			if hit := skillx.Find(r.skills, a.Name); hit != nil {
				addProject(*hit)
			}
		default:
			if hit := skillx.Find(r.skills, a.Name); hit != nil {
				addProject(*hit)
			} else if hit := skillx.Find(r.NativeSkills, a.Name); hit != nil {
				addNative(*hit)
			}
		}
	}
	if includeDynamic {
		for _, s := range r.selectedSkills {
			addProject(s)
		}
		for _, s := range r.selectedNative {
			addNative(s)
		}
	}
	return project, native
}

func (r *Runner) guidanceForRole(target string, includeDynamic bool) string {
	project, native := r.resolveSkillCatalogs(target, includeDynamic)
	return skillx.GuidanceBlock(r.objectiveText, r.Workspace, r.NativeSkillsRoot, project, native, 0, 0)
}

func (r *Runner) nativeSkillGuide(names ...string) string {
	if len(names) == 0 || r.NativeSkillsRoot == "" {
		return ""
	}
	var picked []skillx.Skill
	for _, n := range names {
		if hit := skillx.Find(r.NativeSkills, n); hit != nil {
			picked = append(picked, *hit)
		}
	}
	if len(picked) == 0 {
		return ""
	}
	return skillx.GuidanceBlock(r.objectiveText, r.Workspace, r.NativeSkillsRoot, nil, picked, 0, 0)
}

func (r *Runner) pinnedSkillsNote() string {
	if len(r.Project.Settings.SkillAssignments) == 0 {
		return ""
	}
	var lines []string
	for _, a := range r.Project.Settings.SkillAssignments {
		if len(a.Roles) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("- `%s`: %s", a.Name, strings.Join(a.Roles, ", ")))
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n\nSKILLS FIXADAS POR PAPEL NA CONFIGURAÇÃO DO TIME (já injetadas automaticamente nos respectivos agentes):\n" + strings.Join(lines, "\n")
}

func (r *Runner) primaryChief() team.Agent {
	if pms := r.Team.ByRole(team.RoleProjectManager); len(pms) > 0 {
		for _, a := range pms {
			if strings.EqualFold(a.Name, "chefe") {
				return a
			}
		}
	}
	if archs := r.Team.ByRole(team.RoleArchitect); len(archs) > 0 {
		for _, a := range archs {
			if strings.EqualFold(a.Name, "chefe") {
				return a
			}
		}
	}
	if pms := r.Team.ByRole(team.RoleProjectManager); len(pms) > 0 {
		return pms[0]
	}
	if archs := r.Team.ByRole(team.RoleArchitect); len(archs) > 0 {
		return archs[0]
	}
	return r.Team.Agents[0]
}
