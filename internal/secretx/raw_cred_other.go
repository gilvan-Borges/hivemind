//go:build !windows

package secretx

import "errors"

var errWindowsOnlyCred = errors.New("vault OAuth bruto do agy suportado apenas no Windows")

// ReadRawGenericCredential returns an error on non-Windows hosts.
func ReadRawGenericCredential(_ string) ([]byte, error) {
	return nil, errWindowsOnlyCred
}

// WriteRawGenericCredential returns an error on non-Windows hosts.
func WriteRawGenericCredential(_, _ string, _ []byte) error {
	return errWindowsOnlyCred
}

// DeleteRawGenericCredential returns an error on non-Windows hosts.
func DeleteRawGenericCredential(_ string) error {
	return errWindowsOnlyCred
}
