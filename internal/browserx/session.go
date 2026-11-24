package browserx

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/gilvan-Borges/HiveMind/internal/mcpx"
)

type approverKey struct{}

// WithApprover leva a mesma aprovação da IDE, do terminal e do app Android
// até a ferramenta. Sem ela, ação sensível é recusada.
func WithApprover(ctx context.Context, fn func(prompt string) bool) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, approverKey{}, fn)
}

func approverFrom(ctx context.Context) func(string) bool {
	fn, _ := ctx.Value(approverKey{}).(func(string) bool)
	return fn
}

var (
	gate    sync.Mutex
	current Bridge
	chatFn  ChatHandler
)

// SetChatHandler vale para a ponte deste processo. A IDE e o serve usam isto
// para transformar o side panel em turno do Agent Hive.
func SetChatHandler(fn ChatHandler) {
	gate.Lock()
	chatFn = fn
	if b, ok := current.(*Broker); ok {
		b.SetChatHandler(fn)
	}
	gate.Unlock()
}

// Listen sobe a ponte em 127.0.0.1 ou, se outro HiveMind já é o dono, entra
// como cliente. Nunca escuta em outra interface.
func Listen(ctx context.Context) (Bridge, error) {
	gate.Lock()
	defer gate.Unlock()
	if current != nil && current.Healthy() {
		return current, nil
	}
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	b := newBroker(cfg)
	b.SetChatHandler(chatFn)
	if err := b.start(); err != nil {
		if err == errInUse && probe(cfg.Port) {
			c, derr := dialAgent(ctx, cfg.Port, cfg.Token)
			if derr != nil {
				return nil, derr
			}
			current = c
			return c, nil
		}
		if err == errInUse {
			return nil, fmt.Errorf("a porta 127.0.0.1:%d está ocupada por outro programa", cfg.Port)
		}
		return nil, err
	}
	current = b
	return b, nil
}

// Close encerra a ponte deste processo.
func Close() error {
	gate.Lock()
	cur := current
	current = nil
	gate.Unlock()
	if cur == nil {
		return nil
	}
	return cur.Close()
}

// Status não inclui o token.
func Status() (port int, listening, extension bool, hosts []string, err error) {
	cfg, err := LoadConfig()
	if err != nil {
		return 0, false, false, nil, err
	}
	gate.Lock()
	cur := current
	gate.Unlock()
	if cur != nil && cur.Healthy() {
		return cur.Port(), true, cur.ExtensionOn(), cfg.AllowedHosts, nil
	}
	if probe(cfg.Port) {
		return cfg.Port, true, false, cfg.AllowedHosts, nil
	}
	return cfg.Port, false, false, cfg.AllowedHosts, nil
}

// SetBridgeForTest troca a ponte do processo e devolve a anterior no cleanup.
func SetBridgeForTest(t interface{ Cleanup(func()) }, b Bridge) {
	gate.Lock()
	prev := current
	current = b
	gate.Unlock()
	t.Cleanup(func() {
		gate.Lock()
		current = prev
		gate.Unlock()
	})
}

func allowHost(host string) bool {
	cfg, err := LoadConfig()
	if err != nil {
		return false
	}
	for _, h := range cfg.AllowedHosts {
		if h == host {
			return true
		}
	}
	return false
}

func rememberHost(host string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	cfg.AllowedHosts = append(cfg.AllowedHosts, host)
	return SaveConfig(cfg)
}

func stripApproved(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if k == "_approved" {
			continue
		}
		out[k] = v
	}
	return out
}

func callFromAgent(ctx context.Context, tool string, args map[string]any) (string, error) {
	tool = strings.TrimSpace(tool)
	args = stripApproved(args)
	needs, prompt, host, err := classify(tool, args, allowHost)
	if err != nil {
		return "", err
	}
	approved := false
	if needs {
		fn := approverFrom(ctx)
		if fn == nil || !fn(prompt) {
			return "", fmt.Errorf("permissão recusada para %s", tool)
		}
		approved = true
		if host != "" {
			if err := rememberHost(host); err != nil {
				return "", err
			}
		}
		args["_approved"] = true
	}
	br, err := bridgeFor(ctx)
	if err != nil {
		return "", err
	}
	res, err := br.Call(ctx, tool, args)
	if err != nil {
		return "", err
	}
	if res.Code == "needs_approval" && !approved {
		fn := approverFrom(ctx)
		why := res.Text
		if strings.TrimSpace(why) == "" {
			why = "A extensão pediu confirmação antes de agir no Chrome."
		}
		if fn == nil || !fn(why) {
			return "", fmt.Errorf("permissão recusada para %s", tool)
		}
		args["_approved"] = true
		res, err = br.Call(ctx, tool, args)
		if err != nil {
			return "", err
		}
	}
	if !res.OK {
		msg := strings.TrimSpace(res.Text)
		if msg == "" {
			msg = "a extensão recusou a ação"
		}
		return "", fmt.Errorf("%s", msg)
	}
	text := res.Text
	if res.Image != "" {
		text = strings.TrimSpace(text + "\ndata:image/jpeg;base64," + res.Image)
	}
	if strings.TrimSpace(text) == "" {
		text = "ok"
	}
	return text, nil
}

