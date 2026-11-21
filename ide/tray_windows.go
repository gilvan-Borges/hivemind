//go:build windows

package main

import (
	_ "embed"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

//go:embed build/windows/icon.ico
var trayIconICO []byte

const (
	wmAppTray         = 0x8000 + 42
	wmDestroy         = 0x0002
	wmCommand         = 0x0111
	wmLButtonUp       = 0x0202
	wmLButtonDblClk   = 0x0203
	wmRButtonUp       = 0x0205
	wmContextMenu     = 0x007B
	nimAdd            = 0x00000000
	nimDelete         = 0x00000002
	nifMessage        = 0x00000001
	nifIcon           = 0x00000002
	nifTip            = 0x00000004
	imageIcon         = 1
	lrLoadFromFile    = 0x00000010
	lrDefaultSize     = 0x00000040
	mfString          = 0x00000000
	mfSeparator       = 0x00000800
	tpmRightButton    = 0x0002
	tpmBottomAlign    = 0x0020
	tpmReturnCmd      = 0x0100
	trayCmdOpenIDE    = 1001
	trayCmdShowOrb    = 1002
	trayCmdQuitAll    = 1003
	idiApplication    = 32512
)

type trayWndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type trayPoint struct {
	x, y int32
}

type trayMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      trayPoint
}

type notifyIconData struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     uintptr
}

var (
	trayKernel32                = syscall.NewLazyDLL("kernel32.dll")
	trayUser32                  = syscall.NewLazyDLL("user32.dll")
	trayShell32                 = syscall.NewLazyDLL("shell32.dll")
	trayProcGetModuleHandleW    = trayKernel32.NewProc("GetModuleHandleW")
	trayProcRegisterClassExW    = trayUser32.NewProc("RegisterClassExW")
	trayProcCreateWindowExW     = trayUser32.NewProc("CreateWindowExW")
	trayProcDefWindowProcW      = trayUser32.NewProc("DefWindowProcW")
	trayProcGetMessageW         = trayUser32.NewProc("GetMessageW")
	trayProcTranslateMessage    = trayUser32.NewProc("TranslateMessage")
	trayProcDispatchMessageW    = trayUser32.NewProc("DispatchMessageW")
	trayProcPostMessageW        = trayUser32.NewProc("PostMessageW")
	trayProcPostQuitMessage     = trayUser32.NewProc("PostQuitMessage")
	trayProcLoadImageW          = trayUser32.NewProc("LoadImageW")
	trayProcLoadIconW           = trayUser32.NewProc("LoadIconW")
	trayProcDestroyIcon         = trayUser32.NewProc("DestroyIcon")
	trayProcCreatePopupMenu     = trayUser32.NewProc("CreatePopupMenu")
	trayProcAppendMenuW         = trayUser32.NewProc("AppendMenuW")
	trayProcTrackPopupMenu      = trayUser32.NewProc("TrackPopupMenu")
	trayProcDestroyMenu         = trayUser32.NewProc("DestroyMenu")
	trayProcGetCursorPos        = trayUser32.NewProc("GetCursorPos")
	trayProcSetForegroundWindow = trayUser32.NewProc("SetForegroundWindow")
	trayProcShellNotifyIconW    = trayShell32.NewProc("Shell_NotifyIconW")
)

type nativeTray struct {
	mu      sync.Mutex
	hwnd    uintptr
	hIcon   uintptr
	started bool
	app     *App
}

var (
	activeTrayMu sync.Mutex
	activeTray   *nativeTray
)

func (t *nativeTray) isActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.started
}

func (a *App) startSystemTray() {
	if a.smokeCheck {
		return
	}
	a.tray.mu.Lock()
	if a.tray.started {
		a.tray.mu.Unlock()
		return
	}
	a.tray.started = true
	a.tray.app = a
	a.tray.mu.Unlock()

	activeTrayMu.Lock()
	activeTray = &a.tray
	activeTrayMu.Unlock()

	go a.tray.runLoop()
}

func (a *App) stopSystemTray() {
	a.tray.mu.Lock()
	hwnd := a.tray.hwnd
	a.tray.started = false
	a.tray.mu.Unlock()
	if hwnd != 0 {
		_, _, _ = trayProcPostMessageW.Call(hwnd, wmDestroy, 0, 0)
	}
}

