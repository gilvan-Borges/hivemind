// Package secretx stores and retrieves secrets exclusively inside the OS
// credential vault (Windows Credential Manager + DPAPI, macOS Keychain,
// Linux Secret Service). Secret values are never written to logs, prompts, or
// telemetry events.
package secretx

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const (
	// ServicePrefix namespaces resident secrets inside the host vault.
	ServicePrefix = "HiveMind:resident:"
)

var (
	// ErrVaultUnavailable indicates the OS credential vault cannot be used (fail closed).
	ErrVaultUnavailable = errors.New("cofre de credenciais do sistema indisponível")
	// ErrSecretNotFound indicates no credential exists for the requested key.
	ErrSecretNotFound = errors.New("credencial não encontrada no cofre")
)

// Vault provides fail-closed OS-backed secret storage.
type Vault interface {
	Available() (bool, string)
	Set(name, secret string) error
	Get(name string) (string, error)
	Delete(name string) error
	ListNames() ([]string, error)
}

// NewOSVault returns the platform-native credential vault.
func NewOSVault() Vault {
	return newPlatformVault()
}

// ValidateName verifies that a secret identifier is safe and non-empty.
func ValidateName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("nome do segredo não pode ser vazio")
	}
	if len(name) > 96 {
		return errors.New("nome do segredo excede 96 caracteres")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-', r == ':':
		default:
			return fmt.Errorf("caractere inválido %q no nome do segredo", r)
		}
	}
	return nil
}

// MemoryVault is an in-memory Vault for deterministic tests.
type MemoryVault struct {
	mu      sync.RWMutex
	entries map[string]string
	unavail string
}

// NewMemoryVault returns an isolated in-memory vault for tests.
func NewMemoryVault() *MemoryVault {
	return &MemoryVault{entries: make(map[string]string)}
}

// SetUnavailable forces Available/Set/Get/Delete/ListNames to fail closed in tests.
func (m *MemoryVault) SetUnavailable(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unavail = reason
}

// Available reports whether the test vault is operational.
func (m *MemoryVault) Available() (bool, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.unavail != "" {
		return false, m.unavail
	}
	return true, ""
}

// Set stores or updates a named secret.
func (m *MemoryVault) Set(name, secret string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if secret == "" {
		return errors.New("valor do segredo não pode ser vazio")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unavail != "" {
		return fmt.Errorf("%w: %s", ErrVaultUnavailable, m.unavail)
	}
	m.entries[name] = secret
	return nil
}

// Get retrieves a named secret.
func (m *MemoryVault) Get(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.unavail != "" {
		return "", fmt.Errorf("%w: %s", ErrVaultUnavailable, m.unavail)
	}
	v, ok := m.entries[name]
	if !ok {
		return "", ErrSecretNotFound
	}
	return v, nil
}

// Delete removes a named secret.
func (m *MemoryVault) Delete(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unavail != "" {
		return fmt.Errorf("%w: %s", ErrVaultUnavailable, m.unavail)
	}
	if _, ok := m.entries[name]; !ok {
		return ErrSecretNotFound
	}
	delete(m.entries, name)
	return nil
}

// ListNames returns stored credential identifiers without revealing secret values.
func (m *MemoryVault) ListNames() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.unavail != "" {
		return nil, fmt.Errorf("%w: %s", ErrVaultUnavailable, m.unavail)
	}
	out := make([]string, 0, len(m.entries))
	for k := range m.entries {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}
