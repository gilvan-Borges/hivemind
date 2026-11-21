package resident

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// MappedFolder defines a host directory exposed inside a Windows Sandbox (.wsb).
type MappedFolder struct {
	HostFolder    string
	SandboxFolder string
	ReadOnly      *bool
}

// WSBConfig configures an ephemeral Windows Sandbox (.wsb) session for isolated
// browser/desktop execution (SPEC_AGENTE_RESIDENTE_PROPOSTA / W4-09).
type WSBConfig struct {
	MappedFolders        []MappedFolder
	Networking           string // "Disable" (default) or "Enable"
	LogonCommand         string
	MemoryInMB           int
	VGpu                 string
	AudioInput           string
	ClipboardRedirection string
}

// BuildWSBXML renders a safe XML configuration for Windows Sandbox.
// Networking defaults to Disable and mapped folders default to ReadOnly=true.
// Vault and resident credential directories are always rejected.
func BuildWSBXML(cfg WSBConfig) (string, error) {
	networking := strings.TrimSpace(cfg.Networking)
	if networking == "" {
		networking = "Disable"
	} else if !strings.EqualFold(networking, "Disable") && !strings.EqualFold(networking, "Enable") {
		return "", fmt.Errorf("valor Networking inválido no .wsb: %q", networking)
	}
	vgpu := strings.TrimSpace(cfg.VGpu)
	if vgpu == "" {
		vgpu = "Default"
	}
	audio := strings.TrimSpace(cfg.AudioInput)
	if audio == "" {
		audio = "Disable"
	}
	clipboard := strings.TrimSpace(cfg.ClipboardRedirection)
	if clipboard == "" {
		clipboard = "Disable"
	}

	var b strings.Builder
	b.WriteString("<Configuration>\n")
	if err := writeXMLTag(&b, "VGpu", vgpu, "  "); err != nil {
		return "", err
	}
	if err := writeXMLTag(&b, "Networking", normalizeWSBToggle(networking), "  "); err != nil {
		return "", err
	}
	if err := writeXMLTag(&b, "AudioInput", audio, "  "); err != nil {
		return "", err
	}
	if err := writeXMLTag(&b, "ClipboardRedirection", clipboard, "  "); err != nil {
		return "", err
	}
	if cfg.MemoryInMB > 0 {
		if err := writeXMLTag(&b, "MemoryInMB", fmt.Sprintf("%d", cfg.MemoryInMB), "  "); err != nil {
			return "", err
		}
	}
	if len(cfg.MappedFolders) > 0 {
		b.WriteString("  <MappedFolders>\n")
		for _, mf := range cfg.MappedFolders {
			host := strings.TrimSpace(mf.HostFolder)
			if host == "" {
				return "", fmt.Errorf("MappedFolder sem HostFolder")
			}
			if isSensitiveSandboxHostPath(host) {
				return "", fmt.Errorf("pasta sensível proibida no sandbox .wsb: %s", host)
			}
			ro := true
			if mf.ReadOnly != nil {
				ro = *mf.ReadOnly
			}
			b.WriteString("    <MappedFolder>\n")
			if err := writeXMLTag(&b, "HostFolder", host, "      "); err != nil {
				return "", err
			}
			if sbx := strings.TrimSpace(mf.SandboxFolder); sbx != "" {
				if err := writeXMLTag(&b, "SandboxFolder", sbx, "      "); err != nil {
					return "", err
				}
			}
			roVal := "true"
			if !ro {
				roVal = "false"
			}
			if err := writeXMLTag(&b, "ReadOnly", roVal, "      "); err != nil {
				return "", err
			}
			b.WriteString("    </MappedFolder>\n")
		}
		b.WriteString("  </MappedFolders>\n")
	}
	if cmd := strings.TrimSpace(cfg.LogonCommand); cmd != "" {
		b.WriteString("  <LogonCommand>\n")
		if err := writeXMLTag(&b, "Command", cmd, "    "); err != nil {
			return "", err
		}
		b.WriteString("  </LogonCommand>\n")
	}
	b.WriteString("</Configuration>\n")
	return b.String(), nil
}

func normalizeWSBToggle(val string) string {
	if strings.EqualFold(val, "Enable") {
		return "Enable"
	}
	return "Disable"
}

func writeXMLTag(b *strings.Builder, tag, val, indent string) error {
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(val)); err != nil {
		return err
	}
	b.WriteString(indent)
	b.WriteByte('<')
	b.WriteString(tag)
	b.WriteByte('>')
	b.Write(escaped.Bytes())
	b.WriteString("</")
	b.WriteString(tag)
	b.WriteString(">\n")
	return nil
}

