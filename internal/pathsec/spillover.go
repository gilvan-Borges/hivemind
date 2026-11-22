package pathsec

import (
	"path/filepath"
	"strings"
)

// IsSpilloverPath reports whether rel is strictly a workspace-relative path
// inside the .hivemind/spillover subtree (the toolspill archive the agent's
// own tools produced). Only relative paths qualify: an absolute path never
// matches, so the jail decides those with its own scoped options.
//
// Matching is exact on the first two segments (.hivemind/spillover) plus at
// least one file segment; lookalikes such as ".hivemind/spilloverx" or
// "docs/.hivemind/spillover/x" do NOT match.
func IsSpilloverPath(rel string) bool {
	s := strings.TrimSpace(strings.ReplaceAll(rel, `\`, "/"))
	if s == "" || filepath.IsAbs(s) {
		return false
	}
	norm := strings.ToLower(filepath.ToSlash(filepath.Clean(s)))
	parts := strings.Split(norm, "/")
	if len(parts) < 3 {
		return false
	}
	if cleanSegment(parts[0]) != ".hivemind" || cleanSegment(parts[1]) != "spillover" {
		return false
	}
	for _, p := range parts[2:] {
		if cleanSegment(p) == "" {
			return false
		}
	}
	return true
}

func cleanSegment(part string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(part), ". "))
}
