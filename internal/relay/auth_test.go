package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/relaywire"
	"golang.org/x/time/rate"
)

func testArgon() Argon {
	return Argon{Time: 1, Memory: 8192, Threads: 1, KeyLen: 32}
}

func testLimits() LimitConfig {
	return LimitConfig{
		MaxFails:    5,
		FailWindow:  time.Minute,
		BlockFor:    time.Minute,
		LoginPerMin: 600,
		RPS:         rate.Limit(5000),
		Burst:       5000,
	}
}

func newTestServer(t *testing.T) (*httptest.Server, *Store) {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateAccount("owner@example.com", "senha-inicial-ok", "owner", testArgon()); err != nil {
		t.Fatal(err)
	}
	srv, err := New(store, Options{Argon: testArgon(), Limits: testLimits(), PublicURL: "https://relay.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, store
}

func doJSON(t *testing.T, method, url, token string, in any, out any) *http.Response {
	t.Helper()
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("json %s: %v (%s)", res.Status, err, raw)
		}
	}
	res.Body = io.NopCloser(bytes.NewReader(raw))
	return res
}

type tokenResp struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int    `json:"expiresIn"`
	TokenType    string `json:"tokenType"`
	Account      struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Role  string `json:"role"`
	} `json:"account"`
}

func login(t *testing.T, base, email, password string) tokenResp {
	t.Helper()
	var out tokenResp
	res := doJSON(t, http.MethodPost, base+"/api/v1/auth/login", "", map[string]string{
		"email": email, "password": password,
	}, &out)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login %s: %s", email, res.Status)
	}
	if !strings.HasPrefix(out.AccessToken, relaywire.TokenAccessPrefix) || !strings.HasPrefix(out.RefreshToken, relaywire.TokenRefreshPrefix) {
		t.Fatalf("tokens inesperados: %#v", out)
	}
	if out.TokenType != "Bearer" || out.ExpiresIn <= 0 {
		t.Fatalf("tokenType/expiresIn: %#v", out)
	}
	return out
}

func TestHealthAndAuthSession(t *testing.T) {
	ts, _ := newTestServer(t)
	res, err := http.Get(ts.URL + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("health %d", res.StatusCode)
	}

	bad := doJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/login", "", map[string]string{
		"email": "owner@example.com", "password": "errada-demais",
	}, nil)
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("senha errada: %d", bad.StatusCode)
	}

	tok := login(t, ts.URL, "owner@example.com", "senha-inicial-ok")
	var me map[string]any
	res = doJSON(t, http.MethodGet, ts.URL+"/api/v1/auth/me", tok.AccessToken, nil, &me)
	if res.StatusCode != 200 || me["email"] != "owner@example.com" || me["role"] != "owner" {
		t.Fatalf("me: %d %#v", res.StatusCode, me)
	}

	var refreshed tokenResp
	res = doJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/refresh", "", map[string]string{
		"refreshToken": tok.RefreshToken,
	}, &refreshed)
	if res.StatusCode != 200 || refreshed.AccessToken == "" || refreshed.RefreshToken == tok.RefreshToken {
		t.Fatalf("refresh: %d %#v", res.StatusCode, refreshed)
	}
	reuse := doJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/refresh", "", map[string]string{
		"refreshToken": tok.RefreshToken,
	}, nil)
	if reuse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reuso: %d", reuse.StatusCode)
	}
	dead := doJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/refresh", "", map[string]string{
		"refreshToken": refreshed.RefreshToken,
	}, nil)
	if dead.StatusCode != http.StatusUnauthorized {
		t.Fatalf("refresh após reuso: %d", dead.StatusCode)
	}

	tok = login(t, ts.URL, "owner@example.com", "senha-inicial-ok")
	out := doJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/logout", tok.AccessToken, nil, nil)
	if out.StatusCode != http.StatusNoContent {
		t.Fatalf("logout %d", out.StatusCode)
	}
	meRes := doJSON(t, http.MethodGet, ts.URL+"/api/v1/auth/me", tok.AccessToken, nil, nil)
	if meRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me após logout: %d", meRes.StatusCode)
	}
	refRes := doJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/refresh", "", map[string]string{
		"refreshToken": tok.RefreshToken,
	}, nil)
	if refRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("refresh após logout: %d", refRes.StatusCode)
	}
}

