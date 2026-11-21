//go:build !windows

package fslockx

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

var errLockTimeout = errors.New("fslockx: timeout acquiring file lock")

func osIsTimeout(err error) bool { return errors.Is(err, errLockTimeout) }

func lockFile(f *os.File, timeout time.Duration) error {
	how := unix.LOCK_EX
	if timeout > 0 {
		how |= unix.LOCK_NB
		deadline := time.Now().Add(timeout)
		for {
			if err := unix.Flock(int(f.Fd()), how); err == nil {
				return nil
			}
			if time.Now().After(deadline) {
				return errLockTimeout
			}
			time.Sleep(15 * time.Millisecond)
		}
	}
	return unix.Flock(int(f.Fd()), how)
}

func unlockFile(f *os.File) {
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
