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

// CallTool pede uma ação à extensão do Chrome já conectada.
func CallTool(ctx context.Context, tool string, args map[string]any) (string, error) {
	return callFromAgent(ctx, tool, args)
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
	if tool == "screenshot" && res.Image != "" {
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
	// DevTools (console, rede, DOM, estilos, JavaScript, upload) não entram aqui.
	// A ferramenta devtools devolve o nome delas quando o agente pedir.
	rows := []row{
		{"navigate", "Abre http(s). Host novo pede aprovação.", `{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`},
		{"read_page", "Refs e1 do viewport. Igual se não mudou.", `{"type":"object","properties":{"view":{"type":"string"},"scope":{"type":"string"},"limit":{"type":"number"}}}`},
		{"find", "Até 6 refs.", `{"type":"object","properties":{"query":{"type":"string"},"role":{"type":"string"}},"required":["query"]}`},
		{"get_page_text", "Texto principal.", `{"type":"object","properties":{"limit":{"type":"number"}}}`},
		{"click", "Clica na ref. Comprar pede aprovação.", `{"type":"object","properties":{"ref":{"type":"string"}}}`},
		{"type", "Digita na ref.", `{"type":"object","properties":{"ref":{"type":"string"},"text":{"type":"string"}},"required":["text"]}`},
		{"form_input", "Preenche a ref. submit pede aprovação.", `{"type":"object","properties":{"ref":{"type":"string"},"value":{"type":"string"}},"required":["ref","value"]}`},
		{"scroll", "Rola.", `{"type":"object","properties":{"dy":{"type":"number"}}}`},
		{"key", "Tecla.", `{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]}`},
		{"screenshot", "JPEG da aba. quality e scale.", `{"type":"object","properties":{"quality":{"type":"number"},"scale":{"type":"number"}}}`},
		{"batch", "Até 15 ações numa chamada.", `{"type":"object","properties":{"actions":{"type":"array"}},"required":["actions"]}`},
		{"tabs", "list, create, close ou switch.", `{"type":"object","properties":{"action":{"type":"string"},"url":{"type":"string"},"tabId":{"type":"number"}}}`},
		{"wait", "Rede ociosa ou um texto.", `{"type":"object","properties":{"text":{"type":"string"}}}`},
		{"dialog", "alert/confirm. Aceitar pede aprovação.", `{"type":"object","properties":{"accept":{"type":"boolean"}}}`},
		{"pick", "A pessoa aponta um elemento.", `{"type":"object"}`},
		{"devtools", "Liga console, rede, DOM, estilos, JS e upload.", `{"type":"object"}`},
	}
	out := make([]mcpx.RemoteTool, 0, len(rows))
	for _, r := range rows {
		out = append(out, mcpx.RemoteTool{Server: ServerName, Name: r.name, Description: r.desc, Schema: r.schema})
	}
	return out
}
