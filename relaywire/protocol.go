// Package relaywire is the shared app↔relay↔IDE contract: envelope, RPC
// allowlist, link URI and error codes. The relay stores and forwards these
// messages; it does not interpret bridge payloads. See docs/SPEC_RELAY.md.
package relaywire

import (
	"encoding/json"
	"net/url"
	"strings"
)

const (
	// ProtocolVersion is the only envelope version this release accepts.
	ProtocolVersion = 1

	EncNaClBox = "naclbox"

	TypeHello        = "hello"
	TypeHelloOK      = "hello_ok"
	TypeRPC          = "rpc"
	TypeRPCResult    = "rpc_result"
	TypeEvent        = "event"
	TypeAck          = "ack"
	TypePing         = "ping"
	TypePong         = "pong"
	TypeError        = "error"
	TypePeerKey      = "peer_key"
	TypePeerPresence = "peer_presence"

	RoleApp = "app"
	RoleIDE = "ide"

	TokenAccessPrefix  = "hmr_at_"
	TokenRefreshPrefix = "hmr_rt_"
	TokenDevicePrefix  = "hmr_dev_"

	CodeUnauthorized  = "unauthorized"
	CodeForbidden     = "forbidden"
	CodeNotFound      = "not_found"
	CodeInvalid       = "invalid_request"
	CodeRateLimited   = "rate_limited"
	CodeConflict      = "conflict"
	CodeExpired       = "expired"
	CodeDeviceOffline = "device_offline"
	CodeEncryption    = "encryption_required"
	CodeKeysMissing   = "e2e_keys_missing"
	CodeDecryptFailed = "decrypt_failed"
	CodeKeyMismatch   = "key_mismatch"
	CodeInternal      = "internal"
)

// Envelope is one WebSocket text frame. Payload is opaque JSON. When Enc is
// EncNaClBox, Payload is a BoxPayload and the relay must not decrypt it.
type Envelope struct {
	V        int             `json:"v"`
	ID       string          `json:"id,omitempty"`
	Type     string          `json:"type"`
	Role     string          `json:"role,omitempty"`
	DeviceID string          `json:"deviceId,omitempty"`
	Seq      int64           `json:"seq,omitempty"`
	Enc      string          `json:"enc,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

// Hello is the plaintext payload of a hello frame.
type Hello struct {
	ResumeAfter int64  `json:"resumeAfter,omitempty"`
	PublicKey   string `json:"publicKey,omitempty"`
}

// HelloOK is the plaintext payload of hello_ok.
type HelloOK struct {
	Seq           int64  `json:"seq"`
	ResumeFrom    int64  `json:"resumeFrom"`
	PeerPublicKey string `json:"peerPublicKey,omitempty"`
	E2EReady      bool   `json:"e2eReady"`
	Online        bool   `json:"online"`
}

// PeerKeyNotice tells the other side that its peer rotated or published a key.
type PeerKeyNotice struct {
	PublicKey string `json:"publicKey"`
	Role      string `json:"role"`
}

// PeerPresenceNotice informs apps whether the IDE socket is online.
type PeerPresenceNotice struct {
	Online bool   `json:"online"`
	Role   string `json:"role,omitempty"`
}

// RPCRequest is the decrypted body of an app→IDE call. It mirrors one
// request of the LAN bridge (docs/SPEC_APP_MOBILE_NATIVO.md §5) except
// GET /api/v1/events, which is replaced by TypeEvent frames.
type RPCRequest struct {
	Method       string          `json:"method"`
	Path         string          `json:"path"`
	Query        string          `json:"query,omitempty"`
	Body         json.RawMessage `json:"body,omitempty"`
	Seq          uint64          `json:"seq,omitempty"`
	IssuedAtUnix int64           `json:"issuedAtUnix,omitempty"`
	Nonce        string          `json:"nonce,omitempty"`
}

// RPCResult is the decrypted body of the IDE's reply.
type RPCResult struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// ErrorBody is the plaintext payload of a TypeError frame and the JSON body
// of a failed HTTP call ({"error": {...}}).
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// APIError is the HTTP error document.
type APIError struct {
	Error ErrorBody `json:"error"`
}

// AllowedRPC reports whether an app may tunnel this call onto the IDE bridge.
// The event stream is not an RPC: live events arrive as TypeEvent frames.
func AllowedRPC(method, path string) bool {
	method = strings.ToUpper(strings.TrimSpace(method))
	path = strings.TrimSpace(path)
	if path == "" || strings.Contains(path, "..") || strings.Contains(path, "://") {
		return false
	}
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	switch method + " " + path {
	case "GET /api/v1/health",
		"GET /api/v1/projects",
		"GET /api/v1/lanes",
		"GET /api/v1/sessions",
		"GET /api/v1/session",
		"POST /api/v1/session/read",
		"GET /api/v1/approvals",
		"POST /api/v1/project/permission",
		"POST /api/v1/runs/start",
		"POST /api/v1/runs/cancel",
		"POST /api/v1/runs/redirect",
		"POST /api/v1/runs/steer",
		"POST /api/v1/approvals/resolve",
		"GET /api/v1/git/diffs",
		"POST /api/v1/git/commit",
		"POST /api/v1/git/pr",
		"GET /api/v1/routines",
		"POST /api/v1/routines/enabled",
		"GET /api/v1/loops",
		"POST /api/v1/loops/stop",
		"POST /api/v1/loops/pause",
		"POST /api/v1/loops/resume",
		"GET /api/v1/git/prs",
		"GET /api/v1/git/issues",
		"GET /api/v1/autoreview/status",
		"POST /api/v1/autoreview/poll":
		return true
	default:
		return false
	}
}

// LinkURI builds the hivemind://link payload shown as a QR / typed code.
func LinkURI(relayBase, code string) string {
	q := url.Values{}
	q.Set("relay", strings.TrimRight(strings.TrimSpace(relayBase), "/"))
	q.Set("code", NormalizeCode(code))
	return "hivemind://link?" + q.Encode()
}

// ParseLinkURI decodes a hivemind://link URI.
func ParseLinkURI(uri string) (relayBase, code string, ok bool) {
	const prefix = "hivemind://link?"
	if !strings.HasPrefix(uri, prefix) {
		return "", "", false
	}
	q, err := url.ParseQuery(strings.TrimPrefix(uri, prefix))
	if err != nil {
		return "", "", false
	}
	relayBase = strings.TrimSpace(q.Get("relay"))
	code = NormalizeCode(q.Get("code"))
	if relayBase == "" || code == "" {
		return "", "", false
	}
	return relayBase, code, true
}

// NormalizeCode strips spaces and dashes and uppercases a link code.
func NormalizeCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ReplaceAll(code, " ", "")
	return code
}
