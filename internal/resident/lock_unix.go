//go:build unix

package resident

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// TryAcquireLock takes an exclusive advisory lock so a second process does
// not run the same routine. Failure is closed: the caller does not arm.
func TryAcquireLock(baseDir string) (release func(), ok bool) {
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return nil, false
	}
	f, err := os.OpenFile(filepath.Join(baseDir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false
	}
	_ = f.Truncate(0)
	_, _ = f.WriteString(strconv.Itoa(os.Getpid()))
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, true
}
