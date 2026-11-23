package loopx

// ApplyPermission reports whether this iteration may run in Full.
// A loop never escalates: ask stays governed, and an unattended Full run
// needs the local IDE switch. The spec cannot turn that switch on.
func ApplyPermission(mode string, projectFull, accompanied, allowUnattended bool) bool {
	switch mode {
	case "", "inherit":
	default:
		return false
	}
	return EffectiveFull(projectFull, accompanied, allowUnattended)
}

// EffectiveFull is the unattended-Full rule.
// Accompanied work follows the project. Cron and events do not, unless the
// IDE settings switch allow_full_unattended is on.
func EffectiveFull(projectFull, accompanied, allowUnattended bool) bool {
	if !projectFull {
		return false
	}
	if accompanied {
		return true
	}
	return allowUnattended
}
