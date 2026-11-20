package mcpx

import (
	"context"
	"fmt"
	"os"
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

func stubLookPath(t *testing.T) {
	t.Helper()
	prev := lookPath
	lookPath = func(name string) (string, error) { return name, nil }
	t.Cleanup(func() { lookPath = prev })
}

func TestDisabledServerDoesNotExposeTools(t *testing.T) {
	stubLookPath(t)
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
	stubLookPath(t)
	useTempUserConfig(t)
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

func TestDescribeMarksMissingUvx(t *testing.T) {
	prev := lookPath
	lookPath = func(name string) (string, error) {
		if name == "uvx" {
			return "", os.ErrNotExist
		}
		return name, nil
	}
	t.Cleanup(func() { lookPath = prev })
	views := Describe(File{Servers: DefaultServers()})
	var graph ServerView
	for _, v := range views {
		if v.Name == "code-review-graph" {
			graph = v
		}
	}
	if graph.RuntimeLabel() != "indisponível" || !strings.Contains(graph.Detail, "Python 3.10+") {
		t.Fatalf("describe grafo: %+v", graph)
	}
	if !graph.Enabled {
		t.Fatal("o interruptor gravado continua ligado; só a execução é que não sobe")
	}
}

func TestCodeReviewGraphStaysOffWithoutUvx(t *testing.T) {
	prev := lookPath
	lookPath = func(name string) (string, error) {
		if name == "uvx" {
			return "", os.ErrNotExist
		}
		return name, nil
	}
	t.Cleanup(func() { lookPath = prev })

	dialed := 0
	h := newHost("", func(context.Context, Server) (session, error) {
		dialed++
		return &fakeSession{}, nil
	})
	h.servers = []Server{{
		Name: "code-review-graph", Enabled: true, Transport: TransportStdio, Command: "uvx",
	}}
	if err := h.connect(context.Background(), h.servers[0], false); err == nil {
		t.Fatal("sem uvx o grafo não conecta")
	}
	if dialed != 0 {
		t.Fatalf("não podia discar: %d", dialed)
	}
	view := h.Snapshot()[0]
	if view.RuntimeLabel() != "indisponível" {
		t.Fatalf("label = %s status=%s", view.RuntimeLabel(), view.Status)
	}
	if !strings.Contains(view.Detail, "uvx") || !strings.Contains(view.Detail, "Python 3.10+") {
		t.Fatalf("detalhe: %s", view.Detail)
	}
}

func TestContext7ConnectsWithoutKey(t *testing.T) {
	stubLookPath(t)
	t.Setenv("CONTEXT7_API_KEY", "")
	var header string
	h := newHost("", func(_ context.Context, s Server) (session, error) {
		header = s.Headers["Authorization"]
		return &fakeSession{tools: []RemoteTool{{Server: s.Name, Name: "docs"}}}, nil
	})
	h.servers = []Server{{
		Name: "context7", Enabled: true, Transport: TransportHTTP, URL: "https://mcp.context7.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer ${env?:CONTEXT7_API_KEY}"},
	}}
	if err := h.connect(context.Background(), h.servers[0], false); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(header) != "" {
		t.Fatalf("sem chave o header Authorization não vai: %q", header)
	}
	view := h.Snapshot()[0]
	if view.RuntimeLabel() != "ligado" {
		t.Fatalf("label = %s", view.RuntimeLabel())
	}
}

func TestGitHubUsesGHTokenInMemoryOnly(t *testing.T) {
	const secret = "ghp_TESTTOKENTEST"
	prev := ghAuthToken
	ghAuthToken = func() string { return secret }
	t.Cleanup(func() { ghAuthToken = prev })
	t.Setenv("GITHUB_MCP_TOKEN", "")

	var got string
	h := newHost("", func(_ context.Context, s Server) (session, error) {
		got = s.Headers["Authorization"]
		return nil, fmt.Errorf("falha de teste Bearer %s", secret)
	})
	srv := Server{
		Name: "github", Enabled: true, Transport: TransportHTTP, URL: "https://api.githubcopilot.com/mcp/",
		Headers: map[string]string{"Authorization": "Bearer ${env:GITHUB_MCP_TOKEN}"},
	}
	err := h.connect(context.Background(), srv, false)
	if err == nil {
		t.Fatal("o dial de teste devia falhar")
	}
	if got != "Bearer "+secret {
		t.Fatalf("header = %q", got)
	}
	view := serverView(h.Snapshot(), "github")
	if view.RuntimeLabel() != "indisponível" {
		t.Fatalf("label = %s status=%s", view.RuntimeLabel(), view.Status)
	}
	if strings.Contains(view.Detail, secret) || strings.Contains(err.Error(), secret) {
		t.Fatalf("token vazou no status ou no erro: %s | %v", view.Detail, err)
	}
	if !strings.Contains(view.Detail, "[redigido]") {
		t.Fatalf("detalhe deveria redigir o bearer: %s", view.Detail)
	}
}

func serverView(views []ServerView, name string) ServerView {
	for _, v := range views {
		if v.Name == name {
			return v
		}
	}
	return ServerView{}
}

func TestDescribeGitHubWithoutCredentialIsUnavailable(t *testing.T) {
	prev := ghAuthToken
	ghAuthToken = func() string { return "" }
	t.Cleanup(func() { ghAuthToken = prev })
	t.Setenv("GITHUB_MCP_TOKEN", "")

	servers := DefaultServers()
	for i := range servers {
		if servers[i].Name == "github" {
			servers[i].Enabled = true
		}
	}
	view := serverView(Describe(File{Servers: servers}), "github")
	if view.Status != "needs-secret" || view.RuntimeLabel() != "indisponível" {
		t.Fatalf("view = %+v", view)
	}
	if strings.Contains(fmt.Sprint(view), "ghp_") {
		t.Fatalf("describe vazou token: %+v", view)
	}
}

func TestDescribeGitHubWithGHTokenDoesNotLeakOrBlock(t *testing.T) {
	const secret = "ghp_DESCRIBETOKEN"
	prev := ghAuthToken
	ghAuthToken = func() string { return secret }
	t.Cleanup(func() { ghAuthToken = prev })
	t.Setenv("GITHUB_MCP_TOKEN", "")

	servers := DefaultServers()
	for i := range servers {
		if servers[i].Name == "github" {
			servers[i].Enabled = true
		}
	}
	view := serverView(Describe(File{Servers: servers}), "github")
	if view.Status == "needs-secret" || view.RuntimeLabel() != "ligado" {
		t.Fatalf("com sessão gh o interruptor segue ligado: %+v", view)
	}
	blob := view.Detail + view.Status + view.Description
	if strings.Contains(blob, secret) {
		t.Fatalf("token vazou no describe: %+v", view)
	}
}

func TestGitHubMissingTokenNamesTheVariable(t *testing.T) {
	prev := ghAuthToken
	ghAuthToken = func() string { return "" }
	t.Cleanup(func() { ghAuthToken = prev })
	t.Setenv("GITHUB_MCP_TOKEN", "")

	h := newHost("", func(context.Context, Server) (session, error) {
		t.Fatal("não conecta sem token")
		return nil, nil
	})
	srv := Server{
		Name: "github", Enabled: true, Transport: TransportHTTP, URL: "https://api.githubcopilot.com/mcp/",
		Headers: map[string]string{"Authorization": "Bearer ${env:GITHUB_MCP_TOKEN}"},
	}
	if err := h.connect(context.Background(), srv, false); err == nil {
		t.Fatal("esperava needs-secret")
	}
	view := serverView(h.Snapshot(), "github")
	if view.Status != "needs-secret" || view.RuntimeLabel() != "indisponível" {
		t.Fatalf("view = %+v", view)
	}
	if !strings.Contains(view.Detail, "GITHUB_MCP_TOKEN") || !strings.Contains(view.Detail, "gh auth login") {
		t.Fatalf("detalhe: %s", view.Detail)
	}
}

func TestCodeReviewGraphBlocksCodebaseMemory(t *testing.T) {
	stubLookPath(t)
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
