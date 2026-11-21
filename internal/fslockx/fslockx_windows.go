//go:build windows

package fslockx

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

var errLockTimeout = errors.New("fslockx: timeout acquiring file lock")

func osIsTimeout(err error) bool { return errors.Is(err, errLockTimeout) }

func lockFile(f *os.File, timeout time.Duration) error {
	ol := new(windows.Overlapped)
	// Non-blocking first attempt, then poll until the deadline.
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol); err == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol); err == nil {
			return nil
		}
		if timeout == 0 || time.Now().After(deadline) {
			return errLockTimeout
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func unlockFile(f *os.File) {
	ol := new(windows.Overlapped)
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
}
