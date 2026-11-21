package idecore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/procx"
	"github.com/gilvan-Borges/HiveMind/internal/remote"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// CommentJiraIssue adds a comment to a Jira issue using environment or project credentials.
func CommentJiraIssue(ctx context.Context, workspace, issueKey, comment string) error {
	key := strings.TrimSpace(issueKey)
	if key == "" {
		return fmt.Errorf("informe a chave da issue Jira (ex: PROJ-123)")
	}
	client := jirax.FromWorkspace(workspace)
	if !client.Enabled() {
		return fmt.Errorf("credenciais Jira não configuradas (conecte pelo Conector Jira Nativo, mcp_config.json ou variáveis JIRA_*)")
	}
	if cfg, err := team.Load(workspace); err == nil && !cfg.Settings.JiraEnabled {
		return fmt.Errorf("Jira está desabilitado nas configurações do time")
	}
	return client.Comment(ctx, key, comment)
}

// ProjectSubtask represents a Jira subtask or child issue summarized inside a parent issue card.
type ProjectSubtask = remote.ProjectSubtask

// ProjectIssue represents a Jira or GitHub issue listed in the IDE sidebar.
type ProjectIssue = remote.ProjectIssue

func buildMyJiraIssuesJQL(projectKey string) string {
	projKey := strings.ToUpper(sanitizeJiraProjectKey(projectKey))
	if projKey != "" {
		return fmt.Sprintf(`project = "%s" AND assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC`, projKey)
	}
	return "assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC"
}

