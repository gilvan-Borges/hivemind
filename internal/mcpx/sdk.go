package mcpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/procx"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func dialSDK(ctx context.Context, server Server) (session, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "hivemind", Version: "0.1.0"}, nil)
	var transport mcp.Transport
	switch server.Transport {
	case TransportStdio:
		if strings.TrimSpace(server.Command) == "" {
			return nil, fmt.Errorf("servidor %s sem comando", server.Name)
		}
		cmd := localCommand(server.Command, server.Args...)
		cmd.Env = os.Environ()
		for k, v := range server.Env {
			if k == "HIVEMIND_MCP_CWD" {
				cmd.Dir = v
				continue
			}
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		cmd.Stderr = io.Discard
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
		return nil, err
	}
	return &sdkSession{session: cs, server: server.Name}, nil
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
}

func (s *sdkSession) Tools(ctx context.Context) ([]RemoteTool, error) {
	var out []RemoteTool
	var cursor string
	for page := 0; page < 20; page++ {
		res, err := s.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
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
	res, err := s.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
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
