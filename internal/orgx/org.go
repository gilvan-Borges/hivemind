// Package orgx detects Git/workspace organization identity and evaluates
// profile matching rules so multi-organization setups stay strictly isolated.
package orgx

import (
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const maxGitMetaBytes int64 = 256 << 10

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

type detectMeta struct {
	markerPath string
	markerMod  time.Time
	markerSize int64
	configPath string
	configMod  time.Time
	configSize int64
	id         Identity
}

var (
	detectMu    sync.Mutex
	detectCache = map[string]detectMeta{}
)

// Detect inspects a workspace directory to extract its Git remote organization
// and normalized path metadata without spawning external processes.
// Results are cached and revalidated with a stat, so model calls do not re-read .git/config.
func Detect(workspace string) Identity {
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		return Identity{}
	}
	clean := filepath.Clean(ws)
	key := NormalizeWorkspaceKey(clean)
	detectMu.Lock()
	ent, ok := detectCache[key]
	detectMu.Unlock()
	if ok && detectFresh(ent) {
		return cloneIdentity(ent.id)
	}
	meta := detectSlow(clean)
	detectMu.Lock()
	if len(detectCache) > 128 {
		detectCache = map[string]detectMeta{}
	}
	detectCache[key] = meta
	detectMu.Unlock()
	return cloneIdentity(meta.id)
}

func cloneIdentity(id Identity) Identity {
	if len(id.SuggestedPatterns) > 0 {
		id.SuggestedPatterns = append([]string(nil), id.SuggestedPatterns...)
	}
	return id
}

func detectFresh(m detectMeta) bool {
	if m.markerPath == "" {
		return false
	}
	info, err := os.Lstat(m.markerPath)
	if m.configPath == "" {
		return err != nil
	}
	if err != nil || !info.ModTime().Equal(m.markerMod) || info.Size() != m.markerSize {
		return false
	}
	cfg, err := os.Lstat(m.configPath)
	if err != nil || !cfg.Mode().IsRegular() || !cfg.ModTime().Equal(m.configMod) || cfg.Size() != m.configSize {
		return false
	}
	return true
}

func detectSlow(clean string) detectMeta {
	id := Identity{
		Workspace:     clean,
		WorkspaceLeaf: filepath.Base(clean),
	}
	parent := filepath.Base(filepath.Dir(clean))
	if parent != "" && parent != "." && parent != string(filepath.Separator) && !strings.HasSuffix(parent, ":") {
		id.WorkspaceParent = parent
	}
	meta := detectMeta{id: id, markerPath: filepath.Join(clean, ".git")}
	gitDir, marker := findGitDir(clean)
	if marker != "" {
		meta.markerPath = marker
	}
	if info, err := os.Lstat(meta.markerPath); err == nil {
		meta.markerMod = info.ModTime()
		meta.markerSize = info.Size()
	}
	if gitDir == "" {
		meta.id.SuggestedPatterns = suggestPatterns(meta.id)
		return meta
	}
	resolved := resolveCommonDir(gitDir)
	configPath := filepath.Join(resolved, "config")
	data, info, ok := readGitMeta(configPath)
	if !ok {
		meta.id.SuggestedPatterns = suggestPatterns(meta.id)
		return meta
	}
	meta.configPath = configPath
	meta.configMod = info.ModTime()
	meta.configSize = info.Size()
	if remoteURL := parseOriginURLFromGitConfig(string(data)); remoteURL != "" {
		meta.id.GitRemoteURL = remoteURL
		meta.id.GitHost, meta.id.GitOrg, meta.id.GitRepo = ParseGitRemote(remoteURL)
	}
	meta.id.SuggestedPatterns = suggestPatterns(meta.id)
	return meta
}

