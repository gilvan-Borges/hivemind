//go:build windows

package resident

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

// TryAcquireLock creates an exclusive lock file. A live owner keeps the file
// open, so a second process cannot replace it. A dead owner is reclaimed.
func TryAcquireLock(baseDir string) (release func(), ok bool) {
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return nil, false
	}
	path := filepath.Join(baseDir, "lock")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err == nil {
			_, _ = f.WriteString(strconv.Itoa(os.Getpid()))
			return func() {
				f.Close()
				_ = os.Remove(path)
			}, true
		}
		if !os.IsExist(err) {
			return nil, false
		}
		raw, readErr := os.ReadFile(path)
		pid := 0
		if readErr == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		}
		if pid > 0 && pid != os.Getpid() && processAlive(pid) {
			return nil, false
		}
		_ = os.Remove(path)
	}
	return nil, false
}

func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	windows.CloseHandle(h)
	return true
}
