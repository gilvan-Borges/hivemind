// Package orgx detects Git/workspace organization identity and evaluates
// profile matching rules so multi-organization setups stay strictly isolated.
package orgx

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Identity describes the detected organization and repository metadata for a workspace.
type Identity struct {
	Workspace         string   `json:"workspace"`
	GitRemoteURL      string   `json:"gitRemoteUrl,omitempty"`
	GitHost           string   `json:"gitHost,omitempty"`
	GitOrg            string   `json:"gitOrg,omitempty"`
	GitRepo           string   `json:"gitRepo,omitempty"`
	WorkspaceLeaf     string   `json:"workspaceLeaf,omitempty"`
	WorkspaceParent   string   `json:"workspaceParent,omitempty"`
	SuggestedPatterns []string `json:"suggestedPatterns,omitempty"`
}

// Detect inspects a workspace directory to extract its Git remote organization
// and normalized path metadata without spawning external processes.
func Detect(workspace string) Identity {
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		return Identity{}
	}
	clean := filepath.Clean(ws)
	id := Identity{
		Workspace:     clean,
		WorkspaceLeaf: filepath.Base(clean),
	}
	parent := filepath.Base(filepath.Dir(clean))
	if parent != "" && parent != "." && parent != string(filepath.Separator) && !strings.HasSuffix(parent, ":") {
		id.WorkspaceParent = parent
	}

	if remoteURL := readGitOriginURL(clean); remoteURL != "" {
		id.GitRemoteURL = remoteURL
		id.GitHost, id.GitOrg, id.GitRepo = ParseGitRemote(remoteURL)
	}

	var sugg []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		k := strings.ToLower(s)
		if !seen[k] {
			seen[k] = true
			sugg = append(sugg, s)
		}
	}
	if id.GitOrg != "" {
		add(id.GitOrg)
	}
	if id.WorkspaceParent != "" {
		add(id.WorkspaceParent)
	}
	if len(sugg) == 0 && id.WorkspaceLeaf != "" {
		add(id.WorkspaceLeaf)
	}
	id.SuggestedPatterns = sugg
	return id
}

// NormalizeWorkspaceKey returns a canonical map key for workspace bindings.
func NormalizeWorkspaceKey(workspace string) string {
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		return ""
	}
	clean := filepath.Clean(ws)
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}

// ParseCSVPatterns splits comma/newline-separated org or path patterns cleanly.
func ParseCSVPatterns(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r", "\n")
	raw = strings.ReplaceAll(raw, ";", "\n")
	raw = strings.ReplaceAll(raw, ",", "\n")
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, "\n") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		k := strings.ToLower(p)
		if !seen[k] {
			seen[k] = true
			out = append(out, p)
		}
	}
	return out
}

// MatchPatterns reports whether id matches any of the configured organization/path patterns.
func MatchPatterns(id Identity, patterns []string) (bool, string) {
	normWS := strings.ToLower(strings.ReplaceAll(filepath.Clean(id.Workspace), "\\", "/"))
	var segments []string
	for _, seg := range strings.Split(normWS, "/") {
		if seg != "" {
			segments = append(segments, seg)
		}
	}

	for _, raw := range patterns {
		pat := strings.TrimSpace(raw)
		if pat == "" {
			continue
		}
		lpat := strings.ToLower(pat)

		// 1. Exact Git organization or host/org match (preferred and highest confidence).
		if id.GitOrg != "" {
			orgLower := strings.ToLower(id.GitOrg)
			if lpat == orgLower {
				return true, pat
			}
			if id.GitHost != "" && lpat == strings.ToLower(id.GitHost)+"/"+orgLower {
				return true, pat
			}
		}

		// 2. Explicit wildcard pattern (*foo*).
		if strings.Contains(lpat, "*") {
			if matchWildcard(lpat, strings.ToLower(id.GitOrg)) || matchWildcard(lpat, normWS) {
				return true, pat
			}
			continue
		}

		// 3. Path prefix match when pattern looks like a directory path.
		normPat := strings.ToLower(strings.TrimSuffix(strings.ReplaceAll(pat, "\\", "/"), "/"))
		if strings.Contains(normPat, "/") {
			if normPat != "" && (normWS == normPat || strings.HasPrefix(normWS, normPat+"/") || strings.Contains(normWS, "/"+normPat+"/")) {
				return true, pat
			}
			continue
		}

		// 4. Directory segment or hyphen/underscore token match (e.g. "vibetex" in "C:\projeto-vibetex\ipg").
		if normWS == "" {
			continue
		}
		for _, seg := range segments {
			if seg == normPat {
				return true, pat
			}
			if len(normPat) >= 3 {
				for _, tok := range splitTokens(seg) {
					if tok == normPat {
						return true, pat
					}
				}
			}
		}
	}
	return false, ""
}

func splitTokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ' '
	})
}

func matchWildcard(pattern, target string) bool {
	if pattern == "" || target == "" {
		return false
	}
	parts := strings.Split(pattern, "*")
	idx := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		pos := strings.Index(target[idx:], part)
		if pos < 0 {
			return false
		}
		if i == 0 && !strings.HasPrefix(pattern, "*") && pos != 0 {
			return false
		}
		idx += pos + len(part)
	}
	if !strings.HasSuffix(pattern, "*") && !strings.HasSuffix(target, parts[len(parts)-1]) {
		return false
	}
	return true
}

// ParseGitRemote extracts host, organization/owner, and repository name from SSH or HTTPS Git URLs.
func ParseGitRemote(raw string) (host, org, repo string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", ""
	}
	s = strings.TrimSuffix(s, "/")

	// SCP-style SSH: git@github.com:org/repo.git or git@ssh.dev.azure.com:v3/org/project/repo
	if !strings.Contains(s, "://") && strings.Contains(s, ":") {
		at := strings.IndexByte(s, '@')
		colon := strings.IndexByte(s, ':')
		if colon > at {
			if at >= 0 {
				host = s[at+1 : colon]
			} else {
				host = s[:colon]
			}
			pathPart := strings.TrimPrefix(s[colon+1:], "/")
			org, repo = extractOrgRepoFromPath(host, pathPart)
			return strings.ToLower(host), org, repo
		}
	}

	u, err := url.Parse(s)
	if err != nil {
		return "", "", ""
	}
	host = strings.ToLower(u.Hostname())
	pathPart := strings.Trim(u.Path, "/")
	org, repo = extractOrgRepoFromPath(host, pathPart)
	return host, org, repo
}

func extractOrgRepoFromPath(host, pathPart string) (org, repo string) {
	parts := strings.Split(strings.Trim(pathPart, "/"), "/")
	if len(parts) < 2 {
		return "", ""
	}
	lowerHost := strings.ToLower(host)
	// Azure DevOps SSH: v3/{org}/{project}/{repo}
	if strings.Contains(lowerHost, "dev.azure.com") {
		if len(parts) >= 4 && strings.EqualFold(parts[0], "v3") {
			return parts[1], strings.TrimSuffix(parts[3], ".git")
		}
		// Azure DevOps HTTPS: /{org}/{project}/_git/{repo}
		return parts[0], strings.TrimSuffix(parts[len(parts)-1], ".git")
	}
	org = parts[0]
	repo = strings.TrimSuffix(parts[len(parts)-1], ".git")
	return org, repo
}

func readGitOriginURL(dir string) string {
	gitDir := findGitDir(dir)
	if gitDir == "" {
		return ""
	}
	// Resolve worktree commondir if present so worktrees inherit origin remote too.
	if commonData, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		rel := strings.TrimSpace(string(commonData))
		if rel != "" {
			cand := rel
			if !filepath.IsAbs(cand) {
				cand = filepath.Join(gitDir, cand)
			}
			if info, statErr := os.Stat(cand); statErr == nil && info.IsDir() {
				gitDir = cand
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "config"))
	if err != nil {
		return ""
	}
	return parseOriginURLFromGitConfig(string(data))
}

func findGitDir(start string) string {
	cur := filepath.Clean(start)
	for i := 0; i < 6; i++ {
		cand := filepath.Join(cur, ".git")
		info, err := os.Stat(cand)
		if err == nil {
			if info.IsDir() {
				return cand
			}
			// Worktree .git pointer file: "gitdir: /path/to/.git/worktrees/..."
			if raw, rerr := os.ReadFile(cand); rerr == nil {
				line := strings.TrimSpace(string(raw))
				if strings.HasPrefix(strings.ToLower(line), "gitdir:") {
					target := strings.TrimSpace(line[len("gitdir:"):])
					if !filepath.IsAbs(target) {
						target = filepath.Join(cur, target)
					}
					return filepath.Clean(target)
				}
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return ""
}

func parseOriginURLFromGitConfig(cfg string) string {
	section := ""
	firstURL := ""
	for _, rawLine := range strings.Split(cfg, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		if !strings.HasPrefix(strings.ToLower(section), "remote ") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if !strings.EqualFold(key, "url") || val == "" {
			continue
		}
		if strings.EqualFold(section, `remote "origin"`) {
			return val
		}
		if firstURL == "" {
			firstURL = val
		}
	}
	return firstURL
}
