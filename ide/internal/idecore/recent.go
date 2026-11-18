// Package idecore provides the desktop IDE backend logic independent of the UI toolkit.
package idecore

import (
	"os"
	"path/filepath"
	"strings"
)

const maxRecentProjects = 12

// RecentFilePath returns the path to ide-recent.txt inside %LOCALAPPDATA%\HiveMind
// (or the OS user config directory when LOCALAPPDATA is unset).
func RecentFilePath() string {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		if cfg, err := os.UserConfigDir(); err == nil && cfg != "" {
			base = cfg
		} else {
			base = os.TempDir()
		}
	}
	return filepath.Join(base, "HiveMind", "ide-recent.txt")
}

// LoadRecentProjects reads valid, existing project directories from filePath.
func LoadRecentProjects(filePath string) []string {
	if filePath == "" {
		filePath = RecentFilePath()
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	for _, raw := range strings.Split(string(data), "\n") {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		key := strings.ToLower(abs)
		if _, ok := seen[key]; ok {
			continue
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, abs)
		if len(out) >= maxRecentProjects {
			break
		}
	}
	return out
}

// RecordRecentProject prepends dir to the MRU list in filePath and persists it.
func RecordRecentProject(filePath, dir string) ([]string, error) {
	if filePath == "" {
		filePath = RecentFilePath()
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	abs = filepath.Clean(abs)
	existing := LoadRecentProjects(filePath)
	updated := make([]string, 0, maxRecentProjects)
	updated = append(updated, abs)
	absKey := strings.ToLower(abs)
	for _, item := range existing {
		if strings.ToLower(item) == absKey {
			continue
		}
		updated = append(updated, item)
		if len(updated) >= maxRecentProjects {
			break
		}
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		return nil, err
	}
	content := strings.Join(updated, "\n") + "\n"
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		return nil, err
	}
	return updated, nil
}
