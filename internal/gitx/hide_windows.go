//go:build windows

package gitx

import (
	"os/exec"
	"syscall"
)

func hideWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
