package relay

import (
	"sync"

	"github.com/coder/websocket"
	"github.com/gilvan-Borges/HiveMind/relaywire"
)

const (
	defaultMaxConns = 128
	maxAppsPerIDE   = 8
)

// clientConn is one WebSocket peer bound to a single account and device.
type clientConn struct {
	ws        *websocket.Conn
	send      chan []byte
	closed    chan struct{}
	once      sync.Once
	role      string
	accountID string
	deviceID  string
	sessionID string
}

func newConn(ws *websocket.Conn, role, accountID, deviceID, sessionID string) *clientConn {
	return &clientConn{
		ws:        ws,
		send:      make(chan []byte, 32),
		closed:    make(chan struct{}),
		role:      role,
		accountID: accountID,
		deviceID:  deviceID,
		sessionID: sessionID,
	}
}

// Close unblocks the writer. It is safe to call more than once.
func (c *clientConn) Close() {
	if c == nil {
		return
	}
	c.once.Do(func() {
		close(c.closed)
		_ = c.ws.Close(websocket.StatusGoingAway, "")
	})
}

type lane struct {
	deviceID  string
	accountID string
	mu        sync.Mutex
	ide       *clientConn
	apps      map[*clientConn]struct{}
	rpc       map[string]*clientConn
}

// Hub routes frames between the single IDE of a device and the apps that
// selected that device. Lanes of different devices never share a queue.
type Hub struct {
	mu       sync.Mutex
	lanes    map[string]*lane
	conns    int
	maxConns int
}

// NewHub builds an empty router.
func NewHub(maxConns int) *Hub {
	if maxConns <= 0 {
		maxConns = defaultMaxConns
	}
	return &Hub{lanes: make(map[string]*lane), maxConns: maxConns}
}

func (h *Hub) reserve() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns >= h.maxConns {
		return false
	}
	h.conns++
	return true
}

func (h *Hub) release() {
	h.mu.Lock()
	if h.conns > 0 {
		h.conns--
	}
	h.mu.Unlock()
}

func (h *Hub) lane(deviceID, accountID string) *lane {
	h.mu.Lock()
	defer h.mu.Unlock()
	ln := h.lanes[deviceID]
	if ln == nil {
		ln = &lane{
			deviceID:  deviceID,
			accountID: accountID,
			apps:      make(map[*clientConn]struct{}),
			rpc:       make(map[string]*clientConn),
		}
		h.lanes[deviceID] = ln
	}
	return ln
}

// Attach binds the connection. A new IDE connection replaces the previous one.
// The returned connection, if any, was superseded and must be closed by the caller
// after the lock is released — this method closes it itself.
func (h *Hub) Attach(c *clientConn) error {
	ln := h.lane(c.deviceID, c.accountID)
	ln.mu.Lock()
	defer ln.mu.Unlock()
	if c.accountID != ln.accountID {
		return errNotFound("IDE não encontrada")
	}
	if c.role == relaywire.RoleIDE {
		if ln.ide != nil && ln.ide != c {
			ln.ide.Close()
		}
		ln.ide = c
		return nil
	}
	if len(ln.apps) >= maxAppsPerIDE {
		return &httpError{Status: 429, Code: relaywire.CodeRateLimited, Msg: "muitos apps ligados a esta IDE"}
	}
	ln.apps[c] = struct{}{}
	return nil
}

// Detach removes the connection from its lane.
func (h *Hub) Detach(c *clientConn) {
	if c == nil {
		return
	}
	h.mu.Lock()
	ln := h.lanes[c.deviceID]
	h.mu.Unlock()
	if ln == nil {
		h.release()
		return
	}
	ln.mu.Lock()
	if c.role == relaywire.RoleIDE {
		if ln.ide == c {
			ln.ide = nil
		}
	} else {
		delete(ln.apps, c)
	}
	for id, app := range ln.rpc {
		if app == c {
			delete(ln.rpc, id)
		}
	}
	empty := ln.ide == nil && len(ln.apps) == 0
	ln.mu.Unlock()
	if empty {
		h.mu.Lock()
		if cur := h.lanes[c.deviceID]; cur == ln {
			cur.mu.Lock()
			still := cur.ide == nil && len(cur.apps) == 0
			cur.mu.Unlock()
			if still {
				delete(h.lanes, c.deviceID)
			}
		}
		h.mu.Unlock()
	}
	h.release()
}

