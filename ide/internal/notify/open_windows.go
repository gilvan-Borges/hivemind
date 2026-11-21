//go:build windows

package notify

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	toast "git.sr.ht/~jackmordaunt/go-toast/v2"
	"git.sr.ht/~jackmordaunt/go-toast/v2/wintoast"
	"github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"
)

// appID is the Windows AppUserModelID for HiveMind toasts.
const appID = "HiveMind.IDE"

func init() {
	wintoast.GUID_ImplNotificationActivationCallback = ole.NewGUID("{A7C3E1B2-4F58-4D6A-9B10-6E2F8C4D71A0}")
}

type winSender struct {
	on      func(string)
	initErr error
}

// Open registers the in-process toast callback and returns a COM sender.
// shortcut: só WinRT; se o runtime falhar, Send devolve o erro e a IDE mostra o balão.
func Open(onActivate func(string)) (Sender, error) {
	s := &winSender{on: onActivate}
	toast.SetActivationCallback(func(args string, _ []toast.UserData) {
		if s.on != nil {
			s.on(strings.TrimSpace(args))
		}
	})
	setAppUserModelID(appID)
	exe, _ := os.Executable()
	if err := toast.SetAppData(toast.AppData{
		AppID:         appID,
		ActivationExe: exe,
	}); err != nil {
		s.initErr = fmt.Errorf("registrar o toast: %w", err)
		return nil, s.initErr
	}
	return s, nil
}

func (s *winSender) Send(n Notice) error {
	if s.initErr != nil {
		return s.initErr
	}
	if err := Validate(n); err != nil {
		return err
	}
	return wintoast.Push(appID, WindowsToastXML(n.Title, n.Body, n.ID, n.Sound))
}

func (s *winSender) Close() error { return nil }

func setAppUserModelID(id string) {
	p, err := windows.UTF16PtrFromString(id)
	if err != nil {
		return
	}
	proc := windows.NewLazySystemDLL("shell32.dll").NewProc("SetCurrentProcessExplicitAppUserModelID")
	_, _, _ = proc.Call(uintptr(unsafe.Pointer(p)))
}
