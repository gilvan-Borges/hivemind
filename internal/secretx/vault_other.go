//go:build !windows && !linux && !darwin

package secretx

import (
	"fmt"
	"runtime"
)

type unsupportedVault struct{}

func newPlatformVault() Vault {
	return &unsupportedVault{}
}

func (u *unsupportedVault) Available() (bool, string) {
	return false, fmt.Sprintf("cofre do sistema não suportado em %s", runtime.GOOS)
}

func (u *unsupportedVault) Set(_, _ string) error {
	return ErrVaultUnavailable
}

func (u *unsupportedVault) Get(_ string) (string, error) {
	return "", ErrVaultUnavailable
}

func (u *unsupportedVault) Delete(_ string) error {
	return ErrVaultUnavailable
}

func (u *unsupportedVault) ListNames() ([]string, error) {
	return nil, ErrVaultUnavailable
}
