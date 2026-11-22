package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Repo sandbox deny policy (`.hivemind/sandbox.json` inside the workspace
// root). The file is deliberately deny-only: a repository can tighten what
// its agents are allowed to run, but it can never relax the HiveMind
// allowlist, permission mode, or governance rules. Entries are auditable
// because their origin is always the repo file itself.
type repoSandboxFile struct {
	// DenyCommands blocks any shell command containing the pattern
	// (case-insensitive substring match), in every permission mode.
	DenyCommands []string `json:"deny_commands"`
}

const repoSandboxRelPath = ".hivemind/sandbox.json"

type repoSandboxState struct {
	stamp   string
	denied  []string
	loadErr error
}

var (
	repoSandboxMu    sync.Mutex
	repoSandboxCache = make(map[string]repoSandboxState)
)

// repoSandboxDenies returns the lowercase deny patterns configured by the
// repository root, cached by (mtime, size). Unreadable or malformed files
// degrade to no denials: the core allowlist policy still applies, and a
// broken repo file must not brick every run.
func repoSandboxDenies(root string) []string {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	path := filepath.Join(root, repoSandboxRelPath)
	stamp := fileStamp(path)

	repoSandboxMu.Lock()
	defer repoSandboxMu.Unlock()
	if st, ok := repoSandboxCache[root]; ok && st.stamp == stamp {
		return st.denied
	}
	state := repoSandboxState{stamp: stamp}
	if stamp != "" {
		if raw, err := os.ReadFile(path); err == nil {
			var f repoSandboxFile
			if json.Unmarshal(raw, &f) == nil {
				for _, pat := range f.DenyCommands {
					if low := strings.ToLower(strings.TrimSpace(pat)); low != "" {
						state.denied = append(state.denied, low)
					}
				}
			} else {
				state.loadErr = fmt.Errorf("%s inválido: JSON não parseado", repoSandboxRelPath)
			}
		}
	}
	repoSandboxCache[root] = state
	return state.denied
}

func fileStamp(path string) string {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return ""
	}
	return fmt.Sprintf("%d:%d", fi.ModTime().UnixNano(), fi.Size())
}

// repoSandboxDenyMatch reports whether the command hits one repo deny
// pattern, and which one.
func repoSandboxDenyMatch(root, command string) (pattern string, denied bool) {
	if strings.TrimSpace(command) == "" {
		return "", false
	}
	low := strings.ToLower(strings.TrimSpace(command))
	for _, pat := range repoSandboxDenies(root) {
		if strings.Contains(low, pat) {
			return pat, true
		}
	}
	return "", false
}
