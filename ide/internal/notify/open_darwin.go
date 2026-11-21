//go:build darwin

package notify

// Open asks the window host to deliver the notice on macOS.
func Open(func(string)) (Sender, error) {
	return nil, ErrNeedsHost
}