// matchJiraProjectFromNames infers which Jira project corresponds to a
// repository using ONLY generic signals: exact key equality, containment of
// the normalized project title, and discriminative token matching between the
// repo/remote/branch names and the project names. No customer-specific terms
// are hardcoded (Spec 08: proprietary heuristics removed).
func matchJiraProjectFromNames(projs []jirax.Project, rawNames ...string) string {
	if len(projs) == 0 {
		return ""
	}
	normAlpha := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		return b.String()
	}

	// genericTechSuffixes are engineering-generic words stripped from repo
	// names before matching; nothing client-specific lives here. Tokens that
	// ARE purely technical (mobile, web, api, ...) are never used as
	// matching candidates: they identify no project by themselves.
	genericTechSuffixes := []string{
		"-mobile", "_mobile", "mobile",
		"-ios", "_ios", "-android", "_android",
		"-app", "_app", "app",
		"-web", "_web", "web",
		"-front", "_front", "-frontend", "_frontend",
		"-back", "_back", "-backend", "_backend",
		"-api", "_api", "api",
		"-core", "_core", "-service", "_service", "-srv", "_srv",
		"-demo", "_demo",
	}
	genericTechSet := map[string]bool{}
	for _, suf := range genericTechSuffixes {
		genericTechSet[normAlpha(suf)] = true
	}

	// splitNameTokens breaks a raw name into normalized tokens:
	// separator tokens ( -, _ . space / ) AND camelCase humps, with
	// engineering-generic suffixes stripped. Input case is preserved here so
	// humps are detected; normalization happens per token.
	splitNameTokens := func(raw string) []string {
		s := strings.TrimSpace(raw)
		if s == "" {
			return nil
		}
		var rawTokens []string
		for _, pt := range strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' || r == '.' || r == ' ' || r == '/' }) {
			if pt != "" {
				rawTokens = append(rawTokens, pt)
			}
		}
		// camelCase humps (consecutive capitals stay together: "TIMETRACK" is one token)
		for _, pt := range rawTokens {
			var cur []rune
			for i, r := range pt {
				if i > 0 && r >= 'A' && r <= 'Z' && !(cur[len(cur)-1] >= 'A' && cur[len(cur)-1] <= 'Z') {
					rawTokens = append(rawTokens, string(cur))
					cur = cur[:0]
				}
				cur = append(cur, r)
			}
			if len(cur) > 0 {
				rawTokens = append(rawTokens, string(cur))
			}
		}
		var out []string
		add := func(v string) {
			v = normAlpha(v)
			if len(v) < 3 || genericTechSet[v] {
				return
			}
			for _, existing := range out {
				if existing == v {
					return
				}
			}
			out = append(out, v)
		}
		for _, tok := range rawTokens {
			t := strings.ToLower(tok)
			cur := t
			for changed := true; changed; {
				changed = false
				for _, suf := range genericTechSuffixes {
					if strings.HasSuffix(cur, suf) && len(cur) > len(suf)+1 {
						cur = strings.TrimSuffix(cur, suf)
						changed = true
					}
				}
			}
			add(cur)
			add(t)
		}
		return out
	}

	var candidates []string
	addSource := func(raw string) {
		s := strings.TrimSpace(raw)
		if s == "" {
			return
		}
		if nc := normAlpha(s); len(nc) >= 3 {
			candidates = appendIfMissing(candidates, nc)
		}
		for _, tok := range splitNameTokens(s) {
			candidates = appendIfMissing(candidates, tok)
		}
	}
	for _, raw := range rawNames {
		addSource(raw)
	}

	// Project-side index: original key, main title (case preserved for hump
	// detection) and alias title, plus token frequency across the org.
	type projTokens struct {
		origKey string
		main    string
		toks    []string
	}
	projects := make([]projTokens, 0, len(projs))
	titleTokenFreq := map[string]int{}
	for _, p := range projs {
		rawName := strings.TrimSpace(p.Name)
		mainTitle := rawName
		aliasTitle := ""
		if openIdx := strings.IndexByte(rawName, '('); openIdx >= 0 {
			mainTitle = rawName[:openIdx]
			if closeIdx := strings.IndexByte(rawName[openIdx:], ')'); closeIdx > 1 {
				aliasTitle = rawName[openIdx+1 : openIdx+closeIdx]
			}
		}
		pt := projTokens{origKey: p.Key, main: normAlpha(mainTitle)}
		for _, tok := range splitNameTokens(mainTitle) {
			pt.toks = appendIfMissing(pt.toks, tok)
		}
		for _, tok := range splitNameTokens(aliasTitle) {
			pt.toks = appendIfMissing(pt.toks, tok)
		}
		for _, tok := range pt.toks {
			titleTokenFreq[tok]++
		}
		projects = append(projects, pt)
	}

	// 1. Exact key match (repo "acme" or "acme-mobile" -> project key "ACME").
	for _, cand := range candidates {
		for _, pt := range projects {
			if cand == normAlpha(pt.origKey) {
				return sanitizeJiraProjectKey(pt.origKey)
			}
		}
	}

	// 2. Containment match on the normalized (org-unique) project title or an
	// exact org-unique title token. Titles shared by more than one project
	// never match by containment alone.
	for _, cand := range candidates {
		for _, pt := range projects {
			if len(pt.main) >= 4 && titleTokenFreq[pt.main] <= 1 {
				if cand == pt.main || strings.Contains(cand, pt.main) {
					return sanitizeJiraProjectKey(pt.origKey)
				}
			}
			for _, tok := range pt.toks {
				if len(tok) >= 4 && titleTokenFreq[tok] <= 1 && cand == tok {
					return sanitizeJiraProjectKey(pt.origKey)
				}
			}
		}
	}

	// 3. Discriminative token match: a repo token that appears in exactly ONE
	// project of the org pins that project. Tokens shared by several projects
	// are ambiguous and never match.
	for _, cand := range candidates {
		matches := 0
		matchKey := ""
		for _, pt := range projects {
			if cand == normAlpha(pt.origKey) {
				matches++
				matchKey = pt.origKey
				continue
			}
			for _, tok := range pt.toks {
				if tok == cand {
					matches++
					matchKey = pt.origKey
					break
				}
			}
		}
		if matches == 1 && matchKey != "" {
			return sanitizeJiraProjectKey(matchKey)
		}
	}
	return ""
}

func appendIfMissing(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}

// inferJiraProjectKeyForWorkspace matches the current workspace/repository
// name against the organization's Jira projects using the generic heuristics
// of matchJiraProjectFromNames (exact key, unique-title containment and
// discriminative tokens). If the repository has no corresponding project in
// Jira yet, it returns ("", true) when multiple Jira projects exist in the org
// so tasks from unrelated Jira projects are NEVER mixed in.
func inferJiraProjectKeyForWorkspace(ctx context.Context, workspaceDir string) (matchedKey string, hasMultipleOrgProjects bool) {
	if strings.TrimSpace(workspaceDir) == "" {
		return "", false
	}
	projs, err := jirax.ListProjectsForWorkspace(ctx, workspaceDir)
	if err != nil || len(projs) == 0 {
		return "", false
	}
	if len(projs) == 1 {
		return sanitizeJiraProjectKey(projs[0].Key), false
	}

	var rawNames []string
	g := gitx.Local{}
	if g.IsRepo(ctx, workspaceDir) {
		if remoteURL, rErr := g.RemoteURL(ctx, workspaceDir); rErr == nil {
			if _, repo, ok := githubx.ParseRemoteForConfiguredHosts(remoteURL); ok {
				rawNames = append(rawNames, repo)
			}
		}
	}
	rawNames = append(rawNames, filepath.Base(filepath.Clean(workspaceDir)))

	if matched := matchJiraProjectFromNames(projs, rawNames...); matched != "" {
		return matched, true
	}

	// 3. Fallback: inspect recent git commit messages for an explicit Jira key prefix (e.g. "feat(PROJ-123): ...").
	if g.IsRepo(ctx, workspaceDir) && g.HasCommit(ctx, workspaceDir) {
		if logRaw, lErr := g.Run(ctx, workspaceDir, "log", "-n", "25", "--pretty=%s"); lErr == nil && logRaw != "" {
			counts := map[string]int{}
			bestKey := ""
			bestCount := 0
			upperLog := strings.ToUpper(logRaw)
			for _, p := range projs {
				k := sanitizeJiraProjectKey(p.Key)
				if k == "" {
					continue
				}
				c := strings.Count(upperLog, k+"-")
				if c > 0 {
					counts[k] = c
					if c > bestCount {
						bestCount = c
						bestKey = k
					}
				}
			}
			if bestKey != "" && bestCount >= 2 {
				return bestKey, true
			}
		}
	}

	return "", true
}

