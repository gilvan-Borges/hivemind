// Package shellx evaluates and runs workspace commands.
// Commands that are not on the allowlist are refused. Commands that are
// allowed but destructive ask for confirmation before they run.
package shellx

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// Decision is the policy result for one command line.
type Decision struct {
	Argv      []string
	UseShell  bool
	Allowed   bool
	Dangerous bool
	Reason    string
}

// Confirmer accepts or rejects a dangerous command. A nil confirmer rejects.
type Confirmer func(command string) bool

// Evaluate classifies a command line against allowlist.
func Evaluate(command string, allowlist []string) Decision {
	command = strings.TrimSpace(command)
	if command == "" {
		return Decision{Reason: "comando vazio"}
	}
	argv, shell, err := split(command)
	if err != nil {
		return Decision{Reason: err.Error()}
	}
	if len(argv) == 0 {
		return Decision{Reason: "comando vazio"}
	}
	base := strings.ToLower(filepath.Base(argv[0]))
	base = strings.TrimSuffix(base, ".exe")
	allowed := inList(base, allowlist)
	dangerous, why := isDangerous(base, argv, shell)
	dec := Decision{Argv: argv, UseShell: shell, Allowed: allowed, Dangerous: dangerous}
	switch {
	case !allowed:
		dec.Reason = fmt.Sprintf("%q não está na lista de comandos permitidos", base)
	case dangerous:
		dec.Reason = why
	default:
		dec.Reason = "permitido"
	}
	return dec
}

func inList(base string, allowlist []string) bool {
	for _, item := range allowlist {
		trimmed := strings.TrimSpace(item)
		if trimmed == "*" {
			return true
		}
		norm := strings.ToLower(trimmed)
		norm = strings.TrimSuffix(filepath.Base(norm), ".exe")
		if norm == base {
			return true
		}
	}
	return false
}

// IsCommitOrPRCommand reports whether command attempts to create commits, push, merge,
// or mutate pull requests — actions restricted exclusively to the Chief or Architect.
func IsCommitOrPRCommand(command string) (bool, string) {
	segments := strings.FieldsFunc(command, func(r rune) bool {
		return r == ';' || r == '\n' || r == '\r' || r == '|' || r == '&'
	})
	for _, seg := range segments {
		argv, err := fields(strings.TrimSpace(seg))
		if err != nil || len(argv) == 0 {
			continue
		}
		base := strings.ToLower(strings.TrimSuffix(filepath.Base(argv[0]), ".exe"))
		switch base {
		case "git":
			if gitConfigAliasMutates(argv[1:]) {
				return true, "git -c alias executa commit/push/update-ref"
			}
			sub := firstSubcommand(argv[1:])
			switch sub {
			case "commit", "push", "merge", "rebase", "cherry-pick", "tag",
				"update-ref", "symbolic-ref", "reflog", "filter-branch", "fast-import":
				return true, "git " + sub
			}
		case "gh":
			if len(argv) >= 2 {
				top := strings.ToLower(strings.TrimSpace(argv[1]))
				if top == "api" {
					return true, "gh api"
				}
				if top == "pr" && len(argv) >= 3 {
					act := strings.ToLower(strings.TrimSpace(argv[2]))
					switch act {
					case "create", "merge", "review", "comment", "edit", "close", "reopen", "ready":
						return true, "gh pr " + act
					}
				}
			}
		}
	}
	return false, ""
}

// gitConfigAliasMutates detects `git -c alias.X=commit` style aliases, which
// bypass a top-level subcommand check: the actual subcommand is the alias name.
func gitConfigAliasMutates(args []string) bool {
	for i := 0; i < len(args); i++ {
		raw := strings.TrimSpace(args[i])
		low := strings.ToLower(raw)
		if low == "-c" && i+1 < len(args) {
			if aliasValueMutates(args[i+1]) {
				return true
			}
			i++
			continue
		}
		if strings.HasPrefix(low, "-c") && len(raw) > 2 && aliasValueMutates(raw[2:]) {
			return true
		}
	}
	return false
}

func aliasValueMutates(value string) bool {
	low := strings.ToLower(strings.TrimSpace(value))
	if !strings.HasPrefix(low, "alias.") {
		return false
	}
	for _, verb := range []string{"commit", "push", "merge", "rebase", "cherry-pick", "tag", "update-ref", "symbolic-ref", "filter-branch", "fast-import"} {
		if strings.Contains(low, verb) {
			return true
		}
	}
	return false
}

func firstSubcommand(args []string) string {	for i := 0; i < len(args); i++ {
		tok := strings.TrimSpace(args[i])
		if tok == "" {
			continue
		}
		if strings.HasPrefix(tok, "-") {
			// Skip value argument of flags like -C <dir> or -c <k=v>
			if tok == "-C" || tok == "-c" || tok == "--git-dir" || tok == "--work-tree" {
				i++
			}
			continue
		}
		return strings.ToLower(tok)
	}
	return ""
}

