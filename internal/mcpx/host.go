package mcpx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// lookPath resolve o binário do servidor stdio. Testes substituem.
var lookPath = exec.LookPath

// Bounding for one tool call against a remote server. A stalled MCP process
// (deadlock, network hang, infinite loop in the server) must fail the call
// instead of holding the agent loop indefinitely.
const (
	// DefaultCallTimeout bounds one tool call when the server config does not
	// override it.
	DefaultCallTimeout = 120 * time.Second
	minCallTimeout     = 5 * time.Second
	maxCallTimeout     = 600 * time.Second
)

// callTimeoutFor clamps one server's per-call timeout override. Zero or
// negative values fall back to the default; anything above the ceiling is
// capped so a bad override cannot disable bounding entirely.
func callTimeoutFor(s *Server) time.Duration {
	if s == nil || s.TimeoutSeconds <= 0 {
		return DefaultCallTimeout
	}
	d := time.Duration(s.TimeoutSeconds) * time.Second
	if d < minCallTimeout {
		return minCallTimeout
	}
	if d > maxCallTimeout {
		return maxCallTimeout
	}
	return d
}

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
	for _, other := range exclusivePartners(s.Name) {
		if h.isLive(other) {
			err := fmt.Errorf("não dá para ligar %s junto com %s", s.Name, other)
			h.setStatus(s.Name, "off", err.Error())
			return err
		}
	}
	expanded, missing := expandServer(cloneServers([]Server{s})[0], h.workspace)
	if len(missing) > 0 {
		msg := secretMessage(missing)
		h.setStatus(s.Name, "needs-secret", msg)
		return fmt.Errorf("%s: %s", s.Name, msg)
	}
	if err := validateLocal(expanded); err != nil {
		h.setStatus(s.Name, "error", err.Error())
		return err
	}
	var sess session
	var err error
	if expanded.Transport == TransportInProc {
		sess, err = openInProc(ctx, expanded)
	} else {
		if expanded.Transport == TransportStdio {
			cmdName := strings.TrimSpace(expanded.Command)
			if cmdName == "" {
				err := fmt.Errorf("servidor %s sem comando", s.Name)
				h.setStatus(s.Name, "error", err.Error())
				return err
			}
			if _, err := lookPath(cmdName); err != nil {
				msg := missingCommandDetail(expanded)
				h.setStatus(s.Name, "unavailable", msg)
				return fmt.Errorf("%s: %s", s.Name, msg)
			}
		}
		if expanded.Env == nil {
			expanded.Env = map[string]string{}
		}
		if strings.TrimSpace(h.workspace) != "" {
			expanded.Env["HIVEMIND_MCP_CWD"] = h.workspace
		}
		sess, err = h.dial(ctx, expanded)
	}
	if err != nil {
		msg := dialFailureDetail(s, err)
		h.setStatus(s.Name, "error", msg)
		return fmt.Errorf("%s: %s", s.Name, msg)
	}
	tools, err := sess.Tools(ctx)
	if err != nil {
		_ = sess.Close()
		msg := dialFailureDetail(s, err)
		h.setStatus(s.Name, "error", msg)
		return fmt.Errorf("%s: %s", s.Name, msg)
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
	} else if s.Name == "context7" && strings.TrimSpace(os.Getenv("CONTEXT7_API_KEY")) == "" {
		h.detail[s.Name] = "conectado sem CONTEXT7_API_KEY. Defina a variável se o servidor recusar ou se quiser cota maior."
	}
	return nil
}

func dialFailureDetail(s Server, err error) string {
	msg := redactSecrets(err.Error())
	if s.Name == "code-review-graph" {
		msg += " Confira se uvx e Python 3.10+ estão instalados. O servidor não ficou ligado."
	}
	if s.Name == "context7" && strings.TrimSpace(os.Getenv("CONTEXT7_API_KEY")) == "" {
		msg = "não conectou sem chave. Defina CONTEXT7_API_KEY. " + msg
	}
	return msg
}

