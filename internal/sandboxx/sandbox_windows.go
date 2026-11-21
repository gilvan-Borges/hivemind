//go:build windows

package sandboxx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	mechanismWindows = "appcontainer+jobobject"

	appContainerName = "HiveMind.Resident.Sandbox.v2"
	appContainerDesc = "HiveMind Resident Isolated AppContainer"

	procThreadAttributeSecurityCapabilities = 0x00020009
	procThreadAttributeHandleList           = 0x00020002

	jobObjectExtendedLimitInformation = 9
	jobObjectBasicUIRestrictions        = 4

	jobObjectLimitProcessMemory = 0x00000100
	jobObjectLimitActiveProcess = 0x00000008
	jobObjectLimitKillOnJobClose = 0x00002000

	jobObjectUILimitAll = 0x000000FF

	seFileObject      = 1
	daclSecurityInfo  = 0x00000004
	grantAccess       = 1
	subContainersAndObjectsInherit = 0x3
	trusteeIsSID      = 0
	trusteeIsUnknown  = 0

	fileGenericRead    = 0x00120089
	fileGenericWrite   = 0x00120116
	fileGenericExecute = 0x001200A0
	stdDelete          = 0x00010000
)

var (
	modUserenv  = windows.NewLazySystemDLL("userenv.dll")
	modAdvapi32 = windows.NewLazySystemDLL("advapi32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procCreateAppContainerProfile             = modUserenv.NewProc("CreateAppContainerProfile")
	procDeriveAppContainerSidFromName         = modUserenv.NewProc("DeriveAppContainerSidFromAppContainerName")
	procFreeSid                               = modAdvapi32.NewProc("FreeSid")
	procGetNamedSecurityInfoW                 = modAdvapi32.NewProc("GetNamedSecurityInfoW")
	procSetEntriesInAclW                      = modAdvapi32.NewProc("SetEntriesInAclW")
	procSetNamedSecurityInfoW                 = modAdvapi32.NewProc("SetNamedSecurityInfoW")
	procSetInformationJobObject               = modKernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject              = modKernel32.NewProc("AssignProcessToJobObject")
	procResumeThread                          = modKernel32.NewProc("ResumeThread")

	acSIDMu     sync.Mutex
	cachedACSID *windows.SID
)

type securityCapabilities struct {
	AppContainerSid *windows.SID
	Capabilities    uintptr
	CapabilityCount uint32
	Reserved        uint32
}

type trustee struct {
	MultipleTrustee          uintptr
	MultipleTrusteeOperation int32
	TrusteeForm              int32
	TrusteeType              int32
	Name                     uintptr
}

type explicitAccess struct {
	AccessPermissions uint32
	AccessMode        int32
	Inheritance       uint32
	Trustee           trustee
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobObjectExtendedLimitInfo struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type jobObjectBasicUIRestrictionsInfo struct {
	UIRestrictionsClass uint32
}

func probePlatform() ProbeResult {
	if err := modUserenv.Load(); err != nil {
		return ProbeResult{
			Available: false,
			OS:        "windows",
			Mechanism: mechanismWindows,
			Reason:    fmt.Sprintf("userenv.dll indisponível: %v", err),
		}
	}
	if err := procCreateAppContainerProfile.Find(); err != nil {
		return ProbeResult{
			Available: false,
			OS:        "windows",
			Mechanism: mechanismWindows,
			Reason:    "AppContainer não suportado nesta versão do Windows",
		}
	}
	if _, err := ensureAppContainerSID(); err != nil {
		return ProbeResult{
			Available: false,
			OS:        "windows",
			Mechanism: mechanismWindows,
			Reason:    err.Error(),
		}
	}
	return ProbeResult{
		Available: true,
		OS:        "windows",
		Mechanism: mechanismWindows,
	}
}

func ensureAppContainerSID() (*windows.SID, error) {
	acSIDMu.Lock()
	defer acSIDMu.Unlock()
	if cachedACSID != nil {
		return cachedACSID, nil
	}
	namePtr, err := windows.UTF16PtrFromString(appContainerName)
	if err != nil {
		return nil, err
	}
	descPtr, err := windows.UTF16PtrFromString(appContainerDesc)
	if err != nil {
		return nil, err
	}
	var sid *windows.SID
	hr, _, _ := procCreateAppContainerProfile.Call(
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(descPtr)),
		0,
		0,
		uintptr(unsafe.Pointer(&sid)),
	)
	const hresultAlreadyExists = 0x800700B7
	if hr == hresultAlreadyExists {
		hr, _, _ = procDeriveAppContainerSidFromName.Call(
			uintptr(unsafe.Pointer(namePtr)),
			uintptr(unsafe.Pointer(&sid)),
		)
	}
	if hr != 0 || sid == nil {
		return nil, fmt.Errorf("CreateAppContainerProfile falhou (HRESULT 0x%X)", uint32(hr))
	}
	cachedACSID = sid
	return cachedACSID, nil
}

func grantAppContainerACL(path string, sid *windows.SID, accessMask uint32) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(absPath); err != nil {
		return err
	}
	pObject, err := windows.UTF16PtrFromString(absPath)
	if err != nil {
		return err
	}
	var oldDACL, secDesc uintptr
	r1, _, _ := procGetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(pObject)),
		seFileObject,
		daclSecurityInfo,
		0, 0,
		uintptr(unsafe.Pointer(&oldDACL)),
		0,
		uintptr(unsafe.Pointer(&secDesc)),
	)
	if r1 != 0 {
		return fmt.Errorf("GetNamedSecurityInfoW(%s): %w", absPath, syscall.Errno(r1))
	}
	if secDesc != 0 {
		defer windows.LocalFree(windows.Handle(secDesc))
	}

	ea := explicitAccess{
		AccessPermissions: accessMask,
		AccessMode:        grantAccess,
		Inheritance:       subContainersAndObjectsInherit,
		Trustee: trustee{
			TrusteeForm: trusteeIsSID,
			TrusteeType: trusteeIsUnknown,
			Name:        uintptr(unsafe.Pointer(sid)),
		},
	}
	var newDACL uintptr
	r1, _, _ = procSetEntriesInAclW.Call(
		1,
		uintptr(unsafe.Pointer(&ea)),
		oldDACL,
		uintptr(unsafe.Pointer(&newDACL)),
	)
	if r1 != 0 {
		return fmt.Errorf("SetEntriesInAclW(%s): %w", absPath, syscall.Errno(r1))
	}
	if newDACL != 0 {
		defer windows.LocalFree(windows.Handle(newDACL))
	}

	r1, _, _ = procSetNamedSecurityInfoW.Call(
		uintptr(unsafe.Pointer(pObject)),
		seFileObject,
		daclSecurityInfo,
		0, 0,
		newDACL,
		0,
	)
	if r1 != 0 {
		return fmt.Errorf("SetNamedSecurityInfoW(%s): %w", absPath, syscall.Errno(r1))
	}
	return nil
}

