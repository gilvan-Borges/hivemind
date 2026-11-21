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
	// Spec 06 §3.2: redação única cobrindo os prefixos que faltavam —
	// tokens OAuth do Google (ya29.), do OpenCode (oc_) e da TypeSafe (ts_).
	regexp.MustCompile(`\b(ya29\.[A-Za-z0-9._-]{8,})`),
	regexp.MustCompile(`(?i)\b(oc_[A-Za-z0-9_-]{10,})`),
	regexp.MustCompile(`(?i)\b(ts_[A-Za-z0-9_-]{10,})`),
	// Chaves de refresh/access token em payloads JSON de credencial.
	regexp.MustCompile(`(?i)(["']?(?:refresh_token|access_token|id_token)["']?\s*[:=]\s*["']?)[A-Za-z0-9._-]{8,}`),
}

// RedactSecrets masks common API token patterns, OAuth bearer tokens and any
// explicit secret values before text is logged or emitted in an orchestrator
// event. The bearer/JSON-value patterns keep their prefix and mask only the
// secret itself.
func RedactSecrets(text string, extraSecrets ...string) string {
	out := text
	for _, s := range extraSecrets {
		s = strings.TrimSpace(s)
		if len(s) >= 4 {
			out = strings.ReplaceAll(out, s, "[REDACTED]")
		}
	}
	// Prefix-keeping patterns: bearer header, refresh/access/id token values.
	out = secretPatterns[6].ReplaceAllString(out, "${1}[REDACTED]")
	out = secretPatterns[len(secretPatterns)-1].ReplaceAllString(out, "${1}[REDACTED]")
	for i, re := range secretPatterns {
		if i == 6 || i == len(secretPatterns)-1 {
			continue
		}
		out = re.ReplaceAllString(out, "[REDACTED]")
	}
	return out
}
