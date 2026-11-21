//go:build windows

package main

import (
	"syscall"
	"time"
	"unsafe"
)

const (
	gwlExStyle              = -20
	wsExToolWindow          = 0x00000080
	wsExAppWindow           = 0x00040000
	swHide                  = 0
	swShowNoActivate        = 4
	swpNoSize               = 0x0001
	swpNoMove               = 0x0002
	swpNoZOrder             = 0x0004
	swpNoActivate           = 0x0010
	swpFrameChanged         = 0x0020
	dwmwaWindowCornerPref   = 33
	dwmwaBorderColor        = 34
	dwmwcpDoNotRound        = 1
	dwmwaColorNone          = 0xFFFFFFFE
	wmSysCommand            = 0x0112
	scMoveDrag              = 0xF012
)

var (
	orbUser32                = syscall.NewLazyDLL("user32.dll")
	orbDwmapi                = syscall.NewLazyDLL("dwmapi.dll")
	orbProcFindWindowW       = orbUser32.NewProc("FindWindowW")
	orbProcGetWindowLongPtrW = orbUser32.NewProc("GetWindowLongPtrW")
	orbProcSetWindowLongPtrW = orbUser32.NewProc("SetWindowLongPtrW")
	orbProcShowWindow        = orbUser32.NewProc("ShowWindow")
	orbProcSetWindowPos      = orbUser32.NewProc("SetWindowPos")
	orbProcReleaseCapture    = orbUser32.NewProc("ReleaseCapture")
	orbProcPostMessageW      = orbUser32.NewProc("PostMessageW")
	orbProcDwmSetWindowAttr  = orbDwmapi.NewProc("DwmSetWindowAttribute")
)

func findOrbHWND(windowTitle string) uintptr {
	titlePtr, err := syscall.UTF16PtrFromString(windowTitle)
	if err != nil {
		return 0
	}
	h, _, _ := orbProcFindWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
	return h
}

// startOrbNativeDrag hands mouse dragging directly to Windows (SC_MOVE) for smooth,
// native 60/120fps movement when the user clicks and drags the mascot itself.
func startOrbNativeDrag(windowTitle string) bool {
	hwnd := findOrbHWND(windowTitle)
	if hwnd == 0 {
		return false
	}
	_, _, _ = orbProcReleaseCapture.Call()
	_, _, _ = orbProcPostMessageW.Call(hwnd, wmSysCommand, scMoveDrag, 0)
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
