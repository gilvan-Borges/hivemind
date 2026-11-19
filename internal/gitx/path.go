package gitx

import (
	"fmt"
	"path/filepath"
	"strings"
)

// UnquotePath decodes the C-style quoting git applies to paths that contain
// spaces, quotes, tabs, newlines or non-ASCII bytes (core.quotePath). Without
// this, a file named "ação.ts" arrives as "\"a\\303\\247\\303\\243o.ts\"" and
// diff/stage/revert never match the real path.
func UnquotePath(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	s = s[1 : len(s)-1]
	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			out = append(out, c)
			continue
		}
		i++
		switch s[i] {
		case 'a':
			out = append(out, 0x07)
		case 'b':
			out = append(out, 0x08)
		case 't':
			out = append(out, '\t')
		case 'n':
			out = append(out, '\n')
		case 'v':
			out = append(out, 0x0b)
		case 'f':
			out = append(out, 0x0c)
		case 'r':
			out = append(out, '\r')
		case '"':
			out = append(out, '"')
		case '\\':
			out = append(out, '\\')
		default:
			if s[i] >= '0' && s[i] <= '7' && i+2 < len(s) && s[i+1] >= '0' && s[i+1] <= '7' && s[i+2] >= '0' && s[i+2] <= '7' {
				val := int(s[i]-'0')*64 + int(s[i+1]-'0')*8 + int(s[i+2]-'0')
				out = append(out, byte(val))
				i += 2
				continue
			}
			out = append(out, '\\', s[i])
		}
	}
	return string(out)
}

// ValidateBranchName rejects names that git would interpret as options or that
// are not valid ref names. The IDE passes user/LLM supplied branch names to
// `git checkout`, so a leading "-" (e.g. "-f") must never reach git.
func ValidateBranchName(name string) error {
	n := strings.TrimSpace(name)
	if n == "" {
		return fmt.Errorf("informe o nome da branch")
	}
	if strings.HasPrefix(n, "-") {
		return fmt.Errorf("nome de branch inválido %q: não pode começar com '-'", name)
	}
	if strings.Contains(n, "..") || strings.Contains(n, "@{") ||
		strings.ContainsAny(n, " ~^:?*[\\\t\n\r") ||
		strings.Contains(n, "//") ||
		strings.HasSuffix(n, ".") || strings.HasSuffix(n, ".lock") ||
		strings.HasSuffix(n, "/") {
		return fmt.Errorf("nome de branch inválido %q", name)
	}
	for _, r := range n {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("nome de branch inválido %q", name)
		}
	}
	return nil
}

// LiteralPathspec returns a pathspec that makes git treat rel as a literal path
// instead of interpreting ":" magic (":(glob)", ":/"), even if the file name
// starts with a colon.
func LiteralPathspec(rel string) string {
	return ":(literal)" + filepath.ToSlash(rel)
}
