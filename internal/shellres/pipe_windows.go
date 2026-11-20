//go:build windows

package shellres

import (
	"context"

	"golang.org/x/sys/windows"
)

const mutexName = `Local\HiveMind.Residente`

func runWindows(ctx context.Context, start func() (map[string]Handler, error)) error {
	name, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	if windows.GetLastError() == windows.ERROR_ALREADY_EXISTS {
		return errString("mutex já aberto: este processo é cliente e não cria outro núcleo")
	}
	if _, err := DeliverKey(func([]byte) error {
		return ErrNoHandle
	}); err != nil {
		return err
	}
	if start == nil {
		return ErrUnavailable
	}
	methods, err := start()
	if err != nil {
		return err
	}
	_ = methods
	_ = ctx
	return errString("pipe do usuário ainda sem DuplicateHandle provado; a casca para sem gravar a chave")
}

type errString string

func (e errString) Error() string { return string(e) }
