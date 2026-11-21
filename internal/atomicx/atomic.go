// Package atomicx provides crash-safe atomic file writes with fsync and temporary file replacement.
package atomicx

import (
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// WriteFile writes data to path atomically via a temporary file in the same
// directory, flushes to disk with Sync, sets perm, and renames into place.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if perm != 0 {
		_ = os.Chmod(tmp, perm)
	}
	var lastErr error
	attempts := 1
	if runtime.GOOS == "windows" {
		attempts = 3
	}
	for i := 0; i < attempts; i++ {
		if err := os.Rename(tmp, path); err == nil {
			cleanup = false
			return nil
		} else {
			lastErr = err
		}
		if i+1 < attempts {
			time.Sleep(5 * time.Millisecond)
		}
	}
	return lastErr
}
