//go:build linux

package resident

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/shellx"
)

func (osSurface) OpenURL(ctx context.Context, rawURL string) error {
	target, err := validatePublicURL(rawURL)
	if err != nil {
		return err
	}
	return runHostArgv(ctx, []string{"xdg-open", target})
}

func (osSurface) Click(ctx context.Context, x, y int) error {
	if err := validateClick(x, y); err != nil {
		return err
	}
	return runHostArgv(ctx, []string{"xdotool", "mousemove", strconv.Itoa(x), strconv.Itoa(y), "click", "1"})
}

func (osSurface) TypeText(ctx context.Context, text string) error {
	if err := validateTypeText(text); err != nil {
		return err
	}
	return runHostArgv(ctx, []string{"xdotool", "type", "--clearmodifiers", "--", text})
}

// runHostArgv starts an explicit argv on the host session. The resident package
// does not import os/exec; this path is only the experimental unisolated tools.
func runHostArgv(ctx context.Context, argv []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(argv) == 0 {
		return fmt.Errorf("argv vazio")
	}
	path, err := lookHostBinary(argv[0])
	if err != nil {
		return err
	}
	argv[0] = path
	proc, err := os.StartProcess(path, argv, &os.ProcAttr{
		Env:   shellx.SanitizedEnv(),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		_, waitErr := proc.Wait()
		done <- waitErr
	}()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		return nil
	case <-ctx.Done():
		_ = proc.Kill()
		return ctx.Err()
	}
}

func lookHostBinary(name string) (string, error) {
	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("executável recusado")
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		cand := filepath.Join(dir, name)
		info, err := os.Stat(cand)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return cand, nil
		}
	}
	return "", fmt.Errorf("desktop/browser experimental indisponível: %s ausente no PATH", name)
}
