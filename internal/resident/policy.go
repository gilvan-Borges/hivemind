package resident

import (
	"fmt"
	"strings"
)

// phase1ReadTools is the exact allowlist of tools permitted in Phase 1.
// Anything outside this map fails closed (§12.4, §14 Fase 1).
var phase1ReadTools = map[string]Tier{
	"read_file":     TierRead,
	"list_dir":      TierRead,
	"search_text":   TierRead,
	"find_files":    TierRead,
	"list_projects": TierRead,
	"memory_search": TierRead,
}

// knownNonReadToolTiers classifies known higher-privilege tool names so denial
// audit logs record an accurate privilege tier instead of hiding intent.
var knownNonReadToolTiers = map[string]Tier{
	"write_file":            TierWrite,
	"edit_file":             TierWrite,
	"write_doc":             TierWrite,
	"memory_write":          TierWrite,
	"memory_forget":         TierWrite,
	"run_shell":             TierExec,
	"exec":                  TierExec,
	"shell":                 TierExec,
	"spawn_subagent":        TierExec,
	"cron_add":              TierWrite,
	"http_get":              TierNetwork,
	"http_request":          TierNetwork,
	"web_fetch":             TierNetwork,
	"mcp_enable_server":     TierNetwork,
	"read_secret":           TierCredentials,
	"get_credential":        TierCredentials,
	"git_commit":            TierWrite,
	"git_push":              TierDestructive,
	"git_reset":             TierDestructive,
	"delete_file":           TierDestructive,
	"delegate_to_hive_team": TierDestructive,
	"desktop_click":         TierDestructive,
	"browser_open":          TierNetwork,
}

// ClassifyTool returns the privilege tier for a call and whether the tool is
// recognized in the Phase 1 read-only allowlist.
// Sensitive paths/arguments automatically elevate a read call to TierCredentials
// so it is refused under fail-closed Phase 1 rules (§12.4).
func ClassifyTool(call Call) (tier Tier, allowedInPhase1 bool) {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if name == "" {
		return TierDestructive, false
	}
	cleanArgs := SanitizeArgs(call.Args)

	if t, ok := phase1ReadTools[name]; ok {
		if p, ok := cleanArgs["path"].(string); ok && IsSensitivePath(p) {
			return TierCredentials, false
		}
		if HasSensitiveArg(cleanArgs) {
			return TierCredentials, false
		}
		return t, true
	}

	if t, ok := knownNonReadToolTiers[name]; ok {
		return t, false
	}
	// Unknown tools always fail closed with highest risk classification.
	return TierDestructive, false
}

// tierRank defines privilege ordering for subagent ParentTier caps (§4, §8).
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

// Decide evaluates policy purely without OS side effects (§4, §12.4).
// Phase 1 invariant: fail closed on any non-read tool, unknown tool, sensitive
// credential target, or unknown origin.
//
// For allowed Phase 1 read tools:
//   - OriginLocal -> Allow=true, Ask=false (audited)
//   - OriginRemote / OriginRoutine / OriginSubagent -> Allow=false, Ask=true
//     (requires explicit human approval; timeout or nil approver denies).
func Decide(call Call) Decision {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if name == "" {
		return Decision{Allow: false, Ask: false, Reason: "nome de ferramenta vazio"}
	}

	// Validate origin strictly (never trust missing/unknown origin).
	switch call.Origin {
	case OriginLocal, OriginRemote, OriginRoutine, OriginSubagent:
	default:
		return Decision{
			Allow:  false,
			Ask:    false,
			Reason: fmt.Sprintf("origem desconhecida ou inválida (%q): política falha fechado", call.Origin),
		}
	}

	tier, inPhase1 := ClassifyTool(call)

	// Subagent cannot exceed ParentTier (§4, §8).
	if call.Origin == OriginSubagent && call.ParentTier != "" {
		if tierRank(tier) > tierRank(call.ParentTier) {
			return Decision{
				Allow:  false,
				Ask:    false,
				Reason: fmt.Sprintf("subagente não pode ultrapassar teto do pai (%s > %s)", tier, call.ParentTier),
			}
		}
	}

	// Phase 1 strictly forbids everything outside the read-only allowlist.
	if !inPhase1 || tier != TierRead {
		if tier == TierCredentials {
			return Decision{
				Allow:  false,
				Ask:    false,
				Reason: fmt.Sprintf("acesso a segredo ou caminho sensível recusado na fase 1 (%s)", name),
			}
		}
		return Decision{
			Allow:  false,
			Ask:    false,
			Reason: fmt.Sprintf("ferramenta %q (classe %s) recusada: fase 1 do agente residente é somente leitura", name, tier),
		}
	}

	// Phase 1 read tool: local follows §12.4 (allowed, audited); remote/routine/subagent require human approval.
	switch call.Origin {
	case OriginLocal:
		return Decision{Allow: true, Ask: false, Reason: "leitura local dentro da raiz permitida na fase 1"}
	case OriginRemote:
		return Decision{Allow: false, Ask: true, Reason: "leitura com origem remota exige aprovação humana"}
	case OriginRoutine:
		return Decision{Allow: false, Ask: true, Reason: "leitura com origem de rotina exige aprovação humana"}
	case OriginSubagent:
		return Decision{Allow: false, Ask: true, Reason: "leitura por subagente exige aprovação humana"}
	default:
		return Decision{Allow: false, Ask: false, Reason: "política falha fechado"}
	}
}
