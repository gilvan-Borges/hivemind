//go:build !windows

package browserx

import (
	"os"
	"path/filepath"
	"runtime"
)

func hostManifestDirs() ([]string, error) {
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		base := filepath.Join(home, "Library", "Application Support")
		return []string{
			filepath.Join(base, "Google", "Chrome", "NativeMessagingHosts"),
			filepath.Join(base, "Google", "Chrome for Testing", "NativeMessagingHosts"),
			filepath.Join(base, "Chromium", "NativeMessagingHosts"),
		}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return []string{
		filepath.Join(base, "google-chrome", "NativeMessagingHosts"),
		filepath.Join(base, "google-chrome-for-testing", "NativeMessagingHosts"),
		filepath.Join(base, "chromium", "NativeMessagingHosts"),
	}, nil
}

func registerManifestExtra([]string) error { return nil }
