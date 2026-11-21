package resident

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/secretx"
)

const policyFileName = "policy.json"

// DefaultPolicy returns the fail-closed Phase 2 policy (empty exec & network allowlists).
func DefaultPolicy() Policy {
	return Policy{
		Version:         2,
		AllowedBinaries: nil,
		AllowedHosts:    nil,
	}
}

// PolicyPath resolves %LOCALAPPDATA%\HiveMind\resident\policy.json (host-only, never inside a repo).
func PolicyPath(baseDir string) (string, error) {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = DefaultBaseDir()
	}
	return filepath.Join(baseDir, policyFileName), nil
}

// LoadPolicy loads host-only Phase 2 security allowlists, failing closed to empty allowlists if missing.
func LoadPolicy(baseDir string) (Policy, error) {
	path, err := PolicyPath(baseDir)
	if err != nil {
		return DefaultPolicy(), err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultPolicy(), nil
		}
		return DefaultPolicy(), fmt.Errorf("ler %s: %w", path, err)
	}
	pol := DefaultPolicy()
	if err := json.Unmarshal(data, &pol); err != nil {
		return DefaultPolicy(), fmt.Errorf("decodificar %s: %w", path, err)
	}
	if err := ValidatePolicy(pol); err != nil {
		return DefaultPolicy(), err
	}
	return pol, nil
}

// ValidatePolicy rejects wildcards ("*") and shell wrappers in Phase 2 allowlists.
func ValidatePolicy(pol Policy) error {
	for _, b := range pol.AllowedBinaries {
		if _, err := ValidateAllowedBinary(b); err != nil {
			return err
		}
	}
	for _, h := range pol.AllowedHosts {
		if _, err := ValidateAllowedHost(h); err != nil {
			return err
		}
	}
	return nil
}