func loadTrayIcon() uintptr {
	if len(trayIconICO) > 0 {
		dir := filepath.Join(os.TempDir(), "HiveMind")
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			dir = filepath.Join(local, "HiveMind")
		}
		if os.MkdirAll(dir, 0o755) == nil {
			icoPath := filepath.Join(dir, "tray.ico")
			_ = os.WriteFile(icoPath, trayIconICO, 0o644)
			if p, err := syscall.UTF16PtrFromString(icoPath); err == nil {
				h, _, _ := trayProcLoadImageW.Call(0, uintptr(unsafe.Pointer(p)), imageIcon, 0, 0, lrLoadFromFile|lrDefaultSize)
				if h != 0 {
					return h
				}
			}
		}
	}
	h, _, _ := trayProcLoadIconW.Call(0, uintptr(idiApplication))
	return h
}

func trayWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	activeTrayMu.Lock()
	t := activeTray
	activeTrayMu.Unlock()

	switch msg {
	case wmAppTray:
		switch uint32(lParam & 0xFFFF) {
		case wmLButtonDblClk, wmLButtonUp:
			if t != nil && t.app != nil {
				go func(app *App) {
					app.bringIDEToFront()
					app.orbSetIDEVisible(true)
				}(t.app)
			}
			return 0
		case wmRButtonUp, wmContextMenu:
			if t != nil {
				t.showMenu(hwnd)
			}
			return 0
		}
	case wmDestroy:
		if t != nil {
			t.removeIcon(hwnd)
		}
		_, _, _ = trayProcPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := trayProcDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}

func (t *nativeTray) runLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hInst, _, _ := trayProcGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("HiveMindTrayWndClass")
	wc := trayWndClassEx{
		cbSize:        uint32(unsafe.Sizeof(trayWndClassEx{})),
		lpfnWndProc:   syscall.NewCallback(trayWndProc),
		hInstance:     hInst,
		lpszClassName: className,
	}
	_, _, _ = trayProcRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, _ := trayProcCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		0, 0, 0, 0, 0,
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return
	}

	hIcon := loadTrayIcon()
	t.mu.Lock()
	t.hwnd = hwnd
	t.hIcon = hIcon
	t.mu.Unlock()

	var nid notifyIconData
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = 1
	nid.uFlags = nifMessage | nifIcon | nifTip
	nid.uCallbackMessage = wmAppTray
	nid.hIcon = hIcon
	tipUTF16, _ := syscall.UTF16FromString("HiveMind — rodando em segundo plano")
	copy(nid.szTip[:], tipUTF16)
	_, _, _ = trayProcShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))

	var m trayMsg
	for {
		r, _, _ := trayProcGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		_, _, _ = trayProcTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		_, _, _ = trayProcDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	t.removeIcon(hwnd)
	if hIcon != 0 {
		_, _, _ = trayProcDestroyIcon.Call(hIcon)
	}
}

func (t *nativeTray) removeIcon(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	var nid notifyIconData
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = 1
	_, _, _ = trayProcShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func (t *nativeTray) showMenu(hwnd uintptr) {
	menu, _, _ := trayProcCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer trayProcDestroyMenu.Call(menu)

	addItem := func(id uintptr, label string) {
		p, _ := syscall.UTF16PtrFromString(label)
		_, _, _ = trayProcAppendMenuW.Call(menu, mfString, id, uintptr(unsafe.Pointer(p)))
	}
	addItem(trayCmdOpenIDE, "Abrir HiveMind IDE")
	addItem(trayCmdShowOrb, "Mostrar agente flutuante")
	_, _, _ = trayProcAppendMenuW.Call(menu, mfSeparator, 0, 0)
	addItem(trayCmdQuitAll, "Sair do HiveMind")

	var pt trayPoint
	_, _, _ = trayProcGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	_, _, _ = trayProcSetForegroundWindow.Call(hwnd)
	cmd, _, _ := trayProcTrackPopupMenu.Call(
		menu,
		tpmRightButton|tpmBottomAlign|tpmReturnCmd,
		uintptr(pt.x),
		uintptr(pt.y),
		0,
		hwnd,
		0,
	)
	if t.app == nil {
		return
	}
	switch cmd {
	case trayCmdOpenIDE:
		go func(app *App) {
			app.bringIDEToFront()
			app.orbSetIDEVisible(true)
		}(t.app)
	case trayCmdShowOrb:
		go func(app *App) {
			_ = app.OrbShow()
		}(t.app)
	case trayCmdQuitAll:
		go func(app *App) {
			app.QuitAll()
		}(t.app)
	}
}
