//go:build !windows

package main

func configureOrbNativeWindow(_ string) {}

func startOrbNativeDrag(_ string) bool { return false }
