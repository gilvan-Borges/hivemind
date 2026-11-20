//go:build darwin

package secretx

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

type darwinVault struct{}

func newPlatformVault() Vault {
	return &darwinVault{}
}

func (d *darwinVault) Available() (bool, string) {
	path, err := exec.LookPath("security")
	if err != nil || strings.TrimSpace(path) == "" {
		return false, "utilitário security (Keychain) indisponível"
	}
	return true, ""
}

func (d *darwinVault) Set(name, secret string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if secret == "" {
		return errors.New("valor do segredo não pode ser vazio")
	}
	if ok, reason := d.Available(); !ok {
		return fmt.Errorf("%w: %s", ErrVaultUnavailable, reason)
	}
	cmd := exec.Command("security", "add-generic-password", "-U", "-s", ServicePrefix+name, "-a", name, "-w", secret)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("keychain set: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d *darwinVault) Get(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	if ok, reason := d.Available(); !ok {
		return "", fmt.Errorf("%w: %s", ErrVaultUnavailable, reason)
	}
	out, err := exec.Command("security", "find-generic-password", "-s", ServicePrefix+name, "-a", name, "-w").Output()
	if err != nil {
		return "", ErrSecretNotFound
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

func (d *darwinVault) Delete(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if ok, reason := d.Available(); !ok {
		return fmt.Errorf("%w: %s", ErrVaultUnavailable, reason)
	}
	if err := exec.Command("security", "delete-generic-password", "-s", ServicePrefix+name, "-a", name).Run(); err != nil {
		return ErrSecretNotFound
	}
	return nil
}

func (d *darwinVault) ListNames() ([]string, error) {
	if ok, reason := d.Available(); !ok {
		return nil, fmt.Errorf("%w: %s", ErrVaultUnavailable, reason)
	}
	out, err := exec.Command("security", "dump-keychain").CombinedOutput()
	if err != nil {
		return nil, nil
	}
	seen := map[string]struct{}{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, ServicePrefix); idx >= 0 {
			rest := line[idx+len(ServicePrefix):]
			rest = strings.TrimRight(rest, "\" ")
			if rest != "" {
				seen[rest] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(seen))
	for k := range seen {
		names = append(names, k)
	}
	sort.Strings(names)
	return names, nil
}
