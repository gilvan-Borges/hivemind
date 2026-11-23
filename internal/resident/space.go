package resident

import (
	"fmt"
	"net/url"
	"runtime"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// ExperimentalUnisolatedWarning is the fixed sentence shown in the IDE and in
// the approval card. The isolated space still does not exist.
const ExperimentalUnisolatedWarning = "SEM isolamento: browser e desktop rodam na sessão real deste computador. Não há espaço separado, o cofre não é consultado e, fora de Full Acesso, cada ação pede confirmação."

// IsolatedBrowserAvailable reports whether a browser can run outside the user
// profile, with filtered egress, inside the OS sandbox. Exec isolation is not
// that space: AppContainer+Job Object and bwrap+seccomp do not give a separate
// desktop or an allowlist for a browser process. Until they do, this stays false.
func IsolatedBrowserAvailable() (bool, string) {
	return false, isolatedSpaceReason("browser")
}

// IsolatedDesktopAvailable reports whether input can be sent to a desktop that
// is not the person's session. The host session is never a target.
func IsolatedDesktopAvailable() (bool, string) {
	return false, isolatedSpaceReason("desktop")
}

func isolatedSpaceReason(kind string) string {
	switch runtime.GOOS {
	case "windows":
		return kind + " isolado exigiria AppContainer sem rede e Job Object fora da sessão interativa; esse espaço não sobe, então a chamada é recusada"
	case "linux":
		return kind + " isolado exigiria bwrap+seccomp com egress filtrado; sem o filtro o espaço não sobe, então a chamada é recusada"
	default:
		return kind + " isolado não existe neste sistema; a chamada é recusada"
	}
}

func isolatedSpaceDecision(name string) Decision {
	kind := "browser"
	if name == "desktop_click" || name == "desktop_type" {
		kind = "desktop"
	}
	return Decision{Allow: false, Ask: false, Reason: isolatedSpaceReason(kind)}
}

func isHostSurfaceTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "browser_open", "desktop_click", "desktop_type":
		return true
	default:
		return false
	}
}

// decideExperimentalSurface is the only path that lets browser and desktop run.
// The flag is off unless the caller already checked ExperimentalUnisolated.
// Full Acesso skips the card; every other mode asks. Vault arguments never pass.
func decideExperimentalSurface(call Call, fullAccess bool) Decision {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if !isHostSurfaceTool(name) {
		return Decision{Allow: false, Ask: false, Reason: "ferramenta fora do modo experimental"}
	}
	switch call.Origin {
	case OriginLocal, OriginRemote, OriginRoutine, OriginLoop, OriginSubagent:
	default:
		return Decision{Allow: false, Ask: false, Reason: fmt.Sprintf("origem desconhecida ou inválida (%q): política falha fechado", call.Origin)}
	}
	if surfaceTouchesVault(call.Args) {
		return Decision{Allow: false, Ask: false, Reason: "browser e desktop experimentais não acessam o cofre nem segredos"}
	}
	if err := validateSurfaceArgs(name, call.Args); err != nil {
		return Decision{Allow: false, Ask: false, Reason: err.Error()}
	}
	tier, _ := ClassifyTool(call)
	if call.Origin == OriginSubagent && call.ParentTier != "" && tierRank(tier) > tierRank(call.ParentTier) {
		return Decision{Allow: false, Ask: false, Reason: fmt.Sprintf("subagente não pode ultrapassar teto do pai (%s > %s)", tier, call.ParentTier)}
	}
	if len(call.AllowedTiers) > 0 && !tierAllowed(call.AllowedTiers, tier) {
		return Decision{Allow: false, Ask: false, Reason: fmt.Sprintf("classe %s fora do manifesto desta rotina ou subagente", tier)}
	}
	reason := ExperimentalUnisolatedWarning
	if fullAccess {
		return Decision{Allow: true, Ask: false, Reason: reason}
	}
	return Decision{Allow: false, Ask: true, Reason: reason}
}

func surfaceTouchesVault(args map[string]any) bool {
	if HasSensitiveArg(args) {
		return true
	}
	for _, key := range []string{"credential_name", "secret_name", "secret", "token", "password", "api_key", "apikey", "bearer", "vault"} {
		if strings.TrimSpace(strArg(args, key)) != "" {
			return true
		}
	}
	if raw := strArg(args, "url"); raw != "" {
		if u, err := url.Parse(raw); err == nil && u.User != nil {
			return true
		}
	}
	return false
}

func validateSurfaceArgs(name string, args map[string]any) error {
	switch name {
	case "browser_open":
		_, err := validatePublicURL(strArg(args, "url"))
		return err
	case "desktop_click":
		if args == nil || !hasArg(args, "x") || !hasArg(args, "y") {
			return fmt.Errorf("desktop_click exige coordenadas x e y")
		}
		return validateClick(intArg(args, "x"), intArg(args, "y"))
	case "desktop_type":
		return validateTypeText(strArg(args, "text"))
	default:
		return fmt.Errorf("ferramenta de superfície desconhecida")
	}
}

func hasArg(args map[string]any, key string) bool {
	if args == nil {
		return false
	}
	_, ok := args[key]
	return ok
}

func validatePublicURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return "", fmt.Errorf("url recusada")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("url recusada: só http e https")
	}
	if u.Host == "" || u.User != nil {
		return "", fmt.Errorf("url recusada")
	}
	return u.String(), nil
}

func validateClick(x, y int) error {
	if x < 0 || y < 0 || x > 100000 || y > 100000 {
		return fmt.Errorf("coordenada fora da tela")
	}
	return nil
}

func validateTypeText(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("texto vazio")
	}
	if strings.ContainsRune(text, 0) {
		return fmt.Errorf("texto recusado")
	}
	if len([]rune(text)) > 2000 {
		return fmt.Errorf("texto acima de 2000 caracteres")
	}
	return nil
}

func isFullAccessMode(mode string) bool {
	return team.NormalizePermissionMode(mode) == team.PermissionFullAccess
}