// ListProjectIssues fetches open Jira tasks assigned to the current user and/or open GitHub issues for one-click squad execution.
func ListProjectIssues(ctx context.Context, workspace string) ([]ProjectIssue, error) {
	var issues []ProjectIssue

	// 1. Jira issues when a profile/connector is resolved for this workspace (unless explicitly opted out with bindingMode=="none").
	res := jirax.ResolveForWorkspace(workspace)
	if res.MatchMode != "isolated_none" {
		jc := jirax.FromWorkspace(workspace)
		if jc.Enabled() {
			var cfgProj string
			var cfgLoaded bool
			var cfgJiraEnabled bool
			if cfg, err := team.Load(workspace); err == nil {
				cfgLoaded = true
				cfgProj = strings.TrimSpace(cfg.Settings.JiraProject)
				cfgJiraEnabled = cfg.Settings.JiraEnabled
			}
			if cfgLoaded && !cfgJiraEnabled && strings.EqualFold(cfgProj, "NONE") {
				// User explicitly set "Sem Jira neste repositório" from the quick selector.
			} else {
				projKey := sanitizeJiraProjectKey(cfgProj)
				if strings.EqualFold(projKey, "NONE") {
					projKey = ""
				}
				hasMultiOrgProjs := false
				if projKey == "" && !strings.EqualFold(cfgProj, "*") {
					// READ-ONLY inference (Spec 08): ListProjectIssues must never
					// mutate .hivemind/team.yaml during a listing call. The inferred
					// key only scopes THIS query; persisting a binding stays an
					// explicit user action (EnableProjectJira via quick selector).
					projKey, hasMultiOrgProjs = inferJiraProjectKeyForWorkspace(ctx, workspace)
				}
				if projKey == "" && !hasMultiOrgProjs {
					projKey = sanitizeJiraProjectKey(res.DefaultProject)
				}
				// Only query Jira when we matched a specific Jira project (or when the user explicitly chose "*" for all projects,
				// or when the Jira instance only has a single project). Repositories that don't exist in Jira yet (e.g. vibe-saude)
				// won't pull unrelated tasks from other company projects.
				shouldQueryJira := projKey != "" || strings.EqualFold(cfgProj, "*") || !hasMultiOrgProjs
				if shouldQueryJira {
					jql := buildMyJiraIssuesJQL(projKey)
					list, err := jc.Search(ctx, jql, 50)
					if err == nil {
						// For top-level issues that may have child issues linked via `parent` (e.g. Epic/Parent in team-managed projects)
						// rather than inline `fields.subtasks`, batch-fetch child issues in a single JQL call.
						var parentCandidates []string
						for _, it := range list {
							if it.Key != "" && len(it.Subtasks) == 0 && !strings.Contains(strings.ToLower(it.IssueType), "sub") {
								parentCandidates = append(parentCandidates, fmt.Sprintf("%q", it.Key))
							}
						}
						childrenByParent := make(map[string][]jirax.SubtaskItem)
						if len(parentCandidates) > 0 {
							if len(parentCandidates) > 25 {
								parentCandidates = parentCandidates[:25]
							}
							childCtx, cancelChild := context.WithTimeout(ctx, 5*time.Second)
							childJQL := fmt.Sprintf("parent in (%s) ORDER BY key ASC", strings.Join(parentCandidates, ","))
							if children, cErr := jc.Search(childCtx, childJQL, 100); cErr == nil {
								for _, ch := range children {
									pk := strings.TrimSpace(ch.ParentKey)
									if pk == "" || ch.Key == "" {
										continue
									}
									childrenByParent[pk] = append(childrenByParent[pk], jirax.SubtaskItem{
										Key:     ch.Key,
										Summary: ch.Summary,
										Status:  ch.Status,
										Done:    jirax.IsDoneStatus(ch.Status),
									})
								}
							}
							cancelChild()
						}

						base := strings.TrimRight(strings.TrimSpace(res.BaseURL), "/")
						for _, it := range list {
							browseURL := ""
							if base != "" && it.Key != "" {
								browseURL = base + "/browse/" + url.PathEscape(it.Key)
							}
							parentURL := ""
							if base != "" && it.ParentKey != "" {
								parentURL = base + "/browse/" + url.PathEscape(it.ParentKey)
							}

							rawSubs := it.Subtasks
							if len(rawSubs) == 0 {
								rawSubs = childrenByParent[it.Key]
							} else if extra := childrenByParent[it.Key]; len(extra) > 0 {
								seen := make(map[string]bool, len(rawSubs))
								for _, s := range rawSubs {
									seen[s.Key] = true
								}
								for _, s := range extra {
									if !seen[s.Key] {
										rawSubs = append(rawSubs, s)
									}
								}
							}

							var projSubs []ProjectSubtask
							doneCount := 0
							for _, st := range rawSubs {
								isDone := st.Done || jirax.IsDoneStatus(st.Status)
								if isDone {
									doneCount++
								}
								stURL := ""
								if base != "" && st.Key != "" {
									stURL = base + "/browse/" + url.PathEscape(st.Key)
								}
								projSubs = append(projSubs, ProjectSubtask{
									Key:     st.Key,
									Summary: st.Summary,
									Status:  st.Status,
									Done:    isDone,
									URL:     stURL,
								})
							}
							totalSubs := len(projSubs)
							pendingSubs := totalSubs - doneCount

							issues = append(issues, ProjectIssue{
								Source:         "jira",
								Key:            it.Key,
								Title:          it.Summary,
								Body:           it.Description,
								Status:         it.Status,
								IssueType:      it.IssueType,
								IsSubtask:      it.IsSubtask || it.ParentKey != "",
								ParentKey:      it.ParentKey,
								ParentTitle:    it.ParentSummary,
								ParentURL:      parentURL,
								SubtaskTotal:   totalSubs,
								SubtaskDone:    doneCount,
								SubtaskPending: pendingSubs,
								Subtasks:       projSubs,
								URL:            browseURL,
								IsMine:         true,
							})
						}
					}
				}
			}
		}
	}

	// 2. GitHub issues via native GitHub REST connector first, then `gh` CLI fallback
	g := gitx.Local{}
	if g.IsRepo(ctx, workspace) {
		if remoteURL, err := g.RemoteURL(ctx, workspace); err == nil {
			if owner, repo, ok := githubx.ParseRemoteForConfiguredHosts(remoteURL); ok {
				currentUser, _ := githubx.Detect(owner, repo).CurrentUser(ctx)
				token, baseURL, _ := githubx.LoadCredentials()
				if token != "" {
					if restIssues, err := listIssuesREST(ctx, baseURL, owner, repo, token, currentUser); err == nil {
						issues = append(issues, restIssues...)
						return issues, nil
					}
				}
				if ghBin, err := exec.LookPath("gh"); err == nil {
					cmd := exec.CommandContext(ctx, ghBin, "issue", "list", "--repo", owner+"/"+repo, "--state", "open", "--limit", "20", "--json", "number,title,body,state,author,url")
					procx.ConfigureContext(cmd)
					var out bytes.Buffer
					cmd.Stdout = &out
					if err := cmd.Run(); err == nil {
						var raw []struct {
							Number int    `json:"number"`
							Title  string `json:"title"`
							Body   string `json:"body"`
							State  string `json:"state"`
							URL    string `json:"url"`
							Author struct {
								Login string `json:"login"`
							} `json:"author"`
						}
						if json.Unmarshal(out.Bytes(), &raw) == nil {
							for _, ghIt := range raw {
								author := strings.TrimSpace(ghIt.Author.Login)
								issues = append(issues, ProjectIssue{
									Source: "github",
									Key:    fmt.Sprintf("#%d", ghIt.Number),
									Title:  ghIt.Title,
									Body:   ghIt.Body,
									Status: ghIt.State,
									Author: author,
									URL:    ghIt.URL,
									IsMine: currentUser != "" && strings.EqualFold(author, currentUser),
								})
							}
						}
					}
				}
			}
		}
	}

	return issues, nil
}

func sanitizeJiraProjectKey(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	for _, r := range s {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return ""
		}
	}
	return s
}
