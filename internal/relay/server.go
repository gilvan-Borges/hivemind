package relay

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/relaywire"
)

const (
	defaultAccessTTL  = accessTTL
	defaultRefreshTTL = refreshTTL
	defaultMaxFrame   = 12 << 20
	controlBodyLimit  = 64 << 10
)

// Options configures a Server. Zero values use production defaults.
type Options struct {
	PublicURL  string
	Argon      Argon
	Limits     LimitConfig
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	MaxFrame   int64
	EventKeep  int
	MaxConns   int
}

type qrLoginTicket struct {
	accountID string
	expiresAt time.Time
}

// Server is the relay HTTP and WebSocket API.
type Server struct {
	store      *Store
	hub        *Hub
	limiter    *Limiter
	argon      Argon
	dummy      string
	argonMu    sync.Mutex
	qrMu       sync.Mutex
	qrTickets  map[string]qrLoginTicket
	publicURL  string
	accessTTL  time.Duration
	refreshTTL time.Duration
	maxFrame   int64
	eventKeep  int
}

// New builds a server. dummy argon2id hash is computed up front so unknown
// emails cost the same as a real login.
func New(store *Store, opt Options) (*Server, error) {
	if opt.Argon.Memory == 0 {
		opt.Argon = DefaultArgon()
	}
	if opt.AccessTTL <= 0 {
		opt.AccessTTL = defaultAccessTTL
	}
	if opt.RefreshTTL <= 0 {
		opt.RefreshTTL = defaultRefreshTTL
	}
	if opt.MaxFrame <= 0 {
		opt.MaxFrame = defaultMaxFrame
	}
	if opt.EventKeep <= 0 {
		opt.EventKeep = eventKeep
	}
	dummyPW := make([]byte, 24)
	if _, err := rand.Read(dummyPW); err != nil {
		return nil, err
	}
	dummy, err := HashPassword("dummy-"+string(dummyPW), opt.Argon)
	if err != nil {
		return nil, err
	}
	return &Server{
		store:      store,
		hub:        NewHub(opt.MaxConns),
		limiter:    NewLimiter(opt.Limits),
		argon:      opt.Argon,
		dummy:      dummy,
		qrTickets:  make(map[string]qrLoginTicket),
		publicURL:  strings.TrimRight(strings.TrimSpace(opt.PublicURL), "/"),
		accessTTL:  opt.AccessTTL,
		refreshTTL: opt.RefreshTTL,
		maxFrame:   opt.MaxFrame,
		eventKeep:  opt.EventKeep,
	}, nil
}

// Handler is the full HTTP API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/qr-ticket", s.handleCreateQRTicket)
	mux.HandleFunc("POST /api/v1/auth/qr-redeem", s.handleRedeemQRTicket)
	mux.HandleFunc("POST /api/v1/auth/refresh", s.handleRefresh)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/v1/auth/me", s.handleMe)
	mux.HandleFunc("POST /api/v1/auth/password", s.handlePassword)
	mux.HandleFunc("POST /api/v1/accounts", s.handleCreateAccount)
	mux.HandleFunc("GET /api/v1/devices", s.handleListDevices)
	mux.HandleFunc("POST /api/v1/devices", s.handleCreateDevice)
	mux.HandleFunc("GET /api/v1/devices/{id}", s.handleGetDevice)
	mux.HandleFunc("POST /api/v1/devices/{id}/revoke", s.handleRevokeDevice)
	mux.HandleFunc("POST /api/v1/devices/{id}/peer-key", s.handlePeerKey)
	mux.HandleFunc("POST /api/v1/link-codes", s.handleCreateLink)
	mux.HandleFunc("GET /api/v1/link-codes/{code}", s.handleGetLink)
	mux.HandleFunc("POST /api/v1/link-codes/{code}/confirm", s.handleConfirmLink)
	mux.HandleFunc("GET /api/v1/ws", s.handleWS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("relay: panic: %v", rec)
				writeAPIError(w, http.StatusInternalServerError, relaywire.CodeInternal, "erro interno")
			}
		}()
		if !s.limiter.AllowRequest("ip:" + clientIP(r)) {
			writeAPIError(w, http.StatusTooManyRequests, relaywire.CodeRateLimited, "muitas requisições")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"service": "hivemind-relay",
		"version": Version,
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ipKey := "ip:" + clientIP(r)
	mailKey := "mail:" + strings.ToLower(strings.TrimSpace(req.Email))
	if blocked, retry := s.blocked(ipKey, mailKey); blocked {
		writeRate(w, retry)
		return
	}
	if !s.limiter.AllowLogin(ipKey) {
		writeRate(w, 30*time.Second)
		return
	}
	s.argonMu.Lock()
	sess, err := s.store.Login(req.Email, req.Password, s.dummy, r.UserAgent(), s.argon, s.accessTTL, s.refreshTTL)
	s.argonMu.Unlock()
	if err != nil {
		if statusOf(err) == http.StatusUnauthorized {
			s.limiter.Fail(ipKey)
			s.limiter.Fail(mailKey)
		}
		writeStoreErr(w, err)
		return
	}
	s.limiter.Success(ipKey)
	s.limiter.Success(mailKey)
	writeJSON(w, http.StatusOK, tokenJSON(sess))
}

