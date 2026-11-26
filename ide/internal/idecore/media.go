package idecore

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/pathsec"
)

const maxLocalMediaBytes = 12 << 20 // 12 MB

func normalizeLocalMediaPath(workspace, rawPath string) (string, error) {
	p := strings.TrimSpace(rawPath)
	if p == "" {
		return "", fmt.Errorf("caminho vazio")
	}
	if idx := strings.Index(p, "#"); idx >= 0 {
		p = p[:idx]
	}
	if strings.HasPrefix(strings.ToLower(p), "file://") {
		u, err := url.Parse(p)
		if err == nil {
			p = u.Path
			if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
				p = p[1:]
			}
			p = filepath.FromSlash(p)
		}
	}
	if !filepath.IsAbs(p) {
		if strings.TrimSpace(workspace) == "" {
			return "", fmt.Errorf("workspace vazio para caminho relativo")
		}
		p = filepath.Join(workspace, p)
	}
	abs, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return "", err
	}
	return abs, nil
}

func isAllowedLocalMediaRoot(workspace, abs string) bool {
	lowAbs := strings.ToLower(abs)
	roots := []string{}
	if strings.TrimSpace(workspace) != "" {
		if w, err := filepath.Abs(filepath.Clean(workspace)); err == nil {
			roots = append(roots, w)
		}
	}
	if tmp := os.TempDir(); tmp != "" {
		if t, err := filepath.Abs(filepath.Clean(tmp)); err == nil {
			roots = append(roots, t)
		}
	}
	if localApp := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localApp != "" {
		roots = append(roots, filepath.Join(localApp, "HiveMind"))
	}
	for _, r := range roots {
		lowRoot := strings.ToLower(filepath.Clean(r))
		if lowAbs == lowRoot || strings.HasPrefix(lowAbs, lowRoot+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ReadLocalMediaDataURI reads a local raster image inside the workspace, OS temp
// directory or HiveMind app data directory and returns it as a data:image/... URI.
func ReadLocalMediaDataURI(workspace, rawPath string) (string, error) {
	abs, err := normalizeLocalMediaPath(workspace, rawPath)
	if err != nil {
		return "", err
	}
	if pathsec.IsSensitivePath(abs) {
		return "", fmt.Errorf("leitura bloqueada para arquivo sensível")
	}
	if !isAllowedLocalMediaRoot(workspace, abs) {
		return "", fmt.Errorf("caminho fora das pastas permitidas")
	}
	ext := strings.ToLower(filepath.Ext(abs))
	var mime string
	switch ext {
	case ".png":
		mime = "image/png"
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	case ".webp":
		mime = "image/webp"
	case ".gif":
		mime = "image/gif"
	default:
		return "", fmt.Errorf("extensão de imagem não suportada: %s", ext)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("caminho é um diretório")
	}
	if info.Size() > maxLocalMediaBytes {
		return "", fmt.Errorf("imagem excede limite de 12 MB")
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// OpenLocalFile opens a non-sensitive workspace/local file (such as a PDF) with the OS default app.
func OpenLocalFile(workspace, rawPath string) error {
	abs, err := normalizeLocalMediaPath(workspace, rawPath)
	if err != nil {
		return err
	}
	if pathsec.IsSensitivePath(abs) {
		return fmt.Errorf("abertura bloqueada para arquivo sensível")
	}
	if !isAllowedLocalMediaRoot(workspace, abs) {
		return fmt.Errorf("caminho fora das pastas permitidas")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("caminho é um diretório")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", abs)
	case "darwin":
		cmd = exec.Command("open", abs)
	default:
		cmd = exec.Command("xdg-open", abs)
	}
	return cmd.Start()
}
