package resident

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildWSBXMLDefaultsAndEscaping(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "workspace")
	xmlOut, err := BuildWSBXML(WSBConfig{
		MappedFolders: []MappedFolder{
			{HostFolder: ws, SandboxFolder: `C:\Users\WDAGUtilityAccount\Desktop\Repo`},
		},
		LogonCommand: `cmd.exe /c start "" "https://example.com/?a=1&b=2"`,
		MemoryInMB:   2048,
	})
	if err != nil {
		t.Fatalf("BuildWSBXML: %v", err)
	}
	for _, want := range []string{
		"<Networking>Disable</Networking>",
		"<ReadOnly>true</ReadOnly>",
		"<MemoryInMB>2048</MemoryInMB>",
		"https://example.com/?a=1&amp;b=2",
	} {
		if !strings.Contains(xmlOut, want) {
			t.Fatalf("expected %q in .wsb XML:\n%s", want, xmlOut)
		}
	}
}

func TestBuildWSBXMLRejectsSensitiveVaultFolders(t *testing.T) {
	_, err := BuildWSBXML(WSBConfig{
		MappedFolders: []MappedFolder{
			{HostFolder: `C:\Users\dev\AppData\Local\HiveMind\resident\vault`},
		},
	})
	if err == nil {
		t.Fatal("expected vault host path to be rejected in .wsb mapped folders")
	}
}

func TestWSBSurfaceOpenURLWritesWSBAndInvokesLauncher(t *testing.T) {
	baseDir := t.TempDir()
	ws := t.TempDir()
	var launchedPath string
	surf := NewWSBSurface(baseDir, ws)
	surf.LaunchFn = func(_ context.Context, wsbPath string) error {
		launchedPath = wsbPath
		return nil
	}

	if err := surf.OpenURL(context.Background(), "https://example.com/docs"); err != nil {
		t.Fatalf("WSBSurface.OpenURL: %v", err)
	}
	if launchedPath == "" {
		t.Fatal("expected LaunchFn to be called with .wsb path")
	}
	raw, err := os.ReadFile(launchedPath)
	if err != nil {
		t.Fatalf("read written wsb file: %v", err)
	}
	if !strings.Contains(string(raw), "<Networking>Enable</Networking>") || !strings.Contains(string(raw), "https://example.com/docs") {
		t.Fatalf("unexpected wsb content:\n%s", string(raw))
	}
}

func TestBrokerIsolatedSandboxRoutesSurfaceSafely(t *testing.T) {
	baseDir := t.TempDir()
	ws := t.TempDir()
	var launchedWSB string
	wsbSurf := NewWSBSurface(baseDir, ws)
	wsbSurf.LaunchFn = func(_ context.Context, wsbPath string) error {
		launchedWSB = wsbPath
		return nil
	}

	b := NewBroker(BrokerConfig{
		Origin:                 OriginLocal,
		Audit:                  NewFileAuditLogger(filepath.Join(baseDir, "audit.jsonl")),
		Workspace:              ws,
		BaseDir:                baseDir,
		IsolatedSandbox:        true,
		ExperimentalUnisolated: false,
		Surface:                wsbSurf,
		PermissionMode:         func(string) string { return "full_access" },
	})

	out, err := b.Execute(context.Background(), "browser_open", map[string]any{"url": "https://example.com/ok"})
	if err != nil {
		t.Fatalf("expected IsolatedSandbox browser_open to succeed, got: %v", err)
	}
	if !strings.Contains(out, "Windows Sandbox") || launchedWSB == "" {
		t.Fatalf("unexpected output/launch: out=%q wsb=%q", out, launchedWSB)
	}
}
