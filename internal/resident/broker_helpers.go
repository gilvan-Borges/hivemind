package resident

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/sandboxx"
	"github.com/gilvan-Borges/HiveMind/internal/secretx"
)

func extractTarget(toolName string, args map[string]any) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read_file", "list_dir", "write_file", "edit_file", "delete_file":
		if p := strArg(args, "path"); p != "" {
			return p
		}
		return "."
	case "search_text":
		q := strArg(args, "query")
		if p := strArg(args, "path"); p != "" {
			return p + " :: " + q
		}
		return q
	case "find_files":
		return strArg(args, "pattern")
	case "memory_search":
		return strArg(args, "query")
	case "memory_write", "memory_forget":
		return firstNonEmptyStr(strArg(args, "key"), strArg(args, "id"), strArg(args, "text"))
	case "list_projects":
		return "workspaces"
	case "exec_command":
		if argv := strSliceArg(args, "argv"); len(argv) > 0 {
			return strings.Join(argv, " ")
		}
		return "exec_command"
	case "net_fetch":
		if u := strArg(args, "url"); u != "" {
			if cred := firstNonEmptyStr(strArg(args, "credential_name"), strArg(args, "secret_name")); cred != "" {
				return fmt.Sprintf("%s (credencial=%s)", u, cred)
			}
			return u
		}
		return "net_fetch"
	case "browser_open":
		if u := strArg(args, "url"); u != "" {
			return u
		}
		return "browser_open"
	case "desktop_click":
		return fmt.Sprintf("click %d,%d", intArg(args, "x"), intArg(args, "y"))
	case "desktop_type":
		return strArg(args, "text")
	default:
		if p := strArg(args, "path"); p != "" {
			return p
		}
		if c := strArg(args, "command"); c != "" {
			return c
		}
		if u := strArg(args, "url"); u != "" {
			return u
		}
		return toolName
	}
}

func classifyError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, sandboxx.ErrSandboxUnavailable) {
		return "sandbox_unavailable"
	}
	if errors.Is(err, secretx.ErrVaultUnavailable) || errors.Is(err, secretx.ErrSecretNotFound) {
		return "vault_error"
	}
	low := strings.ToLower(err.Error())
	switch {
	case strings.Contains(low, "fora do workspace"):
		return "path_escape"
	case strings.Contains(low, ".git"), strings.Contains(low, ".hivemind"), strings.Contains(low, "sensível"):
		return "forbidden_path"
	case strings.Contains(low, "not exist"), strings.Contains(low, "no such file"), strings.Contains(low, "cannot find"):
		return "not_found"
	case strings.Contains(low, "context canceled"), strings.Contains(low, "deadline exceeded"), strings.Contains(low, "tempo esgotado"):
		return "canceled"
	case strings.Contains(low, "ssrf"), strings.Contains(low, "allowlist"):
		return "policy_denied"
	default:
		return "io_error"
	}
}

func strArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func intArg(args map[string]any, key string) int {
	if args == nil {
		return 0
	}
	v, ok := args[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	default:
		return 0
	}
}

func boolArg(args map[string]any, key string) bool {
	if args == nil {
		return false
	}
	v, ok := args[key]
	if !ok || v == nil {
		return false
	}
	b, ok := v.(bool)
	if ok {
		return b
	}
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(v)), "true")
}
