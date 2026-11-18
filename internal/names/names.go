// Package names builds Git branch names, commit subjects and ADK agent
// identifiers. User-facing text stays in Portuguese at the CLI; these helpers
// only produce English identifiers.
package names

import (
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

// ADK converts a free-form agent name into an identifier accepted by ADK.
// The name "user" is reserved by the framework.
func ADK(name string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		r = foldRune(r)
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore && b.Len() > 0 {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "agent"
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "a_" + out
	}
	if out == "user" {
		out = "agent_user"
	}
	return out
}

// Slug makes a short git-safe token from free text.
func Slug(s string, max int) string {
	if max <= 0 {
		max = 40
	}
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		r = foldRune(r)
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= max {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > max {
		out = strings.Trim(out[:max], "-")
	}
	if out == "" {
		return "tarefa"
	}
	return out
}

// IssueKey normalizes a Jira key such as PROJ-123. An empty result means the
// input was not a key.
func IssueKey(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	dash := strings.IndexByte(s, '-')
	if dash <= 0 || dash == len(s)-1 {
		return ""
	}
	project, num := s[:dash], s[dash+1:]
	if project == "" || num == "" {
		return ""
	}
	for _, r := range project {
		if !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return ""
		}
	}
	if project[0] < 'A' || project[0] > 'Z' {
		return ""
	}
	for _, r := range num {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return project + "-" + num
}

func foldRune(r rune) rune {
	switch r {
	case 'á', 'à', 'ã', 'â', 'ä', 'å':
		return 'a'
	case 'é', 'è', 'ê', 'ë':
		return 'e'
	case 'í', 'ì', 'î', 'ï':
		return 'i'
	case 'ó', 'ò', 'õ', 'ô', 'ö':
		return 'o'
	case 'ú', 'ù', 'û', 'ü':
		return 'u'
	case 'ç':
		return 'c'
	case 'ñ':
		return 'n'
	default:
		return r
	}
}

// Branch is hivemind/<issue-or-slug>/<agent>.
func Branch(issueKey, agent, objective string) string {
	token := IssueKey(issueKey)
	if token == "" {
		token = Slug(objective, 32)
	} else {
		token = strings.ToLower(token)
	}
	return "hivemind/" + token + "/" + ADK(agent)
}

// CommitMessage includes the Jira key when the run is bound to an issue.
func CommitMessage(issueKey, objective string) string {
	subject := oneLine(objective, 72)
	if subject == "" {
		subject = "alteracoes do HiveMind"
	}
	if key := IssueKey(issueKey); key != "" {
		return "feat(" + key + "): " + subject
	}
	return "feat: " + subject
}

// PRTitle is the pull request title, with the issue key when present.
func PRTitle(issueKey, objective string) string {
	subject := oneLine(objective, 80)
	if subject == "" {
		subject = "HiveMind"
	}
	if key := IssueKey(issueKey); key != "" {
		return key + ": " + subject
	}
	return subject
}

func oneLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(strx.TruncateRunes(s, max, ""))
}
