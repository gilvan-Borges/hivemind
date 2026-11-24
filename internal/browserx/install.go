package browserx

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

type hostManifest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

func manifestFor(exePath string) hostManifest {
	return hostManifest{
		Name:           NativeHostName,
		Description:    "Ponte local do HiveMind para a extensão Agent Hive no Chrome.",
		Path:           exePath,
		Type:           "stdio",
		AllowedOrigins: []string{"chrome-extension://" + ExtensionID + "/"},
	}
}

func manifestJSON(exePath string) ([]byte, error) {
	data, err := json.MarshalIndent(manifestFor(exePath), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Install registra o host native messaging deste sistema. O manifesto aponta
// para uma cópia do binário com o nome hivemind-browser-host, que entra no
// modo stdio. O token não vai no manifesto.
func Install(exe string) (string, error) {
	exe = filepath.Clean(exe)
	info, err := os.Stat(exe)
	if err != nil {
		return "", fmt.Errorf("binário do hivemind: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("binário do hivemind é uma pasta")
	}
	dest, err := installHostBinary(exe)
	if err != nil {
		return "", err
	}
	body, err := manifestJSON(dest)
	if err != nil {
		return "", err
	}
	dirs, err := hostManifestDirs()
	if err != nil {
		return "", err
	}
	var wrote []string
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		path := filepath.Join(dir, NativeHostName+".json")
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return "", err
		}
		wrote = append(wrote, path)
	}
	if err := registerManifestExtra(wrote); err != nil {
		return "", err
	}
	return fmt.Sprintf("host %s registrado para a extensão %s (%d manifesto(s))", NativeHostName, ExtensionID, len(wrote)), nil
}

func installHostBinary(exe string) (string, error) {
	cfg, err := configPath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(cfg)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := "hivemind-browser-host"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dest := filepath.Join(dir, name)
	if same, _ := sameFile(exe, dest); same {
		return dest, nil
	}
	_ = os.Remove(dest)
	if err := os.Link(exe, dest); err != nil {
		if err := copyExecutable(exe, dest); err != nil {
			return "", err
		}
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(dest, 0o755)
	}
	return dest, nil
}

func sameFile(a, b string) (bool, error) {
	ia, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(ia, ib), nil
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
