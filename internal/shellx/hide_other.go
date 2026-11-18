//go:build !windows

package shellx

import "os/exec"

func hideWindow(_ *exec.Cmd) {}
