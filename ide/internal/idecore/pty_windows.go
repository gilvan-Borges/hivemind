//go:build windows

package idecore

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modKernel32                   = windows.NewLazySystemDLL("kernel32.dll")
	procCreatePseudoConsole       = modKernel32.NewProc("CreatePseudoConsole")
	procResizePseudoConsole       = modKernel32.NewProc("ResizePseudoConsole")
	procClosePseudoConsole        = modKernel32.NewProc("ClosePseudoConsole")
	procUpdateProcThreadAttribute = modKernel32.NewProc("UpdateProcThreadAttribute")
)

const procThreadAttributePseudoConsole = 0x00020016

type ptySession struct {
	mu      sync.Mutex
	hPC     windows.Handle
	hProc   windows.Handle
	inFile  *os.File
	outFile *os.File
	closed  bool
}

func packCoord(cols, rows int) uintptr {
	if cols < 20 {
		cols = 80
	}
	if rows < 5 {
		rows = 24
	}
	if cols > 500 {
		cols = 500
	}
	if rows > 200 {
		rows = 200
	}
	return uintptr(uint32(uint16(cols)) | (uint32(uint16(rows)) << 16))
}

func startNativePTY(workspace string, cols, rows int) (*ptySession, error) {
	return startNativePTYWithEnv(workspace, cols, rows, nil)
}

func startNativePTYWithEnv(workspace string, cols, rows int, extraEnv map[string]string) (*ptySession, error) {
	if err := procCreatePseudoConsole.Find(); err != nil {
		return nil, fmt.Errorf("Windows ConPTY indisponível nesta versão do sistema: %w", err)
	}
	absRoot, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil {
		return nil, err
	}

	var inRead, inWrite windows.Handle
	var outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, fmt.Errorf("CreatePipe stdin: %w", err)
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		_ = windows.CloseHandle(inRead)
		_ = windows.CloseHandle(inWrite)
		return nil, fmt.Errorf("CreatePipe stdout: %w", err)
	}

	var hPC windows.Handle
	hr, _, _ := procCreatePseudoConsole.Call(
		packCoord(cols, rows),
		uintptr(inRead),
		uintptr(outWrite),
		0,
		uintptr(unsafe.Pointer(&hPC)),
	)
	if hr != 0 {
		_ = windows.CloseHandle(inRead)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		_ = windows.CloseHandle(outWrite)
		return nil, fmt.Errorf("CreatePseudoConsole falhou: HRESULT=0x%x", hr)
	}

	// ConPTY owns its side of the pipes; close parent duplicates now.
	_ = windows.CloseHandle(inRead)
	_ = windows.CloseHandle(outWrite)

	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		_, _, _ = procClosePseudoConsole.Call(uintptr(hPC))
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, fmt.Errorf("NewProcThreadAttributeList: %w", err)
	}
	defer attrList.Delete()

	r1, _, lastErr := procUpdateProcThreadAttribute.Call(
		uintptr(unsafe.Pointer(attrList.List())),
		0,
		procThreadAttributePseudoConsole,
		uintptr(hPC),
		unsafe.Sizeof(hPC),
		0,
		0,
	)
	if r1 == 0 {
		_, _, _ = procClosePseudoConsole.Call(uintptr(hPC))
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, fmt.Errorf("UpdateProcThreadAttribute: %w", lastErr)
	}

	var siEx windows.StartupInfoEx
	siEx.Cb = uint32(unsafe.Sizeof(siEx))
	siEx.ProcThreadAttributeList = attrList.List()
	siEx.Flags = windows.STARTF_USESTDHANDLES

	cmdLine, err := windows.UTF16PtrFromString("powershell.exe -NoLogo")
	if err != nil {
		_, _, _ = procClosePseudoConsole.Call(uintptr(hPC))
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, err
	}
	workDir, err := windows.UTF16PtrFromString(absRoot)
	if err != nil {
		_, _, _ = procClosePseudoConsole.Call(uintptr(hPC))
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, err
	}

	envBlock := buildPTYEnvBlock(extraEnv)
	var envPtr *uint16
	if len(envBlock) > 0 {
		envPtr = &envBlock[0]
	}

	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(
		nil,
		cmdLine,
		nil,
		nil,
		false,
		flags,
		envPtr,
		workDir,
		&siEx.StartupInfo,
		&pi,
	); err != nil {
		_, _, _ = procClosePseudoConsole.Call(uintptr(hPC))
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, fmt.Errorf("CreateProcess ConPTY: %w", err)
	}
	_ = windows.CloseHandle(pi.Thread)

	return &ptySession{
		hPC:     hPC,
		hProc:   pi.Process,
		inFile:  os.NewFile(uintptr(inWrite), "conpty-in"),
		outFile: os.NewFile(uintptr(outRead), "conpty-out"),
	}, nil
}