// SavePolicy validates and atomically writes policy.json with mode 0600 outside any workspace repo.
func SavePolicy(baseDir string, pol Policy) error {
	if pol.Version <= 0 {
		pol.Version = 2
	}
	cleanBins := make([]string, 0, len(pol.AllowedBinaries))
	seenBins := map[string]struct{}{}
	for _, raw := range pol.AllowedBinaries {
		v, err := ValidateAllowedBinary(raw)
		if err != nil {
			return err
		}
		key := strings.ToLower(v)
		if _, dup := seenBins[key]; !dup {
			seenBins[key] = struct{}{}
			cleanBins = append(cleanBins, v)
		}
	}
	cleanHosts := make([]string, 0, len(pol.AllowedHosts))
	seenHosts := map[string]struct{}{}
	for _, raw := range pol.AllowedHosts {
		v, err := ValidateAllowedHost(raw)
		if err != nil {
			return err
		}
		if _, dup := seenHosts[v]; !dup {
			seenHosts[v] = struct{}{}
			cleanHosts = append(cleanHosts, v)
		}
	}
	pol.AllowedBinaries = cleanBins
	pol.AllowedHosts = cleanHosts

	path, err := PolicyPath(baseDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(pol, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var phase2AllowedTools = map[string]Tier{
	"read_file":             TierRead,
	"list_dir":              TierRead,
	"search_text":           TierRead,
	"find_files":            TierRead,
	"list_projects":         TierRead,
	"memory_search":         TierRead,
	"write_file":            TierWrite,
	"edit_file":             TierWrite,
	"memory_write":          TierWrite,
	"memory_forget":         TierWrite,
	"exec_command":          TierExec,
	"net_fetch":             TierNetwork,
	"use_credential":        TierCredentials,
	"delete_file":           TierDestructive,
	"cron_add":              TierWrite,
	"spawn_subagent":        TierExec,
	"delegate_to_hive_team": TierDestructive,
}

// knownDisallowedToolTiers classifies Phase 3/4/shell tools that MUST fail closed in Phase 2.
var knownDisallowedToolTiers = map[string]Tier{
	"write_doc":         TierWrite,
	"run_shell":         TierExec,
	"exec":              TierExec,
	"shell":             TierExec,
	"http_get":          TierNetwork,
	"http_request":      TierNetwork,
	"web_fetch":         TierNetwork,
	"mcp_enable_server": TierNetwork,
	"read_secret":       TierCredentials,
	"get_credential":    TierCredentials,
	"git_commit":        TierWrite,
	"git_push":          TierDestructive,
	"git_reset":         TierDestructive,
	"desktop_click":     TierDestructive,
	"desktop_type":      TierDestructive,
	"browser_open":      TierNetwork,
}

// ClassifyTool returns the privilege tier for a call and whether it belongs to the
// Phase 2 resident tool allowlist. Sensitive paths/arguments automatically elevate
// to TierCredentials; secret-bearing net_fetch elevates to TierCredentials.
func ClassifyTool(call Call) (tier Tier, allowed bool) {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if name == "" {
		return TierDestructive, false
	}
	cleanArgs := SanitizeArgs(call.Args)

	if t, ok := phase2AllowedTools[name]; ok {
		if p, ok := cleanArgs["path"].(string); ok && IsSensitivePath(p) {
			return TierCredentials, false
		}
		if HasSensitiveArg(cleanArgs) {
			return TierCredentials, false
		}
		if name == "net_fetch" && firstNonEmptyStr(strArg(cleanArgs, "credential_name"), strArg(cleanArgs, "secret_name")) != "" {
			return TierCredentials, true
		}
		return t, true
	}

	if t, ok := knownDisallowedToolTiers[name]; ok {
		return t, false
	}
	return TierDestructive, false
}

func tierRank(t Tier) int {
	switch t {
	case TierRead:
		return 1
	case TierWrite:
		return 2
	case TierExec:
		return 3
	case TierNetwork:
		return 4
	case TierCredentials:
		return 5
	case TierDestructive:
		return 6
	default:
		return 99
	}
}

// RiskLabel returns the Portuguese risk level label shown on approval cards (§11, §12.4).
func RiskLabel(t Tier) string {
	switch t {
	case TierRead:
		return "baixo (leitura)"
	case TierWrite:
		return "médio (escrita)"
	case TierExec:
		return "alto (execução em sandbox)"
	case TierNetwork:
		return "alto (rede de saída)"
	case TierCredentials:
		return "crítico (credencial do cofre)"
	case TierDestructive:
		return "crítico (destrutivo)"
	default:
		return "crítico (desconhecido)"
	}
}

// Decide evaluates policy using an empty Phase 2 Policy (exec & network fail closed).
func Decide(call Call) Decision {
	return DecideWithPolicy(call, Policy{})
}

// DecideWithPolicy evaluates Phase 2 permission tiers purely without OS side effects (§4, §12.4):
//   - Unknown / shell / Phase 3+ tools and unknown origins always fail closed (Allow=false, Ask=false).
//   - Sensitive paths (.env, .git, keys) always fail closed.
//   - Read tools: OriginLocal allowed (audited); OriginRemote/OriginRoutine/OriginSubagent require approval.
//   - Write, Exec (validated argv + binary allowlist), Network (validated host allowlist), Credentials,
//     and Destructive tools ALWAYS require explicit human approval (Ask=true, timeout denies).
func DecideWithPolicy(call Call, pol Policy) Decision {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if name == "" {
		return Decision{Allow: false, Ask: false, Reason: "nome de ferramenta vazio"}
	}

	switch call.Origin {
	case OriginLocal, OriginRemote, OriginRoutine, OriginSubagent, OriginWebhook, OriginSlack:
	default:
		return Decision{
			Allow:  false,
			Ask:    false,
			Reason: fmt.Sprintf("origem desconhecida ou inválida (%q): política falha fechado", call.Origin),
		}
	}

	cleanArgs := SanitizeArgs(call.Args)
	switch name {
	case "browser_open", "desktop_click", "desktop_type":
		// Profile paths and coordinates from the model are ignored. There is no
		// second path that runs on the host session.
		return isolatedSpaceDecision(name)
	}
	tier, inPhase2 := ClassifyTool(Call{
		Name:       name,
		Args:       cleanArgs,
		Origin:     call.Origin,
		Workstream: call.Workstream,
		ParentTier: call.ParentTier,
	})

	if call.Origin == OriginSubagent && call.ParentTier != "" {
		if tierRank(tier) > tierRank(call.ParentTier) {
			return Decision{
				Allow:  false,
				Ask:    false,
				Reason: fmt.Sprintf("subagente não pode ultrapassar teto do pai (%s > %s)", tier, call.ParentTier),
			}
		}
	}
	if len(call.AllowedTiers) > 0 && !tierAllowed(call.AllowedTiers, tier) {
		return Decision{
			Allow:  false,
			Ask:    false,
			Reason: fmt.Sprintf("classe %s fora do manifesto desta rotina ou subagente", tier),
		}
	}

	if !inPhase2 {
		if tier == TierCredentials {
			return Decision{
				Allow:  false,
				Ask:    false,
				Reason: fmt.Sprintf("acesso a segredo ou caminho sensível recusado (%s)", name),
			}
		}
		return Decision{
			Allow:  false,
			Ask:    false,
			Reason: fmt.Sprintf("ferramenta %q (classe %s) recusada pela política", name, tier),
		}
	}

	switch name {
	case "exec_command":
		if _, hasCmdStr := cleanArgs["command"]; hasCmdStr {
			return Decision{Allow: false, Ask: false, Reason: "campo command em string livre é proibido (use argv explícito)"}
		}
		argv := strSliceArg(cleanArgs, "argv")
		if err := ValidateExecCommand(argv, pol.AllowedBinaries); err != nil {
			return Decision{Allow: false, Ask: false, Reason: err.Error()}
		}
		return Decision{
			Allow:  false,
			Ask:    true,
			Reason: fmt.Sprintf("execução em sandbox (origem %s) exige aprovação humana", call.Origin),
		}

	case "net_fetch":
		rawURL := strArg(cleanArgs, "url")
		if _, err := ValidateOutboundURL(rawURL, pol.AllowedHosts); err != nil {
			return Decision{Allow: false, Ask: false, Reason: err.Error()}
		}
		secName := firstNonEmptyStr(strArg(cleanArgs, "credential_name"), strArg(cleanArgs, "secret_name"))
		if secName != "" {
			if err := secretx.ValidateName(secName); err != nil {
				return Decision{Allow: false, Ask: false, Reason: err.Error()}
			}
			return Decision{
				Allow:  false,
				Ask:    true,
				Reason: "requisição autenticada com segredo do cofre exige aprovação humana",
			}
		}
		return Decision{
			Allow:  false,
			Ask:    true,
			Reason: fmt.Sprintf("requisição de rede na allowlist (origem %s) exige aprovação humana", call.Origin),
		}

	case "use_credential":
		secName := firstNonEmptyStr(strArg(cleanArgs, "credential_name"), strArg(cleanArgs, "secret_name"))
		if err := secretx.ValidateName(secName); err != nil {
			return Decision{Allow: false, Ask: false, Reason: err.Error()}
		}
		return Decision{
			Allow:  false,
			Ask:    true,
			Reason: "uso de credencial do cofre exige aprovação humana",
		}

	case "cron_add":
		if call.Origin == OriginSubagent {
			return Decision{Allow: false, Ask: false, Reason: "subagente não cria rotina"}
		}
		if cronLooksLikeShell(cleanArgs) {
			return Decision{Allow: false, Ask: false, Reason: "rotina não aceita comando de shell"}
		}
		if _, err := parseCron(strArg(cleanArgs, "expr")); err != nil {
			return Decision{Allow: false, Ask: false, Reason: err.Error()}
		}
		if strings.TrimSpace(strArg(cleanArgs, "text")) == "" {
			return Decision{Allow: false, Ask: false, Reason: "texto da rotina vazio"}
		}
		if _, err := parseClassList(strSliceArg(cleanArgs, "classes")); err != nil {
			return Decision{Allow: false, Ask: false, Reason: err.Error()}
		}
		return Decision{Allow: false, Ask: true, Reason: "gravar rotina exige aprovação humana"}

	case "spawn_subagent":
		if call.Origin == OriginSubagent {
			return Decision{Allow: false, Ask: false, Reason: "subagente não pode criar outro subagente"}
		}
		if strings.TrimSpace(strArg(cleanArgs, "objective")) == "" {
			return Decision{Allow: false, Ask: false, Reason: "objetivo do subagente vazio"}
		}
		return Decision{Allow: false, Ask: true, Reason: "criar subagente exige aprovação humana"}

	case "delegate_to_hive_team":
		if call.Origin == OriginSubagent {
			return Decision{Allow: false, Ask: false, Reason: "subagente não entrega trabalho ao time"}
		}
		if strings.TrimSpace(strArg(cleanArgs, "objective")) == "" {
			return Decision{Allow: false, Ask: false, Reason: "objetivo do handoff vazio"}
		}
		return Decision{Allow: false, Ask: true, Reason: "handoff para o time exige aprovação humana"}
	}

	switch tier {
	case TierRead:
		if call.Origin == OriginLocal {
			return Decision{Allow: true, Ask: false, Reason: "leitura local dentro da raiz permitida"}
		}
		return Decision{Allow: false, Ask: true, Reason: fmt.Sprintf("leitura com origem %s exige aprovação humana", call.Origin)}

	case TierWrite:
		if p := strArg(cleanArgs, "path"); p != "" && IsSensitivePath(p) {
			return Decision{Allow: false, Ask: false, Reason: "escrita em caminho sensível recusada"}
		}
		return Decision{Allow: false, Ask: true, Reason: fmt.Sprintf("escrita (origem %s) exige aprovação humana", call.Origin)}

	case TierDestructive:
		if p := strArg(cleanArgs, "path"); p != "" && IsSensitivePath(p) {
			return Decision{Allow: false, Ask: false, Reason: "remoção em caminho sensível recusada"}
		}
		return Decision{Allow: false, Ask: true, Reason: "ação destrutiva sempre exige aprovação humana"}

	default:
		return Decision{Allow: false, Ask: false, Reason: errors.New("política falha fechado").Error()}
	}
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func strSliceArg(args map[string]any, key string) []string {
	if args == nil {
		return nil
	}
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	switch s := v.(type) {
	case []string:
		return append([]string(nil), s...)
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return nil
	}
}
