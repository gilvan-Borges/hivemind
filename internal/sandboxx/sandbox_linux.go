//go:build linux

package sandboxx

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const mechanismLinux = "bubblewrap+seccomp"

func probePlatform() ProbeResult {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil || strings.TrimSpace(bwrapPath) == "" {
		return ProbeResult{
			Available: false,
			OS:        "linux",
			Mechanism: mechanismLinux,
			Reason:    "binário bwrap (bubblewrap) não encontrado no PATH",
		}
	}
	if _, err := buildSeccompBPF(false); err != nil {
		return ProbeResult{
			Available: false,
			OS:        "linux",
			Mechanism: mechanismLinux,
			Reason:    err.Error(),
		}
	}
	// Quick user-namespace capability check with --version or true is cheaper on real run;
	// verify bwrap runs a trivial true inside userns/unshare if needed.
	cmd := exec.Command(bwrapPath, "--ro-bind", "/", "/", "--unshare-net", "--die-with-parent", "--", "/bin/true")
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return ProbeResult{
			Available: false,
			OS:        "linux",
			Mechanism: mechanismLinux,
			Reason:    fmt.Sprintf("bwrap não conseguiu criar namespace isolado: %s", msg),
		}
	}
	return ProbeResult{
		Available: true,
		OS:        "linux",
		Mechanism: mechanismLinux,
	}
}

func runPlatform(ctx context.Context, opts Options) (Result, error) {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		return Result{Mechanism: mechanismLinux}, fmt.Errorf("%w: bwrap ausente: %v", ErrSandboxUnavailable, err)
	}
	resolvedExe, err := exec.LookPath(opts.Argv[0])
	if err != nil {
		return Result{Mechanism: mechanismLinux}, fmt.Errorf("executável %q não encontrado: %w", opts.Argv[0], err)
	}

	bpfBytes, err := buildSeccompBPF(opts.AllowNetwork)
	if err != nil {
		return Result{Mechanism: mechanismLinux}, fmt.Errorf("%w: %v", ErrSandboxUnavailable, err)
	}
	seccompR, seccompW, err := os.Pipe()
	if err != nil {
		return Result{Mechanism: mechanismLinux}, fmt.Errorf("criar pipe seccomp: %w", err)
	}
	go func() {
		_, _ = seccompW.Write(bpfBytes)
		_ = seccompW.Close()
	}()
	defer seccompR.Close()

	args := []string{
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
		"--die-with-parent",
		"--new-session",
		"--chdir", opts.WorkDir,
		"--seccomp", "3",
	}
	if !opts.AllowNetwork {
		args = append(args, "--unshare-net")
	}
	if home, hErr := os.UserHomeDir(); hErr == nil && strings.TrimSpace(home) != "" {
		for _, sub := range []string{".ssh", ".aws", ".gnupg", ".kube", ".docker"} {
			p := filepath.Join(home, sub)
			if _, sErr := os.Stat(p); sErr == nil {
				args = append(args, "--tmpfs", p)
			}
		}
	}
	for _, root := range opts.ReadRoots {
		if _, err := os.Stat(root); err == nil {
			args = append(args, "--ro-bind", root, root)
		}
	}
	for _, root := range opts.WriteRoots {
		if _, err := os.Stat(root); err == nil {
			args = append(args, "--bind", root, root)
		}
	}
	args = append(args, "--", resolvedExe)
	if len(opts.Argv) > 1 {
		args = append(args, opts.Argv[1:]...)
	}

	runCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, bwrapPath, args...)
	cmd.Dir = opts.WorkDir
	cmd.Env = sanitizedEnv(opts.ExtraEnv)
	cmd.ExtraFiles = []*os.File{seccompR}

	var stdout, stderr cappedBuffer
	stdout.max = opts.MaxOutputBytes
	stderr.max = opts.MaxOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	started := time.Now()
	err = cmd.Run()
	dur := time.Since(started)

	res := Result{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		Duration:  dur,
		TimedOut:  errors.Is(runCtx.Err(), context.DeadlineExceeded),
		Mechanism: mechanismLinux,
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, fmt.Errorf("processo na sandbox encerrou com código %d", res.ExitCode)
		}
		res.ExitCode = -1
		return res, fmt.Errorf("falha na sandbox bwrap: %w", err)
	}
	return res, nil
}

// buildSeccompBPF compiles a classic cBPF seccomp filter (denying ptrace,
// process_vm_readv/writev, and AF_* socket creation when !allowNetwork).
func buildSeccompBPF(allowNetwork bool) ([]byte, error) {
	var auditArch uint32
	var nrPtrace, nrProcessVMReadv, nrProcessVMWritev, nrSocket uint32
	switch runtime.GOARCH {
	case "amd64":
		auditArch = 0xc000003e // AUDIT_ARCH_X86_64
		nrPtrace = 101
		nrProcessVMReadv = 310
		nrProcessVMWritev = 311
		nrSocket = 41
	case "arm64":
		auditArch = 0xc00000b7 // AUDIT_ARCH_AARCH64
		nrPtrace = 117
		nrProcessVMReadv = 270
		nrProcessVMWritev = 271
		nrSocket = 198
	default:
		return nil, fmt.Errorf("arquitetura %s não suportada pelo filtro seccomp", runtime.GOARCH)
	}

	const (
		bpfLDWABS   = 0x20
		bpfJMPJEQK  = 0x15
		bpfRET      = 0x06
		seccompKill = 0x00000000
		seccompErr  = 0x00050001 // SECCOMP_RET_ERRNO | EPERM
		seccompAllow = 0x7fff0000
		offArch     = 4
		offNR       = 0
	)

	type sockFilter struct {
		Code uint16
		Jt   uint8
		Jf   uint8
		K    uint32
	}

	denied := []uint32{nrPtrace, nrProcessVMReadv, nrProcessVMWritev}
	if !allowNetwork {
		denied = append(denied, nrSocket)
	}

	insns := make([]sockFilter, 0, 6+len(denied))
	// Check architecture.
	insns = append(insns,
		sockFilter{Code: bpfLDWABS, K: offArch},
		sockFilter{Code: bpfJMPJEQK, Jt: 1, Jf: 0, K: auditArch},
		sockFilter{Code: bpfRET, K: seccompKill},
		// Load syscall nr.
		sockFilter{Code: bpfLDWABS, K: offNR},
	)
	for i, nr := range denied {
		remainingToDeny := uint8(len(denied) - 1 - i)
		// If match, jump to EPERM (right after allow).
		insns = append(insns, sockFilter{
			Code: bpfJMPJEQK,
			Jt:   remainingToDeny + 1,
			Jf:   0,
			K:    nr,
		})
	}
	insns = append(insns,
		sockFilter{Code: bpfRET, K: seccompAllow},
		sockFilter{Code: bpfRET, K: seccompErr},
	)

	var buf bytes.Buffer
	for _, f := range insns {
		_ = binary.Write(&buf, binary.LittleEndian, f)
	}
	// Sanity-check SockFilter struct layout matches kernel expectations (8 bytes per instruction).
	if unsafe.Sizeof(syscall.SockFilter{}) != 8 {
		return nil, errors.New("tamanho inesperado de SockFilter")
	}
	return buf.Bytes(), nil
}

type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
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

func (c *cappedBuffer) String() string {
	return c.buf.String()
}

var _ = io.Discard
var _ = filepath.Clean
