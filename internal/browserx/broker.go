package browserx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

var errInUse = errors.New("porta do Chrome ocupada")

// ChatHandler recebe o texto do side panel. A IDE e o `hivemind serve`
// registram o deles. Sem handler, o painel ouve que ninguém está no comando.
type ChatHandler func(text string) (string, error)

// Bridge é quem entrega uma ferramenta à extensão.
type Bridge interface {
	Call(ctx context.Context, tool string, args map[string]any) (Message, error)
	Healthy() bool
	ExtensionOn() bool
	Port() int
	Close() error
}

type extLink struct {
	c  *websocket.Conn
	mu sync.Mutex
}

func (e *extLink) write(ctx context.Context, m Message) error {
	data, err := json.Marshal(m.public())
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.c.Write(ctx, websocket.MessageText, data)
}

// Broker escuta só em 127.0.0.1. A extensão (ou o host nativo, no papel dela)
// é o único executor. Outro processo hivemind entra como "agent".
type Broker struct {
	token string

	mu         sync.Mutex
	port       int
	srv        *http.Server
	ext        *extLink
	pending    map[string]chan Message
	cancelCall context.CancelFunc
	cancelID   string
	chat       ChatHandler
}

func newBroker(cfg Config) *Broker {
	return &Broker{
		token:   cfg.Token,
		port:    cfg.Port,
		pending: map[string]chan Message{},
	}
}

func (b *Broker) Port() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.port
}

func (b *Broker) Healthy() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.srv != nil
}

func (b *Broker) ExtensionOn() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ext != nil
}

func (b *Broker) SetChatHandler(fn ChatHandler) {
	b.mu.Lock()
	b.chat = fn
	b.mu.Unlock()
}

// PushActivity envia um aviso curto de andamento ao vivo para o side panel do Chrome, se conectado.
func (b *Broker) PushActivity(text string) {
	clean := strings.TrimSpace(text)
	if clean == "" {
		return
	}
	b.mu.Lock()
	link := b.ext
	b.mu.Unlock()
	if link == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_ = link.write(ctx, Message{Type: "activity", Text: truncate(Redact(clean, b.token), 180)})
}

