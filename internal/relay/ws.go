package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/gilvan-Borges/HiveMind/relaywire"
)

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	token, sub := tokenFromRequest(r)
	if token == "" {
		writeAPIError(w, http.StatusUnauthorized, relaywire.CodeUnauthorized, "token ausente")
		return
	}
	if !s.hub.reserve() {
		writeAPIError(w, http.StatusServiceUnavailable, relaywire.CodeRateLimited, "muitas conexões")
		return
	}
	attached := false
	var conn *clientConn
	defer func() {
		if attached {
			_ = s.store.TouchDevice(conn.deviceID, time.Now())
			s.hub.Detach(conn)
			conn.Close()
		} else {
			s.hub.release()
		}
	}()

	opts := &websocket.AcceptOptions{
		// Native clients (IDE and Android) are not browsers. Authorization is
		// the bearer token, checked before any frame is routed.
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	}
	if sub != "" {
		opts.Subprotocols = []string{sub}
	}
	ws, err := websocket.Accept(w, r, opts)
	if err != nil {
		return
	}
	ws.SetReadLimit(s.maxFrame)
	defer func() { _ = ws.Close(websocket.StatusNormalClosure, "") }()

	ctx := r.Context()
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, data, err := ws.Read(hctx)
	cancel()
	if err != nil {
		return
	}
	var helloEnv relaywire.Envelope
	if err := json.Unmarshal(data, &helloEnv); err != nil || helloEnv.Type != relaywire.TypeHello || helloEnv.V != relaywire.ProtocolVersion {
		_ = writeWSError(ctx, ws, "", relaywire.CodeInvalid, "hello obrigatório")
		return
	}
	var hello relaywire.Hello
	if len(helloEnv.Payload) > 0 {
		_ = json.Unmarshal(helloEnv.Payload, &hello)
	}
	accountID, sessionID, deviceTokenID, role, err := s.principal(token)
	if err != nil {
		_ = writeWSError(ctx, ws, helloEnv.ID, relaywire.CodeUnauthorized, "não autorizado")
		return
	}
	if helloEnv.Role != role {
		_ = writeWSError(ctx, ws, helloEnv.ID, relaywire.CodeForbidden, "papel não confere com o token")
		return
	}
	deviceID := strings.TrimSpace(helloEnv.DeviceID)
	if deviceID == "" {
		_ = writeWSError(ctx, ws, helloEnv.ID, relaywire.CodeInvalid, "deviceId obrigatório")
		return
	}
	if role == relaywire.RoleIDE && deviceID != deviceTokenID {
		_ = writeWSError(ctx, ws, helloEnv.ID, relaywire.CodeForbidden, "credencial não pertence a esta IDE")
		return
	}
	dev, err := s.store.DeviceForAccount(accountID, deviceID)
	if err != nil || dev.Revoked() {
		_ = writeWSError(ctx, ws, helloEnv.ID, relaywire.CodeNotFound, "IDE não encontrada")
		return
	}
	if role == relaywire.RoleIDE && hello.PublicKey != "" {
		pub, perr := relaywire.ParseKey(hello.PublicKey)
		if perr != nil || !bytes.Equal(pub[:], dev.IdePublicKey) {
			_ = writeWSError(ctx, ws, helloEnv.ID, relaywire.CodeKeyMismatch, "chave da IDE não confere")
			return
		}
	}
	if role == relaywire.RoleApp && hello.PublicKey != "" {
		pub, perr := relaywire.ParseKey(hello.PublicKey)
		if perr != nil {
			_ = writeWSError(ctx, ws, helloEnv.ID, relaywire.CodeInvalid, "chave pública inválida")
			return
		}
		if !bytes.Equal(pub[:], dev.AppPublicKey) {
			dev, err = s.store.SetAppKey(accountID, deviceID, pub[:])
			if err != nil {
				_ = writeWSError(ctx, ws, helloEnv.ID, relaywire.CodeInvalid, "não foi possível gravar a chave")
				return
			}
			s.pushPeerKey(dev, relaywire.RoleApp, relaywire.EncodeKey(pub))
		}
	}

	conn = newConn(ws, role, accountID, deviceID, sessionID)
	if err := s.hub.Attach(conn); err != nil {
		var he *httpError
		code := relaywire.CodeForbidden
		if errors.As(err, &he) {
			code = he.Code
		}
		_ = writeWSError(ctx, ws, helloEnv.ID, code, err.Error())
		return
	}
	attached = true
	_ = s.store.TouchDevice(deviceID, time.Now())

	go func() {
		s.writeLoop(ctx, conn)
		conn.Close()
	}()

	peer := encodeKey(dev.IdePublicKey)
	online := false
	if role == relaywire.RoleIDE {
		peer = encodeKey(dev.AppPublicKey)
		online = true
	} else {
		online = s.hub.Online(deviceID)
	}
	okBody, _ := json.Marshal(relaywire.HelloOK{
		Seq:           dev.EventSeq,
		ResumeFrom:    hello.ResumeAfter,
		PeerPublicKey: peer,
		E2EReady:      dev.E2EReady(),
		Online:        online,
	})
	s.enqueue(conn, relaywire.Envelope{
		V: 1, ID: helloEnv.ID, Type: relaywire.TypeHelloOK, DeviceID: deviceID, Payload: okBody,
	})

	if role == relaywire.RoleApp {
		if evs, err := s.store.EventsAfter(deviceID, hello.ResumeAfter, 500); err == nil {
			for _, ev := range evs {
				s.enqueueRaw(conn, ev.Body)
			}
		}
	}
	if role == relaywire.RoleIDE {
		if queued, err := s.store.PullRPCs(deviceID); err == nil {
			for _, q := range queued {
				s.enqueueRaw(conn, q.Body)
			}
		}
	}

	for {
		rctx, cancel := context.WithTimeout(ctx, 70*time.Second)
		_, data, err := ws.Read(rctx)
		cancel()
		if err != nil {
			return
		}
		s.onFrame(conn, data)
	}
}

