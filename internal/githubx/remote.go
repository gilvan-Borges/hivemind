package githubx

import (
	"net/url"
	"strings"
)

// ParseRemoteForHost extracts owner and repo from a Git remote URL whose host
// matches one of allowedHosts (case-insensitive). Besides the classic
// `git@github.com:owner/repo.git` and `https://github.com/owner/repo.git`
// forms, it understands GitHub Enterprise Server remotes such as
// `git@git.empresa.local:org/repo.git`, `ssh://git@ghe.corp:2222/org/repo`
// and `https://ghe.corp/org/repo.git` (Spec 08: native GHES support).
//
// A remote that carries credentials in the URL (`https://x-access-token:…@…`)
// still parses: the host comparison ignores user info.
func ParseRemoteForHost(raw string, allowedHosts ...string) (owner, repo string, ok bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, ".git")
	host, rest := splitRemoteHostPath(raw)
	if host == "" || rest == "" {
		return "", "", false
	}
	if !hostAllowed(host, allowedHosts) {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// ParseRemote extracts owner and repo from a github.com remote URL.
// Hosts beyond github.com (GitHub Enterprise Server) require ParseRemoteForHost
// with the configured hosts (see ConfiguredHosts).
func ParseRemote(raw string) (owner, repo string, ok bool) {
	return ParseRemoteForHost(raw, "github.com")
}

// ConfiguredHosts returns every host the native connector accepts, derived
// from the configured API base URL (public GitHub plus any Enterprise host
// saved in github-connector.json or GITHUB_API_URL).
func ConfiguredHosts() []string {
	hosts := []string{"github.com"}
	_, baseURL, _ := LoadCredentials()
	if h := hostFromBaseURL(baseURL); h != "" && !strings.EqualFold(h, "github.com") {
		hosts = append(hosts, h)
	}
	return hosts
}

// ParseRemoteForConfiguredHosts parses a remote against github.com plus every
// Enterprise host currently configured (the one-call helper callers should use).
func ParseRemoteForConfiguredHosts(raw string) (owner, repo string, ok bool) {
	return ParseRemoteForHost(raw, ConfiguredHosts()...)
}

func hostFromBaseURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" || strings.EqualFold(base, DefaultAPIBaseURL) {
		return ""
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.Trim(u.Hostname(), "."))
}

// splitRemoteHostPath splits a git remote into (host, path):
//   - scp-like:  git@ghe.corp:org/repo        -> ("ghe.corp", "org/repo")
//   - ssh URL:   ssh://git@ghe.corp:2222/o/r  -> ("ghe.corp", "o/r")
//   - http(s):   https://user@ghe.corp/o/r    -> ("ghe.corp", "o/r")
func splitRemoteHostPath(raw string) (host, rest string) {
	if strings.HasPrefix(raw, "git@") {
		body := strings.TrimPrefix(raw, "git@")
		idx := strings.IndexByte(body, ':')
		if idx <= 0 || idx == len(body)-1 {
			return "", ""
		}
		return strings.ToLower(strings.Trim(body[:idx], ".")), body[idx+1:]
	}
	if i := strings.Index(raw, "://"); i > 0 {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || u.Path == "" {
			return "", ""
		}
		return strings.ToLower(strings.Trim(u.Hostname(), ".")), u.Path
	}
	return "", ""
}

func hostAllowed(host string, allowedHosts []string) bool {
	for _, want := range allowedHosts {
		if strings.EqualFold(strings.TrimSpace(want), host) {
			return true
		}
	}
	return false
}