func buildPTYEnvBlock(extraEnv map[string]string) []uint16 {
	env := os.Environ()
	for k, v := range extraEnv {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		prefix := strings.ToUpper(key) + "="
		filtered := env[:0]
		for _, kv := range env {
			if strings.HasPrefix(strings.ToUpper(kv), prefix) {
				continue
			}
			filtered = append(filtered, kv)
		}
		env = append(filtered, key+"="+v)
	}
	hasTerm := false
	hasColorTerm := false
	for _, kv := range env {
		up := strings.ToUpper(kv)
		if strings.HasPrefix(up, "TERM=") {
			hasTerm = true
		}
		if strings.HasPrefix(up, "COLORTERM=") {
			hasColorTerm = true
		}
	}
	if !hasTerm {
		env = append(env, "TERM=xterm-256color")
	}
	if !hasColorTerm {
		env = append(env, "COLORTERM=truecolor")
	}
	var out []uint16
	for _, kv := range env {
		u16, err := syscall.UTF16FromString(kv)
		if err != nil {
			continue
		}
		out = append(out, u16...)
	}
	out = append(out, 0)
	return out
}

func (p *ptySession) Read(buf []byte) (int, error) {
	if p == nil {
		return 0, io.EOF
	}
	p.mu.Lock()
	f := p.outFile
	closed := p.closed
	p.mu.Unlock()
	if closed || f == nil {
		return 0, io.EOF
	}
	return f.Read(buf)
}

func (p *ptySession) Write(data []byte) (int, error) {
	p.mu.Lock()
	f := p.inFile
	closed := p.closed
	p.mu.Unlock()
	if closed || f == nil {
		return 0, io.ErrClosedPipe
	}
	return f.Write(data)
}

func (p *ptySession) Resize(cols, rows int) error {
	p.mu.Lock()
	hPC := p.hPC
	closed := p.closed
	p.mu.Unlock()
	if closed || hPC == 0 {
		return nil
	}
	hr, _, _ := procResizePseudoConsole.Call(uintptr(hPC), packCoord(cols, rows))
	if hr != 0 {
		return fmt.Errorf("ResizePseudoConsole falhou: HRESULT=0x%x", hr)
	}
	return nil
}

func (p *ptySession) Wait() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	hProc := p.hProc
	closed := p.closed
	p.mu.Unlock()
	if closed || hProc == 0 {
		return 0
	}
	_, _ = windows.WaitForSingleObject(hProc, windows.INFINITE)
	var code uint32
	if err := windows.GetExitCodeProcess(hProc, &code); err == nil {
		return int(code)
	}
	return 0
}

func (p *ptySession) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	hProc := p.hProc
	hPC := p.hPC
	inFile := p.inFile
	outFile := p.outFile
	p.hProc = 0
	p.hPC = 0
	p.inFile = nil
	p.outFile = nil
	p.mu.Unlock()

	if hProc != 0 {
		_ = windows.TerminateProcess(hProc, 0)
	}
	if hPC != 0 {
		_, _, _ = procClosePseudoConsole.Call(uintptr(hPC))
	}
	if inFile != nil {
		_ = inFile.Close()
	}
	if outFile != nil {
		_ = outFile.Close()
	}
	if hProc != 0 {
		_ = windows.CloseHandle(hProc)
	}
	return nil
}
