package remote

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"

	"github.com/gilvan-Borges/HiveMind/internal/secretx"
	"github.com/gilvan-Borges/HiveMind/relaywire"
)

const (
	offerTTL       = 5 * time.Minute
	maxPlainBytes  = 12 << 20
	lanProto       = 2
	lanBoxName     = "lan-bridge-box"
	lanTokenPrefix = "hmlan_"

	redeemMaxFails = 8
	redeemWindow   = 10 * time.Minute
	redeemBlock    = 15 * time.Minute
)

// PairURI is the public half of a one-time LAN offer. It never carries the session token.
type PairURI struct {
	URLs  []string
	Offer string
	PK    string
	Exp   int64
	Host  string
}

type pendingOffer struct {
	id   string
	pub  [32]byte
	priv [32]byte
	exp  time.Time
}

type localCap struct{}

type localKey struct{}

type peerKey struct{}

type lanFrame struct {
	V        int                  `json:"v"`
	DeviceID string               `json:"deviceId"`
	Seq      uint64               `json:"seq"`
	Enc      string               `json:"enc"`
	Payload  relaywire.BoxPayload `json:"payload"`
}

type plainMeta struct {
	Token   string `json:"token"`
	Type    string `json:"type"`
	LastSeq uint64 `json:"lastSeq"`
}

