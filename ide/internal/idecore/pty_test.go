package idecore

import (
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPTYManagerInteractiveRoundtrip(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("ConPTY test runs on Windows")
	}
	root := t.TempDir()

	var mu sync.Mutex
	var out strings.Builder
	gotMarker := make(chan struct{}, 1)

	mgr := NewPTYManager(func(ev PTYEvent) {
		if ev.Data == "" {
			return
		}
		mu.Lock()
		out.WriteString(ev.Data)
		has := strings.Contains(StripANSI(out.String()), "HIVEMIND_PTY_ROUNDTRIP_OK")
		mu.Unlock()
		if has {
			select {
			case gotMarker <- struct{}{}:
			default:
			}
		}
	})
	defer mgr.CloseAll()

	if _, err := mgr.Start(root, root, 100, 28); err != nil {
		t.Fatalf("PTY Start falhou: %v", err)
	}
	if err := mgr.Resize(root, 120, 32); err != nil {
		t.Fatalf("PTY Resize falhou: %v", err)
	}
	if err := mgr.Write(root, "Write-Output 'HIVEMIND_PTY_ROUNDTRIP_OK'\r"); err != nil {
		t.Fatalf("PTY Write falhou: %v", err)
	}

	select {
	case <-gotMarker:
	case <-time.After(10 * time.Second):
		mu.Lock()
		captured := StripANSI(out.String())
		mu.Unlock()
		t.Fatalf("timeout aguardando saída do ConPTY; capturado=%q", captured)
	}

	mgr.Close(root)
}
