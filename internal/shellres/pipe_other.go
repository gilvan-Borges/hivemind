//go:build !windows

package shellres

import "context"

func runWindows(context.Context, func() (map[string]Handler, error)) error {
	return ErrUnavailable
}
