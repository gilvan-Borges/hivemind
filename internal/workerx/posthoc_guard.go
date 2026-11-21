package workerx

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/pathsec"
)

// GuardViolation describes one disallowed change discovered in post-hoc diff validation.
type GuardViolation struct {
	Path   string `json:"path"`
	Reason string `json:"reason"` // "sensitive_path" | "outside_owned_paths"
}

// GuardResult summarizes modified files and any security/ownership violations.
type GuardResult struct {
	Modified   []string         `json:"modified,omitempty"`
	Violations []GuardViolation `json:"violations,omitempty"`
}

// Passed reports whether zero post-hoc violations occurred.
func (r GuardResult) Passed() bool { return len(r.Violations) == 0 }

// ValidatePostHocDiff inspects git status (plus any fallback paths reported by
// the worker) inside worktreeDir and blocks any sensitive path (pathsec) or edit
// outside ownedPaths before integration into _integracao.
func ValidatePostHocDiff(ctx context.Context, g gitx.Git, worktreeDir string, ownedPaths []string, reportedFiles ...string) (GuardResult, error) {
	var res GuardResult
	seen := map[string]bool{}
	var paths []string

	if g != nil && strings.TrimSpace(worktreeDir) != "" && g.IsRepo(ctx, worktreeDir) {
		raw, err := g.Run(ctx, worktreeDir, "status", "--porcelain", "-z", "-uall")
		if err != nil {
			return res, fmt.Errorf("validar diff pós-hoc em %s: %w", worktreeDir, err)
		}
		for _, p := range parsePorcelainPaths(raw) {
			if !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	for _, rf := range reportedFiles {
		clean := normalizeRel(rf)
		if clean != "" && !seen[clean] {
			seen[clean] = true
			paths = append(paths, clean)
		}
	}
	res.Modified = paths

	normOwned := normalizeOwnedList(ownedPaths)
	for _, p := range paths {
		if pathsec.IsSensitivePath(p) {
			res.Violations = append(res.Violations, GuardViolation{
				Path:   p,
				Reason: "sensitive_path",
			})
			continue
		}
		if len(normOwned) > 0 && !matchesAnyOwnedPattern(p, normOwned) {
			res.Violations = append(res.Violations, GuardViolation{
				Path:   p,
				Reason: "outside_owned_paths",
			})
		}
	}
	return res, nil
}

// RevertViolations resets/cleans disallowed files inside worktreeDir.
func RevertViolations(ctx context.Context, g gitx.Git, worktreeDir string, violations []GuardViolation) error {
	if g == nil || strings.TrimSpace(worktreeDir) == "" || len(violations) == 0 {
		return nil
	}
	for _, v := range violations {
		spec := gitx.LiteralPathspec(v.Path)
		_, _ = g.Run(ctx, worktreeDir, "checkout", "HEAD", "--", spec)
		_, _ = g.Run(ctx, worktreeDir, "clean", "-fd", "--", spec)
	}
	return nil
}

func parsePorcelainPaths(raw string) []string {
	var out []string
	// Handle NUL-delimited output (`git status --porcelain -z`) first so trimmed
	// leading spaces in `gitx.Local.Run` never strip the status column of line 1.
	if strings.ContainsRune(raw, '\x00') {
		recs := strings.Split(raw, "\x00")
		for i := 0; i < len(recs); i++ {
			rec := recs[i]
			if len(rec) < 3 {
				continue
			}
			// Check for XY<space>path; if first leading space was stripped by TrimSpace,
			// rec looks like X<space>path (len>=2 with space at index 1).
			var p string
			switch {
			case len(rec) >= 4 && rec[2] == ' ':
				p = rec[3:]
			case rec[1] == ' ':
				p = rec[2:]
			default:
				p = rec
			}
			// For rename/copy (`R`/`C`), next NUL entry holds original path.
			if strings.HasPrefix(strings.TrimSpace(rec[:min(2, len(rec))]), "R") || strings.HasPrefix(strings.TrimSpace(rec[:min(2, len(rec))]), "C") {
				i++
			}
			if clean := normalizeRel(gitx.UnquotePath(p)); clean != "" {
				out = append(out, clean)
			}
		}
		return out
	}

	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if len(line) < 3 {
			continue
		}
		var rest string
		switch {
		case len(line) >= 4 && line[2] == ' ':
			rest = strings.TrimSpace(line[3:])
		case line[1] == ' ':
			rest = strings.TrimSpace(line[2:])
		default:
			continue
		}
		if idx := strings.Index(rest, " -> "); idx >= 0 {
			rest = strings.TrimSpace(rest[idx+4:])
		}
		unquoted := gitx.UnquotePath(rest)
		if clean := normalizeRel(unquoted); clean != "" {
			out = append(out, clean)
		}
	}
	return out
}

func normalizeRel(raw string) string {
	s := strings.TrimSpace(filepath.ToSlash(raw))
	s = strings.TrimPrefix(s, "./")
	s = strings.Trim(s, "/")
	return s
}

func normalizeOwnedList(owned []string) []string {
	var out []string
	for _, raw := range owned {
		s := strings.TrimSpace(filepath.ToSlash(raw))
		s = strings.TrimPrefix(s, "./")
		s = strings.TrimPrefix(s, "/")
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func matchesAnyOwnedPattern(rel string, patterns []string) bool {
	relLow := strings.ToLower(normalizeRel(rel))
	for _, rawPat := range patterns {
		pat := strings.ToLower(strings.TrimSuffix(rawPat, "/"))
		if pat == "" {
			continue
		}
		if strings.HasSuffix(pat, "/**") {
			prefix := strings.TrimSuffix(pat, "/**")
			if relLow == prefix || strings.HasPrefix(relLow, prefix+"/") {
				return true
			}
			continue
		}
		if !strings.ContainsAny(pat, "*?[") {
			if relLow == pat || strings.HasPrefix(relLow, pat+"/") {
				return true
			}
			continue
		}
		if ok, _ := path.Match(pat, relLow); ok {
			return true
		}
		if !strings.Contains(pat, "/") {
			if ok, _ := path.Match(pat, path.Base(relLow)); ok {
				return true
			}
		}
	}
	return false
}