func isDangerous(base string, argv []string, shell bool) (bool, string) {
	blocked := map[string]bool{
		"rm": true, "rmdir": true, "del": true, "erase": true, "rd": true,
		"format": true, "shutdown": true, "reboot": true, "sudo": true, "su": true,
		"mkfs": true, "dd": true, "chmod": true, "chown": true, "reg": true, "regedit": true,
		"powershell": true, "pwsh": true, "cmd": true, "bash": true, "sh": true,
		"curl": true, "wget": true, "iwr": true, "invoke-webrequest": true,
	}
	if blocked[base] {
		return true, fmt.Sprintf("%s pode alterar o sistema ou baixar código e precisa de confirmação", base)
	}
	if shell {
		return true, "metacaracteres de shell precisam de confirmação"
	}
	if base == "git" && gitDangerous(argv) {
		return true, "este subcomando do git reescreve histórico, executa hooks customizados ou envia com --force e precisa de confirmação"
	}
	if ok, why := interpreterInlineDangerous(base, argv[1:]); ok {
		return true, why
	}
	for _, a := range argv[1:] {
		low := strings.ToLower(a)
		switch low {
		case "--force", "-f", "--hard", "invoke-expression", "iex", "-enc", "-encodedcommand":
			if base == "git" && (low == "-f" || low == "--force") {
				continue
			}
			return true, "argumento destrutivo: " + a
		}
	}
	return false, ""
}

func interpreterInlineDangerous(base string, args []string) (bool, string) {
	for _, raw := range args {
		low := strings.ToLower(strings.TrimSpace(raw))
		switch base {
		case "python", "python3":
			if low == "-c" || strings.HasPrefix(low, "-c") {
				return true, "execução inline de código Python (-c) precisa de confirmação"
			}
		case "node", "bun":
			if low == "-e" || low == "--eval" || low == "-p" || low == "--print" || strings.HasPrefix(low, "--eval=") {
				return true, fmt.Sprintf("execução inline via %s (%s) precisa de confirmação", base, raw)
			}
		case "npx":
			if low == "-y" || low == "--yes" || low == "-c" || low == "--call" {
				return true, "execução de pacote remoto npx sem prompt precisa de confirmação"
			}
		case "find":
			if low == "-exec" || low == "-execdir" || low == "-delete" {
				return true, "find com -exec/-delete precisa de confirmação"
			}
		}
	}
	return false, ""
}

func gitDangerous(argv []string) bool {
	for _, raw := range argv[1:] {
		trimmed := strings.TrimSpace(raw)
		low := strings.ToLower(trimmed)
		if strings.HasPrefix(low, "--config-env") || strings.HasPrefix(low, "--git-dir") || strings.HasPrefix(low, "--work-tree") {
			return true
		}
		if !strings.HasPrefix(trimmed, "-") {
			break
		}
		if low == "-c" || strings.HasPrefix(low, "-c") {
			return true
		}
	}
	joined := strings.ToLower(strings.Join(argv, " "))
	if strings.Contains(joined, "--force") || strings.Contains(joined, " -f") || strings.HasSuffix(joined, " -f") {
		return true
	}
	if strings.Contains(joined, "reset") && strings.Contains(joined, "--hard") {
		return true
	}
	if strings.Contains(joined, "clean") && (strings.Contains(joined, "-f") || strings.Contains(joined, "-d") || strings.Contains(joined, "-x")) {
		return true
	}
	if strings.Contains(joined, "branch") && strings.Contains(joined, "-d") {
		return true
	}
	if strings.Contains(joined, "push") && (strings.Contains(joined, "--force") || strings.Contains(joined, " -f") || strings.Contains(joined, "+")) {
		return true
	}
	return false
}

func split(command string) ([]string, bool, error) {
	if strings.Contains(command, "$(") || strings.Contains(command, "${") {
		argv, err := fields(command)
		return argv, true, err
	}
	for _, r := range command {
		switch r {
		case '|', '>', '<', '&', ';', '`', '\n', '\r':
			argv, err := fields(command)
			return argv, true, err
		}
	}
	argv, err := fields(command)
	return argv, false, err
}

func fields(s string) ([]string, error) {
	rs := []rune(s)
	var out []string
	var b strings.Builder
	var quote rune
	hasToken := false

	flush := func() {
		if hasToken {
			out = append(out, b.String())
			b.Reset()
			hasToken = false
		}
	}

	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if quote != 0 {
			if quote == '"' && r == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\') {
				i++
				b.WriteRune(rs[i])
				continue
			}
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
			continue
		}
		switch {
		case r == '\\':
			hasToken = true
			if i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\'' || unicode.IsSpace(rs[i+1])) {
				i++
				b.WriteRune(rs[i])
			} else {
				b.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			hasToken = true
		case unicode.IsSpace(r):
			flush()
		default:
			hasToken = true
			b.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("aspas ou escape sem fechar")
	}
	flush()
	return out, nil
}