type redeemBody struct {
	AppPub     string `json:"appPub"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type redeemPlain struct {
	V      int    `json:"v"`
	Offer  string `json:"offer"`
	AppPub string `json:"appPub"`
	Nonce  string `json:"nonce"`
}

type redeemOK struct {
	V        int    `json:"v"`
	Token    string `json:"token"`
	BoxPub   string `json:"boxPub"`
	DeviceID string `json:"deviceId"`
}

type redeemFail struct {
	n     int
	start time.Time
	until time.Time
}

type redeemLimiter struct {
	mu sync.Mutex
	by map[string]*redeemFail
}

func (s *Server) clock() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Server) secretsOrOS() secretx.Vault {
	if s != nil && s.secrets != nil {
		return s.secrets
	}
	return secretx.NewOSVault()
}

// AttachLocal marks an in-process call. The mark cannot be set by a LAN client.
func (s *Server) AttachLocal(r *http.Request) *http.Request {
	if s == nil || r == nil || s.local == nil {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), localKey{}, s.local))
}

func (s *Server) localOK(r *http.Request) bool {
	if s == nil || s.local == nil || r == nil {
		return false
	}
	cap, ok := r.Context().Value(localKey{}).(*localCap)
	return ok && cap == s.local
}

// Pairing mints a fresh offer and invalidates the previous one. The session token is not included.
func (s *Server) Pairing() (PairingInfo, error) {
	kp, err := relaywire.NewKeyPair()
	if err != nil {
		return PairingInfo{}, err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return PairingInfo{}, err
	}
	exp := s.clock().Add(offerTTL)
	off := &pendingOffer{
		id:   base64.RawURLEncoding.EncodeToString(raw),
		pub:  kp.Public,
		priv: kp.Private,
		exp:  exp,
	}
	st := s.Status()
	s.mu.Lock()
	s.offer = off
	s.mu.Unlock()
	uri := BuildPairURI(st.URLs, off.id, kp.PublicB64(), exp.Unix(), st.Host)
	dataURL, err := QRDataURL(uri, 320)
	if err != nil {
		return PairingInfo{}, err
	}
	return PairingInfo{
		URI:       uri,
		URLs:      st.URLs,
		Host:      st.Host,
		QRDataURL: dataURL,
		Exp:       exp.Unix(),
	}, nil
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request, next http.Handler) {
	if r.URL.Path == "/api/v1/health" {
		next.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/api/v1/pair/redeem" {
		s.handleRedeem(w, r)
		return
	}
	if s.localOK(r) {
		next.ServeHTTP(w, r)
		return
	}
	plain, peer, ok := s.acceptFrame(w, r)
	if !ok {
		return
	}
	if isResume(plain) {
		s.writeResume(w, peer)
		return
	}
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(bytes.NewReader(plain))
	r2.ContentLength = int64(len(plain))
	r2 = r2.WithContext(context.WithValue(r2.Context(), peerKey{}, peer))
	if r.URL.Path == "/api/v1/events" {
		next.ServeHTTP(w, r2)
		return
	}
	sw := &sealWriter{dst: w, srv: s, peer: peer}
	next.ServeHTTP(sw, r2)
	sw.finish()
}

func isResume(plain []byte) bool {
	var meta plainMeta
	if err := json.Unmarshal(plain, &meta); err != nil {
		return false
	}
	return meta.Type == "resume"
}

func (s *Server) writeResume(w http.ResponseWriter, peer [32]byte) {
	events := []StreamEvent{}
	if s.hub != nil {
		events = s.hub.ReplayEvents()
	}
	s.finishSealed(w, http.StatusOK, peer, map[string]any{
		"v":      lanProto,
		"type":   "resume",
		"events": events,
	})
}

func (s *Server) acceptFrame(w http.ResponseWriter, r *http.Request) ([]byte, [32]byte, bool) {
	var zero [32]byte
	body, err := readLimited(r, maxBodyBytes)
	if err != nil {
		writeAPIError(w, http.StatusRequestEntityTooLarge, relaywire.CodeInvalid, "corpo grande demais")
		return nil, zero, false
	}
	var fr lanFrame
	if err := json.Unmarshal(body, &fr); err != nil || fr.V != lanProto || fr.Enc != relaywire.EncNaClBox || fr.DeviceID == "" || fr.Seq == 0 || fr.Payload.Nonce == "" || fr.Payload.Ciphertext == "" {
		writeAPIError(w, http.StatusUnauthorized, relaywire.CodeUnauthorized, "não autorizado")
		return nil, zero, false
	}
	s.mu.Lock()
	dev, found := s.deviceCopy(fr.DeviceID)
	hash := s.cfg.TokenHash
	s.mu.Unlock()
	if !found || hash == "" {
		writeAPIError(w, http.StatusUnauthorized, relaywire.CodeUnauthorized, "não autorizado")
		return nil, zero, false
	}
	appPub, err := relaywire.ParseKey(dev.AppPub)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, relaywire.CodeUnauthorized, "não autorizado")
		return nil, zero, false
	}
	kp, err := s.ensureBox()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, relaywire.CodeInternal, "cofre indisponível")
		return nil, zero, false
	}
	plain, err := kp.Open(appPub, fr.Payload)
	if err != nil || len(plain) > maxPlainBytes {
		writeAPIError(w, http.StatusUnauthorized, relaywire.CodeUnauthorized, "não autorizado")
		return nil, zero, false
	}
	var meta plainMeta
	if err := json.Unmarshal(plain, &meta); err != nil || !tokenMatches(meta.Token, hash) {
		writeAPIError(w, http.StatusUnauthorized, relaywire.CodeUnauthorized, "não autorizado")
		return nil, zero, false
	}
	s.mu.Lock()
	dev, found = s.deviceCopy(fr.DeviceID)
	if !found {
		s.mu.Unlock()
		writeAPIError(w, http.StatusUnauthorized, relaywire.CodeUnauthorized, "não autorizado")
		return nil, zero, false
	}
	if fr.Seq <= dev.LastSeq {
		s.mu.Unlock()
		s.finishSealed(w, http.StatusConflict, appPub, relaywire.APIError{Error: relaywire.ErrorBody{Code: relaywire.CodeConflict, Message: "seq repetido"}})
		return nil, zero, false
	}
	if fr.Seq > dev.LastSeq+1 {
		expect := dev.LastSeq + 1
		s.mu.Unlock()
		s.finishSealed(w, http.StatusConflict, appPub, map[string]any{
			"v": lanProto, "type": "resume", "expectSeq": expect,
		})
		return nil, zero, false
	}
	s.setDeviceSeq(fr.DeviceID, fr.Seq)
	if err := saveConfig(s.configPath, s.cfg); err != nil {
		s.setDeviceSeq(fr.DeviceID, fr.Seq-1)
		s.mu.Unlock()
		writeAPIError(w, http.StatusInternalServerError, relaywire.CodeInternal, "falha ao gravar o estado")
		return nil, zero, false
	}
	s.mu.Unlock()
	return plain, appPub, true
}

func (s *Server) deviceCopy(id string) (LanDevice, bool) {
	for i := range s.cfg.Devices {
		if s.cfg.Devices[i].DeviceID == id {
			return s.cfg.Devices[i], true
		}
	}
	return LanDevice{}, false
}

func (s *Server) setDeviceSeq(id string, seq uint64) {
	for i := range s.cfg.Devices {
		if s.cfg.Devices[i].DeviceID == id {
			s.cfg.Devices[i].LastSeq = seq
			return
		}
	}
}

func (s *Server) handleRedeem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, relaywire.CodeInvalid, "método inválido")
		return
	}
	ip := remoteIP(r)
	if s.redeemBlocked(ip) {
		writeAPIError(w, http.StatusTooManyRequests, relaywire.CodeRateLimited, "muitas tentativas")
		return
	}
	body, err := readLimited(r, maxBodyBytes)
	if err != nil {
		s.noteRedeemFail(ip)
		writeAPIError(w, http.StatusRequestEntityTooLarge, relaywire.CodeInvalid, "corpo grande demais")
		return
	}
	var req redeemBody
	if err := json.Unmarshal(body, &req); err != nil || req.Nonce == "" || req.Ciphertext == "" || req.AppPub == "" {
		s.noteRedeemFail(ip)
		writeAPIError(w, http.StatusBadRequest, relaywire.CodeInvalid, "corpo JSON inválido")
		return
	}
	appPub, err := relaywire.ParseKey(req.AppPub)
	if err != nil {
		s.noteRedeemFail(ip)
		s.writeOfferInvalid(w)
		return
	}
	s.mu.Lock()
	off := s.offer
	s.mu.Unlock()
	if off == nil || !s.clock().Before(off.exp) {
		s.noteRedeemFail(ip)
		s.writeOfferInvalid(w)
		return
	}
	offerKey := &relaywire.KeyPair{Public: off.pub, Private: off.priv}
	plain, err := offerKey.Open(appPub, relaywire.BoxPayload{Nonce: req.Nonce, Ciphertext: req.Ciphertext})
	if err != nil || len(plain) > maxPlainBytes {
		s.noteRedeemFail(ip)
		s.writeOfferInvalid(w)
		return
	}
	var inner redeemPlain
	if err := json.Unmarshal(plain, &inner); err != nil || inner.V != lanProto || inner.Offer != off.id || inner.AppPub != req.AppPub || !nonceOK(inner.Nonce) {
		s.noteRedeemFail(ip)
		s.writeOfferInvalid(w)
		return
	}
	s.mu.Lock()
	if s.offer == nil || s.offer.id != off.id || !s.clock().Before(s.offer.exp) {
		s.mu.Unlock()
		s.noteRedeemFail(ip)
		s.writeOfferInvalid(w)
		return
	}
	s.offer = nil
	prev := s.cfg
	s.mu.Unlock()

	kp, err := s.ensureBox()
	if err != nil {
		s.noteRedeemFail(ip)
		writeAPIError(w, http.StatusServiceUnavailable, relaywire.CodeInternal, "cofre indisponível")
		return
	}
	tok, err := newSessionToken()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, relaywire.CodeInternal, "falha ao gerar a sessão")
		return
	}
	devID, err := newDeviceID()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, relaywire.CodeInternal, "falha ao gerar a sessão")
		return
	}
	s.mu.Lock()
	s.cfg.Token = ""
	s.cfg.TokenHash = hashToken(tok)
	s.cfg.BoxPub = kp.PublicB64()
	s.cfg.Devices = []LanDevice{{
		DeviceID: devID,
		AppPub:   req.AppPub,
		LastSeq:  0,
	}}
	if err := saveConfig(s.configPath, s.cfg); err != nil {
		s.cfg = prev
		s.mu.Unlock()
		writeAPIError(w, http.StatusInternalServerError, relaywire.CodeInternal, "falha ao gravar o estado")
		return
	}
	s.mu.Unlock()

	sealed, err := offerKey.SealJSON(appPub, redeemOK{
		V:        lanProto,
		Token:    tok,
		BoxPub:   kp.PublicB64(),
		DeviceID: devID,
	})
	if err != nil {
		s.mu.Lock()
		s.cfg = prev
		_ = saveConfig(s.configPath, s.cfg)
		s.mu.Unlock()
		s.noteRedeemFail(ip)
		writeAPIError(w, http.StatusInternalServerError, relaywire.CodeInternal, "falha ao cifrar a resposta")
		return
	}
	s.noteRedeemSuccess(ip)
	writeJSON(w, http.StatusOK, json.RawMessage(sealed))
}

func (s *Server) writeOfferInvalid(w http.ResponseWriter) {
	writeAPIError(w, http.StatusGone, relaywire.CodeExpired, "oferta inválida")
}

func nonceOK(s string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil && len(raw) == 16
}

func (s *Server) ensureBox() (*relaywire.KeyPair, error) {
	s.boxMu.Lock()
	defer s.boxMu.Unlock()
	if s.box != nil {
		return s.box, nil
	}
	v := s.secretsOrOS()
	ok, why := v.Available()
	if !ok {
		if why == "" {
			return nil, secretx.ErrVaultUnavailable
		}
		return nil, fmt.Errorf("%w: %s", secretx.ErrVaultUnavailable, why)
	}
	existing, err := v.Get(lanBoxName)
	if err != nil && !errors.Is(err, secretx.ErrSecretNotFound) {
		return nil, err
	}
	var kp *relaywire.KeyPair
	if errors.Is(err, secretx.ErrSecretNotFound) || existing == "" {
		kp, err = relaywire.NewKeyPair()
		if err != nil {
			return nil, err
		}
		if err := v.Set(lanBoxName, base64.RawURLEncoding.EncodeToString(kp.Private[:])); err != nil {
			return nil, err
		}
	} else {
		kp, err = keyFromPrivate(existing)
		if err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	if s.cfg.BoxPub != kp.PublicB64() {
		prev := s.cfg.BoxPub
		s.cfg.BoxPub = kp.PublicB64()
		if err := saveConfig(s.configPath, s.cfg); err != nil {
			s.cfg.BoxPub = prev
			s.mu.Unlock()
			return nil, err
		}
	}
	s.box = kp
	s.mu.Unlock()
	return kp, nil
}

func keyFromPrivate(b64 string) (*relaywire.KeyPair, error) {
	raw, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil || len(raw) != 32 {
		return nil, secretx.ErrVaultUnavailable
	}
	pub, err := curve25519.X25519(raw, curve25519.Basepoint)
	if err != nil || len(pub) != 32 {
		return nil, secretx.ErrVaultUnavailable
	}
	kp := &relaywire.KeyPair{}
	copy(kp.Private[:], raw)
	copy(kp.Public[:], pub)
	return kp, nil
}

func (s *Server) finishSealed(w http.ResponseWriter, status int, peer [32]byte, v any) {
	kp, err := s.ensureBox()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, relaywire.CodeInternal, "cofre indisponível")
		return
	}
	raw, err := kp.SealJSON(peer, v)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, relaywire.CodeInternal, "falha ao cifrar a resposta")
		return
	}
	writeJSON(w, status, json.RawMessage(raw))
}

func (s *Server) writeStream(w io.Writer, fl http.Flusher, r *http.Request, ev StreamEvent) {
	if peer, ok := r.Context().Value(peerKey{}).([32]byte); ok {
		kp, err := s.ensureBox()
		if err != nil {
			return
		}
		raw, err := kp.SealJSON(peer, ev)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", raw)
		fl.Flush()
		return
	}
	buf, _ := json.Marshal(ev)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, buf)
	fl.Flush()
}

type sealWriter struct {
	dst  http.ResponseWriter
	srv  *Server
	peer [32]byte
	buf  bytes.Buffer
	code int
	done bool
}

func (w *sealWriter) Header() http.Header { return w.dst.Header() }

func (w *sealWriter) WriteHeader(status int) {
	if w.code == 0 {
		w.code = status
	}
}

func (w *sealWriter) Write(p []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.buf.Write(p)
}

func (w *sealWriter) finish() {
	if w == nil || w.done {
		return
	}
	w.done = true
	status := w.code
	if status == 0 {
		status = http.StatusOK
	}
	kp, err := w.srv.ensureBox()
	if err != nil {
		writeAPIError(w.dst, http.StatusServiceUnavailable, relaywire.CodeInternal, "cofre indisponível")
		return
	}
	box, err := kp.Seal(w.peer, w.buf.Bytes())
	if err != nil {
		writeAPIError(w.dst, http.StatusInternalServerError, relaywire.CodeInternal, "falha ao cifrar a resposta")
		return
	}
	writeJSON(w.dst, status, box)
}

func readLimited(r *http.Request, n int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, n+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > n {
		return nil, fmt.Errorf("corpo grande demais")
	}
	return b, nil
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		host = r.RemoteAddr
	}
	if host == "" {
		return "desconhecido"
	}
	return host
}

func (s *Server) limiter() *redeemLimiter {
	if s.redeems == nil {
		s.redeems = &redeemLimiter{by: map[string]*redeemFail{}}
	}
	return s.redeems
}

func (s *Server) redeemBlocked(ip string) bool {
	return s.limiter().blocked(ip, s.clock())
}

func (s *Server) noteRedeemFail(ip string) {
	s.limiter().fail(ip, s.clock())
}

func (s *Server) noteRedeemSuccess(ip string) {
	s.limiter().success(ip)
}

func (l *redeemLimiter) blocked(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.by[ip]
	if st == nil {
		return false
	}
	if !st.until.IsZero() && now.Before(st.until) {
		return true
	}
	return false
}

func (l *redeemLimiter) fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.by[ip]
	if st == nil || (!st.until.IsZero() && !now.Before(st.until)) || (st.until.IsZero() && now.Sub(st.start) > redeemWindow) {
		st = &redeemFail{start: now}
		l.by[ip] = st
	}
	if !st.until.IsZero() && now.Before(st.until) {
		return
	}
	st.n++
	if st.n >= redeemMaxFails {
		st.until = now.Add(redeemBlock)
	}
	if len(l.by) > 4096 {
		for k, v := range l.by {
			if k == ip {
				continue
			}
			if (!v.until.IsZero() && !now.Before(v.until)) || (v.until.IsZero() && now.Sub(v.start) > redeemWindow) {
				delete(l.by, k)
			}
		}
		if len(l.by) > 4096 {
			for k, v := range l.by {
				if k != ip && (v.until.IsZero() || !now.Before(v.until)) {
					delete(l.by, k)
					if len(l.by) <= 4096 {
						break
					}
				}
			}
		}
	}
}

func (l *redeemLimiter) success(ip string) {
	l.mu.Lock()
	delete(l.by, ip)
	l.mu.Unlock()
}

func newSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return lanTokenPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func newDeviceID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "lan_" + hex.EncodeToString(b), nil
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func tokenMatches(tok, hexHash string) bool {
	if tok == "" || len(hexHash) != hex.EncodedLen(sha256.Size) {
		return false
	}
	sum := sha256.Sum256([]byte(tok))
	want, err := hex.DecodeString(hexHash)
	if err != nil || len(want) != sha256.Size {
		return false
	}
	return subtle.ConstantTimeCompare(sum[:], want) == 1
}

func writeAPIError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, relaywire.APIError{Error: relaywire.ErrorBody{Code: code, Message: msg}})
}
