package sandboxx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProbeAndRunBasic(t *testing.T) {
	r := NewOSRunner()
	probe := r.Probe()
	if runtime.GOOS == "windows" {
		if !probe.Available {
			t.Fatalf("AppContainer probe unavailable on Windows: %+v", probe)
		}
	}
	if !probe.Available {
		t.Skipf("sandbox unavailable on this host (%s): %s", probe.Mechanism, probe.Reason)
	}

	ws := t.TempDir()
	// Use the already-compiled test binary itself as the sandboxed target via a helper env var.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// bwrap replaces /tmp with a tmpfs, so the Go test binary (under /tmp/go-build)
	// is invisible unless its own directory is ro-bound. Never bind all of /tmp.
	var readRoots []string
	if runtime.GOOS == "linux" {
		dir := filepath.Clean(filepath.Dir(exe))
		switch dir {
		case "/", "/tmp", "/var/tmp", "/private/tmp", ".":
			t.Fatalf("refusing to ro-bind %s for the test binary", dir)
		}
		readRoots = []string{dir}
	}
	res, err := r.Run(context.Background(), Options{
		Argv:       []string{exe, "-test.run=^TestSandboxHelperProcess$"},
		WorkDir:    ws,
		ReadRoots:  readRoots,
		WriteRoots: []string{ws},
		ExtraEnv:   []string{"HIVEMIND_SANDBOX_HELPER=echo_ok"},
		Timeout:    15 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run failed: %v (stdout=%q stderr=%q)", err, res.Stdout, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "SANDBOX_HELPER_OK") {
		t.Fatalf("unexpected stdout: %q", res.Stdout)
	}
	if runtime.GOOS == "windows" && res.Duration > 2*time.Second {
		t.Fatalf("unexpectedly slow sandbox spawn: %s", res.Duration)
	}
}

func TestSandboxWindowsIsolationDeniesOutsideReadAndNetwork(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("AppContainer isolation test is Windows-specific")
	}
	r := NewOSRunner()
	probe := r.Probe()
	if !probe.Available {
		t.Fatalf("Windows sandbox unavailable: %+v", probe)
	}

	ws := t.TempDir()
	outsideDir := t.TempDir()
	secretPath := filepath.Join(outsideDir, "outside-secret.txt")
	if err := os.WriteFile(secretPath, []byte("top-secret-outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	// 1. Reading an outside temp file without ReadRoots grant must fail in AppContainer.
	res, err := r.Run(context.Background(), Options{
		Argv:    []string{exe, "-test.run=^TestSandboxHelperProcess$"},
		WorkDir: ws,
		ExtraEnv: []string{
			"HIVEMIND_SANDBOX_HELPER=read_file",
			"HIVEMIND_HELPER_TARGET=" + secretPath,
		},
		Timeout: 10 * time.Second,
	})
	if err == nil || res.ExitCode == 0 || !strings.Contains(res.Stderr, "read error:") {
		skipIfTSanChildInitFailed(t, res.Stderr)
		t.Fatalf("expected AppContainer to deny reading ungranted file %s, got err=%v code=%d stdout=%q stderr=%q", secretPath, err, res.ExitCode, res.Stdout, res.Stderr)
	}

	// 2. Writing inside workspace without WriteRoots grant must fail in AppContainer.
	blockedWrite := filepath.Join(ws, "no-write.txt")
	res, err = r.Run(context.Background(), Options{
		Argv:    []string{exe, "-test.run=^TestSandboxHelperProcess$"},
		WorkDir: ws,
		ExtraEnv: []string{
			"HIVEMIND_SANDBOX_HELPER=write_file",
			"HIVEMIND_HELPER_TARGET=" + blockedWrite,
		},
		Timeout: 10 * time.Second,
	})
	if err == nil || res.ExitCode == 0 || !strings.Contains(res.Stderr, "write error:") {
		skipIfTSanChildInitFailed(t, res.Stderr)
		t.Fatalf("expected AppContainer to deny writing %s without WriteRoots, got err=%v code=%d stdout=%q stderr=%q", blockedWrite, err, res.ExitCode, res.Stdout, res.Stderr)
	}

	// 3. Outbound network must fail inside AppContainer (no internetClient capability).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	res, err = r.Run(context.Background(), Options{
		Argv:    []string{exe, "-test.run=^TestSandboxHelperProcess$"},
		WorkDir: ws,
		ExtraEnv: []string{
			"HIVEMIND_SANDBOX_HELPER=dial_tcp",
			"HIVEMIND_HELPER_TARGET=" + ln.Addr().String(),
		},
		Timeout: 10 * time.Second,
	})
	if err == nil || res.ExitCode == 0 || !strings.Contains(res.Stderr, "dial error:") {
		skipIfTSanChildInitFailed(t, res.Stderr)
		t.Fatalf("expected AppContainer to block TCP socket without internetClient, got err=%v code=%d stdout=%q stderr=%q", err, res.ExitCode, res.Stdout, res.Stderr)
	}
}

// skipIfTSanChildInitFailed avoids false CI failures when Go -race (ThreadSanitizer)
// fails to map shadow memory (error 487) inside a Windows AppContainer child process
// before the Go runtime even starts.
func skipIfTSanChildInitFailed(t *testing.T, stderr string) {
	t.Helper()
	if strings.Contains(stderr, "ThreadSanitizer failed to allocate") {
		t.Skipf("Windows -race ThreadSanitizer failed to initialize inside AppContainer child: %s", strings.TrimSpace(stderr))
	}
}

func TestSandboxFailClosedWhenValidationFails(t *testing.T) {
	r := NewOSRunner()
	_, err := r.Run(context.Background(), Options{WorkDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected empty argv to fail closed")
	}
	if err := ValidateArgv([]string{"git", "status"}); err != nil {
		t.Fatalf("valid argv rejected: %v", err)
	}
	if err := ValidateArgv([]string{"bad\x00exe"}); err == nil {
		t.Fatal("expected NUL byte in argv to be rejected")
	}
	if errors.Is(ErrSandboxUnavailable, nil) {
		t.Fatal("ErrSandboxUnavailable should be non-nil")
	}
}

// TestSandboxHelperProcess is invoked as a subprocess by sandbox tests above.
func TestSandboxHelperProcess(t *testing.T) {
	mode := os.Getenv("HIVEMIND_SANDBOX_HELPER")
	if mode == "" {
		return
	}
	target := os.Getenv("HIVEMIND_HELPER_TARGET")
	switch mode {
	case "echo_ok":
		fmt.Println("SANDBOX_HELPER_OK")
	case "read_file":
		data, err := os.ReadFile(target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read error: %v\n", err)
			os.Exit(2)
		}
		fmt.Printf("READ:%s\n", string(data))
	case "write_file":
		if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "write error: %v\n", err)
			os.Exit(3)
		}
		fmt.Println("WRITE_OK")
	case "dial_tcp":
		conn, err := net.DialTimeout("tcp", target, 2*time.Second)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dial error: %v\n", err)
			os.Exit(4)
		}
		_ = conn.Close()
		fmt.Println("DIAL_OK")
	default:
		os.Exit(5)
	}
}