// Online reports whether the IDE socket of this device is up.
func (h *Hub) Online(deviceID string) bool {
	h.mu.Lock()
	ln := h.lanes[deviceID]
	h.mu.Unlock()
	if ln == nil {
		return false
	}
	ln.mu.Lock()
	ok := ln.ide != nil
	ln.mu.Unlock()
	return ok
}

// KickDevice closes every socket of a device (used on revoke).
func (h *Hub) KickDevice(deviceID string) {
	h.mu.Lock()
	ln := h.lanes[deviceID]
	h.mu.Unlock()
	if ln == nil {
		return
	}
	ln.mu.Lock()
	if ln.ide != nil {
		ln.ide.Close()
	}
	for app := range ln.apps {
		app.Close()
	}
	ln.mu.Unlock()
}

// KickSession closes app sockets authenticated by this user session.
func (h *Hub) KickSession(sessionID string) {
	if sessionID == "" {
		return
	}
	h.mu.Lock()
	lanes := make([]*lane, 0, len(h.lanes))
	for _, ln := range h.lanes {
		lanes = append(lanes, ln)
	}
	h.mu.Unlock()
	for _, ln := range lanes {
		ln.mu.Lock()
		for app := range ln.apps {
			if app.sessionID == sessionID {
				app.Close()
			}
		}
		ln.mu.Unlock()
	}
}

// WatchRPC remembers which app is waiting for this RPC id, including while
// the IDE is offline.
func (h *Hub) WatchRPC(deviceID, rpcID string, from *clientConn) {
	if rpcID == "" || from == nil {
		return
	}
	h.mu.Lock()
	ln := h.lanes[deviceID]
	h.mu.Unlock()
	if ln == nil {
		return
	}
	ln.mu.Lock()
	ln.rpc[rpcID] = from
	ln.mu.Unlock()
}

// ForwardToIDE delivers an RPC to the IDE. queued is true when the IDE is
// offline or its buffer is full — the caller must persist the frame.
func (h *Hub) ForwardToIDE(deviceID, rpcID string, from *clientConn, frame []byte) (queued bool) {
	h.mu.Lock()
	ln := h.lanes[deviceID]
	h.mu.Unlock()
	if ln == nil {
		return true
	}
	ln.mu.Lock()
	defer ln.mu.Unlock()
	if ln.ide == nil {
		return true
	}
	select {
	case <-ln.ide.closed:
		return true
	case ln.ide.send <- frame:
		if rpcID != "" && from != nil {
			ln.rpc[rpcID] = from
		}
		return false
	default:
		ln.ide.Close()
		return true
	}
}

// ForwardToApps fans a frame out to every app watching the device.
// Slow apps are closed so they reconnect and resume from the event log.
func (h *Hub) ForwardToApps(deviceID string, frame []byte) {
	h.mu.Lock()
	ln := h.lanes[deviceID]
	h.mu.Unlock()
	if ln == nil {
		return
	}
	ln.mu.Lock()
	defer ln.mu.Unlock()
	for app := range ln.apps {
		select {
		case <-app.closed:
		case app.send <- frame:
		default:
			app.Close()
		}
	}
}

// DeliverResult routes an IDE reply back to the app that sent the RPC.
func (h *Hub) DeliverResult(deviceID, rpcID string, frame []byte) {
	h.mu.Lock()
	ln := h.lanes[deviceID]
	h.mu.Unlock()
	if ln == nil {
		return
	}
	ln.mu.Lock()
	app := ln.rpc[rpcID]
	delete(ln.rpc, rpcID)
	ln.mu.Unlock()
	if app == nil {
		return
	}
	select {
	case <-app.closed:
	case app.send <- frame:
	default:
		app.Close()
	}
}

// NotifyPeerKey tells the opposite role that a public key was published.
func (h *Hub) NotifyPeerKey(deviceID, forRole string, frame []byte) {
	h.mu.Lock()
	ln := h.lanes[deviceID]
	h.mu.Unlock()
	if ln == nil {
		return
	}
	ln.mu.Lock()
	defer ln.mu.Unlock()
	if forRole == relaywire.RoleIDE && ln.ide != nil {
		select {
		case ln.ide.send <- frame:
		default:
		}
		return
	}
	if forRole == relaywire.RoleApp {
		for app := range ln.apps {
			select {
			case app.send <- frame:
			default:
			}
		}
	}
}
