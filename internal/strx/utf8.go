// Package strx provides UTF-8-safe string truncation helpers.
package strx

import "unicode/utf8"

// TruncateBytes truncates s to at most maxBytes without splitting multi-byte UTF-8 runes,
// appending suffix when truncation occurs.
func TruncateBytes(s string, maxBytes int, suffix string) string {
	if maxBytes <= 0 {
		return suffix
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + suffix
}

// TruncateRunes truncates s to at most maxRunes Unicode code points,
// appending suffix when truncation occurs.
func TruncateRunes(s string, maxRunes int, suffix string) string {
	if maxRunes <= 0 {
		return suffix
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	count := 0
	for idx := range s {
		if count == maxRunes {
			return s[:idx] + suffix
		}
		count++
	}
	return s
}
