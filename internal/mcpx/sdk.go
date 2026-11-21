package mcpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/procx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func dialSDK(ctx context.Context, server Server) (session, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "hivemind", Version: "0.1.0"}, nil)
	var transport mcp.Transport
	var stderrBuf *cappedStderr
	switch server.Transport {
	case TransportStdio:
		if strings.TrimSpace(server.Command) == "" {
			return nil, fmt.Errorf("servidor %s sem comando", server.Name)
		}
		cmd := localCommand(server.Command, server.Args...)
		envList, cwd := filteredCommandEnv(server.Env)
		cmd.Env = envList
		if cwd != "" {
			cmd.Dir = cwd
		}
		stderrBuf = &cappedStderr{max: 16 << 10}
		cmd.Stderr = stderrBuf
		transport = &mcp.CommandTransport{Command: cmd}
	case TransportHTTP:
		if strings.TrimSpace(server.URL) == "" {
			return nil, fmt.Errorf("servidor %s sem URL", server.Name)
		}
		transport = &mcp.StreamableClientTransport{
			Endpoint:             server.URL,
			HTTPClient:           headerClient(server.Headers),
			MaxRetries:           -1,
			DisableStandaloneSSE: true,
		}
	default:
		return nil, fmt.Errorf("transporte MCP não suportado: %s", server.Transport)
	}
	cs, err := client.Connect(dialCtx, transport, nil)
	if err != nil {
		if tail := redactSecrets(stderrBuf.String()); tail != "" {
			return nil, fmt.Errorf("%w (stderr: %s)", err, tail)
		}
		return nil, err
	}
	return &sdkSession{session: cs, server: server.Name, stderr: stderrBuf}, nil
}

var allowedHostEnvKeys = map[string]bool{
	"PATH":                    true,
	"SYSTEMROOT":              true,
	"WINDIR":                  true,
	"COMSPEC":                 true,
	"PATHEXT":                 true,
	"HOME":                    true,
	"USERPROFILE":             true,
	"APPDATA":                 true,
	"LOCALAPPDATA":            true,
	"PROGRAMDATA":             true,
	"PROGRAMFILES":            true,
	"PROGRAMFILES(X86)":       true,
	"COMMONPROGRAMFILES":      true,
	"COMMONPROGRAMFILES(X86)": true,
	"TEMP":                    true,
	"TMP":                     true,
	"TMPDIR":                  true,
	"XDG_CONFIG_HOME":         true,
	"XDG_CACHE_HOME":          true,
	"XDG_DATA_HOME":           true,
	"TERM":                    true,
	"LANG":                    true,
	"LC_ALL":                  true,
	"SSL_CERT_FILE":           true,
	"SSL_CERT_DIR":            true,
	"NODE_EXTRA_CA_CERTS":     true,
	"UV_CACHE_DIR":            true,
	"NPM_CONFIG_CACHE":        true,
	"GOPATH":                  true,
	"GOROOT":                  true,
}

func filteredCommandEnv(extra map[string]string) ([]string, string) {
	byUpper := make(map[string]string, 32)
	for _, entry := range os.Environ() {
		k, v, ok := strings.Cut(entry, "=")
		if !ok || !allowedHostEnvKeys[strings.ToUpper(k)] {
			continue
		}
		byUpper[strings.ToUpper(k)] = k + "=" + v
	}
	var cwd string
	if len(extra) > 0 {
		keys := make([]string, 0, len(extra))
		for k := range extra {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := extra[k]
			if k == "HIVEMIND_MCP_CWD" {
				cwd = v
				continue
			}
			up := strings.ToUpper(k)
			if runtime.GOOS != "windows" {
				up = k
			}
			byUpper[up] = k + "=" + v
		}
	}
	keys := make([]string, 0, len(byUpper))
	for k := range byUpper {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, byUpper[k])
	}
	return out, cwd
}

type cappedStderr struct {
	mu  sync.Mutex
	max int
	buf bytes.Buffer
}

func (c *cappedStderr) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.max <= 0 {
		return len(p), nil
	}
	if len(p) >= c.max {
		c.buf.Reset()
		_, _ = c.buf.Write(p[len(p)-c.max:])
		return len(p), nil
	}
	if over := c.buf.Len() + len(p) - c.max; over > 0 {
		keep := append([]byte(nil), c.buf.Bytes()[over:]...)
		c.buf.Reset()
		_, _ = c.buf.Write(keep)
	}
	_, _ = c.buf.Write(p)
	return len(p), nil
}

func (c *cappedStderr) String() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(c.buf.String())
}

func localCommand(name string, args ...string) *exec.Cmd {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd.exe", append([]string{"/d", "/c", name}, args...)...)
	} else {
		cmd = exec.Command(name, args...)
	}
	procx.HideWindow(cmd)
	return cmd
}

type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	for k, v := range t.headers {
		if strings.TrimSpace(v) == "" {
			continue
		}
		clone.Header.Set(k, v)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

func headerClient(headers map[string]string) *http.Client {
	return &http.Client{Transport: headerTransport{headers: headers}}
}

type sdkSession struct {
	session *mcp.ClientSession
	server  string
	stderr  *cappedStderr
}

func (s *sdkSession) Tools(ctx context.Context) ([]RemoteTool, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out []RemoteTool
	var cursor string
	for page := 0; page < 20; page++ {
		res, err := s.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			if tail := redactSecrets(s.stderr.String()); tail != "" {
				return nil, fmt.Errorf("%w (stderr: %s)", err, tail)
			}
			return nil, err
		}
		for _, tool := range res.Tools {
			if tool == nil || strings.TrimSpace(tool.Name) == "" {
				continue
			}
			out = append(out, RemoteTool{
				Server:      s.server,
				Name:        tool.Name,
				Description: tool.Description,
				Schema:      schemaText(tool.InputSchema),
			})
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	return out, nil
}

func (s *sdkSession) Call(ctx context.Context, name string, args map[string]any) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := s.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		if tail := redactSecrets(s.stderr.String()); tail != "" {
			return "", fmt.Errorf("%w (stderr: %s)", err, tail)
		}
		return "", err
	}
	text := contentText(res)
	if res.IsError {
		if text == "" {
			text = "a ferramenta MCP retornou erro"
		}
		return "", fmt.Errorf("%s", text)
	}
	if text == "" && res.StructuredContent != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err == nil {
			text = string(raw)
		}
	}
	if text == "" {
		text = "(sem conteúdo)"
	}
	if len(text) > 12000 {
		text = text[:12000] + "\n…(saída truncada)"
	}
	return text, nil
}

func (s *sdkSession) Close() error {
	if s.session == nil {
		return nil
	}
	return s.session.Close()
}

func contentText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

func schemaText(schema any) string {
	if schema == nil {
		return ""
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(schema); err != nil {
		return ""
	}
	text := strings.TrimSpace(buf.String())
	if len(text) > 800 {
		text = text[:800] + "…"
	}
	return text
}
