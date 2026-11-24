package browserx

import (
	"crypto/subtle"
	"regexp"
	"strings"
)

var (
	bearerValue = regexp.MustCompile(`(?i)bearer\s+\S+`)
	querySecret = regexp.MustCompile(`(?i)(token|password|passwd|secret|api[_-]?key)=([^&\s]+)`)
	knownPrefix = regexp.MustCompile(`\b(ghp_|github_pat_|sk-|xoxb-|ya29\.|AIza)[A-Za-z0-9._\-]+`)
)

// Redact tira segredos óbvios de um texto que vai para o modelo ou para o
// terminal. O token da ponte é passado à parte e também some.
func Redact(s, token string) string {
	if token != "" && strings.Contains(s, token) {
		s = strings.ReplaceAll(s, token, "[redigido]")
	}
	s = bearerValue.ReplaceAllString(s, "Bearer [redigido]")
	s = querySecret.ReplaceAllString(s, "$1=[redigido]")
	s = knownPrefix.ReplaceAllString(s, "[redigido]")
	return s
}

func tokenOK(got, want string) bool {
	if want == "" || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
