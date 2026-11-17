// Package workspace confines filesystem access to a project directory.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolve returns the absolute path of rel inside root.
// Absolute paths are accepted only when they already live inside root.
// Symlinks are evaluated so a link cannot escape the workspace.
func Resolve(root, rel string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootAbs = resolved
	}
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "." {
		return rootAbs, nil
	}

	var candidate string
	if filepath.IsAbs(rel) {
		candidate = filepath.Clean(rel)
	} else {
		candidate = filepath.Clean(filepath.Join(rootAbs, rel))
	}
	candidate, err = evalExistingPrefix(candidate)
	if err != nil {
		return "", err
	}
	if !Within(rootAbs, candidate) {
		return "", fmt.Errorf("caminho fora do workspace: %s", rel)
	}
	relToRoot, err := filepath.Rel(rootAbs, candidate)
	if err != nil {
		return "", err
	}
	for _, part := range strings.Split(relToRoot, string(os.PathSeparator)) {
		if part == ".git" {
			return "", fmt.Errorf("acesso a .git não é permitido: %s", rel)
		}
	}
	return candidate, nil
}

// Within reports whether target is root or a path inside root.
func Within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

// evalExistingPrefix resolves symlinks on the longest existing prefix and
// rejoins the not-yet-created suffix. That keeps jail checks correct for new files.
func evalExistingPrefix(path string) (string, error) {
	path = filepath.Clean(path)
	var missing []string
	cur := path
	for {
		_, err := os.Lstat(cur)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return path, nil
		}
		missing = append(missing, filepath.Base(cur))
		cur = parent
	}
}
