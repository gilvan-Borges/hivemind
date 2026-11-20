//go:build !unix && !windows

package modelx

func lockUsageFile(string) (func(), error) {
	return func() {}, nil
}