func missingCommandDetail(s Server) string {
	if s.Name == "code-review-graph" || strings.EqualFold(strings.TrimSpace(s.Command), "uvx") {
		return "uvx não está no PATH. O Code Review Graph precisa de uv e Python 3.10+ (https://docs.astral.sh/uv/). Nesta execução ele fica desligado; instale o uv ou desligue o servidor no painel MCP."
	}
	return fmt.Sprintf("comando %q não está no PATH. O servidor fica desligado nesta execução.", s.Command)
}

// ArmHiveBrowser liga a extensão do Chrome nesta tarefa. Playwright e
// chrome-devtools, se estiverem no ar, saem até o restore. A preferência gravada não muda.
func (h *Host) ArmHiveBrowser(ctx context.Context) (func(), error) {
	if h == nil {
		return nil, fmt.Errorf("MCP não está ativo nesta execução")
	}
	var parked []string
	for _, other := range exclusivePartners("hivemind-browser") {
		if h.isLive(other) {
			h.dropLive(other)
			parked = append(parked, other)
		}
	}
	if _, err := h.EnableForTask(ctx, "hivemind-browser"); err != nil {
		h.restoreParked(context.Background(), parked)
		return nil, err
	}
	return func() {
		h.dropLive("hivemind-browser")
		h.restoreParked(context.Background(), parked)
	}, nil
}

func (h *Host) dropLive(name string) {
	h.mu.Lock()
	sess := h.live[name]
	delete(h.live, name)
	delete(h.tools, name)
	temp := h.temporary[name]
	if temp {
		delete(h.temporary, name)
	}
	h.status[name] = "off"
	if sess != nil && !temp {
		h.detail[name] = "pausado nesta tarefa"
	} else {
		h.detail[name] = ""
	}
	h.mu.Unlock()
	if sess != nil {
		_ = sess.Close()
	}
}

func (h *Host) restoreParked(ctx context.Context, names []string) {
	for _, name := range names {
		s := h.server(name)
		if s == nil || !s.Enabled {
			continue
		}
		_ = h.connect(ctx, *s, false)
	}
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

// Call invoca uma ferramenta. Servidor desligado não é chamado. Cada chamada
// tem um teto de tempo (timeout_seconds do servidor, default 120s, teto
// 600s): um servidor MCP travado devolve erro em vez de segurar a rodada do
// agente indefinidamente.
func (h *Host) Call(ctx context.Context, serverName, toolName string, args map[string]any) (string, error) {
	if h == nil {
		return "", fmt.Errorf("MCP não está ativo nesta execução")
	}
	h.mu.Lock()
	sess := h.live[serverName]
	var srv *Server
	for i := range h.servers {
		if h.servers[i].Name == serverName {
			srv = &h.servers[i]
			break
		}
	}
	h.mu.Unlock()
	if sess == nil {
		return "", fmt.Errorf("servidor %s está desligado; use mcp_enable_server se a tarefa precisar dele", serverName)
	}
	callCtx, cancel := context.WithTimeout(ctx, callTimeoutFor(srv))
	defer cancel()
	return sess.Call(callCtx, toolName, args)
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
	views := viewsFrom(cfg.Servers, nil, nil, nil)
	for i := range views {
		if i >= len(cfg.Servers) {
			continue
		}
		s := cfg.Servers[i]
		if s.Enabled && s.Name == "context7" && strings.TrimSpace(os.Getenv("CONTEXT7_API_KEY")) == "" && views[i].Detail == "" {
			views[i].Detail = "sem CONTEXT7_API_KEY: a conexão tenta sem chave. Se falhar, defina CONTEXT7_API_KEY."
		}
		if s.Enabled && s.Transport == TransportStdio {
			if cmdName := strings.TrimSpace(s.Command); cmdName != "" {
				if _, err := lookPath(cmdName); err != nil {
					views[i].Status = "unavailable"
					views[i].Detail = missingCommandDetail(s)
				}
			}
		}
		// Sem credencial o connect recusa. A lista não pode dizer "ligado" antes disso.
		if views[i].Status == "pending" {
			if _, missing := expandServer(cloneServers([]Server{s})[0], ""); len(missing) > 0 {
				views[i].Status = "needs-secret"
				views[i].Detail = secretMessage(missing)
			}
		}
	}
	return views
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
	h.detail[name] = redactSecrets(detail)
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
