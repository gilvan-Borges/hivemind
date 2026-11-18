//go:build windows

package main

import (
	"os"
	"syscall"
	"time"
	"unsafe"
)

var (
	modKernel32         = syscall.NewLazyDLL("kernel32.dll")
	modUser32           = syscall.NewLazyDLL("user32.dll")
	procGetModuleHandle = modKernel32.NewProc("GetModuleHandleW")
	procLoadImageW      = modUser32.NewProc("LoadImageW")
	procEnumWindows     = modUser32.NewProc("EnumWindows")
	procGetWindowPID    = modUser32.NewProc("GetWindowThreadProcessId")
	procSendMessageW    = modUser32.NewProc("SendMessageW")
	procSetClassLongPtr = modUser32.NewProc("SetClassLongPtrW")
)

const (
	winImageIcon   = 1
	winWMSetIcon   = 0x0080
	winIconSmall   = 0
	winIconBig     = 1
	winGCLPHIcon   = ^uintptr(13) // -14 (GCLP_HICON)
	winGCLPHIconSm = ^uintptr(33) // -34 (GCLP_HICONSM)
	appIconResID   = 3
)

func applyWindowIcons() {
	go func() {
		setIconsForPID()
		time.Sleep(200 * time.Millisecond)
		setIconsForPID()
	}()
}

func setIconsForPID() {
	hInst, _, _ := procGetModuleHandle.Call(0)
	if hInst == 0 {
		return
	}
	hBig, _, _ := procLoadImageW.Call(hInst, uintptr(appIconResID), winImageIcon, 48, 48, 0)
	hSmall, _, _ := procLoadImageW.Call(hInst, uintptr(appIconResID), winImageIcon, 16, 16, 0)
	if hBig == 0 && hSmall == 0 {
		return
	}
	if hBig == 0 {
		hBig = hSmall
	}
	if hSmall == 0 {
		hSmall = hBig
	}

	myPID := uint32(os.Getpid())
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var pid uint32
		procGetWindowPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid == myPID {
			procSetClassLongPtr.Call(hwnd, winGCLPHIcon, hBig)
			procSetClassLongPtr.Call(hwnd, winGCLPHIconSm, hSmall)
			procSendMessageW.Call(hwnd, winWMSetIcon, winIconBig, hBig)
			procSendMessageW.Call(hwnd, winWMSetIcon, winIconSmall, hSmall)
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
}
