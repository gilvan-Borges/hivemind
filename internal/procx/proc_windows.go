//go:build windows

// Package procx configures subprocess window visibility and process-tree cancellation.
package procx

import (
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

const createNoWindow = 0x08000000

// HideWindow prevents Windows from flashing a console window for a child process.
func HideWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}

// ConfigureContext hides console windows and ensures context cancellation terminates
// the entire child process tree (cmd must have been created with exec.CommandContext).
func ConfigureContext(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	HideWindow(cmd)
	cmd.Cancel = func() error {
		if cmd.Process == nil || cmd.Process.Pid <= 0 {
			return nil
		}
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		HideWindow(kill)
		if err := kill.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = 3 * time.Second
}
