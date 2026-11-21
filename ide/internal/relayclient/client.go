package relayclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/gilvan-Borges/HiveMind/ide/internal/remote"
	"github.com/gilvan-Borges/HiveMind/relaywire"
)

// Options wires the relay client to the local bridge.
type Options struct {
	ConfigPath string
	Handler    http.Handler
	// PrepareLocal marks the in-process hop. The LAN bridge rejects a clear bearer.
	PrepareLocal func(*http.Request) *http.Request
	Subscribe    func() (<-chan remote.StreamEvent, func())
	// Snapshot is the live lane state. It is sent inside a relay_gap event
	// when the local backlog had to drop events while the relay was down.
	Snapshot func() []remote.LaneSnapshot
}

// Status is the view shown in the IDE. It contains no secrets.
type Status struct {
	Enabled    bool   `json:"enabled"`
	LoggedIn   bool   `json:"loggedIn"`
	Email      string `json:"email"`
	Role       string `json:"role"`
	BaseURL    string `json:"baseUrl"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	Linked     bool   `json:"linked"`
	Online     bool   `json:"online"`
	LastError  string `json:"lastError"`
	E2E        bool   `json:"e2e"`
}

// LinkCode is a short-lived pairing code rendered as text and QR.
type LinkCode struct {
	Code      string `json:"code"`
	ExpiresAt string `json:"expiresAt"`
	URI       string `json:"uri"`
	QRDataURL string `json:"qrDataUrl"`
}

// Client keeps one outbound WebSocket to the relay and serves the local
// bridge to the selected phone.
type Client struct {
	opt Options

	mu       sync.Mutex
	cfg      Config
	access   string
	accessAt time.Time
	keys     *relaywire.KeyPair
	peer     [32]byte
	hasPeer  bool
	online   bool
	lastErr  string

	started   bool
	cancel    context.CancelFunc
	ring      *eventRing
	http      *http.Client
	refreshMu sync.Mutex
}

// Load reads the config and prepares the client. It does not dial the network.
func Load(opt Options) (*Client, error) {
	if opt.ConfigPath == "" {
		opt.ConfigPath = DefaultPath()
	}
	cfg, err := load(opt.ConfigPath)
	if err != nil {
		return nil, err
	}
	c := &Client{
		opt:  opt,
		cfg:  cfg,
		ring: newRing(),
		http: &http.Client{Timeout: 20 * time.Second},
	}
	if err := c.loadKeys(); err != nil {
		return nil, err
	}
	return c, nil
}

// Start dials the relay when remote access was left enabled.
func (c *Client) Start() {
	if c == nil {
		return
	}
	c.mu.Lock()
	on := c.cfg.Enabled && c.cfg.DeviceToken != ""
	c.mu.Unlock()
	if on {
		c.ensureLoop()
	}
}

// Stop closes the socket.
func (c *Client) Stop() {
	if c == nil {
		return
	}
	c.stopLoop()
}

// Status snapshots the UI state.
func (c *Client) Status() Status {
	if c == nil {
		return Status{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *Client) statusLocked() Status {
	return Status{
		Enabled:    c.cfg.Enabled,
		LoggedIn:   c.cfg.RefreshToken != "",
		Email:      c.cfg.Email,
		Role:       c.cfg.Role,
		BaseURL:    c.cfg.BaseURL,
		DeviceID:   c.cfg.DeviceID,
		DeviceName: c.cfg.DeviceName,
		Linked:     c.cfg.DeviceToken != "",
		Online:     c.online,
		LastError:  c.lastErr,
		E2E:        c.hasPeer && c.cfg.DeviceToken != "",
	}
}

// Login stores a refresh token. The password is not written to disk.
func (c *Client) Login(ctx context.Context, baseURL, email, password string) (Status, error) {
	base, err := normalizeBase(baseURL)
	if err != nil {
		return c.Status(), err
	}
	if err := c.ensureKeys(); err != nil {
		return c.Status(), err
	}
	var out tokenResponse
	if err := c.do(ctx, http.MethodPost, base, "/api/v1/auth/login", "", map[string]string{
		"email": email, "password": password,
	}, &out); err != nil {
		c.setErr(err)
		return c.Status(), err
	}
	c.mu.Lock()
	c.cfg.BaseURL = base
	c.cfg.Email = out.Account.Email
	c.cfg.Role = out.Account.Role
	c.cfg.RefreshToken = out.RefreshToken
	c.access = out.AccessToken
	c.accessAt = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	c.lastErr = ""
	err = save(c.opt.ConfigPath, c.cfg)
	c.mu.Unlock()
	if err != nil {
		return c.Status(), err
	}
	return c.Status(), nil
}

// ChangePassword updates the signed-in account password and adopts the new session tokens.
func (c *Client) ChangePassword(ctx context.Context, current, next string) (Status, error) {
	var out tokenResponse
	if err := c.authPost(ctx, "/api/v1/auth/password", map[string]string{
		"currentPassword": current,
		"newPassword":     next,
	}, &out); err != nil {
		return c.Status(), err
	}
	c.mu.Lock()
	c.cfg.RefreshToken = out.RefreshToken
	if out.Account.Email != "" {
		c.cfg.Email = out.Account.Email
	}
	if out.Account.Role != "" {
		c.cfg.Role = out.Account.Role
	}
	c.access = out.AccessToken
	c.accessAt = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	c.lastErr = ""
	err := save(c.opt.ConfigPath, c.cfg)
	c.mu.Unlock()
	if err != nil {
		return c.Status(), err
	}
	return c.Status(), nil
}

// CreateAccount creates a new member account on the relay (requires owner role).
func (c *Client) CreateAccount(ctx context.Context, email, password string) error {
	return c.authPost(ctx, "/api/v1/accounts", map[string]string{
		"email":    strings.TrimSpace(email),
		"password": password,
	}, nil)
}

// Logout revokes the user session and disables the socket. The device
// credential is kept so the same PC can be enabled again without re-pairing.
func (c *Client) Logout(ctx context.Context) (Status, error) {
	c.mu.Lock()
	base, refresh := c.cfg.BaseURL, c.cfg.RefreshToken
	c.cfg.RefreshToken = ""
	c.cfg.Role = ""
	c.cfg.Enabled = false
	c.access = ""
	c.online = false
	err := save(c.opt.ConfigPath, c.cfg)
	c.mu.Unlock()
	c.stopLoop()
	if refresh != "" && base != "" {
		_ = c.do(ctx, http.MethodPost, base, "/api/v1/auth/logout", "", map[string]string{"refreshToken": refresh}, nil)
	}
	if err != nil {
		return c.Status(), err
	}
	return c.Status(), nil
}

// SetEnabled turns the outbound tunnel on or off.
func (c *Client) SetEnabled(ctx context.Context, on bool) (Status, error) {
	if on {
		if err := c.ensureKeys(); err != nil {
			return c.Status(), err
		}
		c.mu.Lock()
		linked := c.cfg.DeviceToken != ""
		c.mu.Unlock()
		if !linked {
			if err := c.registerDevice(ctx); err != nil {
				c.setErr(err)
				return c.Status(), err
			}
		}
	}
	c.mu.Lock()
	c.cfg.Enabled = on
	if !on {
		c.online = false
	}
	c.lastErr = ""
	err := save(c.opt.ConfigPath, c.cfg)
	c.mu.Unlock()
	if err != nil {
		return c.Status(), err
	}
	if on {
		c.ensureLoop()
	} else {
		c.stopLoop()
	}
	return c.Status(), nil
}

// CreateQRLogin asks the relay for a short-lived one-time QR login ticket so the phone
// can sign in to the same relay account with a single scan (no typing URL, email, or password).
func (c *Client) CreateQRLogin(ctx context.Context) (LinkCode, error) {
	c.mu.Lock()
	base := c.cfg.BaseURL
	c.mu.Unlock()
	if strings.TrimSpace(base) == "" {
		return LinkCode{}, errors.New("faça login no relay primeiro")
	}
	var out struct {
		Ticket    string `json:"ticket"`
		ExpiresAt string `json:"expiresAt"`
		URI       string `json:"uri"`
	}
	if err := c.authPost(ctx, "/api/v1/auth/qr-ticket", map[string]string{}, &out); err != nil {
		return LinkCode{}, err
	}
	uri := strings.TrimSpace(out.URI)
	if uri == "" || strings.Contains(uri, "relay=&") {
		uri = "hivemind://qr-login?relay=" + url.QueryEscape(base) + "&ticket=" + url.QueryEscape(out.Ticket)
	}
	qr, err := remote.QRDataURL(uri, 280)
	if err != nil {
		return LinkCode{}, err
	}
	return LinkCode{Code: "QR-LOGIN", ExpiresAt: out.ExpiresAt, URI: uri, QRDataURL: qr}, nil
}

// CreateLinkCode asks the relay for a code the phone confirms.
func (c *Client) CreateLinkCode(ctx context.Context) (LinkCode, error) {
	if err := c.ensureKeys(); err != nil {
		return LinkCode{}, err
	}
	c.mu.Lock()
	if c.cfg.DeviceToken != "" {
		c.mu.Unlock()
		return LinkCode{}, errors.New("esta IDE já está vinculada; desvincule antes de gerar outro código")
	}
	name := c.deviceNameLocked()
	pub := c.keys.PublicB64()
	c.mu.Unlock()
	var out linkResponse
	if err := c.authPost(ctx, "/api/v1/link-codes", map[string]string{
		"role": "ide", "name": name, "publicKey": pub,
	}, &out); err != nil {
		return LinkCode{}, err
	}
	qr, err := remote.QRDataURL(out.URI, 280)
	if err != nil {
		return LinkCode{}, err
	}
	go c.pollLink(out.Code)
	return LinkCode{Code: out.Code, ExpiresAt: out.ExpiresAt, URI: out.URI, QRDataURL: qr}, nil
}

// ClaimLinkCode confirms a code created on the phone.
func (c *Client) ClaimLinkCode(ctx context.Context, code string) (Status, error) {
	if err := c.ensureKeys(); err != nil {
		return c.Status(), err
	}
	code = relaywire.NormalizeCode(code)
	if code == "" {
		return c.Status(), errors.New("informe o código")
	}
	c.mu.Lock()
	if c.cfg.DeviceToken != "" {
		c.mu.Unlock()
		return c.Status(), errors.New("esta IDE já está vinculada")
	}
	name := c.deviceNameLocked()
	pub := c.keys.PublicB64()
	c.mu.Unlock()
	var out struct {
		DeviceToken string     `json:"deviceToken"`
		Device      deviceJSON `json:"device"`
	}
	if err := c.authPost(ctx, "/api/v1/link-codes/"+url.PathEscape(code)+"/confirm", map[string]string{
		"publicKey": pub, "name": name,
	}, &out); err != nil {
		c.setErr(err)
		return c.Status(), err
	}
	if err := c.adopt(out.DeviceToken, out.Device); err != nil {
		return c.Status(), err
	}
	return c.Status(), nil
}

// Unlink revokes this IDE on the relay and drops the local credential.
func (c *Client) Unlink(ctx context.Context) (Status, error) {
	c.mu.Lock()
	id := c.cfg.DeviceID
	c.mu.Unlock()
	if id != "" {
		err := c.authPost(ctx, "/api/v1/devices/"+url.PathEscape(id)+"/revoke", map[string]string{}, nil)
		if err != nil && !errors.Is(err, errNotFound) {
			c.setErr(err)
			return c.Status(), err
		}
	}
	c.mu.Lock()
	c.cfg.DeviceID = ""
	c.cfg.DeviceToken = ""
	c.cfg.PeerPublicKey = ""
	c.cfg.Enabled = false
	c.hasPeer = false
	c.peer = [32]byte{}
	c.online = false
	c.lastErr = ""
	err := save(c.opt.ConfigPath, c.cfg)
	c.mu.Unlock()
	c.stopLoop()
	if err != nil {
		return c.Status(), err
	}
	return c.Status(), nil
}

func (c *Client) registerDevice(ctx context.Context) error {
	c.mu.Lock()
	name := c.deviceNameLocked()
	pub := ""
	if c.keys != nil {
		pub = c.keys.PublicB64()
	}
	c.mu.Unlock()
	var out struct {
		DeviceToken string     `json:"deviceToken"`
		Device      deviceJSON `json:"device"`
	}
	if err := c.authPost(ctx, "/api/v1/devices", map[string]string{"name": name, "publicKey": pub}, &out); err != nil {
		return err
	}
	return c.adopt(out.DeviceToken, out.Device)
}

func (c *Client) adopt(token string, dev deviceJSON) error {
	if token == "" || dev.ID == "" {
		return errors.New("o relay não devolveu a credencial da IDE")
	}
	c.mu.Lock()
	c.cfg.DeviceID = dev.ID
	c.cfg.DeviceToken = token
	if dev.Name != "" {
		c.cfg.DeviceName = dev.Name
	}
	if dev.AppPublicKey != "" {
		c.cfg.PeerPublicKey = dev.AppPublicKey
		if pub, err := relaywire.ParseKey(dev.AppPublicKey); err == nil {
			c.peer = pub
			c.hasPeer = true
		}
	}
	c.lastErr = ""
	err := save(c.opt.ConfigPath, c.cfg)
	c.mu.Unlock()
	return err
}

func (c *Client) pollLink(code string) {
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		taken := c.cfg.DeviceToken != ""
		c.mu.Unlock()
		if taken {
			return
		}
		var out linkResponse
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := c.authGet(ctx, "/api/v1/link-codes/"+url.PathEscape(code), &out)
		cancel()
		if err == nil && out.Status == "ready" && out.DeviceToken != "" {
			_ = c.adopt(out.DeviceToken, deviceJSON{
				ID: out.DeviceID, Name: out.Name, AppPublicKey: out.AppPublicKey, IdePublicKey: out.IdePublicKey,
			})
			c.mu.Lock()
			enabled := c.cfg.Enabled
			c.mu.Unlock()
			if enabled {
				c.ensureLoop()
			}
			return
		}
		if err == nil && out.Status == "expired" {
			c.setErr(errors.New("código de vínculo expirado"))
			return
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func (c *Client) ensureLoop() {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.started = true
	sub := c.opt.Subscribe
	c.mu.Unlock()
	if sub != nil {
		ch, unsub := sub()
		go c.consume(ctx, ch, unsub)
	}
	go c.loop(ctx)
}

func (c *Client) stopLoop() {
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.started = false
	c.online = false
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *Client) consume(ctx context.Context, ch <-chan remote.StreamEvent, unsub func()) {
	defer unsub()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			c.ring.add(ev)
		}
	}
}

func (c *Client) loop(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		c.mu.Lock()
		enabled := c.cfg.Enabled && c.cfg.DeviceToken != ""
		c.mu.Unlock()
		if !enabled {
			select {
			case <-ctx.Done():
				return
			case <-time.After(400 * time.Millisecond):
			}
			continue
		}
		err := c.session(ctx)
		c.setOnline(false)
		if err != nil && ctx.Err() == nil {
			c.setErr(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *Client) session(ctx context.Context) error {
	c.mu.Lock()
	base := c.cfg.BaseURL
	token := c.cfg.DeviceToken
	deviceID := c.cfg.DeviceID
	pub := ""
	if c.keys != nil {
		pub = c.keys.PublicB64()
	}
	c.mu.Unlock()
	wsURL, err := wsURL(base)
	if err != nil {
		return err
	}
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(12 << 20)

	helloPayload, _ := json.Marshal(relaywire.Hello{PublicKey: pub})
	if err := writeConn(ctx, conn, relaywire.Envelope{
		V: 1, ID: "hello", Type: relaywire.TypeHello, Role: relaywire.RoleIDE, DeviceID: deviceID, Payload: helloPayload,
	}); err != nil {
		return err
	}

	send := make(chan []byte, 32)
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 2)
	go func() {
		err := writeLoop(sessionCtx, conn, send)
		cancel()
		errc <- err
	}()

	env, err := readConn(sessionCtx, conn)
	if err != nil {
		return err
	}
	if env.Type == relaywire.TypeError {
		return errors.New(errorMessage(env))
	}
	if env.Type != relaywire.TypeHelloOK {
		return fmt.Errorf("handshake: %s", env.Type)
	}
	var okBody relaywire.HelloOK
	_ = json.Unmarshal(env.Payload, &okBody)
	if okBody.PeerPublicKey != "" {
		_ = c.rememberPeer(okBody.PeerPublicKey)
	}
	c.setOnline(true)
	c.setErr(nil)

	go func() {
		c.flush(sessionCtx, send)
		for {
			select {
			case <-sessionCtx.Done():
				return
			case <-c.ring.notify:
				c.flush(sessionCtx, send)
			}
		}
	}()

	for {
		env, err := readConn(sessionCtx, conn)
		if err != nil {
			return err
		}
		switch env.Type {
		case relaywire.TypePing:
			_ = enqueue(sessionCtx, send, relaywire.Envelope{V: 1, ID: env.ID, Type: relaywire.TypePong, DeviceID: deviceID})
		case relaywire.TypePeerKey:
			var notice relaywire.PeerKeyNotice
			if json.Unmarshal(env.Payload, &notice) == nil && notice.PublicKey != "" {
				_ = c.rememberPeer(notice.PublicKey)
				c.flush(sessionCtx, send)
			}
		case relaywire.TypeRPC:
			c.handleRPC(sessionCtx, send, env)
		case relaywire.TypeError:
			c.setErr(errors.New(errorMessage(env)))
		}
		select {
		case err := <-errc:
			return err
		default:
		}
	}
}

func (c *Client) handleRPC(ctx context.Context, send chan []byte, env relaywire.Envelope) {
	peer, keys, ok := c.crypto()
	if !ok || env.Enc != relaywire.EncNaClBox {
		c.replyErr(ctx, send, env.ID, relaywire.CodeKeysMissing, "ainda não há chave do app")
		return
	}
	var req relaywire.RPCRequest
	if err := keys.OpenJSON(peer, env.Payload, &req); err != nil {
		c.replyErr(ctx, send, env.ID, relaywire.CodeDecryptFailed, "não foi possível abrir a mensagem")
		return
	}
	result := c.callBridge(ctx, req)
	raw, err := keys.SealJSON(peer, result)
	if err != nil {
		c.replyErr(ctx, send, env.ID, relaywire.CodeInternal, "falha ao cifrar a resposta")
		return
	}
	_ = enqueue(ctx, send, relaywire.Envelope{
		V: 1, ID: env.ID, Type: relaywire.TypeRPCResult, DeviceID: env.DeviceID, Enc: relaywire.EncNaClBox, Payload: raw,
	})
}

func (c *Client) callBridge(ctx context.Context, req relaywire.RPCRequest) relaywire.RPCResult {
	if !relaywire.AllowedRPC(req.Method, req.Path) {
		return rpcResult(http.StatusBadRequest, `{"error":"rota não tunelada pelo relay"}`)
	}
	if c.opt.Handler == nil {
		return rpcResult(http.StatusServiceUnavailable, `{"error":"bridge local indisponível"}`)
	}
	target := req.Path
	if req.Query != "" {
		target += "?" + req.Query
	}
	var body io.Reader
	if len(req.Body) > 0 && string(req.Body) != "null" {
		body = bytes.NewReader(req.Body)
	}
	httpReq := httptest.NewRequest(req.Method, target, body)
	httpReq = httpReq.WithContext(ctx)
	if c.opt.PrepareLocal != nil {
		httpReq = c.opt.PrepareLocal(httpReq)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c.opt.Handler.ServeHTTP(rec, httpReq)
	resp := rec.Body.Bytes()
	if len(resp) > 10<<20 {
		return rpcResult(http.StatusRequestEntityTooLarge, `{"error":"resposta grande demais para o relay"}`)
	}
	if len(resp) == 0 {
		resp = []byte("null")
	}
	return relaywire.RPCResult{Status: rec.Code, Body: json.RawMessage(resp)}
}

func (c *Client) flush(ctx context.Context, send chan []byte) {
	if _, _, ok := c.crypto(); !ok {
		return
	}
	items, gap := c.ring.drain()
	if gap {
		body := map[string]any{"reason": "backlog_truncated"}
		if c.opt.Snapshot != nil {
			body["lanes"] = c.opt.Snapshot()
		}
		raw, err := json.Marshal(body)
		if err == nil {
			c.sendEvent(ctx, send, remote.StreamEvent{Type: "relay_gap", Data: raw})
		}
	}
	for _, ev := range items {
		if ctx.Err() != nil {
			return
		}
		c.sendEvent(ctx, send, ev)
	}
}

func (c *Client) sendEvent(ctx context.Context, send chan []byte, ev remote.StreamEvent) {
	peer, keys, ok := c.crypto()
	if !ok {
		c.ring.add(ev)
		return
	}
	raw, err := keys.SealJSON(peer, ev)
	if err != nil {
		return
	}
	c.mu.Lock()
	id := c.cfg.DeviceID
	c.mu.Unlock()
	_ = enqueue(ctx, send, relaywire.Envelope{
		V: 1, ID: "ev", Type: relaywire.TypeEvent, DeviceID: id, Enc: relaywire.EncNaClBox, Payload: raw,
	})
}

func (c *Client) replyErr(ctx context.Context, send chan []byte, id, code, msg string) {
	raw, _ := json.Marshal(relaywire.ErrorBody{Code: code, Message: msg})
	c.mu.Lock()
	deviceID := c.cfg.DeviceID
	c.mu.Unlock()
	_ = enqueue(ctx, send, relaywire.Envelope{V: 1, ID: id, Type: relaywire.TypeError, DeviceID: deviceID, Payload: raw})
}

func (c *Client) crypto() ([32]byte, *relaywire.KeyPair, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.hasPeer || c.keys == nil {
		return [32]byte{}, nil, false
	}
	return c.peer, c.keys, true
}

func (c *Client) rememberPeer(b64 string) error {
	pub, err := relaywire.ParseKey(b64)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.peer = pub
	c.hasPeer = true
	c.cfg.PeerPublicKey = relaywire.EncodeKey(pub)
	err = save(c.opt.ConfigPath, c.cfg)
	c.mu.Unlock()
	return err
}

func (c *Client) ensureKeys() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.keys != nil {
		return nil
	}
	k, err := relaywire.NewKeyPair()
	if err != nil {
		return err
	}
	c.keys = k
	c.cfg.PublicKey = k.PublicB64()
	c.cfg.PrivateKey = encodePriv(k.Private)
	return save(c.opt.ConfigPath, c.cfg)
}

func (c *Client) loadKeys() error {
	if c.cfg.PublicKey == "" || c.cfg.PrivateKey == "" {
		return nil
	}
	pub, err := relaywire.ParseKey(c.cfg.PublicKey)
	if err != nil {
		return err
	}
	priv, err := relaywire.ParseKey(c.cfg.PrivateKey)
	if err != nil {
		return err
	}
	c.keys = &relaywire.KeyPair{Public: pub, Private: priv}
	if c.cfg.PeerPublicKey != "" {
		peer, err := relaywire.ParseKey(c.cfg.PeerPublicKey)
		if err != nil {
			return err
		}
		c.peer = peer
		c.hasPeer = true
	}
	return nil
}

func (c *Client) deviceNameLocked() string {
	if c.cfg.DeviceName != "" {
		return c.cfg.DeviceName
	}
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return "HiveMind IDE"
	}
	return name
}

func (c *Client) authPost(ctx context.Context, path string, in any, out any) error {
	tok, base, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, base, path, tok, in, out)
}

func (c *Client) authGet(ctx context.Context, path string, out any) error {
	tok, base, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodGet, base, path, tok, nil, out)
}

func (c *Client) accessToken(ctx context.Context) (string, string, error) {
	c.mu.Lock()
	base := c.cfg.BaseURL
	refresh := c.cfg.RefreshToken
	access := c.access
	exp := c.accessAt
	c.mu.Unlock()
	if base == "" || refresh == "" {
		return "", "", errors.New("entre na conta do relay")
	}
	if access != "" && time.Until(exp) > 30*time.Second {
		return access, base, nil
	}
	// One refresh at a time. The relay revokes the session if an already
	// rotated refresh token is presented again, so concurrent callers must
	// share the token produced by the first one.
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	c.mu.Lock()
	base = c.cfg.BaseURL
	refresh = c.cfg.RefreshToken
	access = c.access
	exp = c.accessAt
	c.mu.Unlock()
	if base == "" || refresh == "" {
		return "", "", errors.New("entre na conta do relay")
	}
	if access != "" && time.Until(exp) > 30*time.Second {
		return access, base, nil
	}
	var out tokenResponse
	if err := c.do(ctx, http.MethodPost, base, "/api/v1/auth/refresh", "", map[string]string{"refreshToken": refresh}, &out); err != nil {
		return "", "", err
	}
	c.mu.Lock()
	c.cfg.RefreshToken = out.RefreshToken
	c.access = out.AccessToken
	c.accessAt = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	err := save(c.opt.ConfigPath, c.cfg)
	c.mu.Unlock()
	if err != nil {
		return "", "", err
	}
	return out.AccessToken, base, nil
}

func (c *Client) do(ctx context.Context, method, base, path, token string, in any, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return httpFailure(res.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) setOnline(v bool) {
	c.mu.Lock()
	c.online = v
	c.mu.Unlock()
}

func (c *Client) setErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.lastErr = ""
		return
	}
	c.lastErr = err.Error()
}

type tokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int    `json:"expiresIn"`
	Account      struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	} `json:"account"`
}

type deviceJSON struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	IdePublicKey string `json:"idePublicKey"`
	AppPublicKey string `json:"appPublicKey"`
}

type linkResponse struct {
	Code         string `json:"code"`
	ExpiresAt    string `json:"expiresAt"`
	URI          string `json:"uri"`
	Status       string `json:"status"`
	DeviceID     string `json:"deviceId"`
	DeviceToken  string `json:"deviceToken"`
	Name         string `json:"name"`
	IdePublicKey string `json:"idePublicKey"`
	AppPublicKey string `json:"appPublicKey"`
}

var errNotFound = errors.New("não encontrado")

func httpFailure(status int, raw []byte) error {
	var doc relaywire.APIError
	_ = json.Unmarshal(raw, &doc)
	msg := doc.Error.Message
	if msg == "" {
		msg = fmt.Sprintf("relay respondeu %d", status)
	}
	if status == http.StatusNotFound {
		return fmt.Errorf("%w: %s", errNotFound, msg)
	}
	return errors.New(msg)
}

func normalizeBase(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("URL do relay inválida")
	}
	host := u.Hostname()
	if u.Scheme == "http" && host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return "", errors.New("um relay público exige https")
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

func wsURL(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", errors.New("URL do relay inválida")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/ws"
	return u.String(), nil
}

func encodePriv(k [32]byte) string { return relaywire.EncodeKey(k) }

func rpcResult(status int, body string) relaywire.RPCResult {
	return relaywire.RPCResult{Status: status, Body: json.RawMessage(body)}
}

func errorMessage(env relaywire.Envelope) string {
	var eb relaywire.ErrorBody
	if json.Unmarshal(env.Payload, &eb) == nil && eb.Message != "" {
		return eb.Message
	}
	return "erro do relay"
}

func writeConn(ctx context.Context, conn *websocket.Conn, env relaywire.Envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, b)
}

func readConn(ctx context.Context, conn *websocket.Conn) (relaywire.Envelope, error) {
	rctx, cancel := context.WithTimeout(ctx, 70*time.Second)
	defer cancel()
	_, data, err := conn.Read(rctx)
	if err != nil {
		return relaywire.Envelope{}, err
	}
	var env relaywire.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return relaywire.Envelope{}, err
	}
	return env, nil
}

func enqueue(ctx context.Context, send chan []byte, env relaywire.Envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case send <- b:
		return nil
	}
}

func writeLoop(ctx context.Context, conn *websocket.Conn, send <-chan []byte) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case b, ok := <-send:
			if !ok {
				return nil
			}
			wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			err := conn.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				return err
			}
		}
	}
}

const ringCap = 512

type eventRing struct {
	mu        sync.Mutex
	items     []remote.StreamEvent
	notify    chan struct{}
	truncated bool
}

func newRing() *eventRing {
	return &eventRing{notify: make(chan struct{}, 1)}
}

func (r *eventRing) add(ev remote.StreamEvent) {
	r.mu.Lock()
	r.items = append(r.items, ev)
	if len(r.items) > ringCap {
		r.items = r.items[len(r.items)-ringCap:]
		r.truncated = true
	}
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

func (r *eventRing) drain() ([]remote.StreamEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.items
	gap := r.truncated
	r.items = nil
	r.truncated = false
	return out, gap
}
