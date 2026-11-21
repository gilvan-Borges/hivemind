//go:build windows

package resident

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

// TryAcquireLock acquires both a Windows named kernel mutex scoped to baseDir
// and an exclusive LockFileEx byte-range lock on <baseDir>/lock. Both are
// automatically reclaimed by the kernel if the owning process terminates.
func TryAcquireLock(baseDir string) (release func(), ok bool) {
	absDir, err := filepath.Abs(strings.TrimSpace(baseDir))
	if err != nil {
		return nil, false
	}
	if err := os.MkdirAll(absDir, 0o700); err != nil {
		return nil, false
	}

	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(absDir))))
	mutexName := "Local\\HiveMind.Residente." + hex.EncodeToString(sum[:8])
	namePtr, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return nil, false
	}
	mtx, err := windows.CreateMutex(nil, false, namePtr)
	if err != nil {
		if mtx != 0 {
			_ = windows.CloseHandle(mtx)
		}
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return nil, false
		}
		return nil, false
	}

	path := filepath.Join(absDir, "lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		_ = windows.CloseHandle(mtx)
		return nil, false
	}

	var ol windows.Overlapped
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &ol); err != nil {
		_ = f.Close()
		_ = windows.CloseHandle(mtx)
		return nil, false
	}

	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	_, _ = f.WriteString(strconv.Itoa(os.Getpid()))
	_ = f.Sync()

	return func() {
		var relOL windows.Overlapped
		_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &relOL)
		_ = f.Close()
		_ = windows.CloseHandle(mtx)
		_ = os.Remove(path)
	}, true
}
