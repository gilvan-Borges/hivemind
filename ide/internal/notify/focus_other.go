//go:build !windows

package notify

// FocusMainWindow is a no-op where the window host already presents the IDE.
func FocusMainWindow(string) {}
