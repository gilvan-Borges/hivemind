package remote

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/secretx"
	"github.com/gilvan-Borges/HiveMind/relaywire"
	"golang.org/x/crypto/curve25519"
)

// Aceite da fase 1 em ide/internal/remote: QR v2 sem token, hash no disco,
// resgate único, seq, cofre fechado e corpo ilegível.

type lanSession struct {
	token    string
	deviceID string
	app      *relaywire.KeyPair
	pcPub    [32]byte
	seq      uint64
}

func (c *lanSession) seal(t *testing.T, body any) []byte {
	t.Helper()
	c.seq++
	box, err := c.app.Seal(c.pcPub, withToken(t, body, c.token))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(lanFrame{
		V: lanProto, DeviceID: c.deviceID, Seq: c.seq, Enc: relaywire.EncNaClBox, Payload: box,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func withToken(t *testing.T, body any, token string) []byte {
	t.Helper()
	if body == nil {
		raw, err := json.Marshal(map[string]string{"token": token})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("corpo de teste não é objeto: %v", err)
	}
	tb, err := json.Marshal(token)
	if err != nil {
		t.Fatal(err)
	}
	m["token"] = tb
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func unwrapLAN(sess *lanSession, rec *httptest.ResponseRecorder) {
	var box relaywire.BoxPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &box); err != nil || box.Nonce == "" || box.Ciphertext == "" {
		return
	}
	plain, err := sess.app.Open(sess.pcPub, box)
	if err != nil {
		return
	}
	rec.Body = bytes.NewBuffer(plain)
}

func mustBox(t *testing.T, raw []byte) relaywire.BoxPayload {
	t.Helper()
	var box relaywire.BoxPayload
	if err := json.Unmarshal(raw, &box); err != nil || box.Nonce == "" {
		t.Fatalf("caixa inválida: %s", raw)
	}
	return box
}

func pairDevice(t *testing.T, srv *Server) *lanSession {
	t.Helper()
	info, err := srv.Pairing()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(info.URI, "token=") {
		t.Fatalf("QR com token: %s", info.URI)
	}
	parsed, ok := ParsePairURI(info.URI)
	if !ok {
		t.Fatalf("URI rejeitada: %s", info.URI)
	}
	app, err := relaywire.NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	rec := redeemParsed(t, srv, parsed, app)
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem %d %s", rec.Code, rec.Body.String())
	}
	opened := openRedeem(t, app, parsed.PK, rec.Body.Bytes())
	pc, err := relaywire.ParseKey(opened.BoxPub)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(opened.Token, lanTokenPrefix) || opened.DeviceID == "" {
		t.Fatalf("resposta de resgate: %+v", opened)
	}
	return &lanSession{token: opened.Token, deviceID: opened.DeviceID, app: app, pcPub: pc}
}

func redeemParsed(t *testing.T, srv *Server, parsed PairURI, app *relaywire.KeyPair) *httptest.ResponseRecorder {
	t.Helper()
	offerPub, err := relaywire.ParseKey(parsed.PK)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	plain, err := json.Marshal(redeemPlain{
		V:      lanProto,
		Offer:  parsed.Offer,
		AppPub: app.PublicB64(),
		Nonce:  base64.RawURLEncoding.EncodeToString(nonce),
	})
	if err != nil {
		t.Fatal(err)
	}
	box, err := app.Seal(offerPub, plain)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(redeemBody{AppPub: app.PublicB64(), Nonce: box.Nonce, Ciphertext: box.Ciphertext})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pair/redeem", bytes.NewReader(body))
	req.RemoteAddr = "198.51.100.10:4242"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func openRedeem(t *testing.T, app *relaywire.KeyPair, offerPK string, raw []byte) redeemOK {
	t.Helper()
	offerPub, err := relaywire.ParseKey(offerPK)
	if err != nil {
		t.Fatal(err)
	}
	var opened redeemOK
	if err := app.OpenJSON(offerPub, raw, &opened); err != nil {
		t.Fatalf("abrindo resgate: %v corpo=%s", err, raw)
	}
	return opened
}

func testServer(t *testing.T) *Server {
	t.Helper()
	path := filepath.Join(t.TempDir(), "remote-bridge.json")
	cfg, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(NewHub(), &fakeDeps{cancelResult: true, resolveOK: true}, cfg)
	srv.configPath = path
	srv.secrets = secretx.NewMemoryVault()
	srv.urls = []string{"http://127.0.0.1:47890"}
	return srv
}

func TestRedeemPersistsHashOnly(t *testing.T) {
	srv := testServer(t)
	sess := pairDevice(t, srv)
	raw, err := os.ReadFile(srv.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), sess.token) || strings.Contains(string(raw), `"token"`) {
		t.Fatalf("remote-bridge.json contém o token cru: %s", raw)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.TokenHash) != 64 || cfg.TokenHash != hashToken(sess.token) {
		t.Fatalf("tokenHash: %q", cfg.TokenHash)
	}
	priv, err := srv.secrets.Get(lanBoxName)
	if err != nil || priv == "" {
		t.Fatalf("cofre: %v", err)
	}
	if strings.Contains(string(raw), priv) {
		t.Fatal("privada de longo prazo gravada no JSON")
	}
}

func TestRedeemIsSingleUseAndExpired(t *testing.T) {
	srv := testServer(t)
	info, err := srv.Pairing()
	if err != nil {
		t.Fatal(err)
	}
	parsed, ok := ParsePairURI(info.URI)
	if !ok {
		t.Fatal(info.URI)
	}
	app, err := relaywire.NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	first := redeemParsed(t, srv, parsed, app)
	if first.Code != http.StatusOK {
		t.Fatalf("primeiro resgate %d %s", first.Code, first.Body.String())
	}
	second := redeemParsed(t, srv, parsed, app)
	if second.Code != http.StatusGone {
		t.Fatalf("segundo resgate deveria ser 410, veio %d %s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "oferta inválida") || !strings.Contains(second.Body.String(), `"expired"`) {
		t.Fatalf("envelope: %s", second.Body.String())
	}

	expired := testServer(t)
	info, err = expired.Pairing()
	if err != nil {
		t.Fatal(err)
	}
	parsed, ok = ParsePairURI(info.URI)
	if !ok {
		t.Fatal(info.URI)
	}
	expired.mu.Lock()
	expired.offer.exp = expired.clock().Add(-time.Second)
	expired.mu.Unlock()
	app2, err := relaywire.NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	rec := redeemParsed(t, expired, parsed, app2)
	if rec.Code != http.StatusGone || !strings.Contains(rec.Body.String(), "oferta inválida") {
		t.Fatalf("oferta vencida: %d %s", rec.Code, rec.Body.String())
	}
}

func TestVaultUnavailableDoesNotWritePrivateKey(t *testing.T) {
	srv := testServer(t)
	vault := srv.secrets.(*secretx.MemoryVault)
	vault.SetUnavailable("cofre de teste")
	info, err := srv.Pairing()
	if err != nil {
		t.Fatal(err)
	}
	parsed, ok := ParsePairURI(info.URI)
	if !ok {
		t.Fatal(info.URI)
	}
	app, err := relaywire.NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	rec := redeemParsed(t, srv, parsed, app)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("cofre indisponível deveria falhar, veio %d %s", rec.Code, rec.Body.String())
	}
	raw, err := os.ReadFile(srv.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hmlan_") || strings.Contains(string(raw), "private") || strings.Contains(string(raw), `"token"`) {
		t.Fatalf("JSON após falha do cofre: %s", raw)
	}
	names, err := vault.ListNames()
	if err == nil && len(names) != 0 {
		t.Fatalf("cofre indisponível gravou %v", names)
	}
}

func TestSeqReplayDoesNotExecute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote-bridge.json")
	cfg, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	deps := &fakeDeps{cancelResult: true, resolveOK: true}
	srv := NewServer(NewHub(), deps, cfg)
	srv.configPath = path
	srv.secrets = secretx.NewMemoryVault()
	srv.urls = []string{"http://127.0.0.1:47890"}
	sess := pairDevice(t, srv)
	body := map[string]any{"laneId": "l1", "workspace": "C:/repo", "objective": "uma vez"}
	raw := sess.seal(t, body)
	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/runs/start", bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}
	first := send()
	if first.Code != http.StatusAccepted {
		t.Fatalf("primeira execução %d %s", first.Code, first.Body.String())
	}
	second := send()
	if second.Code != http.StatusConflict {
		t.Fatalf("seq repetido deveria ser 409, veio %d %s", second.Code, second.Body.String())
	}
	if len(deps.started) != 1 {
		t.Fatalf("seq repetido executou de novo: %d", len(deps.started))
	}
}

func TestSeqGapAsksResume(t *testing.T) {
	srv := testServer(t)
	sess := pairDevice(t, srv)
	rec := doJSON(t, srv, http.MethodPost, "/api/v1/runs/start", sess, map[string]any{
		"laneId": "l", "workspace": "w", "objective": "ok",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("seq 1: %d %s", rec.Code, rec.Body.String())
	}
	sess.seq = 2
	gap := doJSON(t, srv, http.MethodPost, "/api/v1/runs/start", sess, map[string]any{
		"laneId": "l", "workspace": "w", "objective": "furado",
	})
	if gap.Code != http.StatusConflict || !strings.Contains(gap.Body.String(), `"resume"`) {
		t.Fatalf("buraco: %d %s", gap.Code, gap.Body.String())
	}
	deps := srv.deps.(*fakeDeps)
	if len(deps.started) != 1 {
		t.Fatalf("buraco executou a rota: %+v", deps.started)
	}
	sess.seq = 1
	resumed := doJSON(t, srv, http.MethodPost, "/api/v1/runs/start", sess, map[string]any{
		"type": "resume", "lastSeq": 1,
	})
	if resumed.Code != http.StatusOK || !strings.Contains(resumed.Body.String(), `"events"`) {
		t.Fatalf("resume: %d %s", resumed.Code, resumed.Body.String())
	}
	if len(deps.started) != 1 {
		t.Fatalf("resume não pode chamar o modelo de rota: %d", len(deps.started))
	}
}

func TestSnifferSeesOpaqueRunStart(t *testing.T) {
	srv := testServer(t)
	sess := pairDevice(t, srv)
	const secret = "plano-secreto-invisivel"
	var seen []byte
	proxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append([]byte(nil), body...)
		r.Body = io.NopCloser(bytes.NewReader(body))
		srv.Handler().ServeHTTP(w, r)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runs/start", bytes.NewReader(sess.seal(t, map[string]any{
		"laneId": "l", "workspace": "w", "objective": secret,
	})))
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("start via proxy %d %s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(seen, []byte(secret)) || bytes.Contains(seen, []byte(sess.token)) {
		t.Fatalf("corpo visível sem a chave: %s", seen)
	}
	other, err := relaywire.NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	var fr lanFrame
	if err := json.Unmarshal(seen, &fr); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Open(sess.pcPub, fr.Payload); err == nil {
		t.Fatal("chave alheia abriu o corpo")
	}
}

func TestLegacyBearerRejectedAfterMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote-bridge.json")
	legacy := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if err := os.WriteFile(path, []byte(`{"token":"`+legacy+`","port":47890}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfigFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(NewHub(), &fakeDeps{}, cfg)
	srv.configPath = path
	srv.secrets = secretx.NewMemoryVault()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", strings.NewReader(`{"workspace":"w"}`))
	req.Header.Set("Authorization", "Bearer "+legacy)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Bearer v1 deveria ser 401, veio %d %s", rec.Code, rec.Body.String())
	}
}

func TestRedeemRateLimit(t *testing.T) {
	srv := testServer(t)
	for i := 0; i < redeemMaxFails; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/pair/redeem", strings.NewReader(`{"appPub":"aa","nonce":"bb","ciphertext":"cc"}`))
		req.RemoteAddr = "203.0.113.9:9"
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("bloqueou cedo na tentativa %d", i+1)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pair/redeem", strings.NewReader(`{"appPub":"aa","nonce":"bb","ciphertext":"cc"}`))
	req.RemoteAddr = "203.0.113.9:9"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "rate_limited") {
		t.Fatalf("limite: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLongTermPublicMatchesVault(t *testing.T) {
	kp, err := relaywire.NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	got, err := keyFromPrivate(base64.RawURLEncoding.EncodeToString(kp.Private[:]))
	if err != nil {
		t.Fatal(err)
	}
	if got.Public != kp.Public {
		t.Fatal("pública derivada não confere com o par gerado")
	}
	pub, err := curve25519.X25519(kp.Private[:], curve25519.Basepoint)
	if err != nil || len(pub) != 32 {
		t.Fatal(err)
	}
}

func TestHeadlessDoesNotPrintSessionToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remote-bridge.json")
	legacy := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	raw := []byte(`{"token":"` + legacy + `","port":` + strconv.Itoa(port) + `,"enabled":false}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	old := configPathFn
	configPathFn = func() string { return path }
	defer func() { configPathFn = old }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = stdout }()

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- RunHeadless(ctx, t.TempDir()) }()

	var mu sync.Mutex
	var buf strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		tmp := make([]byte, 1024)
		for {
			n, err := r.Read(tmp)
			if n > 0 {
				mu.Lock()
				buf.Write(tmp[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	deadline := time.Now().Add(8 * time.Second)
	seen := false
	for time.Now().Before(deadline) {
		mu.Lock()
		text := buf.String()
		mu.Unlock()
		if strings.Contains(text, "Oferta: hivemind://pair?") {
			seen = true
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	cancel()
	_ = w.Close()
	select {
	case <-errc:
	case <-time.After(5 * time.Second):
		t.Fatal("headless não encerrou")
	}
	<-done
	mu.Lock()
	text := buf.String()
	mu.Unlock()
	if !seen {
		t.Fatalf("banner sem oferta: %s", text)
	}
	if strings.Contains(text, legacy) || strings.Contains(text, "hmlan_") || strings.Contains(text, "token=") {
		t.Fatalf("headless imprimiu segredo:\n%s", text)
	}
	if !strings.Contains(text, "Válida até ") {
		t.Fatalf("banner sem expiração: %s", text)
	}
}
