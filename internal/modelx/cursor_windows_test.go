//go:build windows

package modelx

import (
	"context"
	"testing"
)

func TestCursorCommandHidesWindowOnWindows(t *testing.T) {
	cmd := cursorCommand(context.Background(), `C:\Tools\agent.cmd`, []string{"models"})
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