func (s *Server) handleCreateQRTicket(w http.ResponseWriter, r *http.Request) {
	acct, _, ok := s.user(w, r)
	if !ok {
		return
	}
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		writeAPIError(w, http.StatusInternalServerError, relaywire.CodeInternal, "falha ao gerar ticket")
		return
	}
	ticket := "hmr_qr_" + hex.EncodeToString(raw[:])
	now := time.Now().UTC()
	exp := now.Add(5 * time.Minute)

	s.qrMu.Lock()
	if s.qrTickets == nil {
		s.qrTickets = make(map[string]qrLoginTicket)
	}
	for k, v := range s.qrTickets {
		if now.After(v.expiresAt) {
			delete(s.qrTickets, k)
		}
	}
	s.qrTickets[ticket] = qrLoginTicket{accountID: acct.ID, expiresAt: exp}
	s.qrMu.Unlock()

	base := s.publicURL
	if base == "" {
		scheme := "https"
		if r.TLS == nil && !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "http"
		}
		if r.Host != "" {
			base = scheme + "://" + r.Host
		}
	}
	uri := "hivemind://qr-login?relay=" + url.QueryEscape(base) + "&ticket=" + url.QueryEscape(ticket)
	writeJSON(w, http.StatusCreated, map[string]any{
		"ticket":    ticket,
		"expiresAt": exp.Format(time.RFC3339),
		"uri":       uri,
	})
}

