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
		item = strings.ToLower(strings.TrimSpace(item))
		item = strings.TrimSuffix(filepath.Base(item), ".exe")
		if item == base {
			return true
		}
	}
	return false
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
		return true, "este subcomando do git reescreve histórico ou envia com --force e precisa de confirmação"
	}
	for _, a := range argv[1:] {
		low := strings.ToLower(a)
		switch low {
		case "--force", "-f", "--hard", "invoke-expression", "iex", "-enc", "-encodedcommand":
			if base == "git" && (low == "-f" || low == "--force") {
				continue
			}
			if low == "--force" || low == "--hard" || low == "invoke-expression" || low == "iex" || low == "-enc" || low == "-encodedcommand" {
				return true, "argumento destrutivo: " + a
			}
		}
	}
	return false, ""
}

func gitDangerous(argv []string) bool {
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
	for _, r := range command {
		switch r {
		case '|', '>', '<', '&', ';', '`':
			argv, err := fields(command)
			return argv, true, err
		}
	}
	argv, err := fields(command)
	return argv, false, err
}

func fields(s string) ([]string, error) {
	var out []string
	var b strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if b.Len() > 0 || quote != 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
				flush()
			} else {
				b.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case unicode.IsSpace(r):
			flush()
		default:
			b.WriteRune(r)
		}
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("aspas ou escape sem fechar")
	}
	flush()
	return out, nil
}
