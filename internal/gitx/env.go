package gitx

import (
	"os"
	"strings"
)

// NonInteractiveEnv returns an environment safe for unattended git execution.
// It drops any inherited interactive prompters (GIT_TERMINAL_PROMPT,
// GCM_INTERACTIVE, GIT_ASKPASS, SSH_ASKPASS) and pins non-interactive values,
// so credential helpers can never block an agent turn waiting for a TTY prompt:
//
//	GIT_TERMINAL_PROMPT=0  — never prompt on the terminal
//	GCM_INTERACTIVE=Never  — Git Credential Manager must fail, not prompt
//	GIT_ASKPASS=""         — no askpass helper
//	SSH_ASKPASS=""         — no SSH askpass helper
//
// This is the single source of non-interactive git env for the CLI, the
// orchestrator and the IDE (idecore delegates here instead of duplicating).
func NonInteractiveEnv() []string {
	drop := map[string]bool{
		"GIT_TERMINAL_PROMPT": true,
		"GCM_INTERACTIVE":     true,
		"GIT_ASKPASS":         true,
		"SSH_ASKPASS":         true,
	}
	out := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if ok && drop[strings.ToUpper(name)] {
			continue
		}
		out = append(out, entry)
	}
	return append(out,
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=Never",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
	)
}