func suggestPatterns(id Identity) []string {
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
	return sugg
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
// A known Git org is authoritative: a folder name or substring cannot select another org's profile.
// Path and token rules apply only when the workspace has no Git organization.
func MatchPatterns(id Identity, patterns []string) (bool, string) {
	normWS := normalizeWS(id.Workspace)
	var segments []string
	for _, seg := range strings.Split(normWS, "/") {
		if seg != "" {
			segments = append(segments, seg)
		}
	}
	orgLower := strings.ToLower(strings.TrimSpace(id.GitOrg))
	hostOrg := ""
	if orgLower != "" && id.GitHost != "" {
		hostOrg = strings.ToLower(id.GitHost) + "/" + orgLower
	}

	for _, raw := range patterns {
		pat := strings.TrimSpace(raw)
		if pat == "" {
			continue
		}
		lpat := strings.ToLower(pat)
		if strings.Trim(lpat, "*?") == "" {
			continue
		}

		if strings.ContainsAny(lpat, "*?") {
			if orgLower != "" && (matchWildcard(lpat, orgLower) || (hostOrg != "" && matchWildcard(lpat, hostOrg))) {
				return true, pat
			}
			if orgLower != "" {
				continue
			}
			if matchWildcard(lpat, strings.ToLower(id.WorkspaceLeaf)) || matchWildcard(lpat, strings.ToLower(id.WorkspaceParent)) {
				return true, pat
			}
			continue
		}

		if orgLower != "" && (lpat == orgLower || (hostOrg != "" && lpat == hostOrg)) {
			return true, pat
		}
		if orgLower != "" {
			continue
		}

		normPat := strings.ToLower(strings.Trim(strings.ReplaceAll(pat, "\\", "/"), "/"))
		if strings.Contains(normPat, "/") {
			if pathAnchored(normWS, normPat) {
				return true, pat
			}
			continue
		}
		if normWS == "" {
			continue
		}
		for _, seg := range segments {
			if seg == lpat {
				return true, pat
			}
			if len(lpat) >= 3 {
				for _, tok := range splitTokens(seg) {
					if tok == lpat {
						return true, pat
					}
				}
			}
		}
	}
	return false, ""
}

func normalizeWS(workspace string) string {
	if strings.TrimSpace(workspace) == "" {
		return ""
	}
	return strings.ToLower(strings.ReplaceAll(filepath.Clean(workspace), "\\", "/"))
}

func pathAnchored(normWS, normPat string) bool {
	if normWS == "" || normPat == "" {
		return false
	}
	if normWS == normPat || strings.HasPrefix(normWS, normPat+"/") {
		return true
	}
	// Segment sequence ("Clientes/Acme"), never a raw substring of one name.
	return strings.Contains(normWS, "/"+normPat+"/") || strings.HasSuffix(normWS, "/"+normPat)
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

// readGitMeta reads a regular file up to maxGitMetaBytes. Symlinks are refused
// so a crafted .git cannot redirect the parser at an arbitrary secrets file.
func readGitMeta(path string) ([]byte, os.FileInfo, bool) {
	info, err := os.Lstat(path)
	if err != nil || info == nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxGitMetaBytes {
		return nil, nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxGitMetaBytes+1))
	if err != nil || int64(len(data)) > maxGitMetaBytes {
		return nil, nil, false
	}
	return data, info, true
}

func pathHasDotGit(p string) bool {
	for _, seg := range strings.Split(filepath.Clean(p), string(filepath.Separator)) {
		if seg == ".git" {
			return true
		}
	}
	return false
}

func isSafeGitDir(dir string) bool {
	dir = filepath.Clean(dir)
	if dir == "" || !pathHasDotGit(dir) {
		return false
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	data, _, ok := readGitMeta(filepath.Join(dir, "HEAD"))
	return ok && len(data) > 0 && len(data) <= 4096
}

func findGitDir(start string) (gitDir, marker string) {
	cur := filepath.Clean(start)
	for i := 0; i < 6; i++ {
		cand := filepath.Join(cur, ".git")
		info, err := os.Lstat(cand)
		if err == nil {
			if info.IsDir() {
				return cand, cand
			}
			// Worktree pointer file. The target must itself be a git dir
			// (path contains ".git" and a regular HEAD). Absolute pointers at
			// ~/.aws, includes, or other trees are ignored.
			if data, _, ok := readGitMeta(cand); ok {
				line := strings.TrimSpace(string(data))
				if nl := strings.IndexByte(line, '\n'); nl >= 0 {
					line = strings.TrimSpace(line[:nl])
				}
				if len(line) <= 4096 && strings.HasPrefix(strings.ToLower(line), "gitdir:") {
					target := strings.TrimSpace(line[len("gitdir:"):])
					if target != "" && !strings.ContainsAny(target, "\r\n") {
						if !filepath.IsAbs(target) {
							target = filepath.Join(cur, target)
						}
						target = filepath.Clean(target)
						if isSafeGitDir(target) {
							return target, cand
						}
					}
				}
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return "", ""
}

// resolveCommonDir follows a worktree commondir only when it stays inside the
// same .git tree. Absolute paths that escape to another directory are ignored.
func resolveCommonDir(gitDir string) string {
	data, _, ok := readGitMeta(filepath.Join(gitDir, "commondir"))
	if !ok {
		return gitDir
	}
	rel := strings.TrimSpace(string(data))
	if rel == "" || strings.ContainsAny(rel, "\r\n") {
		return gitDir
	}
	var cand string
	if filepath.IsAbs(rel) {
		cand = filepath.Clean(rel)
	} else {
		cand = filepath.Clean(filepath.Join(gitDir, rel))
	}
	back, err := filepath.Rel(cand, filepath.Clean(gitDir))
	if err != nil || back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) {
		return gitDir
	}
	if !pathHasDotGit(cand) {
		return gitDir
	}
	info, err := os.Lstat(cand)
	if err != nil || !info.IsDir() {
		return gitDir
	}
	return cand
}

func parseOriginURLFromGitConfig(cfg string) string {
	if int64(len(cfg)) > maxGitMetaBytes {
		cfg = cfg[:maxGitMetaBytes]
	}
	inRemote := false
	origin := false
	firstURL := ""
	for _, rawLine := range strings.Split(cfg, "\n") {
		if len(rawLine) > 2048 {
			continue
		}
		line := strings.TrimSpace(strings.TrimRight(rawLine, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inRemote, origin = false, false
			if !strings.HasSuffix(line, "]") {
				continue
			}
			body := strings.TrimSpace(line[1 : len(line)-1])
			low := strings.ToLower(body)
			if low == "include" || strings.HasPrefix(low, "includeif") {
				continue
			}
			sub, ok := parseRemoteSubsection(body)
			if !ok {
				continue
			}
			inRemote = true
			origin = strings.EqualFold(sub, "origin")
			continue
		}
		if !inRemote {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if !strings.EqualFold(key, "url") {
			continue
		}
		val := sanitizeRemoteURL(line[eq+1:])
		if val == "" {
			continue
		}
		if origin {
			return val
		}
		if firstURL == "" {
			firstURL = val
		}
	}
	return firstURL
}

func parseRemoteSubsection(body string) (string, bool) {
	fields := strings.Fields(body)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "remote") {
		return "", false
	}
	sub := fields[1]
	if len(sub) < 2 || sub[0] != '"' || sub[len(sub)-1] != '"' {
		return "", false
	}
	sub = sub[1 : len(sub)-1]
	if sub == "" || strings.ContainsAny(sub, "\\\"\n\r") {
		return "", false
	}
	return sub, true
}

func sanitizeRemoteURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" || strings.ContainsAny(s, "\r\n\x00") {
		return ""
	}
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			s = strings.TrimSpace(s[1 : len(s)-1])
		}
	}
	if s == "" || strings.ContainsAny(s, " \t\r\n\x00") {
		return ""
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return ""
		}
		u.User = nil
		u.Fragment = ""
		u.RawQuery = ""
		return u.String()
	}
	return s
}
