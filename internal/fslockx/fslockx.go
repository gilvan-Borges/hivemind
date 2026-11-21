// Package fslockx provides a small cross-process advisory file lock used by
// the health stores (jev/health.json, modelx account-pool-health.json) so the
// CLI, the IDE and hivemind serve never lose each other's circuit state
// (Spec 06 §3: "Unificar saúde sob um contrato protegido por lock de arquivo OS").
package fslockx

import (
	"os"
	"path/filepath"
	"time"
)

// Lock acquires an exclusive advisory lock on path+".lock", waiting up to
// timeout. The returned release func is safe to call multiple times. A zero
// timeout locks without waiting.
func Lock(path string, timeout time.Duration) (release func(), err error) {
	if path == "" {
		return func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f, timeout); err != nil {
		_ = f.Close()
		if osIsTimeout(err) {
			// Best effort: proceed unlocked rather than blocking the hot path.
			return func() {}, nil
		}
		return nil, err
	}
	var once bool
	return func() {
		if once {
			return
		}
		once = true
		unlockFile(f)
		_ = f.Close()
	}, nil
}