func TestLoginRateLimit(t *testing.T) {
	ts, _ := newTestServer(t)
	for i := 0; i < 5; i++ {
		res := doJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/login", "", map[string]string{
			"email": "owner@example.com", "password": "errada-demais",
		}, nil)
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("tentativa %d: %d", i+1, res.StatusCode)
		}
	}
	res := doJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/login", "", map[string]string{
		"email": "owner@example.com", "password": "senha-inicial-ok",
	}, nil)
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("bloqueio: %d", res.StatusCode)
	}
	if res.Header.Get("Retry-After") == "" {
		t.Fatal("sem Retry-After")
	}
}

func TestAccountIsolationAndLink(t *testing.T) {
	ts, _ := newTestServer(t)
	owner := login(t, ts.URL, "owner@example.com", "senha-inicial-ok")
	created := doJSON(t, http.MethodPost, ts.URL+"/api/v1/accounts", owner.AccessToken, map[string]string{
		"email": "outro@example.com", "password": "senha-do-outro",
	}, nil)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("criar conta: %d", created.StatusCode)
	}
	other := login(t, ts.URL, "outro@example.com", "senha-do-outro")
	forbidden := doJSON(t, http.MethodPost, ts.URL+"/api/v1/accounts", other.AccessToken, map[string]string{
		"email": "terceiro@example.com", "password": "senha-terceiro",
	}, nil)
	if forbidden.StatusCode != http.StatusForbidden {
		t.Fatalf("member criando conta: %d", forbidden.StatusCode)
	}

	ideKey, err := relaywire.NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	appKey, err := relaywire.NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	var link map[string]any
	res := doJSON(t, http.MethodPost, ts.URL+"/api/v1/link-codes", owner.AccessToken, map[string]string{
		"role": "ide", "name": "pc-trabalho", "publicKey": ideKey.PublicB64(),
	}, &link)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("link: %d %#v", res.StatusCode, link)
	}
	code := link["code"].(string)
	if !strings.Contains(link["uri"].(string), "hivemind://link?") {
		t.Fatalf("uri: %v", link["uri"])
	}
	confirm := doJSON(t, http.MethodPost, ts.URL+"/api/v1/link-codes/"+code+"/confirm", other.AccessToken, map[string]string{
		"publicKey": appKey.PublicB64(),
	}, nil)
	if confirm.StatusCode != http.StatusNotFound {
		t.Fatalf("outra conta confirmou: %d", confirm.StatusCode)
	}
	var confirmed map[string]any
	confirm = doJSON(t, http.MethodPost, ts.URL+"/api/v1/link-codes/"+code+"/confirm", owner.AccessToken, map[string]string{
		"publicKey": appKey.PublicB64(),
	}, &confirmed)
	if confirm.StatusCode != http.StatusOK {
		t.Fatalf("confirm: %d %#v", confirm.StatusCode, confirmed)
	}
	if _, ok := confirmed["deviceToken"]; ok {
		t.Fatal("o app não deve receber o deviceToken quando a IDE iniciou o vínculo")
	}
	var polled map[string]any
	poll := doJSON(t, http.MethodGet, ts.URL+"/api/v1/link-codes/"+code, owner.AccessToken, nil, &polled)
	if poll.StatusCode != 200 || polled["status"] != "ready" || !strings.HasPrefix(polled["deviceToken"].(string), relaywire.TokenDevicePrefix) {
		t.Fatalf("poll: %d %#v", poll.StatusCode, polled)
	}
	again := doJSON(t, http.MethodGet, ts.URL+"/api/v1/link-codes/"+code, owner.AccessToken, nil, &map[string]any{})
	_ = again

	var ownerList, otherList struct {
		Devices []map[string]any `json:"devices"`
	}
	if res := doJSON(t, http.MethodGet, ts.URL+"/api/v1/devices", owner.AccessToken, nil, &ownerList); res.StatusCode != 200 || len(ownerList.Devices) != 1 {
		t.Fatalf("lista owner: %d %d", res.StatusCode, len(ownerList.Devices))
	}
	if res := doJSON(t, http.MethodGet, ts.URL+"/api/v1/devices", other.AccessToken, nil, &otherList); res.StatusCode != 200 || len(otherList.Devices) != 0 {
		t.Fatalf("lista other: %d %#v", res.StatusCode, otherList.Devices)
	}
	id := ownerList.Devices[0]["id"].(string)
	rev := doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices/"+id+"/revoke", other.AccessToken, nil, nil)
	if rev.StatusCode != http.StatusNotFound {
		t.Fatalf("revoke cruzado: %d", rev.StatusCode)
	}
	if ownerList.Devices[0]["e2eReady"] != true {
		t.Fatalf("e2e: %#v", ownerList.Devices[0])
	}
}

