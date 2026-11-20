//go:build !windows

package extsess

// listProcesses fails closed outside Windows: no shell and no guessed sessions.
func listProcesses() ([]processInfo, error) {
	return nil, nil
}
