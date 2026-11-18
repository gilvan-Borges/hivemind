//go:build !windows

package modelx

import "os/exec"

func hideWindow(_ *exec.Cmd) {}