func (s *Server) writeLoop(ctx context.Context, c *clientConn) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.closed:
			return
		case <-ticker.C:
			id := "ping"
			raw, _ := json.Marshal(relaywire.Envelope{V: 1, ID: id, Type: relaywire.TypePing, DeviceID: c.deviceID})
			if err := s.writeFrame(ctx, c, raw); err != nil {
				return
			}
		case msg := <-c.send:
			if err := s.writeFrame(ctx, c, msg); err != nil {
				return
			}
		}
	}
}

func (s *Server) writeFrame(ctx context.Context, c *clientConn, msg []byte) error {
	wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return c.ws.Write(wctx, websocket.MessageText, msg)
}

func (s *Server) onFrame(c *clientConn, data []byte) {
	var env relaywire.Envelope
	if err := json.Unmarshal(data, &env); err != nil || env.V != relaywire.ProtocolVersion {
		s.sendErr(c, "", relaywire.CodeInvalid, "envelope inválido")
		return
	}
	if env.DeviceID != "" && env.DeviceID != c.deviceID {
		s.sendErr(c, env.ID, relaywire.CodeForbidden, "deviceId não autorizado nesta conexão")
		return
	}
	env.DeviceID = c.deviceID
	switch env.Type {
	case relaywire.TypePing:
		s.enqueue(c, relaywire.Envelope{V: 1, ID: env.ID, Type: relaywire.TypePong, DeviceID: c.deviceID})
	case relaywire.TypePong, relaywire.TypeAck, relaywire.TypeHello:
		return
	case relaywire.TypeRPC:
		if c.role != relaywire.RoleApp {
			s.sendErr(c, env.ID, relaywire.CodeForbidden, "somente o app envia rpc")
			return
		}
		s.forwardRPC(c, env)
	case relaywire.TypeRPCResult:
		if c.role != relaywire.RoleIDE {
			s.sendErr(c, env.ID, relaywire.CodeForbidden, "somente a IDE responde rpc")
			return
		}
		s.forwardResult(c, env)
	case relaywire.TypeError:
		if c.role == relaywire.RoleIDE && strings.TrimSpace(env.ID) != "" {
			s.forwardResult(c, env)
			return
		}
		return
	case relaywire.TypeEvent:
		if c.role != relaywire.RoleIDE {
			s.sendErr(c, env.ID, relaywire.CodeForbidden, "somente a IDE emite eventos")
			return
		}
		s.forwardEvent(c, env)
	default:
		s.sendErr(c, env.ID, relaywire.CodeInvalid, "tipo desconhecido")
	}
}

func (s *Server) forwardRPC(c *clientConn, env relaywire.Envelope) {
	dev, err := s.store.DeviceForAccount(c.accountID, c.deviceID)
	if err != nil {
		s.sendErr(c, env.ID, relaywire.CodeNotFound, "IDE não encontrada")
		return
	}
	if code, msg, ok := boxOK(env, dev); !ok {
		s.sendErr(c, env.ID, code, msg)
		return
	}
	if strings.TrimSpace(env.ID) == "" {
		s.sendErr(c, "", relaywire.CodeInvalid, "id obrigatório")
		return
	}
	env.Seq = 0
	env.Role = ""
	frame, _ := json.Marshal(env)
	s.hub.WatchRPC(c.deviceID, env.ID, c)
	if s.hub.ForwardToIDE(c.deviceID, env.ID, c, frame) {
		if err := s.store.EnqueueRPC(c.deviceID, c.accountID, env.ID, frame); err != nil {
			s.sendErr(c, env.ID, relaywire.CodeInternal, "não foi possível enfileirar")
			return
		}
		// The IDE may have attached while the row was inserted.
		s.hub.ForwardToIDE(c.deviceID, env.ID, c, frame)
	}
}

