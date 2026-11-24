package browserx

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token == "" || cfg.Port != DefaultPort {
		t.Fatalf("config: %+v", cfg)
	}
	info, err := os.Stat(mustConfigPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("pareamento deveria ser 0600, é %o", info.Mode().Perm())
	}
	return cfg
}

func mustConfigPath(t *testing.T) string {
	t.Helper()
	p, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func startBroker(t *testing.T, cfg Config) *Broker {
	t.Helper()
	b := newBroker(cfg)
	b.port = 0
	if err := b.start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	if b.Port() <= 0 {
		t.Fatal("porta inválida")
	}
	var conn net.Conn
	var err error
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(b.Port()))
	for i := 0; i < 50; i++ {
		conn, err = net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	return b
}

func dialExt(t *testing.T, b *Broker, token string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://127.0.0.1:"+strconv.Itoa(b.Port())+"/browser", &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	conn.SetReadLimit(1 << 20)
	if err := writeRaw(ctx, conn, Message{Type: "hello", Role: "extension", Token: token}); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var hello Message
	if err := json.Unmarshal(data, &hello); err != nil || !hello.OK {
		t.Fatalf("hello: %s", data)
	}
	if strings.Contains(string(data), token) {
		t.Fatal("a resposta do hello não pode ecoar o token")
	}
	return conn
}

func TestHealthAndCallRoundTrip(t *testing.T) {
	cfg := testConfig(t)
	b := startBroker(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(b.Port())+"/health", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if strings.Contains(string(body), cfg.Token) || !strings.Contains(string(body), serviceName) {
		t.Fatalf("health: %s", body)
	}

	conn := dialExt(t, b, cfg.Token)
	errc := make(chan error, 1)
	go func() {
		_, data, err := conn.Read(context.Background())
		if err != nil {
			errc <- err
			return
		}
		_, data, err = conn.Read(context.Background())
		if err != nil {
			errc <- err
			return
		}
		var call Message
		if json.Unmarshal(data, &call) != nil || call.Tool != "read_page" {
			errc <- errString("call inesperada")
			return
		}
		raw, _ := json.Marshal(Message{Type: "result", ID: call.ID, OK: true, Text: "- e1 button \"Ok\""})
		errc <- conn.Write(context.Background(), websocket.MessageText, raw)
	}()
	callCtx, callCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer callCancel()
	resMsg, err := b.Call(callCtx, "read_page", map[string]any{})
	if err != nil || !resMsg.OK || !strings.Contains(resMsg.Text, "e1") {
		t.Fatalf("call: %+v %v", resMsg, err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestBadTokenIsRejectedWithoutEcho(t *testing.T) {
	cfg := testConfig(t)
	b := startBroker(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://127.0.0.1:"+strconv.Itoa(b.Port())+"/browser", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	bogus := strings.Repeat("cd", 32)
	if err := writeRaw(ctx, conn, Message{Type: "hello", Role: "extension", Token: bogus}); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err == nil && strings.Contains(string(data), bogus) {
		t.Fatal("token recusado foi ecoado")
	}
}

func TestStopCancelsInflight(t *testing.T) {
	cfg := testConfig(t)
	b := startBroker(t, cfg)
	conn := dialExt(t, b, cfg.Token)
	go func() {
		_, data, _ := conn.Read(context.Background())
		_ = data
		_, _, _ = conn.Read(context.Background())
		raw, _ := json.Marshal(Message{Type: "stop"})
		_ = conn.Write(context.Background(), websocket.MessageText, raw)
	}()
	_, err := b.Call(context.Background(), "wait", map[string]any{"ms": 1000})
	if err == nil || !strings.Contains(err.Error(), "Parar") {
		t.Fatalf("esperava interrupção, veio %v", err)
	}
}

func TestNativeHostRelays(t *testing.T) {
	cfg := testConfig(t)
	b := startBroker(t, cfg)
	cfg.Port = b.Port()
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	hostRead, testWrite := io.Pipe()
	testRead, hostWrite := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- serveNative(ctx, hostRead, hostWrite) }()

	deadline := time.Now().Add(3 * time.Second)
	for !b.ExtensionOn() {
		if time.Now().After(deadline) {
			t.Fatal("host nativo não conectou")
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := make(chan Message, 1)
	go func() {
		res, err := b.Call(context.Background(), "tabs_list", map[string]any{})
		if err != nil {
			got <- Message{Text: err.Error()}
			return
		}
		got <- res
	}()
	frame, err := readFrame(testRead)
	if err != nil {
		t.Fatal(err)
	}
	var msg Message
	if json.Unmarshal(frame, &msg) != nil {
		t.Fatalf("quadro: %s", frame)
	}
	if msg.Type == "activity" {
		frame, err = readFrame(testRead)
		if err != nil {
			t.Fatal(err)
		}
		if json.Unmarshal(frame, &msg) != nil {
			t.Fatalf("quadro: %s", frame)
		}
	}
	if msg.Tool != "tabs_list" || strings.Contains(string(frame), cfg.Token) {
		t.Fatalf("ferramenta: %s", frame)
	}
	ack, _ := json.Marshal(Message{Type: "result", ID: msg.ID, OK: true, Text: "1 aba"})
	if err := writeFrame(testWrite, ack); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-got:
		if !res.OK || res.Text != "1 aba" {
			t.Fatalf("resultado: %+v", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
	cancel()
	_ = hostRead.Close()
	_ = testWrite.Close()
}

func TestFramesRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte(`{"type":"ping","text":"ok"}`)
	if err := writeFrame(&buf, payload); err != nil {
		t.Fatal(err)
	}
	got, err := readFrame(&buf)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("frame: %q %v", got, err)
	}
}

func TestInstallWritesManifestWithoutToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)
	exe := filepath.Join(home, "hivemind")
	if err := os.WriteFile(exe, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	msg, err := Install(exe)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "token") {
		t.Fatalf("mensagem: %s", msg)
	}
	path := filepath.Join(home, "google-chrome", "NativeMessagingHosts", NativeHostName+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "token") || !strings.Contains(string(data), ExtensionID) {
		t.Fatalf("manifesto: %s", data)
	}
	var m hostManifest
	if json.Unmarshal(data, &m) != nil || m.Type != "stdio" || !strings.HasSuffix(m.Path, "hivemind-browser-host") {
		t.Fatalf("manifesto: %+v", m)
	}
}

func TestCallRequiresApprovalAndStripsForgedFlag(t *testing.T) {
	cfg := testConfig(t)
	br := &seqBridge{}
	SetBridgeForTest(t, br)
	ctx := context.Background()
	if _, err := callFromAgent(ctx, "javascript_exec", map[string]any{"expression": "1", "_approved": true}); err == nil {
		t.Fatal("javascript sem aprovador tem de ser recusado, mesmo com a flag forjada")
	}
	if br.calls != 0 {
		t.Fatal("a extensão não pode receber javascript recusado")
	}
	asked := 0
	ctx = WithApprover(ctx, func(string) bool {
		asked++
		return false
	})
	if _, err := callFromAgent(ctx, "click", map[string]any{"ref": "e1", "submit": true}); err == nil || asked != 1 {
		t.Fatalf("click de envio: asked=%d err=%v", asked, err)
	}

	ctx = WithApprover(context.Background(), func(string) bool { return true })
	br.replies = []Message{{OK: true, Text: "feito"}}
	text, err := callFromAgent(ctx, "javascript_exec", map[string]any{"expression": "document.title"})
	if err != nil || text != "feito" || br.calls != 1 {
		t.Fatalf("aprovado: %q %v calls=%d", text, err, br.calls)
	}
	if br.last["_approved"] != true {
		t.Fatalf("flag: %#v", br.last)
	}

	br.calls = 0
	br.replies = nil
	if _, err := callFromAgent(ctx, "navigate", map[string]any{"url": "file:///etc/passwd"}); err == nil {
		t.Fatal("file:// tem de ser recusado")
	}
	if _, err := callFromAgent(ctx, "navigate", map[string]any{"url": "https://user:pw@example.com"}); err == nil {
		t.Fatal("url com senha tem de ser recusada")
	}
	text, err = callFromAgent(ctx, "navigate", map[string]any{"url": "https://example.com/path"})
	if err != nil || text != "feito" {
		t.Fatalf("navigate: %q %v", text, err)
	}
	if !allowHost("example.com") {
		t.Fatal("host aprovado deveria entrar na lista")
	}
	raw, _ := os.ReadFile(mustConfigPath(t))
	if strings.Contains(string(raw), "pw@") {
		t.Fatal("a config não pode guardar segredo da url")
	}
	_ = cfg
}

func TestExtensionCanAskApprovalAgain(t *testing.T) {
	testConfig(t)
	br := &seqBridge{replies: []Message{
		{OK: false, Code: "needs_approval", Text: "o botão diz Comprar"},
		{OK: true, Text: "clicou"},
	}}
	SetBridgeForTest(t, br)
	asked := 0
	ctx := WithApprover(context.Background(), func(prompt string) bool {
		asked++
		return strings.Contains(prompt, "Comprar")
	})
	text, err := callFromAgent(ctx, "click", map[string]any{"ref": "e3"})
	if err != nil || text != "clicou" || asked != 1 || br.calls != 2 {
		t.Fatalf("text=%q err=%v asked=%d calls=%d", text, err, asked, br.calls)
	}
}

func TestRedactTokenAndBearer(t *testing.T) {
	token := strings.Repeat("ab", 32)
	got := Redact("Authorization: Bearer sk-test-fake-0000 token="+token, token)
	if strings.Contains(got, token) || strings.Contains(got, "sk-test") {
		t.Fatalf("vazou: %s", got)
	}
}

type seqBridge struct {
	replies []Message
	calls   int
	last    map[string]any
}

func (s *seqBridge) Call(_ context.Context, _ string, args map[string]any) (Message, error) {
	s.calls++
	s.last = args
	if len(s.replies) == 0 {
		return Message{OK: true, Text: "feito"}, nil
	}
	i := s.calls - 1
	if i >= len(s.replies) {
		i = len(s.replies) - 1
	}
	return s.replies[i], nil
}

func (s *seqBridge) Healthy() bool     { return true }
func (s *seqBridge) ExtensionOn() bool { return true }
func (s *seqBridge) Port() int         { return 1 }
func (s *seqBridge) Close() error      { return nil }
