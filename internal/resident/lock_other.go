//go:build !unix && !windows

package resident

// TryAcquireLock fails closed where there is no OS lock primitive.
func TryAcquireLock(string) (func(), bool) { return nil, false }