func createSandboxJobObject(opts Options) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("CreateJobObject: %w", err)
	}
	var ext jobObjectExtendedLimitInfo
	ext.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose | jobObjectLimitActiveProcess | jobObjectLimitProcessMemory
	ext.BasicLimitInformation.ActiveProcessLimit = opts.MaxProcesses
	ext.ProcessMemoryLimit = uintptr(opts.MaxMemoryBytes)

	r1, _, e1 := procSetInformationJobObject.Call(
		uintptr(job),
		jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&ext)),
		unsafe.Sizeof(ext),
	)
	if r1 == 0 {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("SetInformationJobObject(limits): %w", e1)
	}

	ui := jobObjectBasicUIRestrictionsInfo{UIRestrictionsClass: jobObjectUILimitAll}
	r1, _, e1 = procSetInformationJobObject.Call(
		uintptr(job),
		jobObjectBasicUIRestrictions,
		uintptr(unsafe.Pointer(&ui)),
		unsafe.Sizeof(ui),
	)
	if r1 == 0 {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("SetInformationJobObject(ui): %w", e1)
	}
	return job, nil
}

func runPlatform(ctx context.Context, opts Options) (Result, error) {
	sid, err := ensureAppContainerSID()
	if err != nil {
		return Result{Mechanism: mechanismWindows}, fmt.Errorf("%w: %v", ErrSandboxUnavailable, err)
	}

	resolvedExe, err := exec.LookPath(opts.Argv[0])
	if err != nil {
		return Result{Mechanism: mechanismWindows}, fmt.Errorf("executável %q não encontrado: %w", opts.Argv[0], err)
	}
	resolvedExe, err = filepath.Abs(resolvedExe)
	if err != nil {
		return Result{Mechanism: mechanismWindows}, err
	}

	readTargets := append([]string(nil), opts.ReadRoots...)
	for _, root := range dedupeAbsPaths(readTargets) {
		if err := grantAppContainerACL(root, sid, fileGenericRead|fileGenericExecute); err != nil {
			return Result{Mechanism: mechanismWindows}, fmt.Errorf("conceder ACL de leitura em %s: %w", root, err)
		}
	}
	exeDir := filepath.Dir(resolvedExe)
	sysRoot := strings.TrimSpace(os.Getenv("SystemRoot"))
	underSystemRoot := sysRoot != "" && strings.HasPrefix(strings.ToLower(filepath.Clean(exeDir)), strings.ToLower(filepath.Clean(sysRoot)))
	if !underSystemRoot {
		// Best-effort for protected tool directories (e.g. Program Files without elevation),
		// which already grant read/execute to ALL APPLICATION PACKAGES by default.
		_ = grantAppContainerACL(exeDir, sid, fileGenericRead|fileGenericExecute)
	}
	for _, root := range opts.WriteRoots {
		if err := grantAppContainerACL(root, sid, fileGenericRead|fileGenericWrite|fileGenericExecute|stdDelete); err != nil {
			return Result{Mechanism: mechanismWindows}, fmt.Errorf("conceder ACL de escrita em %s: %w", root, err)
		}
	}

	job, err := createSandboxJobObject(opts)
	if err != nil {
		return Result{Mechanism: mechanismWindows}, fmt.Errorf("%w: %v", ErrSandboxUnavailable, err)
	}
	defer windows.CloseHandle(job)

	// Create pipes for stdin (NUL), stdout, stderr with explicit inheritable handles.
	nulIn, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return Result{Mechanism: mechanismWindows}, err
	}
	defer nulIn.Close()
	if err := windows.SetHandleInformation(windows.Handle(nulIn.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		return Result{Mechanism: mechanismWindows}, err
	}

	outR, outW, err := os.Pipe()
	if err != nil {
		return Result{Mechanism: mechanismWindows}, err
	}
	defer outR.Close()
	defer outW.Close()
	_ = windows.SetHandleInformation(windows.Handle(outR.Fd()), windows.HANDLE_FLAG_INHERIT, 0)
	_ = windows.SetHandleInformation(windows.Handle(outW.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT)

	errR, errW, err := os.Pipe()
	if err != nil {
		return Result{Mechanism: mechanismWindows}, err
	}
	defer errR.Close()
	defer errW.Close()
	_ = windows.SetHandleInformation(windows.Handle(errR.Fd()), windows.HANDLE_FLAG_INHERIT, 0)
	_ = windows.SetHandleInformation(windows.Handle(errW.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT)

	attrList, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return Result{Mechanism: mechanismWindows}, fmt.Errorf("NewProcThreadAttributeList: %w", err)
	}
	defer attrList.Delete()

	secCaps := securityCapabilities{
		AppContainerSid: sid,
	}
	var netCaps []windows.SIDAndAttributes
	if opts.AllowNetwork {
		// Well-known AppContainer capabilities:
		// S-1-15-3-1 = internetClient, S-1-15-3-3 = privateNetworkClientServer.
		for _, capStr := range []string{"S-1-15-3-1", "S-1-15-3-3"} {
			capSID, capErr := windows.StringToSid(capStr)
			if capErr == nil && capSID != nil {
				netCaps = append(netCaps, windows.SIDAndAttributes{
					Sid:        capSID,
					Attributes: windows.SE_GROUP_ENABLED,
				})
			}
		}
		if len(netCaps) > 0 {
			secCaps.Capabilities = uintptr(unsafe.Pointer(&netCaps[0]))
			secCaps.CapabilityCount = uint32(len(netCaps))
		}
	}
	if err := attrList.Update(procThreadAttributeSecurityCapabilities, unsafe.Pointer(&secCaps), unsafe.Sizeof(secCaps)); err != nil {
		return Result{Mechanism: mechanismWindows}, fmt.Errorf("attr SECURITY_CAPABILITIES: %w", err)
	}

	handles := []windows.Handle{
		windows.Handle(nulIn.Fd()),
		windows.Handle(outW.Fd()),
		windows.Handle(errW.Fd()),
	}
	if err := attrList.Update(procThreadAttributeHandleList, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		return Result{Mechanism: mechanismWindows}, fmt.Errorf("attr HANDLE_LIST: %w", err)
	}

	cmdLine := buildWindowsCommandLine(opts.Argv)
	cmdPtr, err := windows.UTF16PtrFromString(cmdLine)
	if err != nil {
		return Result{Mechanism: mechanismWindows}, err
	}
	appPtr, err := windows.UTF16PtrFromString(resolvedExe)
	if err != nil {
		return Result{Mechanism: mechanismWindows}, err
	}
	dirPtr, err := windows.UTF16PtrFromString(opts.WorkDir)
	if err != nil {
		return Result{Mechanism: mechanismWindows}, err
	}
	envBlock, err := buildUTF16EnvBlock(sanitizedEnv(opts.ExtraEnv))
	if err != nil {
		return Result{Mechanism: mechanismWindows}, err
	}

	var si windows.StartupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES | windows.STARTF_USESHOWWINDOW
	si.ShowWindow = windows.SW_HIDE
	si.StdInput = handles[0]
	si.StdOutput = handles[1]
	si.StdErr = handles[2]
	si.ProcThreadAttributeList = attrList.List()

	var pi windows.ProcessInformation
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW | windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)

	started := time.Now()
	err = windows.CreateProcess(
		appPtr,
		cmdPtr,
		nil,
		nil,
		true,
		flags,
		&envBlock[0],
		dirPtr,
		&si.StartupInfo,
		&pi,
	)
	runtime.KeepAlive(netCaps)
	// Close parent copies of write ends immediately so pipes reach EOF cleanly.
	_ = outW.Close()
	_ = errW.Close()
	if err != nil {
		return Result{Mechanism: mechanismWindows, Duration: time.Since(started)}, fmt.Errorf("CreateProcess AppContainer: %w", err)
	}
	defer windows.CloseHandle(pi.Process)
	defer windows.CloseHandle(pi.Thread)

	r1, _, e1 := procAssignProcessToJobObject.Call(uintptr(job), uintptr(pi.Process))
	if r1 == 0 {
		_ = windows.TerminateProcess(pi.Process, 1)
		return Result{Mechanism: mechanismWindows, Duration: time.Since(started)}, fmt.Errorf("AssignProcessToJobObject: %w", e1)
	}

	prevSuspend, _, e1 := procResumeThread.Call(uintptr(pi.Thread))
	if int32(prevSuspend) == -1 {
		_ = windows.TerminateProcess(pi.Process, 1)
		return Result{Mechanism: mechanismWindows, Duration: time.Since(started)}, fmt.Errorf("ResumeThread: %w", e1)
	}

	var outBuf, errBuf cappedBuf
	outBuf.max = opts.MaxOutputBytes
	errBuf.max = opts.MaxOutputBytes
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&outBuf, outR)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&errBuf, errR)
	}()

	runCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	waitDone := make(chan uint32, 1)
	go func() {
		event, _ := windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		waitDone <- event
	}()

	timedOut := false
	select {
	case <-waitDone:
	case <-runCtx.Done():
		timedOut = errors.Is(runCtx.Err(), context.DeadlineExceeded)
		_ = windows.TerminateJobObject(job, 1)
		<-waitDone
	}
	wg.Wait()

	var exitCode uint32
	_ = windows.GetExitCodeProcess(pi.Process, &exitCode)

	res := Result{
		ExitCode:  int(exitCode),
		Stdout:    outBuf.String(),
		Stderr:    errBuf.String(),
		Duration:  time.Since(started),
		TimedOut:  timedOut,
		Mechanism: mechanismWindows,
	}
	if timedOut {
		return res, fmt.Errorf("tempo esgotado (%s) na sandbox AppContainer", opts.Timeout)
	}
	if runCtx.Err() != nil {
		return res, runCtx.Err()
	}
	if exitCode != 0 {
		return res, fmt.Errorf("processo AppContainer encerrou com código %d", exitCode)
	}
	return res, nil
}

func buildWindowsCommandLine(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = syscall.EscapeArg(arg)
	}
	return strings.Join(quoted, " ")
}

func buildUTF16EnvBlock(env []string) ([]uint16, error) {
	var block []uint16
	for _, entry := range env {
		if entry == "" || strings.ContainsRune(entry, '\x00') {
			continue
		}
		u, err := windows.UTF16FromString(entry)
		if err != nil {
			return nil, err
		}
		block = append(block, u...)
	}
	if len(block) == 0 {
		block = append(block, 0)
	}
	block = append(block, 0)
	return block, nil
}

type cappedBuf struct {
	buf bytes.Buffer
	max int
}

func (c *cappedBuf) Write(p []byte) (int, error) {
	if c.max <= 0 {
		return len(p), nil
	}
	rem := c.max - c.buf.Len()
	if rem > 0 {
		if len(p) <= rem {
			c.buf.Write(p)
		} else {
			c.buf.Write(p[:rem])
		}
	}
	return len(p), nil
}

func (c *cappedBuf) String() string {
	return c.buf.String()
}