func isSensitiveSandboxHostPath(raw string) bool {
	norm := strings.ToLower(filepath.ToSlash(filepath.Clean(strings.TrimSpace(raw))))
	base := filepath.Base(norm)
	for _, blocked := range []string{"vault", "secrets", ".ssh", ".aws", ".gnupg"} {
		if base == blocked || strings.Contains(norm, "/"+blocked+"/") {
			return true
		}
	}
	if strings.Contains(norm, "/hivemind/resident") && !strings.Contains(norm, "/wsb") {
		return true
	}
	return false
}

// WriteWSBFile writes the .wsb manifest into dir with mode 0600 and returns the file path.
func WriteWSBFile(dir string, cfg WSBConfig) (string, error) {
	xmlStr, err := BuildWSBXML(cfg)
	if err != nil {
		return "", err
	}
	targetDir := strings.TrimSpace(dir)
	if targetDir == "" {
		targetDir = filepath.Join(os.TempDir(), "HiveMind", "wsb")
	}
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return "", fmt.Errorf("criar pasta do sandbox .wsb: %w", err)
	}
	outPath := filepath.Join(targetDir, "hivemind-resident.wsb")
	if err := os.WriteFile(outPath, []byte(xmlStr), 0o600); err != nil {
		return "", fmt.Errorf("gravar manifesto .wsb: %w", err)
	}
	return outPath, nil
}

// WindowsSandboxAvailable reports whether WindowsSandbox.exe can be resolved on the host.
func WindowsSandboxAvailable() (bool, string) {
	if runtime.GOOS != "windows" {
		return false, "Windows Sandbox (.wsb) disponível apenas no Windows 10/11 Pro/Enterprise"
	}
	sysRoot := strings.TrimSpace(os.Getenv("SystemRoot"))
	if sysRoot != "" {
		cand := filepath.Join(sysRoot, "System32", "WindowsSandbox.exe")
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return true, cand
		}
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		cand := filepath.Join(dir, "WindowsSandbox.exe")
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return true, cand
		}
	}
	return false, "WindowsSandbox.exe não encontrado (ative o recurso opcional Área Restrita do Windows)"
}

// WSBSurface runs browser/desktop requests by provisioning an isolated Windows Sandbox .wsb.
type WSBSurface struct {
	BaseDir   string
	Workspace string
	LaunchFn  func(ctx context.Context, wsbPath string) error
}

// NewWSBSurface returns a WSBSurface storing transient .wsb files under baseDir/wsb.
func NewWSBSurface(baseDir, workspace string) *WSBSurface {
	return &WSBSurface{
		BaseDir:   baseDir,
		Workspace: workspace,
	}
}

func (s *WSBSurface) wsbDir() string {
	if strings.TrimSpace(s.BaseDir) != "" {
		return filepath.Join(s.BaseDir, "wsb")
	}
	return ""
}

func (s *WSBSurface) launch(ctx context.Context, wsbPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.LaunchFn != nil {
		return s.LaunchFn(ctx, wsbPath)
	}
	ok, detail := WindowsSandboxAvailable()
	if !ok {
		return fmt.Errorf("sandbox Windows indisponível: %s", detail)
	}
	proc, err := os.StartProcess(detail, []string{detail, wsbPath}, &os.ProcAttr{
		Dir: filepath.Dir(wsbPath),
	})
	if err != nil {
		return fmt.Errorf("iniciar Windows Sandbox: %w", err)
	}
	_ = proc.Release()
	return nil
}

// OpenURL validates rawURL, provisions a read-only .wsb profile with egress enabled
// for the browser session, and launches Windows Sandbox.
func (s *WSBSurface) OpenURL(ctx context.Context, rawURL string) error {
	safeURL, err := validatePublicURL(rawURL)
	if err != nil {
		return err
	}
	ro := true
	var folders []MappedFolder
	if ws := strings.TrimSpace(s.Workspace); ws != "" && !isSensitiveSandboxHostPath(ws) {
		folders = append(folders, MappedFolder{
			HostFolder:    ws,
			SandboxFolder: `C:\Users\WDAGUtilityAccount\Desktop\Workspace`,
			ReadOnly:      &ro,
		})
	}
	cfg := WSBConfig{
		MappedFolders: folders,
		Networking:    "Enable",
		LogonCommand:  fmt.Sprintf(`cmd.exe /c start "" "%s"`, safeURL),
	}
	wsbPath, err := WriteWSBFile(s.wsbDir(), cfg)
	if err != nil {
		return err
	}
	return s.launch(ctx, wsbPath)
}

// Click refuses unsupervised host desktop clicks in WSB mode (interaction runs inside the sandbox window).
func (s *WSBSurface) Click(_ context.Context, x, y int) error {
	return fmt.Errorf("desktop_click (%d,%d) no modo Windows Sandbox ocorre dentro da janela isolada", x, y)
}

// TypeText refuses unsupervised host desktop typing in WSB mode.
func (s *WSBSurface) TypeText(_ context.Context, _ string) error {
	return fmt.Errorf("desktop_type no modo Windows Sandbox ocorre dentro da janela isolada")
}
