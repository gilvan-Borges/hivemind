package jev

import (
	"regexp"
	"strings"
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(sk-[a-z0-9_-]{6,})`),
	regexp.MustCompile(`(?i)\b(crsr_[a-z0-9_-]{6,})`),
	regexp.MustCompile(`(?i)\b(gsk_[a-z0-9_-]{6,})`),
	regexp.MustCompile(`(?i)\b(xai-[a-z0-9_-]{6,})`),
	regexp.MustCompile(`\b(AIza[0-9A-Za-z_-]{10,})`),
	regexp.MustCompile(`\b(gh[pousr]_[0-9A-Za-z]{10,})`),
	regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._-]{8,}`),
	regexp.MustCompile(`\b(hmlan_[A-Za-z0-9_-]{12,})`),
}

// RedactSecrets masks common API token patterns and any explicit secret values
// before text is logged or emitted in an orchestrator event.
func RedactSecrets(text string, extraSecrets ...string) string {
	out := text
	for _, s := range extraSecrets {
		s = strings.TrimSpace(s)
		if len(s) >= 4 {
			out = strings.ReplaceAll(out, s, "[REDACTED]")
		}
	}
	for i, re := range secretPatterns {
		if i == len(secretPatterns)-2 {
			out = re.ReplaceAllString(out, "${1}[REDACTED]")
			continue
		}
		out = re.ReplaceAllString(out, "[REDACTED]")
	}
	return out
}