func TestPasswordRoundTripAndLimiter(t *testing.T) {
	hash, err := HashPassword("senha-inicial-ok", testArgon())
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword("senha-inicial-ok", hash)
	if err != nil || !ok {
		t.Fatalf("verify: %v %v", ok, err)
	}
	ok, err = VerifyPassword("outra-senha-xx", hash)
	if err != nil || ok {
		t.Fatalf("senha errada passou: %v %v", ok, err)
	}
	if _, err := HashPassword("curta", testArgon()); err == nil {
		t.Fatal("senha curta deveria falhar")
	}

	l := NewLimiter(LimitConfig{MaxFails: 3, FailWindow: time.Minute, BlockFor: time.Minute, LoginPerMin: 100, RPS: 100, Burst: 100})
	now := time.Now()
	l.now = func() time.Time { return now }
	for i := 0; i < 2; i++ {
		if blocked, _ := l.Fail("ip:1"); blocked {
			t.Fatal("bloqueou cedo")
		}
	}
	if blocked, _ := l.Fail("ip:1"); !blocked {
		t.Fatal("deveria bloquear na terceira falha")
	}
	if ok, _ := l.LoginBlocked("ip:1"); !ok {
		t.Fatal("LoginBlocked")
	}
	l.Success("ip:1")
	if ok, _ := l.LoginBlocked("ip:1"); ok {
		t.Fatal("success deveria limpar")
	}
}

func TestUsernameIdentifierAndUpsertAccount(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	acct, created, err := st.UpsertAccount("Usuario.Teste", "Senha@123", "owner", testArgon())
	if err != nil {
		t.Fatalf("UpsertAccount create: %v", err)
	}
	if !created || acct.Email != "usuario.teste" || acct.Role != "owner" {
		t.Fatalf("unexpected upsert result: created=%v acct=%+v", created, acct)
	}

	authed, err := st.Authenticate("usuario.teste", "Senha@123", "dummy")
	if err != nil || authed.ID != acct.ID {
		t.Fatalf("Authenticate username failed: err=%v authed=%+v", err, authed)
	}

	updated, created, err := st.UpsertAccount("usuario.teste", "NovaSenha9", "member", testArgon())
	if err != nil {
		t.Fatalf("UpsertAccount update: %v", err)
	}
	if created || updated.ID != acct.ID || updated.Role != "member" {
		t.Fatalf("unexpected update result: created=%v acct=%+v", created, updated)
	}

	list, err := st.ListAccounts()
	if err != nil || len(list) != 1 || list[0].Email != "usuario.teste" {
		t.Fatalf("ListAccounts: err=%v list=%+v", err, list)
	}
}

