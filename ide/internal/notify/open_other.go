//go:build !windows && !linux && !darwin

package notify

// Open reports that this OS has no native sender of its own.
func Open(func(string)) (Sender, error) {
	return nil, ErrNeedsHost
}
