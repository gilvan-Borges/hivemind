package resident

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/sandboxx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

func (b *Broker) handleExecCommand(ctx context.Context, args map[string]any) (string, error) {
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(ws); statErr != nil || !info.IsDir() {
		return "", fmt.Errorf("workspace inválido para sandbox: %w", statErr)
	}
	argv := strSliceArg(args, "argv")
	if err := ValidateExecCommand(argv, b.cfg.Policy.AllowedBinaries); err != nil {
		return "", err
	}
	timeout := sandboxx.DefaultTimeout
	if sec := intArg(args, "timeout_sec"); sec > 0 {
		if sec > 120 {
			sec = 120
		}
		timeout = time.Duration(sec) * time.Second
	}

	res, err := b.cfg.Sandbox.Run(ctx, sandboxx.Options{
		Argv:         argv,
		WorkDir:      ws,
		ReadRoots:    []string{ws},
		WriteRoots:   []string{ws},
		AllowNetwork: false,
		Timeout:      timeout,
	})
	var sb strings.Builder
	fmt.Fprintf(&sb, "sandbox=%s exit=%d duration=%dms\n", res.Mechanism, res.ExitCode, res.Duration.Milliseconds())
	if strings.TrimSpace(res.Stdout) != "" {
		fmt.Fprintf(&sb, "[stdout]\n%s\n", res.Stdout)
	}
	if strings.TrimSpace(res.Stderr) != "" {
		fmt.Fprintf(&sb, "[stderr]\n%s\n", res.Stderr)
	}
	if err != nil {
		out := strings.TrimSpace(sb.String())
		if out != "" {
			return out, fmt.Errorf("%v\n%s", err, strx.TruncateBytes(out, 2048, "…"))
		}
		return "", err
	}
	return strings.TrimSpace(sb.String()), nil
}

func (b *Broker) handleNetFetch(ctx context.Context, args map[string]any) (string, error) {
	rawURL := strArg(args, "url")
	u, err := ValidateOutboundURL(rawURL, b.cfg.Policy.AllowedHosts)
	if err != nil {
		return "", err
	}
	method := strings.ToUpper(strArg(args, "method"))
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodHead {
		return "", fmt.Errorf("método HTTP %q não permitido (apenas GET/HEAD)", method)
	}

	client := b.cfg.HTTPClient
	if client == nil {
		client = NewGuardedHTTPClient(b.cfg.Policy.AllowedHosts, 15*time.Second)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return "", err
	}
	if bearer, ok := args["_resolved_bearer"].(string); ok && bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("User-Agent", "HiveMind-Resident/2.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	const maxBodyBytes = 64 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return "", err
	}
	truncated := len(body) > maxBodyBytes
	if truncated {
		body = body[:maxBodyBytes]
	}
	out := fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(body))
	if truncated {
		out += "\n… [resposta truncada]"
	}
	return out, nil
}