func TestSeedOnce(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := seedOwner(store, Settings{DataDir: dir, SeedEmail: "owner@example.com", SeedPassword: "senha-inicial-ok"}); err != nil {
		t.Fatal(err)
	}
	if err := seedOwner(store, Settings{DataDir: dir, SeedEmail: "owner@example.com", SeedPassword: "outra-senha-longa"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate("owner@example.com", "senha-inicial-ok", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate("owner@example.com", "outra-senha-longa", ""); err == nil {
		t.Fatal("o segundo seed não pode trocar a senha")
	}
	var hash string
	if err := store.db.QueryRow(`SELECT password_hash FROM accounts`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hash, "m=19456,t=2,p=1") {
		t.Fatalf("hash de produção: %s", hash)
	}
}

func TestLimiterFloodDoesNotClearActiveLockout(t *testing.T) {
	l := NewLimiter(LimitConfig{MaxFails: 3, FailWindow: 10 * time.Minute, BlockFor: 15 * time.Minute, LoginPerMin: 10, RPS: 20, Burst: 40})
	now := time.Now()
	l.now = func() time.Time { return now }

	const attacker = "ip:203.0.113.99"
	for i := 0; i < 3; i++ {
		l.Fail(attacker)
	}
	if blocked, _ := l.LoginBlocked(attacker); !blocked {
		t.Fatal("expected attacker IP to be blocked")
	}

	// Send 5,000 distinct flooding IPs across AllowRequest, AllowLogin, and Fail.
	for i := 0; i < 5000; i++ {
		k := fmt.Sprintf("ip:198.51.100.%d", i)
		l.AllowRequest(k)
		l.AllowLogin(k)
		l.Fail(k)
	}

	if blocked, _ := l.LoginBlocked(attacker); !blocked {
		t.Fatal("flood cleared active lockout for attacker IP")
	}
}

func TestQRTicketPersistsAndRespectsLoginBlocked(t *testing.T) {
	ts, store := newTestServer(t)
	owner := login(t, ts.URL, "owner@example.com", "senha-inicial-ok")

	var ticketRes struct {
		Ticket string `json:"ticket"`
	}
	res := doJSON(t, http.MethodPost, ts.URL+"/api/v1/auth/qr-ticket", owner.AccessToken, nil, &ticketRes)
	if res.StatusCode != http.StatusCreated || !strings.HasPrefix(ticketRes.Ticket, "hmr_qr_") {
		t.Fatalf("create qr ticket: %d %+v", res.StatusCode, ticketRes)
	}

	// Simulate server restart backed by the same SQLite store.
	restarted, err := New(store, Options{Argon: testArgon()})
	if err != nil {
		t.Fatal(err)
	}
	ts2 := httptest.NewServer(restarted.Handler())
	defer ts2.Close()

	// Attacker triggers LoginBlocked on an IP before trying to redeem a ticket.
	for i := 0; i < 5; i++ {
		restarted.limiter.Fail("ip:198.51.100.77")
	}
	blockedReq, err := http.NewRequest(http.MethodPost, ts2.URL+"/api/v1/auth/qr-redeem", strings.NewReader(`{"ticket":"`+ticketRes.Ticket+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	blockedReq.Header.Set("Content-Type", "application/json")
	blockedReq.Header.Set("X-Forwarded-For", "198.51.100.77")
	blockedResp, err := http.DefaultClient.Do(blockedReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = blockedResp.Body.Close()
	if blockedResp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 when LoginBlocked, got %d", blockedResp.StatusCode)
	}

	// Legitimate client redeems the persisted ticket once.
	var redeemed tokenResp
	redeemRes := doJSON(t, http.MethodPost, ts2.URL+"/api/v1/auth/qr-redeem", "", map[string]string{"ticket": ticketRes.Ticket}, &redeemed)
	if redeemRes.StatusCode != http.StatusOK || redeemed.AccessToken == "" {
		t.Fatalf("expected ticket redeem after restart, got %d %+v", redeemRes.StatusCode, redeemed)
	}
	// Second redeem fails (single-use atomic deletion).
	again := doJSON(t, http.MethodPost, ts2.URL+"/api/v1/auth/qr-redeem", "", map[string]string{"ticket": ticketRes.Ticket}, nil)
	if again.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on second redeem, got %d", again.StatusCode)
	}
}
func TestClientIPPrefersRightmostXFF(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Forwarded-For", "198.51.100.1, 203.0.113.50")
	got := clientIP(req)
	if got != "203.0.113.50" {
		t.Fatalf("clientIP=%q want rightmost 203.0.113.50", got)
	}
	req2 := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	req2.RemoteAddr = "127.0.0.1:1"
	req2.Header.Set("X-Forwarded-For", "198.51.100.77")
	if clientIP(req2) != "198.51.100.77" {
		t.Fatalf("single-hop XFF should still apply")
	}
}
