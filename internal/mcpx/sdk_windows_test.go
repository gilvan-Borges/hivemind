//go:build windows

package mcpx

import (
	"testing"
)

func TestLocalCommandHidesWindowOnWindows(t *testing.T) {
	cmd := localCommand("uvx", "code-review-graph", "serve")
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr nil: janela de cmd.exe abriria no Windows")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow deve ser true")
	}
	const createNoWindow = 0x08000000
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("CreationFlags = 0x%x, esperado CREATE_NO_WINDOW (0x%x)", cmd.SysProcAttr.CreationFlags, createNoWindow)
	}
}
