//go:build windows

package resident

import (
	"context"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	procSendInput    = user32.NewProc("SendInput")
	procSetCursorPos = user32.NewProc("SetCursorPos")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseEventLeftDown = 0x0002
	mouseEventLeftUp   = 0x0004
	keyEventKeyUp      = 0x0002
	keyEventUnicode    = 0x0004
	swShowNormal       = 1
)

func (osSurface) OpenURL(ctx context.Context, rawURL string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := validatePublicURL(rawURL)
	if err != nil {
		return err
	}
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, nil, nil, swShowNormal)
}

func (osSurface) Click(ctx context.Context, x, y int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateClick(x, y); err != nil {
		return err
	}
	if unsafe.Sizeof(mouseInput{}) != 40 {
		return fmt.Errorf("entrada de desktop experimental exige Windows 64 bits")
	}
	if r, _, err := procSetCursorPos.Call(uintptr(x), uintptr(y)); r == 0 {
		return fmt.Errorf("posicionar cursor: %v", err)
	}
	batch := [...]mouseInput{
		{Type: inputMouse, Mi: mouseUnion{DwFlags: mouseEventLeftDown}},
		{Type: inputMouse, Mi: mouseUnion{DwFlags: mouseEventLeftUp}},
	}
	return sendInputs(uintptr(len(batch)), unsafe.Pointer(&batch[0]), unsafe.Sizeof(batch[0]), uintptr(len(batch)))
}

func (osSurface) TypeText(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateTypeText(text); err != nil {
		return err
	}
	if unsafe.Sizeof(keyInput{}) != 40 {
		return fmt.Errorf("entrada de desktop experimental exige Windows 64 bits")
	}
	runes := []rune(text)
	batch := make([]keyInput, 0, len(runes)*2)
	for _, r := range runes {
		scan := uint16(r)
		batch = append(batch,
			keyInput{Type: inputKeyboard, Ki: keyUnion{WScan: scan, DwFlags: keyEventUnicode}},
			keyInput{Type: inputKeyboard, Ki: keyUnion{WScan: scan, DwFlags: keyEventUnicode | keyEventKeyUp}},
		)
	}
	return sendInputs(uintptr(len(batch)), unsafe.Pointer(&batch[0]), unsafe.Sizeof(batch[0]), uintptr(len(batch)))
}

func sendInputs(n uintptr, ptr unsafe.Pointer, size, want uintptr) error {
	got, _, err := procSendInput.Call(n, uintptr(ptr), size)
	if got != want {
		if err != nil && err != windows.ERROR_SUCCESS {
			return fmt.Errorf("enviar entrada: %v", err)
		}
		return fmt.Errorf("enviar entrada: %d de %d", got, want)
	}
	return nil
}
