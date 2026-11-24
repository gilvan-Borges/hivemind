//go:build windows

package browserx

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

func hostManifestDirs() ([]string, error) {
	base, err := configPath()
	if err != nil {
		return nil, err
	}
	return []string{filepath.Dir(base)}, nil
}

func registerManifestExtra(manifests []string) error {
	if len(manifests) == 0 {
		return nil
	}
	// Um arquivo, duas chaves: Chrome e Chromium leem o caminho no registro.
	path := manifests[0]
	for _, key := range []string{
		`Software\Google\Chrome\NativeMessagingHosts\` + NativeHostName,
		`Software\Chromium\NativeMessagingHosts\` + NativeHostName,
	} {
		k, _, err := registry.CreateKey(registry.CURRENT_USER, key, registry.SET_VALUE)
		if err != nil {
			return err
		}
		err = k.SetStringValue("", path)
		_ = k.Close()
		if err != nil {
			return err
		}
	}
	_ = os.Chmod(path, 0o644)
	return nil
}
