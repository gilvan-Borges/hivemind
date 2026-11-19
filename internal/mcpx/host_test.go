package mcpx

import (
	"context"
	"strings"
	"testing"
)

type fakeSession struct {
	tools []RemoteTool
	calls int
}

func (f *fakeSession) Tools(context.Context) ([]RemoteTool, error) { return f.tools, nil }
func (f *fakeSession) Call(context.Context, string, map[string]any) (string, error) {
	f.calls++
	return "pong", nil
}
func (f *fakeSession) Close() error { return nil }

func TestDisabledServerDoesNotExposeTools(t *testing.T) {
	t.Setenv("CONTEXT7_API_KEY", "chave-de-teste")
	dialed := map[string]int{}
	h := newHost("", func(_ context.Context, s Server) (session, error) {
		dialed[s.Name]++
		return &fakeSession{tools: []RemoteTool{{Server: s.Name, Name: "query", Description: "docs", Schema: `{"type":"object"}`}}}, nil
	})
	h.servers = cloneServers(DefaultServers())
	h.connectEnabled(context.Background())

	if dialed["playwright"] != 0 || dialed["github"] != 0 || dialed["toolbox"] != 0 {
		t.Fatalf("servidor desligado não pode ser discado: %#v", dialed)
	}
	if dialed["context7"] != 1 || dialed["code-review-graph"] != 1 {
		t.Fatalf("servidores ligados deveriam conectar uma vez: %#v", dialed)
	}
	for _, tool := range h.ExposedTools() {
		if tool.Server == "playwright" || tool.Server == "github" {
			t.Fatalf("ferramenta de servidor desligado vazou: %+v", tool)
		}
	}
	for _, view := range h.Snapshot() {
		if !view.Enabled && len(view.Tools) > 0 {
			t.Fatalf("servidor desligado expôs esquema: %+v", view)
		}
	}
}

func TestEnableForTaskDoesNotPersistAndCloses(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	h := newHost(workspace, func(_ context.Context, s Server) (session, error) {
		return &fakeSession{tools: []RemoteTool{{Server: s.Name, Name: "navigate", Schema: `{"type":"object"}`}}}, nil
	})
	h.servers = cloneServers(DefaultServers())
	msg, err := h.EnableForTask(context.Background(), "playwright")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "só nesta tarefa") {
		t.Fatalf("mensagem: %s", msg)
	}
	if _, err := h.Call(context.Background(), "playwright", "navigate", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.EnableForTask(context.Background(), "chrome-devtools"); err == nil {
		t.Fatal("chrome-devtools deveria ser recusado enquanto o playwright está ligado")
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Call(context.Background(), "playwright", "navigate", nil); err == nil {
		t.Fatal("depois de Close o servidor temporário tem de estar desligado")
	}
	loaded, err := Load(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if findServer(loaded.Servers, "playwright").Enabled {
		t.Fatal("enable da tarefa não pode gravar o interruptor")
	}
}

func TestCodeReviewGraphBlocksCodebaseMemory(t *testing.T) {
	h := newHost("", func(_ context.Context, s Server) (session, error) {
		return &fakeSession{}, nil
	})
	h.servers = []Server{
		{Name: "code-review-graph", Enabled: true, Transport: TransportStdio, Command: "uvx"},
		{Name: "codebase-memory", Enabled: false, Transport: TransportStdio, Command: "npx"},
	}
	if err := h.connect(context.Background(), h.servers[0], false); err != nil {
		t.Fatal(err)
	}
	if err := h.connect(context.Background(), h.servers[1], true); err == nil {
		t.Fatal("os dois grafos não podem ficar ligados juntos")
	}
}