func (s *Server) handleRedeemQRTicket(w http.ResponseWriter, r *http.Request) {
	ipKey := "ip:" + clientIP(r)
	if !s.limiter.AllowLogin(ipKey) {
		writeAPIError(w, http.StatusTooManyRequests, relaywire.CodeRateLimited, "muitas tentativas; aguarde um instante")
		return
	}
	var req struct {
		Ticket string `json:"ticket"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ticket := strings.TrimSpace(req.Ticket)
	if !strings.HasPrefix(ticket, "hmr_qr_") {
		s.limiter.Fail(ipKey)
		writeAPIError(w, http.StatusUnauthorized, relaywire.CodeUnauthorized, "QR Code inválido ou expirado")
		return
	}
	now := time.Now().UTC()
	s.qrMu.Lock()
	entry, ok := s.qrTickets[ticket]
	if ok {
		delete(s.qrTickets, ticket)
	}
	s.qrMu.Unlock()
	if !ok || now.After(entry.expiresAt) {
		s.limiter.Fail(ipKey)
		writeAPIError(w, http.StatusUnauthorized, relaywire.CodeUnauthorized, "QR Code inválido ou expirado")
		return
	}
	sess, err := s.store.issue(entry.accountID, r.UserAgent(), s.accessTTL, s.refreshTTL)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.limiter.Success(ipKey)
	writeJSON(w, http.StatusOK, tokenJSON(sess))
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refreshToken"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	sess, err := s.store.Refresh(strings.TrimSpace(req.RefreshToken), r.UserAgent(), s.accessTTL, s.refreshTTL)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tokenJSON(sess))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refreshToken"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, controlBodyLimit)).Decode(&req)
	var sessionIDs []string
	if tok := bearer(r); tok != "" {
		if _, sid, err := s.store.LookupAccess(tok); err == nil {
			sessionIDs = append(sessionIDs, sid)
			_ = s.store.RevokeSession(sid)
		}
	}
	if strings.TrimSpace(req.RefreshToken) != "" {
		if sid, err := s.store.RevokeByRefresh(strings.TrimSpace(req.RefreshToken)); err == nil {
			sessionIDs = append(sessionIDs, sid)
		}
	}
	if len(sessionIDs) == 0 {
		writeAPIError(w, http.StatusUnauthorized, relaywire.CodeUnauthorized, "sessão não encontrada")
		return
	}
	for _, sid := range sessionIDs {
		s.hub.KickSession(sid)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	acct, _, ok := s.user(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, accountJSON(acct))
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	acct, sid, ok := s.user(w, r)
	if !ok {
		return
	}
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s.argonMu.Lock()
	sess, err := s.store.ChangePassword(acct.ID, req.CurrentPassword, req.NewPassword, r.UserAgent(), s.argon, s.accessTTL, s.refreshTTL)
	s.argonMu.Unlock()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.hub.KickSession(sid)
	writeJSON(w, http.StatusOK, tokenJSON(sess))
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	acct, _, ok := s.user(w, r)
	if !ok {
		return
	}
	if acct.Role != "owner" {
		writeAPIError(w, http.StatusForbidden, relaywire.CodeForbidden, "somente o owner cria contas")
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s.argonMu.Lock()
	created, err := s.store.CreateAccount(req.Email, req.Password, "member", s.argon)
	s.argonMu.Unlock()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, accountJSON(created))
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	acct, _, ok := s.user(w, r)
	if !ok {
		return
	}
	list, err := s.store.ListDevices(acct.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	out := make([]any, 0, len(list))
	for _, d := range list {
		out = append(out, s.deviceJSON(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

func (s *Server) handleCreateDevice(w http.ResponseWriter, r *http.Request) {
	acct, _, ok := s.user(w, r)
	if !ok {
		return
	}
	var req struct {
		Name      string `json:"name"`
		PublicKey string `json:"publicKey"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	pub, err := relaywire.ParseKey(req.PublicKey)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, relaywire.CodeInvalid, err.Error())
		return
	}
	dev, token, err := s.store.CreateDevice(acct.ID, req.Name, pub[:], nil)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"device":      s.deviceJSON(dev),
		"deviceToken": token,
	})
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	acct, _, ok := s.user(w, r)
	if !ok {
		return
	}
	dev, err := s.store.DeviceForAccount(acct.ID, r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if dev.Revoked() {
		writeAPIError(w, http.StatusNotFound, relaywire.CodeNotFound, "IDE não encontrada")
		return
	}
	writeJSON(w, http.StatusOK, s.deviceJSON(dev))
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	acct, _, ok := s.user(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if err := s.store.RevokeDevice(acct.ID, id); err != nil {
		writeStoreErr(w, err)
		return
	}
	s.hub.KickDevice(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePeerKey(w http.ResponseWriter, r *http.Request) {
	acct, _, ok := s.user(w, r)
	if !ok {
		return
	}
	var req struct {
		PublicKey string `json:"publicKey"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	pub, err := relaywire.ParseKey(req.PublicKey)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, relaywire.CodeInvalid, err.Error())
		return
	}
	id := r.PathValue("id")
	dev, err := s.store.SetAppKey(acct.ID, id, pub[:])
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	s.pushPeerKey(dev, relaywire.RoleApp, relaywire.EncodeKey(pub))
	writeJSON(w, http.StatusOK, s.deviceJSON(dev))
}

func (s *Server) handleCreateLink(w http.ResponseWriter, r *http.Request) {
	acct, _, ok := s.user(w, r)
	if !ok {
		return
	}
	var req struct {
		Role      string `json:"role"`
		Name      string `json:"name"`
		PublicKey string `json:"publicKey"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	pub, err := relaywire.ParseKey(req.PublicKey)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, relaywire.CodeInvalid, err.Error())
		return
	}
	lc, err := s.store.CreateLinkCode(acct.ID, req.Role, req.Name, pub[:])
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.linkJSON(r, lc, ""))
}

func (s *Server) handleGetLink(w http.ResponseWriter, r *http.Request) {
	acct, _, ok := s.user(w, r)
	if !ok {
		return
	}
	lc, err := s.store.GetLinkCode(acct.ID, r.PathValue("code"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	token := ""
	if lc.Initiator == relaywire.RoleIDE && !lc.ConsumedAt.IsZero() {
		token, err = s.store.TakeLinkDeviceToken(acct.ID, lc.Code)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
	}
	status := "pending"
	switch {
	case time.Now().UTC().After(lc.ExpiresAt) && lc.ConsumedAt.IsZero():
		status = "expired"
	case !lc.ConsumedAt.IsZero():
		status = "ready"
	}
	body := s.linkJSON(r, lc, token)
	body["status"] = status
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleConfirmLink(w http.ResponseWriter, r *http.Request) {
	acct, _, ok := s.user(w, r)
	if !ok {
		return
	}
	ipKey := "link:" + acct.ID
	if blocked, retry := s.limiter.LoginBlocked(ipKey); blocked {
		writeRate(w, retry)
		return
	}
	var req struct {
		PublicKey string `json:"publicKey"`
		Name      string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	pub, err := relaywire.ParseKey(req.PublicKey)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, relaywire.CodeInvalid, err.Error())
		return
	}
	dev, token, err := s.store.ConfirmLink(acct.ID, r.PathValue("code"), req.Name, pub[:])
	if err != nil {
		if statusOf(err) == http.StatusNotFound || statusOf(err) == http.StatusGone {
			s.limiter.Fail(ipKey)
		}
		writeStoreErr(w, err)
		return
	}
	s.limiter.Success(ipKey)
	resp := map[string]any{"device": s.deviceJSON(dev)}
	if token != "" {
		resp["deviceToken"] = token
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) pushPeerKey(dev Device, publishedRole, pub string) {
	notice := relaywire.PeerKeyNotice{PublicKey: pub, Role: publishedRole}
	raw, _ := json.Marshal(notice)
	env := relaywire.Envelope{V: relaywire.ProtocolVersion, Type: relaywire.TypePeerKey, DeviceID: dev.ID, Payload: raw}
	frame, _ := json.Marshal(env)
	target := relaywire.RoleIDE
	if publishedRole == relaywire.RoleIDE {
		target = relaywire.RoleApp
	}
	s.hub.NotifyPeerKey(dev.ID, target, frame)
}

func (s *Server) user(w http.ResponseWriter, r *http.Request) (Account, string, bool) {
	acct, sid, err := s.store.LookupAccess(bearer(r))
	if err != nil {
		writeStoreErr(w, err)
		return Account{}, "", false
	}
	return acct, sid, true
}

func (s *Server) blocked(keys ...string) (bool, time.Duration) {
	var max time.Duration
	blocked := false
	for _, k := range keys {
		if ok, retry := s.limiter.LoginBlocked(k); ok {
			blocked = true
			if retry > max {
				max = retry
			}
		}
	}
	return blocked, max
}

func (s *Server) deviceJSON(d Device) map[string]any {
	m := map[string]any{
		"id":           d.ID,
		"name":         d.Name,
		"online":       s.hub.Online(d.ID),
		"createdAt":    d.CreatedAt.Format(time.RFC3339),
		"idePublicKey": encodeKey(d.IdePublicKey),
		"appPublicKey": encodeKey(d.AppPublicKey),
		"e2eReady":     d.E2EReady(),
		"eventSeq":     d.EventSeq,
	}
	if !d.LastSeenAt.IsZero() {
		m["lastSeenAt"] = d.LastSeenAt.Format(time.RFC3339)
	}
	return m
}

func (s *Server) linkJSON(r *http.Request, lc LinkCode, deviceToken string) map[string]any {
	m := map[string]any{
		"code":         lc.Code,
		"role":         lc.Initiator,
		"name":         lc.Name,
		"expiresAt":    lc.ExpiresAt.Format(time.RFC3339),
		"uri":          relaywire.LinkURI(s.publicBase(r), lc.Code),
		"idePublicKey": encodeKey(lc.IdePublicKey),
		"appPublicKey": encodeKey(lc.AppPublicKey),
	}
	if lc.DeviceID != "" {
		m["deviceId"] = lc.DeviceID
	}
	if deviceToken != "" {
		m["deviceToken"] = deviceToken
	}
	return m
}

func (s *Server) publicBase(r *http.Request) string {
	if s.publicURL != "" {
		return s.publicURL
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if isLoopback(clientIP(r)) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func tokenJSON(sess issuedSession) map[string]any {
	return map[string]any{
		"accessToken":  sess.AccessToken,
		"refreshToken": sess.RefreshToken,
		"expiresIn":    sess.ExpiresIn,
		"tokenType":    "Bearer",
		"account":      accountJSON(sess.Account),
	}
}

func accountJSON(a Account) map[string]any {
	return map[string]any{"id": a.ID, "email": a.Email, "role": a.Role}
}

func encodeKey(b []byte) string {
	if len(b) != 32 {
		return ""
	}
	var k [32]byte
	copy(k[:], b)
	return relaywire.EncodeKey(k)
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, controlBodyLimit))
	if err := dec.Decode(dst); err != nil {
		writeAPIError(w, http.StatusBadRequest, relaywire.CodeInvalid, "corpo JSON inválido")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, code, msg string) {
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "30")
	}
	writeJSON(w, status, relaywire.APIError{Error: relaywire.ErrorBody{Code: code, Message: msg}})
}

func writeRate(w http.ResponseWriter, retry time.Duration) {
	sec := int(retry.Seconds())
	if sec < 1 {
		sec = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(sec))
	writeJSON(w, http.StatusTooManyRequests, relaywire.APIError{Error: relaywire.ErrorBody{
		Code: relaywire.CodeRateLimited, Message: "muitas tentativas; tente de novo mais tarde",
	}})
}

func writeStoreErr(w http.ResponseWriter, err error) {
	var he *httpError
	if errors.As(err, &he) {
		if he.Status == http.StatusTooManyRequests {
			writeRate(w, 30*time.Second)
			return
		}
		writeAPIError(w, he.Status, he.Code, he.Msg)
		return
	}
	log.Printf("relay: %v", err)
	writeAPIError(w, http.StatusInternalServerError, relaywire.CodeInternal, "erro interno")
}

func statusOf(err error) int {
	var he *httpError
	if errors.As(err, &he) {
		return he.Status
	}
	return 500
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if isLoopback(host) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if ip := net.ParseIP(first); ip != nil {
				return ip.String()
			}
		}
	}
	return host
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
