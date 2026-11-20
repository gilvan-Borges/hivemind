// Package sandboxx enforces fail-closed OS-level process sandboxing for the
// Hive resident assistant (AppContainer + Job Object on Windows, bubblewrap +
// seccomp on Linux, Seatbelt on macOS). No CGO is used.
package sandboxx

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/shellx"
)

// ErrSandboxUnavailable is returned when the host OS cannot enforce a mandatory
// process sandbox (fail-closed policy).
var ErrSandboxUnavailable = errors.New("sandbox do sistema operacional indisponível (falha fechada)")

const (
	// DefaultTimeout bounds sandboxed subprocess lifetime if the caller does not specify one.
	DefaultTimeout = 60 * time.Second
	// DefaultMaxOutputBytes caps stdout/stderr captured from a sandboxed process.
	DefaultMaxOutputBytes = 256 * 1024
	// DefaultMaxMemoryBytes caps process memory in Windows Job Objects (1 GiB).
	DefaultMaxMemoryBytes = 1024 * 1024 * 1024
	// DefaultMaxProcesses caps active child processes in Windows Job Objects.
	DefaultMaxProcesses = 16
)

// ProbeResult describes OS sandbox availability on the current host.
type ProbeResult struct {
	Available bool   `json:"available"`
	OS        string `json:"os"`
	Mechanism string `json:"mechanism"`
	Reason    string `json:"reason,omitempty"`
}

// Options configures a single fail-closed sandboxed execution.
type Options struct {
	Argv           []string
	WorkDir        string
	ReadRoots      []string
	WriteRoots     []string
	AllowNetwork   bool
	Timeout        time.Duration
	MaxOutputBytes int
	MaxMemoryBytes uint64
	MaxProcesses   uint32
	ExtraEnv       []string
}

// Result captures the outcome of a sandboxed command execution.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
	TimedOut bool
	Mechanism string
}

// Runner executes argv commands inside a mandatory OS sandbox.
type Runner interface {
	Probe() ProbeResult
	Run(ctx context.Context, opts Options) (Result, error)
}

// OSRunner is the production fail-closed OS sandbox runner.
type OSRunner struct{}

// NewOSRunner returns the platform-native fail-closed sandbox runner.
func NewOSRunner() *OSRunner {
	return &OSRunner{}
}

// Probe inspects host support for the native OS sandbox.
func (r *OSRunner) Probe() ProbeResult {
	return probePlatform()
}

// Run executes opts.Argv inside the OS sandbox or fails closed.
func (r *OSRunner) Run(ctx context.Context, opts Options) (Result, error) {
	normalized, err := normalizeOptions(opts)
	if err != nil {
		return Result{}, err
	}
	probe := r.Probe()
	if !probe.Available {
		return Result{Mechanism: probe.Mechanism}, fmt.Errorf("%w: %s", ErrSandboxUnavailable, probe.Reason)
	}
	return runPlatform(ctx, normalized)
}

// ValidateArgv verifies explicit argv without invoking an interactive shell.
func ValidateArgv(argv []string) error {
	if len(argv) == 0 {
		return errors.New("argv vazio: especifique executável e argumentos explícitos")
	}
	exe := strings.TrimSpace(argv[0])
	if exe == "" {
		return errors.New("executável vazio em argv[0]")
	}
	if strings.ContainsAny(exe, "\r\n\x00") {
		return errors.New("executável contém caractere inválido")
	}
	for i, arg := range argv {
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("argumento %d contém byte NUL", i)
		}
	}
	return nil
}

func normalizeOptions(opts Options) (Options, error) {
	if err := ValidateArgv(opts.Argv); err != nil {
		return Options{}, err
	}
	wd := strings.TrimSpace(opts.WorkDir)
	if wd == "" {
		return Options{}, errors.New("WorkDir obrigatório para execução na sandbox")
	}
	absWD, err := filepath.Abs(wd)
	if err != nil {
		return Options{}, fmt.Errorf("WorkDir inválido: %w", err)
	}
	opts.WorkDir = absWD
	opts.ReadRoots = dedupeAbsPaths(append([]string{absWD}, opts.ReadRoots...))
	opts.WriteRoots = dedupeAbsPaths(opts.WriteRoots)
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.MaxOutputBytes <= 0 {
		opts.MaxOutputBytes = DefaultMaxOutputBytes
	}
	if opts.MaxMemoryBytes == 0 {
		opts.MaxMemoryBytes = DefaultMaxMemoryBytes
	}
	if opts.MaxProcesses == 0 {
		opts.MaxProcesses = DefaultMaxProcesses
	}
	return opts, nil
}

func dedupeAbsPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		key := filepath.Clean(abs)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

func sanitizedEnv(extra []string) []string {
	base := shellx.SanitizedEnv()
	if len(extra) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(extra))
	out = append(out, base...)
	for _, kv := range extra {
		if strings.Contains(kv, "=") {
			out = append(out, kv)
		}
	}
	return out
}
