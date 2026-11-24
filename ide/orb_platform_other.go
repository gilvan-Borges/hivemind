//go:build !windows

package main

func configureOrbNativeWindow(_ string) {}

func startOrbNativeDrag(_ string) bool { return false }

func startOrbNativeResize(_ string, _ string, _ func(int, int)) bool { return false }

func orbPointerDown() bool { return false }

func focusOrbWindow(string) {}

func orbForegroundIsOther(string) bool { return false }
