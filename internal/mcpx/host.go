package mcpx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// session é a fatia do cliente MCP que o Host precisa. Os testes substituem o dial.
type session interface {
	Tools(ctx context.Context) ([]RemoteTool, error)
	Call(ctx context.Context, name string, args map[string]any) (string, error)
	Close() error
}

// Host mantém só os servidores ligados nesta execução. Desligar no fim da
// tarefa fecha o processo; o arquivo de config não muda por causa de um
// enable temporário.
type Host struct {
	workspace string
	servers   []Server
	dial      func(ctx context.Context, server Server) (session, error)

	mu        sync.Mutex
	live      map[string]session
	tools     map[string][]RemoteTool
	status    map[string]string
	detail    map[string]string
	temporary map[string]bool
}

// Start lê a config e conecta apenas os servidores com Enabled. Falha de um
// servidor não impede os outros nem a execução.
func Start(ctx context.Context, workspace string) *Host {
	h := newHost(workspace, dialSDK)
	h.connectEnabled(ctx)
	return h
}

func newHost(workspace string, dial func(context.Context, Server) (session, error)) *Host {
	cfg, err := Load(workspace)
	h := &Host{
		workspace: workspace,
		dial:      dial,
		live:      map[string]session{},
		tools:     map[string][]RemoteTool{},
		status:    map[string]string{},
		detail:    map[string]string{},
		temporary: map[string]bool{},
	}
	if err != nil {
		h.detail["*"] = err.Error()
		return h
	}
	h.servers = cfg.Servers
	for _, s := range h.servers {
		if s.Enabled {
			h.status[s.Name] = "pending"
		} else {
			h.status[s.Name] = "off"
		}
	}
	return h
}

func (h *Host) connectEnabled(ctx context.Context) {
	if h == nil {
		return
	}
	h.mu.Lock()
	servers := cloneServers(h.servers)
	h.mu.Unlock()
	for _, s := range servers {
		if !s.Enabled {
			continue
		}
		h.connect(ctx, s, false)
	}
}

