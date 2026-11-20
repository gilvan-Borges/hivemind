package resident

import "runtime"

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