func (b *Broker) start() error {
	if strings.TrimSpace(b.token) == "" {
		return fmt.Errorf("ponte sem token")
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(b.port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if isInUse(err) {
			return errInUse
		}
		return err
	}
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok || tcp.IP == nil || !tcp.IP.IsLoopback() {
		_ = ln.Close()
		return fmt.Errorf("a ponte do Chrome recusou um bind que não é loopback")
	}
	b.mu.Lock()
	b.port = tcp.Port
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", b.handleHealth)
	mux.HandleFunc("GET /browser", b.serveWS)
	b.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	srv := b.srv
	b.mu.Unlock()
	go func() { _ = srv.Serve(ln) }()
	return nil
}

func (b *Broker) handleHealth(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !isLoopback(host) {
		http.Error(w, "só loopback", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"service":"%s","extension":%t}`, serviceName, b.ExtensionOn())
}

func (b *Broker) serveWS(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !isLoopback(host) {
		http.Error(w, "só loopback", http.StatusForbidden)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)
	helloCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	_, data, err := conn.Read(helloCtx)
	cancel()
	if err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "")
		return
	}
	var hello Message
	if json.Unmarshal(data, &hello) != nil || hello.Type != "hello" || !tokenOK(hello.Token, b.token) {
		_ = writeMsg(r.Context(), conn, Message{Type: "hello", OK: false, Text: "não autorizado"})
		_ = conn.Close(websocket.StatusPolicyViolation, "")
		return
	}
	var extLinkRef *extLink
	if hello.Role == "extension" {
		extLinkRef = &extLink{c: conn}
	}
	if err := writeMsg(r.Context(), conn, Message{Type: "hello", OK: true, Role: hello.Role}); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "")
		return
	}
	if extLinkRef != nil {
		b.mu.Lock()
		old := b.ext
		b.ext = extLinkRef
		b.mu.Unlock()
		if old != nil {
			_ = old.c.Close(websocket.StatusGoingAway, "")
		}
	}
	switch hello.Role {
	case "extension":
		b.readExtension(r.Context(), conn)
	case "agent":
		b.readAgent(r.Context(), conn)
	default:
		_ = conn.Close(websocket.StatusPolicyViolation, "")
	}
}

func writeMsg(ctx context.Context, conn *websocket.Conn, m Message) error {
	return writeRaw(ctx, conn, m.public())
}

func writeRaw(ctx context.Context, conn *websocket.Conn, m Message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

func (b *Broker) readExtension(ctx context.Context, conn *websocket.Conn) {
	b.mu.Lock()
	link := b.ext
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		if b.ext != nil && b.ext.c == conn {
			b.ext = nil
		}
		pending := b.pending
		b.pending = make(map[string]chan Message)
		cancel := b.cancelCall
		b.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		for id, ch := range pending {
			select {
			case ch <- Message{Type: "result", ID: id, OK: false, Text: "extensão do Chrome desconectou"}:
			default:
			}
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var m Message
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Type {
		case "result":
			b.deliver(m)
		case "stop":
			b.cancelInflight()
		case "chat":
			if link != nil {
				go b.handleChat(link, m.Text)
			}
		case "allow_host":
			if host := strings.TrimSpace(strings.ToLower(m.Host)); httpHostOK(host) && !allowHost(host) {
				_ = rememberHost(host)
			}
		}
	}
}

func (b *Broker) deliver(m Message) {
	b.mu.Lock()
	ch := b.pending[m.ID]
	b.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- m:
	default:
	}
}

func (b *Broker) cancelInflight() {
	b.mu.Lock()
	cancel := b.cancelCall
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (b *Broker) handleChat(link *extLink, text string) {
	defer func() { _ = recover() }()
	text = strings.TrimSpace(text)
	reply := "Abra a IDE ou rode hivemind serve para o Agent Hive receber o chat."
	var err error
	b.mu.Lock()
	fn := b.chat
	b.mu.Unlock()
	if text == "" {
		reply = "mensagem vazia"
	} else if fn != nil {
		reply, err = fn(truncate(text, 8000))
		if err != nil {
			reply = Redact(err.Error(), b.token)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	reply = Redact(reply, b.token)
	code := ""
	if err != nil {
		code = "error"
	}
	runes := []rune(reply)
	if len(runes) > 80 {
		const step = 48
		for i := 0; i < len(runes); i += step {
			end := i + step
			if end > len(runes) {
				end = len(runes)
			}
			_ = link.write(ctx, Message{Type: "chat_delta", Text: string(runes[i:end])})
		}
	}
	_ = link.write(ctx, Message{Type: "chat_reply", Text: reply, Code: code})
}

func httpHostOK(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, r := range host {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '-' {
			return false
		}
	}
	return host == "localhost" || strings.Contains(host, ".")
}

// Push manda um quadro para o side panel. O teste de demonstração usa isto.
func (b *Broker) Push(ctx context.Context, m Message) error {
	b.mu.Lock()
	link := b.ext
	b.mu.Unlock()
	if link == nil {
		return fmt.Errorf("extensão do Chrome não está conectada")
	}
	m.Token = ""
	return link.write(ctx, m)
}

func (b *Broker) readAgent(ctx context.Context, conn *websocket.Conn) {
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var m Message
		if json.Unmarshal(data, &m) != nil || m.Type != "call" {
			continue
		}
		res, callErr := b.Call(ctx, m.Tool, m.Args)
		if callErr != nil {
			res = Message{OK: false, Text: Redact(callErr.Error(), b.token)}
		}
		res.Type = "result"
		res.ID = m.ID
		res.Token = ""
		if err := writeMsg(ctx, conn, res); err != nil {
			return
		}
	}
}

// AnnounceUser mostra uma fala do usuário no side panel já conectado.
func (b *Broker) AnnounceUser(ctx context.Context, text string) error {
	b.mu.Lock()
	link := b.ext
	b.mu.Unlock()
	if link == nil {
		return fmt.Errorf("extensão do Chrome não está conectada")
	}
	return link.write(ctx, Message{Type: "user_line", Text: text})
}

// Call manda a ferramenta para a extensão e espera o resultado.
func (b *Broker) Call(ctx context.Context, tool string, args map[string]any) (Message, error) {
	if strings.TrimSpace(tool) == "" {
		return Message{}, fmt.Errorf("ferramenta vazia")
	}
	id := newID()
	ch := make(chan Message, 1)
	callCtx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	link := b.ext
	if link == nil {
		b.mu.Unlock()
		cancel()
		return Message{}, fmt.Errorf("extensão do Chrome não está conectada. Rode hivemind browser install e abra o side panel")
	}
	b.pending[id] = ch
	b.cancelCall = cancel
	b.cancelID = id
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, id)
		if b.cancelID == id {
			b.cancelCall = nil
			b.cancelID = ""
		}
		b.mu.Unlock()
		cancel()
	}()
	if err := link.write(callCtx, Message{Type: "activity", Text: activityText(tool, args)}); err != nil {
		return Message{}, fmt.Errorf("extensão do Chrome caiu")
	}
	if err := link.write(callCtx, Message{Type: "call", ID: id, Tool: tool, Args: args}); err != nil {
		return Message{}, fmt.Errorf("extensão do Chrome caiu")
	}
	select {
	case <-callCtx.Done():
		abort, cancelAbort := context.WithTimeout(context.Background(), time.Second)
		_ = link.write(abort, Message{Type: "abort", ID: id})
		cancelAbort()
		if errors.Is(callCtx.Err(), context.Canceled) && ctx.Err() == nil {
			return Message{}, fmt.Errorf("interrompido pelo botão Parar da extensão")
		}
		return Message{}, callCtx.Err()
	case res := <-ch:
		res.Text = Redact(res.Text, b.token)
		if res.Image != "" && len(res.Image) > 700_000 {
			res.Image = ""
			res.Text = strings.TrimSpace(res.Text + "\nscreenshot grande demais e foi omitido")
		}
		return res, nil
	}
}

func (b *Broker) Close() error {
	b.mu.Lock()
	srv := b.srv
	ext := b.ext
	b.srv = nil
	b.ext = nil
	cancel := b.cancelCall
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if ext != nil {
		_ = ext.c.Close(websocket.StatusGoingAway, "")
	}
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	return nil
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func isInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

func isLoopback(host string) bool {
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// probe diz se quem está na porta é a nossa ponte, sem revelar o token.
func probe(port int) bool {
	ok, _ := probeHealth(port)
	return ok
}

func probeHealth(port int) (ok bool, extensionOn bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/health", port), nil)
	if err != nil {
		return false, false
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, false
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), serviceName) {
		return false, false
	}
	var payload struct {
		Extension bool `json:"extension"`
	}
	_ = json.Unmarshal(body, &payload)
	return true, payload.Extension
}
