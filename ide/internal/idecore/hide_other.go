//go:build !windows

package idecore

import "os/exec"

func hideWindow(_ *exec.Cmd) {}
