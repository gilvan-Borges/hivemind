//go:build !windows

package githubx

import "os/exec"

func hideWindow(_ *exec.Cmd) {}
