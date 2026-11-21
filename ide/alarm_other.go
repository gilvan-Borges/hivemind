//go:build !windows

package main

func playNativeAlertAlarm(_ string) bool {
	return false
}
