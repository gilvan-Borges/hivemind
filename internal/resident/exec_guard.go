package resident

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// forbiddenShellExecutables blocks all interactive/scripting shell wrappers (§12.4).
var forbiddenShellExecutables = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ash": true, "fish": true, "ksh": true,
	"cmd": true, "cmd.exe": true,
	"powershell": true, "powershell.exe": true, "pwsh": true, "pwsh.exe": true,
	"wscript": true, "wscript.exe": true, "cscript": true, "cscript.exe": true,
	"mshta": true, "mshta.exe": true, "wsl": true, "wsl.exe": true,
	"env": true, "printenv": true, "set": true, "export": true, "declare": true,
	"cmdkey": true, "cmdkey.exe": true, "secret-tool": true,
}

// ValidateAllowedBinary checks one configured binary allowlist rule.
// Rejects "*" or empty entries (fail closed).
func ValidateAllowedBinary(rule string) (string, error) {
	r := strings.TrimSpace(rule)
	if r == "" {
		return "", errors.New("binário na allowlist não pode ser vazio")
	}
	if strings.ContainsAny(r, "*?<>|\r\n\x00") {
		return "", fmt.Errorf("curinga ou caractere inválido na allowlist de binários: %q", rule)
	}
	base := strings.ToLower(filepath.Base(r))
	if forbiddenShellExecutables[base] || forbiddenShellExecutables[strings.TrimSuffix(base, ".exe")] {
		return "", fmt.Errorf("interpretador ou utilitário de ambiente %q não pode entrar na allowlist", rule)
	}
	return r, nil
}

// ValidateExecCommand enforces explicit argv (no sh -c), blocks token-printing
// invocations (e.g. `gh auth token`, `printenv`), blocks sensitive file targets,
// and verifies the binary is explicitly present in allowedBinaries (default empty = deny).
func ValidateExecCommand(argv []string, allowedBinaries []string) error {
	if len(argv) == 0 {
		return errors.New("argv vazio: especifique executável e argumentos como lista")
	}
	exeRaw := strings.TrimSpace(argv[0])
	if exeRaw == "" {
		return errors.New("executável vazio em argv[0]")
	}
	if strings.ContainsAny(exeRaw, "\r\n\x00|&;<>`$()") || strings.Contains(exeRaw, "=") {
		return fmt.Errorf("executável %q contém caractere proibido", exeRaw)
	}

	baseLower := strings.ToLower(filepath.Base(exeRaw))
	stemLower := strings.TrimSuffix(baseLower, ".exe")

	if forbiddenShellExecutables[baseLower] || forbiddenShellExecutables[stemLower] {
		return fmt.Errorf("shell livre ou dump de ambiente recusado (%q): use argv explícito de binário permitido", exeRaw)
	}

	if isTokenDumpingInvocation(stemLower, argv[1:]) {
		return fmt.Errorf("comando que exibe token ou credencial recusado (%s)", exeRaw)
	}

	// Refuse arguments pointing at sensitive credential paths (.env, .git/config, key files).
	for _, a := range argv[1:] {
		if IsSensitivePath(a) || containsTokenPrintFlag(a) {
			return fmt.Errorf("argumento sensível ou de extração de credencial recusado")
		}
	}

	if len(allowedBinaries) == 0 {
		return fmt.Errorf("nenhum binário na allowlist de execução (%q bloqueado): configure allowedBinaries na política", baseLower)
	}

	for _, rawRule := range allowedBinaries {
		rule, err := ValidateAllowedBinary(rawRule)
		if err != nil {
			continue
		}
		if binaryMatchesRule(exeRaw, baseLower, stemLower, rule) {
			return nil
		}
	}
	return fmt.Errorf("binário %q não está na allowlist de execução", exeRaw)
}

func binaryMatchesRule(exeRaw, baseLower, stemLower, rule string) bool {
	if strings.ContainsAny(rule, `/\`) {
		cleanRule := filepath.Clean(rule)
		cleanExe := filepath.Clean(exeRaw)
		return strings.EqualFold(cleanRule, cleanExe)
	}
	rBase := strings.ToLower(rule)
	rStem := strings.TrimSuffix(rBase, ".exe")
	return baseLower == rBase || stemLower == rStem
}

func containsTokenPrintFlag(arg string) bool {
	low := strings.ToLower(strings.TrimSpace(arg))
	for _, needle := range []string{
		"--show-token", "-t=token", "oauth_token", "access_token", "client_secret", "_authtoken",
	} {
		if strings.Contains(low, needle) {
			return true
		}
	}
	return false
}

func isTokenDumpingInvocation(stem string, args []string) bool {
	lowerArgs := make([]string, 0, len(args))
	for _, a := range args {
		s := strings.ToLower(strings.TrimSpace(a))
		if s != "" {
			lowerArgs = append(lowerArgs, s)
		}
	}
	joined := strings.Join(lowerArgs, " ")

	switch stem {
	case "gh":
		// `gh auth token` or `gh config get oauth_token`
		if len(lowerArgs) >= 2 && lowerArgs[0] == "auth" && (lowerArgs[1] == "token" || lowerArgs[1] == "status" && strings.Contains(joined, "--show-token")) {
			return true
		}
		if strings.Contains(joined, "auth token") || strings.Contains(joined, "oauth_token") {
			return true
		}
	case "git":
		if len(lowerArgs) >= 1 && lowerArgs[0] == "credential" {
			return true
		}
		if strings.Contains(joined, "credential.helper") || strings.Contains(joined, "--get-url") && strings.Contains(joined, "@") {
			return true
		}
	case "aws":
		if strings.Contains(joined, "configure get") || strings.Contains(joined, "get-secret-value") || strings.Contains(joined, "get-session-token") {
			return true
		}
	case "gcloud":
		if strings.Contains(joined, "print-access-token") || strings.Contains(joined, "print-identity-token") {
			return true
		}
	case "az":
		if strings.Contains(joined, "get-access-token") {
			return true
		}
	case "kubectl":
		if strings.Contains(joined, "get secret") || strings.Contains(joined, "get secrets") {
			return true
		}
	case "npm", "pnpm", "yarn":
		if strings.Contains(joined, "_authtoken") || strings.Contains(joined, "token list") {
			return true
		}
	case "docker", "podman":
		if strings.Contains(joined, "inspect") && strings.Contains(joined, "env") {
			return true
		}
	case "security", "op", "vault":
		return true
	}
	return false
}

// ParseExplicitArgv extracts strict []string argv from tool args and rejects free shell strings.
func ParseExplicitArgv(args map[string]any) ([]string, error) {
	if args == nil {
		return nil, errors.New("argumentos ausentes para exec_command")
	}
	if _, hasCmdStr := args["command"]; hasCmdStr {
		return nil, errors.New("campo 'command' em string livre é proibido (use argv explícito)")
	}
	if _, hasShellStr := args["shell"]; hasShellStr {
		return nil, errors.New("invocação via shell livre é proibida")
	}
	raw, ok := args["argv"]
	if !ok || raw == nil {
		return nil, errors.New("campo 'argv' explícito é obrigatório")
	}
	var out []string
	switch v := raw.(type) {
	case []string:
		out = append([]string(nil), v...)
	case []any:
		out = make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("argv[%d] deve ser string", i)
			}
			out = append(out, s)
		}
	default:
		return nil, errors.New("argv deve ser lista de strings")
	}
	for i, arg := range out {
		if strings.ContainsRune(arg, '\x00') {
			return nil, fmt.Errorf("argv[%d] contém byte NUL", i)
		}
	}
	return out, nil
}