func bridgeFor(ctx context.Context) (Bridge, error) {
	gate.Lock()
	cur := current
	gate.Unlock()
	if cur != nil && cur.Healthy() {
		return cur, nil
	}
	return Listen(ctx)
}

func init() {
	mcpx.RegisterInProc(ServerName, func(context.Context) (mcpx.InProcHandler, error) {
		return mcpx.InProcHandler{
			Tools: func(context.Context) ([]mcpx.RemoteTool, error) { return Catalog(), nil },
			Call:  callFromAgent,
			Close: func() error { return nil },
		}, nil
	})
}

// Catalog é o esquema que entra no prompt quando o servidor está ligado.
func Catalog() []mcpx.RemoteTool {
	type row struct{ name, desc, schema string }
	rows := []row{
		{"navigate", "Abre uma URL http(s) na aba atual do Chrome real do usuário. Site fora da lista pede aprovação.", `{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`},
		{"read_page", "Lê a árvore de acessibilidade da aba e devolve refs e1, e2… para click, type e form_input.", `{"type":"object","properties":{"tabId":{"type":"number"}}}`},
		{"find", "Procura texto ou papel na última árvore lida e devolve as refs que casam.", `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`},
		{"click", "Clica numa ref da árvore ou em coordenadas CSS. Enviar, comprar ou pagar pede aprovação.", `{"type":"object","properties":{"ref":{"type":"string"},"x":{"type":"number"},"y":{"type":"number"},"submit":{"type":"boolean"},"name":{"type":"string"}}}`},
		{"type", "Foca a ref e insere texto com eventos reais de teclado. Não use para enviar o formulário.", `{"type":"object","properties":{"ref":{"type":"string"},"text":{"type":"string"}},"required":["text"]}`},
		{"form_input", "Preenche um campo. Com submit true, pede aprovação antes de enviar.", `{"type":"object","properties":{"ref":{"type":"string"},"value":{"type":"string"},"submit":{"type":"boolean"}},"required":["ref","value"]}`},
		{"scroll", "Rola a página com evento de roda do mouse.", `{"type":"object","properties":{"dx":{"type":"number"},"dy":{"type":"number"},"ref":{"type":"string"}}}`},
		{"key", "Aperta uma tecla (Enter, Tab, Escape, setas, ou um caractere).", `{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]}`},
		{"screenshot", "Captura a aba em JPEG para o agente ver a tela.", `{"type":"object"}`},
		{"tabs_list", "Lista as abas abertas.", `{"type":"object"}`},
		{"tabs_create", "Abre uma aba. URL nova pede aprovação.", `{"type":"object","properties":{"url":{"type":"string"}}}`},
		{"tabs_close", "Fecha uma aba pelo tabId.", `{"type":"object","properties":{"tabId":{"type":"number"}},"required":["tabId"]}`},
		{"tabs_switch", "Ativa uma aba pelo tabId.", `{"type":"object","properties":{"tabId":{"type":"number"}},"required":["tabId"]}`},
		{"console_read", "Devolve os últimos console logs da aba, sem segredos.", `{"type":"object"}`},
		{"network_read", "Devolve as últimas requisições (método, URL filtrada, status). Sem cookies nem Authorization.", `{"type":"object"}`},
		{"get_dom", "HTML externo do documento ou da ref, truncado.", `{"type":"object","properties":{"ref":{"type":"string"}}}`},
		{"computed_styles", "Estilos computados principais da ref (cor, fonte, display, caixa).", `{"type":"object","properties":{"ref":{"type":"string"}},"required":["ref"]}`},
		{"javascript_exec", "Executa JavaScript na página. Sempre pede aprovação. O resultado pode conter dado da página.", `{"type":"object","properties":{"expression":{"type":"string"}},"required":["expression"]}`},
		{"wait", "Espera milissegundos (teto 10s) ou até o texto aparecer na árvore.", `{"type":"object","properties":{"ms":{"type":"number"},"text":{"type":"string"}}}`},
	}
	out := make([]mcpx.RemoteTool, 0, len(rows))
	for _, r := range rows {
		out = append(out, mcpx.RemoteTool{Server: ServerName, Name: r.name, Description: r.desc, Schema: r.schema})
	}
	return out
}