func (h *Host) connect(ctx context.Context, s Server, temporary bool) error {
	if other := exclusivePartner(s.Name); other != "" && h.isLive(other) {
		err := fmt.Errorf("não dá para ligar %s junto com %s", s.Name, other)
		h.setStatus(s.Name, "off", err.Error())
		return err
	}
	expanded, secretsOK := expandServer(cloneServers([]Server{s})[0], h.workspace)
	if !secretsOK {
		msg := "falta variável de ambiente referenciada na configuração (nada foi gravado no Git)"
		h.setStatus(s.Name, "needs-secret", msg)
		return fmt.Errorf("%s: %s", s.Name, msg)
	}
	if err := validateLocal(expanded); err != nil {
		h.setStatus(s.Name, "error", err.Error())
		return err
	}
	if expanded.Env == nil {
		expanded.Env = map[string]string{}
	}
	if strings.TrimSpace(h.workspace) != "" {
		expanded.Env["HIVEMIND_MCP_CWD"] = h.workspace
	}
	sess, err := h.dial(ctx, expanded)
	if err != nil {
		h.setStatus(s.Name, "error", err.Error())
		return err
	}
	tools, err := sess.Tools(ctx)
	if err != nil {
		_ = sess.Close()
		h.setStatus(s.Name, "error", err.Error())
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if prev := h.live[s.Name]; prev != nil {
		_ = prev.Close()
	}
	h.live[s.Name] = sess
	h.tools[s.Name] = tools
	h.status[s.Name] = "ready"
	h.detail[s.Name] = ""
	if temporary {
		h.temporary[s.Name] = true
		h.detail[s.Name] = "ligado só nesta tarefa"
	}
	return nil
}

// EnableForTask liga um servidor só nesta execução, sem gravar a preferência.
// No fim, Close derruba o processo.
func (h *Host) EnableForTask(ctx context.Context, name string) (string, error) {
	if h == nil {
		return "", fmt.Errorf("MCP não está ativo nesta execução")
	}
	name = strings.TrimSpace(name)
	s := h.server(name)
	if s == nil {
		return "", fmt.Errorf("servidor MCP desconhecido: %s", name)
	}
	if h.isLive(name) {
		return fmt.Sprintf("%s já está ligado nesta execução", name), nil
	}
	temporary := !s.Enabled
	if err := h.connect(ctx, *s, temporary); err != nil {
		return "", err
	}
	if temporary {
		return fmt.Sprintf("%s ligado só nesta tarefa. As ferramentas entram por mcp_call. Ao terminar, o servidor desliga.", name), nil
	}
	return fmt.Sprintf("%s ligado nesta execução.", name), nil
}

// Call invoca uma ferramenta. Servidor desligado não é chamado.
func (h *Host) Call(ctx context.Context, serverName, toolName string, args map[string]any) (string, error) {
	if h == nil {
		return "", fmt.Errorf("MCP não está ativo nesta execução")
	}
	h.mu.Lock()
	sess := h.live[serverName]
	h.mu.Unlock()
	if sess == nil {
		return "", fmt.Errorf("servidor %s está desligado; use mcp_enable_server se a tarefa precisar dele", serverName)
	}
	return sess.Call(ctx, toolName, args)
}

// ExposedTools devolve só ferramentas de servidores ligados agora.
func (h *Host) ExposedTools() []RemoteTool {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []RemoteTool
	for _, s := range h.servers {
		out = append(out, h.tools[s.Name]...)
	}
	return out
}

// Snapshot lista todos os servidores. Desligados não trazem ferramentas.
func (h *Host) Snapshot() []ServerView {
	if h == nil {
		cfg, err := Load("")
		if err != nil {
			return nil
		}
		return viewsFrom(cfg.Servers, nil, nil, nil)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return viewsFrom(h.servers, h.status, h.detail, h.tools)
}

// Describe lista servidores sem conectar. A IDE usa isto no painel.
func Describe(cfg File) []ServerView {
	return viewsFrom(cfg.Servers, nil, nil, nil)
}

func viewsFrom(servers []Server, status, detail map[string]string, tools map[string][]RemoteTool) []ServerView {
	out := make([]ServerView, 0, len(servers))
	for _, s := range servers {
		view := ServerView{
			Name:        s.Name,
			Description: s.Description,
			Transport:   s.Transport,
			Enabled:     s.Enabled,
			Builtin:     s.Builtin,
			Status:      "off",
		}
		if status != nil && status[s.Name] != "" {
			view.Status = status[s.Name]
		} else if s.Enabled {
			view.Status = "pending"
		}
		if detail != nil {
			view.Detail = detail[s.Name]
		}
		if view.Status == "ready" {
			view.Enabled = true
			for _, t := range tools[s.Name] {
				view.Tools = append(view.Tools, t.Name)
			}
		}
		out = append(out, view)
	}
	return out
}

// Close encerra os processos desta execução. Enable temporário não fica gravado.
func (h *Host) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var first error
	for name, sess := range h.live {
		if err := sess.Close(); err != nil && first == nil {
			first = err
		}
		delete(h.live, name)
		delete(h.tools, name)
		if h.temporary[name] {
			delete(h.temporary, name)
			h.status[name] = "off"
		}
	}
	return first
}

func (h *Host) server(name string) *Server {
	h.mu.Lock()
	defer h.mu.Unlock()
	return findServer(h.servers, name)
}

func (h *Host) isLive(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.live[name]
	return ok
}

func (h *Host) setStatus(name, status, detail string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status[name] = status
	h.detail[name] = detail
}

// ParseArgs interpreta o JSON de argumentos de mcp_call. Vazio vira mapa vazio.
func ParseArgs(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("arguments precisa ser um objeto JSON: %w", err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}
