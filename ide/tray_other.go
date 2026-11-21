//go:build !windows

package main

type nativeTray struct{}

func (t *nativeTray) isActive() bool { return false }
func (a *App) startSystemTray()      {}
func (a *App) stopSystemTray()       {}
