package relayclient

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gilvan-Borges/HiveMind/ide/internal/remote"
	"github.com/gilvan-Borges/HiveMind/internal/relay"
	"github.com/gilvan-Borges/HiveMind/relaywire"
)

func TestNormalizeBaseRequiresHTTPS(t *testing.T) {
	if _, err := normalizeBase("http://relay.example.com"); err == nil {
		t.Fatal("http público deveria falhar")
	}
	got, err := normalizeBase("https://relay.example.com/relay/")
	if err != nil || got != "https://relay.example.com/relay" {
		t.Fatalf("normalize: %q %v", got, err)
	}
}

func TestClientTunnelsEncryptedRPCAndEvent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := relay.Open(filepath.Join(dir, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	params := relay.Argon{Time: 1, Memory: 8192, Threads: 1, KeyLen: 32}
	if _, err := store.CreateAccount("owner@example.com", "senha-inicial-ok", "owner", params); err != nil {
		t.Fatal(err)
	}
	srv, err := relay.New(store, relay.Options{Argon: params})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	var mu sync.Mutex
	var gotPath, gotAuth string
	var prepared bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.RequestURI()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"projects":[],"marker":"local"}`))
	})
	events := make(chan remote.StreamEvent, 4)
	client, err := Load(Options{
		ConfigPath: filepath.Join(t.TempDir(), "relay.json"),
		Handler:    handler,
		PrepareLocal: func(r *http.Request) *http.Request {
			mu.Lock()
			prepared = true
			mu.Unlock()
			return r
		},
		Subscribe: func() (<-chan remote.StreamEvent, func()) {
			return events, func() {}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Stop)

	if _, err := client.Login(ctx, ts.URL, "owner@example.com", "senha-inicial-ok"); err != nil {
		t.Fatal(err)
	}
	st, err := client.SetEnabled(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Linked || st.DeviceID == "" {
		t.Fatalf("status: %#v", st)
	}
	info, err := os.Stat(client.opt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("permissão %o", info.Mode().Perm())
	}
	waitUntil(t, 5*time.Second, func() bool { return client.Status().Online })

	owner := httpLogin(t, ts.URL)
	appKey, err := relaywire.NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	var dev struct {
		IdePublicKey string `json:"idePublicKey"`
		E2EReady     bool   `json:"e2eReady"`
	}
	res := httpJSON(t, http.MethodPost, ts.URL+"/api/v1/devices/"+st.DeviceID+"/peer-key", owner.AccessToken, map[string]string{
		"publicKey": appKey.PublicB64(),
	}, &dev)
	if res != http.StatusOK || dev.IdePublicKey == "" {
		t.Fatalf("peer %d", res)
	}
	idePub, err := relaywire.ParseKey(dev.IdePublicKey)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 5*time.Second, func() bool { return client.Status().E2E })

	app := dialApp(t, ts.URL, owner.AccessToken)
	hello, _ := json.Marshal(relaywire.Hello{PublicKey: appKey.PublicB64()})
	writeJSON(t, app, relaywire.Envelope{
		V: 1, ID: "h", Type: relaywire.TypeHello, Role: relaywire.RoleApp, DeviceID: st.DeviceID, Payload: hello,
	})
	if env := readJSON(t, app, relaywire.TypeHelloOK); !bytes.Contains(env.Payload, []byte(`"e2eReady":true`)) && !bytes.Contains(env.Payload, []byte(`"e2eReady": true`)) {
		var ok relaywire.HelloOK
		_ = json.Unmarshal(env.Payload, &ok)
		if !ok.E2EReady {
			t.Fatalf("hello app: %s", env.Payload)
		}
	}

	const marker = "SEGREDO-OBJETIVO-9f3c"
	req := relaywire.RPCRequest{
		Method: "GET", Path: "/api/v1/projects", Query: "workspace=demo",
		Body: json.RawMessage(`{"marker":"` + marker + `"}`),
	}
	sealed, err := appKey.SealJSON(idePub, req)
	if err != nil {
		t.Fatal(err)
	}
	writeJSON(t, app, relaywire.Envelope{
		V: 1, ID: "rpc-1", Type: relaywire.TypeRPC, DeviceID: st.DeviceID, Enc: relaywire.EncNaClBox, Payload: sealed,
	})
	result := readJSON(t, app, relaywire.TypeRPCResult)
	if bytes.Contains(result.Payload, []byte(marker)) || bytes.Contains(result.Payload, []byte("projects")) {
		t.Fatal("resultado veio em claro")
	}
	var opened relaywire.RPCResult
	if err := appKey.OpenJSON(idePub, result.Payload, &opened); err != nil {
		t.Fatal(err)
	}
	if opened.Status != 200 || !bytes.Contains(opened.Body, []byte(`"projects"`)) {
		t.Fatalf("resultado: %#v", opened)
	}
	mu.Lock()
	path, auth, wasPrepared := gotPath, gotAuth, prepared
	mu.Unlock()
	if path != "/api/v1/projects?workspace=demo" || auth != "" || !wasPrepared {
		t.Fatalf("bridge local: %s auth=%q prepared=%v", path, auth, wasPrepared)
	}

	events <- remote.StreamEvent{Type: "lane_event", Data: json.RawMessage(`{"marker":"` + marker + `"}`)}
	ev := readJSON(t, app, relaywire.TypeEvent)
	if bytes.Contains(ev.Payload, []byte(marker)) {
		t.Fatal("evento em claro")
	}
	if ev.Seq == 0 {
		t.Fatal("evento sem seq")
	}
	var inner remote.StreamEvent
	if err := appKey.OpenJSON(idePub, ev.Payload, &inner); err != nil {
		t.Fatal(err)
	}
	if inner.Type != "lane_event" || bytes.Contains(inner.Data, []byte(marker)) == false {
		t.Fatalf("evento aberto: %#v", inner)
	}
	stored, err := store.EventsAfter(st.DeviceID, 0, 10)
	if err != nil || len(stored) == 0 {
		t.Fatalf("log: %v %d", err, len(stored))
	}
	if bytes.Contains(stored[0].Body, []byte(marker)) {
		t.Fatal("sqlite do relay contém o plaintext")
	}
}

func httpLogin(t *testing.T, base string) struct {
	AccessToken string `json:"accessToken"`
} {
	t.Helper()
	var out struct {
		AccessToken string `json:"accessToken"`
	}
	if httpJSON(t, http.MethodPost, base+"/api/v1/auth/login", "", map[string]string{
		"email": "owner@example.com", "password": "senha-inicial-ok",
	}, &out) != 200 || out.AccessToken == "" {
		t.Fatal("login http")
	}
	return out
}

func httpJSON(t *testing.T, method, url, token string, in any, out any) int {
	t.Helper()
	var body *bytes.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	} else {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func dialApp(t *testing.T, base, token string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(base, "http")+"/api/v1/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })
	return c
}

func writeJSON(t *testing.T, c *websocket.Conn, env relaywire.Envelope) {
	t.Helper()
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, c *websocket.Conn, want string) relaywire.Envelope {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Until(deadline))
		_, data, err := c.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("lendo %s: %v", want, err)
		}
		var env relaywire.Envelope
		if json.Unmarshal(data, &env) != nil {
			t.Fatalf("json %s", data)
		}
		if env.Type == relaywire.TypePing || env.Type == relaywire.TypePong || env.Type == relaywire.TypePeerKey {
			continue
		}
		if want != "" && env.Type != want {
			t.Fatalf("frame %s, esperava %s (%s)", env.Type, want, data)
		}
		return env
	}
	t.Fatal("timeout")
	return relaywire.Envelope{}
}

func waitUntil(t *testing.T, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timeout de espera")
}

func TestRingSignalsTruncation(t *testing.T) {
	r := newRing()
	for i := 0; i < ringCap+3; i++ {
		r.add(remote.StreamEvent{Type: "lane_event"})
	}
	items, gap := r.drain()
	if !gap || len(items) != ringCap {
		t.Fatalf("gap=%v len=%d", gap, len(items))
	}
	items, gap = r.drain()
	if gap || len(items) != 0 {
		t.Fatalf("segundo drain gap=%v len=%d", gap, len(items))
	}
}

func TestConcurrentRefreshRotatesOnce(t *testing.T) {
	var mu sync.Mutex
	current := "hmr_rt_seed"
	refreshes := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RefreshToken string `json:"refreshToken"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		defer mu.Unlock()
		if body.RefreshToken != current {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"reuso"}}`))
			return
		}
		refreshes++
		current = "hmr_rt_next"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accessToken":  "hmr_at_next",
			"refreshToken": current,
			"expiresIn":    900,
			"account":      map[string]string{"email": "owner@example.com"},
		})
	}))
	defer ts.Close()

	path := filepath.Join(t.TempDir(), "relay.json")
	c, err := Load(Options{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	c.cfg.BaseURL = ts.URL
	c.cfg.RefreshToken = "hmr_rt_seed"
	c.cfg.Email = "owner@example.com"
	if err := save(path, c.cfg); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := c.accessToken(context.Background())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if refreshes != 1 {
		t.Fatalf("refresh rodou %d vezes", refreshes)
	}
}

func TestChangePasswordAndCreateAccount(t *testing.T) {
	var createdEmail string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accessToken":  "hmr_at_1",
				"refreshToken": "hmr_rt_1",
				"expiresIn":    900,
				"account":      map[string]string{"email": "gilvan.b", "role": "owner"},
			})
		case "/api/v1/auth/password":
			if got := r.Header.Get("Authorization"); got != "Bearer hmr_at_1" {
				t.Fatalf("unexpected auth header: %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accessToken":  "hmr_at_2",
				"refreshToken": "hmr_rt_2",
				"expiresIn":    900,
				"account":      map[string]string{"email": "gilvan.b", "role": "owner"},
			})
		case "/api/v1/accounts":
			if got := r.Header.Get("Authorization"); got != "Bearer hmr_at_2" {
				t.Fatalf("unexpected auth header after password change: %q", got)
			}
			var body struct {
				Email    string `json:"email"`
				Password string `json:"password"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			createdEmail = body.Email
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "acct_2", "email": body.Email, "role": "member"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	c, err := Load(Options{ConfigPath: filepath.Join(t.TempDir(), "relay.json")})
	if err != nil {
		t.Fatal(err)
	}
	st, err := c.Login(context.Background(), ts.URL, "gilvan.b", "Senha@123")
	if err != nil {
		t.Fatal(err)
	}
	if !st.LoggedIn || st.Email != "gilvan.b" || st.Role != "owner" {
		t.Fatalf("unexpected login status: %+v", st)
	}
	st, err = c.ChangePassword(context.Background(), "Senha@123", "NovaSenha9")
	if err != nil {
		t.Fatal(err)
	}
	if st.Role != "owner" || c.cfg.RefreshToken != "hmr_rt_2" {
		t.Fatalf("unexpected status after password change: %+v rt=%q", st, c.cfg.RefreshToken)
	}
	if err := c.CreateAccount(context.Background(), "dev.user", "Senha@999"); err != nil {
		t.Fatal(err)
	}
	if createdEmail != "dev.user" {
		t.Fatalf("expected created email dev.user, got %q", createdEmail)
	}
}
