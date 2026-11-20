//go:build linux

package secretx

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

type linuxVault struct{}

func newPlatformVault() Vault {
	return &linuxVault{}
}

func (l *linuxVault) Available() (bool, string) {
	path, err := exec.LookPath("secret-tool")
	if err != nil || strings.TrimSpace(path) == "" {
		return false, "secret-tool (libsecret) não encontrado no PATH"
	}
	return true, ""
}

func (l *linuxVault) Set(name, secret string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if secret == "" {
		return errors.New("valor do segredo não pode ser vazio")
	}
	if ok, reason := l.Available(); !ok {
		return fmt.Errorf("%w: %s", ErrVaultUnavailable, reason)
	}
	cmd := exec.Command("secret-tool", "store", "--label", ServicePrefix+name, "service", "hivemind-resident", "account", name)
	cmd.Stdin = strings.NewReader(secret)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("secret-tool store: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (l *linuxVault) Get(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	if ok, reason := l.Available(); !ok {
		return "", fmt.Errorf("%w: %s", ErrVaultUnavailable, reason)
	}
	cmd := exec.Command("secret-tool", "lookup", "service", "hivemind-resident", "account", name)
	out, err := cmd.Output()
	if err != nil {
		return "", ErrSecretNotFound
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

func (l *linuxVault) Delete(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if ok, reason := l.Available(); !ok {
		return fmt.Errorf("%w: %s", ErrVaultUnavailable, reason)
	}
	cmd := exec.Command("secret-tool", "clear", "service", "hivemind-resident", "account", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("secret-tool clear: %w", err)
	}
	return nil
}

func (l *linuxVault) ListNames() ([]string, error) {
	if ok, reason := l.Available(); !ok {
		return nil, fmt.Errorf("%w: %s", ErrVaultUnavailable, reason)
	}
	cmd := exec.Command("secret-tool", "search", "--all", "service", "hivemind-resident")
	out, _ := cmd.CombinedOutput()
	seen := map[string]struct{}{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "attribute.account = ") {
			k := strings.TrimSpace(strings.TrimPrefix(line, "attribute.account = "))
			if k != "" {
				seen[k] = struct{}{}
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
