//go:build windows

package extsess

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func listProcesses() ([]processInfo, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return nil, err
	}
	var out []processInfo
	for {
		name := windows.UTF16ToString(entry.ExeFile[:])
		info := processInfo{Name: name, PID: int(entry.ProcessID)}
		if ms, ok := processCreateMs(entry.ProcessID); ok {
			info.CreateMs = ms
			info.KnownTime = true
		}
		out = append(out, info)
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}
	return out, nil
}

func processCreateMs(pid uint32) (int64, bool) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(h)
	var create, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &create, &exit, &kernel, &user); err != nil {
		return 0, false
	}
	return create.Nanoseconds() / int64(1e6), true
}
