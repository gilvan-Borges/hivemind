//go:build linux || darwin

// Package procx configures subprocess window visibility and process-tree cancellation.
package procx

import (
	"os/exec"
	"syscall"
	"time"
)

// HideWindow is a no-op on Unix platforms.
func HideWindow(_ *exec.Cmd) {}

// ConfigureContext places the child in its own process group so context cancellation
// terminates the entire process group (cmd must have been created with exec.CommandContext).
func ConfigureContext(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil || cmd.Process.Pid <= 0 {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = 3 * time.Second
}
