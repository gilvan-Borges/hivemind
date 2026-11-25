package resident

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/shellx"
)

func (b *Broker) fullResolve(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("parâmetro path é obrigatório")
	}
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel), nil
	}
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(ws, rel)), nil
}

func (b *Broker) readFull(rel string) (string, error) {
	p, err := b.fullResolve(rel)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	const max = 256 * 1024
	if len(data) > max {
		data = data[:max]
	}
	return string(data), nil
}

func (b *Broker) writeFull(rel, content string) (string, error) {
	p, err := b.fullResolve(rel)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return "", err
	}
	return "arquivo gravado: " + p, nil
}

func (b *Broker) execFull(ctx context.Context, args map[string]any) (string, error) {
	argv := strSliceArg(args, "argv")
	if len(argv) == 0 {
		command := strings.TrimSpace(strArg(args, "command"))
		if command == "" {
			return "", fmt.Errorf("comando vazio")
		}
		if runtime.GOOS == "windows" {
			argv = []string{"cmd", "/C", command}
		} else {
			argv = []string{"sh", "-c", command}
		}
	}
	cwd := strings.TrimSpace(strArg(args, "cwd"))
	if cwd == "" {
		cwd = strings.TrimSpace(b.cfg.Workspace)
	}
	timeout := 60 * time.Second
	if sec := intArg(args, "timeout_sec"); sec > 0 {
		if sec > 600 {
			sec = 600
		}
		timeout = time.Duration(sec) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout, stderr, exit, err := shellx.RunDirect(runCtx, cwd, argv)
	out := fmt.Sprintf("modo=full exit=%d\n", exit)
	if stdout != "" {
		out += "[stdout]\n" + stdout
	}
	if stderr != "" {
		out += "[stderr]\n" + stderr
	}
	if len(out) > 64*1024 {
		out = out[:64*1024] + "\n…"
	}
	if err != nil {
		return strings.TrimSpace(out), err
	}
	return strings.TrimSpace(out), nil
}

func (b *Broker) netFull(ctx context.Context, args map[string]any) (string, error) {
	raw := strings.TrimSpace(strArg(args, "url"))
	method := strings.ToUpper(strings.TrimSpace(strArg(args, "method")))
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if payload := strArg(args, "body"); payload != "" {
		body = strings.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, raw, body)
	if err != nil {
		return "", err
	}
	if req.URL == nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
		return "", fmt.Errorf("url recusada: só http e https")
	}
	if bearer, ok := args["_resolved_bearer"].(string); ok && bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	client := b.cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	const maxBody = 64 * 1024
	buf, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return "", err
	}
	truncated := len(buf) > maxBody
	if truncated {
		buf = buf[:maxBody]
	}
	out := fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(buf))
	if truncated {
		out += "\n…"
	}
	return out, nil
}
