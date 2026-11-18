//go:build !windows && !linux && !darwin

// Package procx configures subprocess window visibility and process-tree cancellation.
package procx

import "os/exec"

// HideWindow is a no-op on non-Windows platforms.
func HideWindow(_ *exec.Cmd) {}

// ConfigureContext is a no-op fallback on unsupported platforms.
func ConfigureContext(_ *exec.Cmd) {}
