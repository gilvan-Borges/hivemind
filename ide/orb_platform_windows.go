//go:build windows

package main

import (
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	gwlExStyle            = -20
	wsExToolWindow        = 0x00000080
	wsExAppWindow         = 0x00040000
	swHide                = 0
	swShowNoActivate      = 4
	swpNoSize             = 0x0001
	swpNoZOrder           = 0x0004
	swpNoActivate         = 0x0010
	swpFrameChanged       = 0x0020
	swpAsyncWindowPos     = 0x4000
	swpNoMove             = 0x0002
	vkLButton             = 0x01
	dwmwaWindowCornerPref = 33
	dwmwaBorderColor      = 34
	dwmwcpDoNotRound      = 1
	dwmwaColorNone        = 0xFFFFFFFE
)

type winPoint struct {
	x int32
	y int32
}

type winRect struct {
	left   int32
	top    int32
	right  int32
	bottom int32
}

var (
	orbUser32                = syscall.NewLazyDLL("user32.dll")
	orbDwmapi                = syscall.NewLazyDLL("dwmapi.dll")
	orbProcFindWindowW       = orbUser32.NewProc("FindWindowW")
	orbProcGetWindowLongPtrW = orbUser32.NewProc("GetWindowLongPtrW")
	orbProcSetWindowLongPtrW = orbUser32.NewProc("SetWindowLongPtrW")
	orbProcShowWindow        = orbUser32.NewProc("ShowWindow")
	orbProcSetWindowPos      = orbUser32.NewProc("SetWindowPos")
	orbProcGetCursorPos      = orbUser32.NewProc("GetCursorPos")
	orbProcGetWindowRect     = orbUser32.NewProc("GetWindowRect")
	orbProcGetAsyncKeyState  = orbUser32.NewProc("GetAsyncKeyState")
	orbProcDwmSetWindowAttr  = orbDwmapi.NewProc("DwmSetWindowAttribute")

	orbDragMu     sync.Mutex
	orbDragActive bool
)

func findOrbHWND(windowTitle string) uintptr {
	titlePtr, err := syscall.UTF16PtrFromString(windowTitle)
	if err != nil {
		return 0
	}
	h, _, _ := orbProcFindWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
	return h
}

func isLeftMouseDown() bool {
	state, _, _ := orbProcGetAsyncKeyState.Call(uintptr(vkLButton))
	return (uint16(state) & 0x8000) != 0
}

// startOrbNativeDrag tracks the global Win32 cursor and moves the floating Orb
// window in physical screen coordinates while the primary mouse button is held.
// This avoids WebView2 capture/thread-queue restrictions on transparent tool windows.
func startOrbNativeDrag(windowTitle string) bool {
	hwnd := findOrbHWND(windowTitle)
	if hwnd == 0 || !isLeftMouseDown() {
		return false
	}

	var startPt winPoint
	okPt, _, _ := orbProcGetCursorPos.Call(uintptr(unsafe.Pointer(&startPt)))
	var startRect winRect
	okRect, _, _ := orbProcGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&startRect)))
	if okPt == 0 || okRect == 0 {
		return false
	}

	orbDragMu.Lock()
	if orbDragActive {
		orbDragMu.Unlock()
		return true
	}
	orbDragActive = true
	orbDragMu.Unlock()

	go func(h uintptr, sPt winPoint, baseX, baseY int32) {
		defer func() {
			orbDragMu.Lock()
			orbDragActive = false
			orbDragMu.Unlock()
		}()

		lastX, lastY := baseX, baseY
		ticker := time.NewTicker(8 * time.Millisecond)
		defer ticker.Stop()

		for range ticker.C {
			if !isLeftMouseDown() {
				return
			}
			var cur winPoint
			ok, _, _ := orbProcGetCursorPos.Call(uintptr(unsafe.Pointer(&cur)))
			if ok == 0 {
				return
			}
			nx := baseX + (cur.x - sPt.x)
			ny := baseY + (cur.y - sPt.y)
			if nx != lastX || ny != lastY {
				lastX, lastY = nx, ny
				_, _, _ = orbProcSetWindowPos.Call(
					h,
					0,
					uintptr(nx),
					uintptr(ny),
					0,
					0,
					swpNoSize|swpNoZOrder|swpNoActivate|swpAsyncWindowPos,
				)
			}
		}
	}(hwnd, startPt, startRect.left, startRect.top)

	return true
}

// configureOrbNativeWindow removes the floating Orb window from the Windows Taskbar
// and Alt+Tab switcher (WS_EX_TOOLWINDOW) and strips Windows 11 corner rounding/borders
// so only the transparent mascot floats above the desktop.
func configureOrbNativeWindow(windowTitle string) {
	titlePtr, err := syscall.UTF16PtrFromString(windowTitle)
	if err != nil {
		return
	}

	var hwnd uintptr
	for i := 0; i < 40; i++ {
		h, _, _ := orbProcFindWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
		if h != 0 {
			hwnd = h
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if hwnd == 0 {
		return
	}

	// Disable Windows 11 rounded corners and border line so transparent mascot has no box.
	cornerPref := uint32(dwmwcpDoNotRound)
	_, _, _ = orbProcDwmSetWindowAttr.Call(
		hwnd,
		dwmwaWindowCornerPref,
		uintptr(unsafe.Pointer(&cornerPref)),
		unsafe.Sizeof(cornerPref),
	)
	borderNone := uint32(dwmwaColorNone)
	_, _, _ = orbProcDwmSetWindowAttr.Call(
		hwnd,
		dwmwaBorderColor,
		uintptr(unsafe.Pointer(&borderNone)),
		unsafe.Sizeof(borderNone),
	)

	// Switch from WS_EX_APPWINDOW to WS_EX_TOOLWINDOW so it never shows on the taskbar.
	idx := int32(gwlExStyle)
	exStyle, _, _ := orbProcGetWindowLongPtrW.Call(hwnd, uintptr(idx))
	newStyle := (exStyle | wsExToolWindow) &^ wsExAppWindow
	if newStyle != exStyle {
		_, _, _ = orbProcShowWindow.Call(hwnd, swHide)
		_, _, _ = orbProcSetWindowLongPtrW.Call(hwnd, uintptr(idx), newStyle)
		_, _, _ = orbProcSetWindowPos.Call(
			hwnd,
			0, 0, 0, 0, 0,
			swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChanged,
		)
		_, _, _ = orbProcShowWindow.Call(hwnd, swShowNoActivate)
	}
}
