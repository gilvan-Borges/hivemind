//go:build !windows

package idecore

import (
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type ptySession struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	closed bool
}

func startNativePTY(workspace string, cols, rows int) (*ptySession, error) {
	return startNativePTYWithEnv(workspace, cols, rows, nil)
}

func startNativePTYWithEnv(workspace string, _, _ int, extraEnv map[string]string) (*ptySession, error) {
	absRoot, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("sh", "-i")
	cmd.Dir = absRoot
	if len(extraEnv) > 0 {
		env := append([]string(nil), cmd.Environ()...)
		for k, v := range extraEnv {
			if strings.TrimSpace(k) != "" {
				env = append(env, strings.TrimSpace(k)+"="+v)
			}
		}
		cmd.Env = env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	return &ptySession{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

func (p *ptySession) Read(buf []byte) (int, error) {
	if p == nil || p.stdout == nil {
		return 0, io.EOF
	}
	return p.stdout.Read(buf)
}

func (p *ptySession) Write(data []byte) (int, error) {
	p.mu.Lock()
	w := p.stdin
	closed := p.closed
	p.mu.Unlock()
	if closed || w == nil {
		return 0, io.ErrClosedPipe
	}
	return w.Write(data)
}

func (p *ptySession) Resize(_, _ int) error { return nil }

func (p *ptySession) Wait() int {
	if p == nil || p.cmd == nil {
		return 0
	}
	return resolveExitCode(p.cmd.Wait())
}

func (p *ptySession) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	cmd := p.cmd
	stdin := p.stdin
	stdout := p.stdout
	p.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if stdin != nil {
		_ = stdin.Close()
	}
	if stdout != nil {
		_ = stdout.Close()
	}
	return nil
}
