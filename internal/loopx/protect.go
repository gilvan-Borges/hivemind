package loopx

import (
	"path"
	"strings"
)

// Violations returns the changed paths that match a protected pattern.
// featuresRel is exempt when the only change inside it is the passes field;
// the caller checks that separately and passes an empty featuresRel when the
// exemption holds.
func Violations(files, patterns []string) []string {
	var out []string
	for _, file := range files {
		file = strings.TrimSpace(path.Clean(filepathSlash(file)))
		if file == "" || file == "." {
			continue
		}
		for _, pattern := range patterns {
			if MatchPath(pattern, file) {
				out = append(out, file)
				break
			}
		}
	}
	return out
}

// MatchPath matches a slash-separated glob. "**" crosses directories.
// A pattern without a slash matches the file name.
func MatchPath(pattern, file string) bool {
	pattern = strings.TrimPrefix(filepathSlash(pattern), "/")
	file = strings.TrimPrefix(path.Clean(filepathSlash(file)), "/")
	if pattern == "" || file == "" || file == "." {
		return false
	}
	if !strings.Contains(pattern, "/") {
		ok, err := path.Match(pattern, path.Base(file))
		return err == nil && ok
	}
	return matchSegments(splitPath(pattern), splitPath(file))
}

func matchSegments(pat, file []string) bool {
	if len(pat) == 0 {
		return len(file) == 0
	}
	if pat[0] == "**" {
		if matchSegments(pat[1:], file) {
			return true
		}
		if len(file) == 0 {
			return false
		}
		return matchSegments(pat, file[1:])
	}
	if len(file) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], file[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pat[1:], file[1:])
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func filepathSlash(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}