func (s *Server) forwardResult(c *clientConn, env relaywire.Envelope) {
	dev, err := s.store.DeviceForAccount(c.accountID, c.deviceID)
	if err != nil {
		return
	}
	if code, msg, ok := boxOK(env, dev); !ok {
		s.sendErr(c, env.ID, code, msg)
		return
	}
	_ = s.store.DeleteRPC(env.ID)
	env.Seq = 0
	env.Role = ""
	frame, _ := json.Marshal(env)
	s.hub.DeliverResult(c.deviceID, env.ID, frame)
}

func (s *Server) forwardEvent(c *clientConn, env relaywire.Envelope) {
	dev, err := s.store.DeviceForAccount(c.accountID, c.deviceID)
	if err != nil {
		s.sendErr(c, env.ID, relaywire.CodeNotFound, "IDE não encontrada")
		return
	}
	if code, msg, ok := boxOK(env, dev); !ok {
		s.sendErr(c, env.ID, code, msg)
		return
	}
	env.Role = ""
	_, body, err := s.store.AppendEvent(c.deviceID, s.eventKeep, func(seq int64) []byte {
		env.Seq = seq
		b, _ := json.Marshal(env)
		return b
	})
	if err != nil {
		s.sendErr(c, env.ID, relaywire.CodeInternal, "não foi possível gravar o evento")
		return
	}
	s.hub.ForwardToApps(c.deviceID, body)
}

func boxOK(env relaywire.Envelope, dev Device) (string, string, bool) {
	if !dev.E2EReady() {
		return relaywire.CodeKeysMissing, "troca de chaves incompleta", false
	}
	if env.Enc != relaywire.EncNaClBox {
		return relaywire.CodeEncryption, "o plano de dados exige enc=naclbox", false
	}
	var p relaywire.BoxPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil || p.Nonce == "" || p.Ciphertext == "" {
		return relaywire.CodeInvalid, "caixa naclbox inválida", false
	}
	return "", "", true
}

func (s *Server) principal(token string) (accountID, sessionID, deviceID, role string, err error) {
	switch {
	case strings.HasPrefix(token, relaywire.TokenAccessPrefix):
		acct, sid, err := s.store.LookupAccess(token)
		if err != nil {
			return "", "", "", "", err
		}
		return acct.ID, sid, "", relaywire.RoleApp, nil
	case strings.HasPrefix(token, relaywire.TokenDevicePrefix):
		dev, err := s.store.LookupDevice(token)
		if err != nil {
			return "", "", "", "", err
		}
		return dev.AccountID, "", dev.ID, relaywire.RoleIDE, nil
	default:
		return "", "", "", "", errUnauthorized("token inválido")
	}
}

func (s *Server) enqueue(c *clientConn, env relaywire.Envelope) {
	if env.V == 0 {
		env.V = 1
	}
	b, err := json.Marshal(env)
	if err != nil {
		return
	}
	s.enqueueRaw(c, b)
}

func (s *Server) enqueueRaw(c *clientConn, b []byte) {
	select {
	case <-c.closed:
	case c.send <- b:
	}
}

func (s *Server) sendErr(c *clientConn, id, code, msg string) {
	raw, _ := json.Marshal(relaywire.ErrorBody{Code: code, Message: msg})
	s.enqueue(c, relaywire.Envelope{V: 1, ID: id, Type: relaywire.TypeError, DeviceID: c.deviceID, Payload: raw})
}

func writeWSError(ctx context.Context, ws *websocket.Conn, id, code, msg string) error {
	raw, _ := json.Marshal(relaywire.ErrorBody{Code: code, Message: msg})
	env, _ := json.Marshal(relaywire.Envelope{V: 1, ID: id, Type: relaywire.TypeError, Payload: raw})
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return ws.Write(wctx, websocket.MessageText, env)
}

func offeredSubprotocols(r *http.Request) []string {
	var out []string
	for _, part := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func tokenFromRequest(r *http.Request) (token, subprotocol string) {
	token = bearer(r)
	for _, p := range offeredSubprotocols(r) {
		if strings.HasPrefix(p, "bearer.") {
			if token == "" {
				token = strings.TrimPrefix(p, "bearer.")
			}
			return token, p
		}
	}
	return token, ""
}
