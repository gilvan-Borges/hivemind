// Package strx provides UTF-8-safe string truncation helpers.
package strx

import (
	"fmt"
	"unicode/utf8"
)

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

// TruncateHeadTailBytes keeps up to headBytes from the start and tailBytes from
// the end of s on UTF-8 rune boundaries, inserting an omission marker in between
// so trailing compiler/test errors remain visible.
func TruncateHeadTailBytes(s string, headBytes, tailBytes int) string {
	if headBytes <= 0 && tailBytes <= 0 {
		return ""
	}
	if len(s) <= headBytes+tailBytes {
		return s
	}
	if headBytes < 0 {
		headBytes = 0
	}
	if tailBytes < 0 {
		tailBytes = 0
	}
	headCut := headBytes
	if headCut > len(s) {
		headCut = len(s)
	}
	for headCut > 0 && !utf8.RuneStart(s[headCut]) {
		headCut--
	}
	tailStart := len(s) - tailBytes
	if tailStart < headCut {
		tailStart = headCut
	}
	for tailStart < len(s) && !utf8.RuneStart(s[tailStart]) {
		tailStart++
	}
	omitted := tailStart - headCut
	if omitted <= 0 {
		return s
	}
	return fmt.Sprintf("%s\n…(%d bytes omitidos)…\n%s", s[:headCut], omitted, s[tailStart:])
}
