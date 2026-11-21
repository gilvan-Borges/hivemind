package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode"

	"github.com/gilvan-Borges/HiveMind/internal/atomicx"
)

var migrateMu sync.Mutex

// AppDataRoot returns the user-level HiveMind storage directory outside any Git repository:
//   - HIVEMIND_DATA_DIR when explicitly set
//   - %LOCALAPPDATA%\HiveMind on Windows
//   - os.UserConfigDir()/HiveMind (or ~/.hivemind) on Linux/macOS
func AppDataRoot() string {
	if env := strings.TrimSpace(os.Getenv("HIVEMIND_DATA_DIR")); env != "" {
		return filepath.Clean(env)
	}
	if runtime.GOOS == "windows" {
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			return filepath.Join(local, "HiveMind")
		}
	}
	if cfg, err := os.UserConfigDir(); err == nil && strings.TrimSpace(cfg) != "" {
		return filepath.Join(cfg, "HiveMind")
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".hivemind")
	}
	return filepath.Join(os.TempDir(), "HiveMind")
}

// ProjectSlug returns a deterministic "<sanitized-name>-<8-hex-hash>" identifier for workspaceRoot.
func ProjectSlug(workspaceRoot string) string {
	abs, err := filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil || abs == "" {
		abs = filepath.Clean(strings.TrimSpace(workspaceRoot))
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil && resolved != "" {
		abs = resolved
	}
	norm := filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		norm = strings.ToLower(norm)
	}
	sum := sha256.Sum256([]byte(filepath.ToSlash(norm)))
	shortHash := hex.EncodeToString(sum[:5]) // 10 hex chars

	base := filepath.Base(abs)
	var slug strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(base) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			slug.WriteRune(r)
			lastDash = false
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			slug.WriteRune(r)
			lastDash = false
		} else if !lastDash && slug.Len() > 0 {
			slug.WriteByte('-')
			lastDash = true
		}
		if slug.Len() >= 28 {
			break
		}
	}
	prefix := strings.Trim(slug.String(), "-")
	if prefix == "" {
		prefix = "workspace"
	}
	return prefix + "-" + shortHash
}

// StateDir returns the external per-workspace state directory
// (<AppDataRoot>/workspaces/<ProjectSlug>) used for sessions, worktrees,
// MEMORY.md, logs, and auto-review state — completely outside the project's Git repo.
func StateDir(workspaceRoot string) string {
	return filepath.Join(AppDataRoot(), "workspaces", ProjectSlug(workspaceRoot))
}

// SessionsDir returns the external sessions directory for workspaceRoot,
// migrating any legacy <workspace>/.hivemind/sessions files on first access.
func SessionsDir(workspaceRoot string) string {
	ext := filepath.Join(StateDir(workspaceRoot), "sessions")
	migrateLegacySessions(workspaceRoot, ext)
	return ext
}

// WorktreesDir returns the external worktrees root for workspaceRoot
// (<AppDataRoot>/workspaces/<slug>/worktrees).
func WorktreesDir(workspaceRoot string) string {
	return filepath.Join(StateDir(workspaceRoot), "worktrees")
}

// WorktreePath returns the external worktree directory for a specific agent or "_integracao".
func WorktreePath(workspaceRoot, agent string) string {
	return filepath.Join(WorktreesDir(workspaceRoot), agent)
}

// MemoryPath returns the external MEMORY.md path for workspaceRoot,
// migrating any legacy <workspace>/.hivemind/MEMORY.md on first access.
func MemoryPath(workspaceRoot string) string {
	ext := filepath.Join(StateDir(workspaceRoot), "MEMORY.md")
	migrateLegacyFile(filepath.Join(workspaceRoot, ".hivemind", "MEMORY.md"), ext)
	cleanEmptyLegacyHivemindDir(workspaceRoot)
	return ext
}

// AutoReviewStatePath returns the external auto-review-state.json path for workspaceRoot,
// migrating any legacy <workspace>/.hivemind/auto-review-state.json on first access.
func AutoReviewStatePath(workspaceRoot string) string {
	ext := filepath.Join(StateDir(workspaceRoot), "auto-review-state.json")
	migrateLegacyFile(filepath.Join(workspaceRoot, ".hivemind", "auto-review-state.json"), ext)
	cleanEmptyLegacyHivemindDir(workspaceRoot)
	return ext
}

// LogsDir returns the external logs directory for workspaceRoot.
func LogsDir(workspaceRoot string) string {
	ext := filepath.Join(StateDir(workspaceRoot), "logs")
	_ = os.RemoveAll(filepath.Join(workspaceRoot, ".hivemind", "logs"))
	cleanEmptyLegacyHivemindDir(workspaceRoot)
	return ext
}

func migrateLegacyFile(legacyPath, externalPath string) {
	migrateMu.Lock()
	defer migrateMu.Unlock()
	if strings.TrimSpace(legacyPath) == "" || strings.TrimSpace(externalPath) == "" {
		return
	}
	legacyInfo, err := os.Stat(legacyPath)
	if err != nil || legacyInfo.IsDir() {
		return
	}
	if _, extErr := os.Stat(externalPath); os.IsNotExist(extErr) {
		if data, rErr := os.ReadFile(legacyPath); rErr == nil {
			if mkErr := os.MkdirAll(filepath.Dir(externalPath), 0o700); mkErr == nil {
				if wErr := atomicx.WriteFile(externalPath, data, 0o600); wErr == nil {
					_ = os.Remove(legacyPath)
				}
			}
		}
		return
	}
	// External already exists: remove legacy copy inside the repo so git status stays clean.
	_ = os.Remove(legacyPath)
}

func migrateLegacySessions(workspaceRoot, externalSessionsDir string) {
	migrateMu.Lock()
	defer migrateMu.Unlock()
	if strings.TrimSpace(workspaceRoot) == "" {
		return
	}
	legacyDir := filepath.Join(workspaceRoot, ".hivemind", "sessions")
	entries, err := os.ReadDir(legacyDir)
	if err != nil {
		return
	}
	if err := os.MkdirAll(externalSessionsDir, 0o700); err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		src := filepath.Join(legacyDir, e.Name())
		dst := filepath.Join(externalSessionsDir, e.Name())
		if _, statErr := os.Stat(dst); os.IsNotExist(statErr) {
			if data, rErr := os.ReadFile(src); rErr == nil {
				if wErr := atomicx.WriteFile(dst, data, 0o600); wErr == nil {
					_ = os.Remove(src)
				}
			}
		} else if e.Name() != "index.json" {
			_ = os.Remove(src)
		} else {
			// Rebuild index on next load if both existed
			_ = os.Remove(src)
			_ = os.Remove(dst)
		}
	}
	if rem, rErr := os.ReadDir(legacyDir); rErr == nil && len(rem) == 0 {
		_ = os.Remove(legacyDir)
	}
	cleanEmptyLegacyHivemindDir(workspaceRoot)
}

func cleanEmptyLegacyHivemindDir(workspaceRoot string) {
	if strings.TrimSpace(workspaceRoot) == "" {
		return
	}
	hm := filepath.Join(workspaceRoot, ".hivemind")
	entries, err := os.ReadDir(hm)
	if err == nil && len(entries) == 0 {
		_ = os.Remove(hm)
	}
}
