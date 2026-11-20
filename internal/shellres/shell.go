package shellres

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"

	"golang.org/x/crypto/nacl/secretbox"

	"github.com/gilvan-Borges/HiveMind/internal/resident"
	"github.com/gilvan-Borges/HiveMind/internal/sandboxx"
)

// PaletteShortcut is the shell palette. It is not Ctrl+K.
const PaletteShortcut = "Ctrl+Alt+Shift+H"

// ErrUnavailable means this OS does not run the shell.
var ErrUnavailable = errors.New("casca disponível apenas no Windows")

// ErrNoHandle means the session key could not be duplicated. No file is written.
var ErrNoHandle = errors.New("handle da chave não duplicado")

// AllowedMethods are the resident methods the shell may forward.
var AllowedMethods = []string{
	"ResidentStatus",
	"ResidentStartTurn",
	"ResidentSaveConfig",
	"ResidentSavePolicy",
	"ResidentSetSecret",
	"ResidentDeleteSecret",
	"ResidentListMemory",
	"ResidentAuditLog",
}

// Handler answers one method. The body is already opened.
type Handler func(ctx context.Context, body json.RawMessage) (any, error)

// Run starts the owner on Windows. Anywhere else it fails closed and does not call start.
func Run(ctx context.Context, start func() (map[string]Handler, error)) error {
	if runtime.GOOS != "windows" {
		return ErrUnavailable
	}
	return runWindows(ctx, start)
}

// DeliverKey asks dup to hand the session key to the client. Failure does not write a file.
func DeliverKey(dup func(key []byte) error) ([32]byte, error) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return key, err
	}
	if dup == nil {
		return [32]byte{}, ErrNoHandle
	}
	if err := dup(key[:]); err != nil {
		return [32]byte{}, err
	}
	return key, nil
}

// Dispatch routes one method. exec_command still has to pass the exec guard and the sandbox probe.
func Dispatch(ctx context.Context, method string, body json.RawMessage, methods map[string]Handler) (any, error) {
	if method == "exec_command" {
		var req struct {
			Argv []string `json:"argv"`
		}
		_ = json.Unmarshal(body, &req)
		if err := resident.ValidateExecCommand(req.Argv, nil); err != nil {
			return nil, err
		}
		if !sandboxx.NewOSRunner().Probe().Available {
			return nil, sandboxx.ErrSandboxUnavailable
		}
		return nil, errors.New("casca não executa")
	}
	h, ok := methods[method]
	if !ok || !allowed(method) {
		return nil, fmt.Errorf("método não disponível")
	}
	return h(ctx, body)
}

func allowed(method string) bool {
	for _, m := range AllowedMethods {
		if m == method {
			return true
		}
	}
	return false
}

type frame struct {
	Method string          `json:"method"`
	Nonce  string          `json:"nonce,omitempty"`
	Box    string          `json:"box,omitempty"`
	Plain  json.RawMessage `json:"plain,omitempty"`
}

func Serve(ctx context.Context, rw io.ReadWriteCloser, key *[32]byte, methods map[string]Handler) error {
	defer rw.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, err := readFrame(rw)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var fr frame
		if err := json.Unmarshal(raw, &fr); err != nil {
			return err
		}
		body := fr.Plain
		if fr.Box != "" {
			if key == nil {
				return errors.New("caixa sem chave")
			}
			opened, err := openBox(key, fr.Nonce, fr.Box)
			if err != nil {
				return err
			}
			body = opened
		}
		result, callErr := Dispatch(ctx, fr.Method, body, methods)
		resp := map[string]any{"ok": callErr == nil}
		if callErr != nil {
			resp["error"] = callErr.Error()
		} else if key != nil {
			nonce, box, err := sealBox(key, result)
			if err != nil {
				return err
			}
			resp["nonce"] = nonce
			resp["box"] = box
		} else {
			resp["result"] = result
		}
		if err := writeFrame(rw, resp); err != nil {
			return err
		}
	}
}

func readFrame(r io.Reader) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return nil, err
	}
	if n > 12<<20 {
		return nil, errors.New("quadro grande demais")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeFrame(w io.Writer, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(raw))); err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

func sealBox(key *[32]byte, v any) (string, string, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return "", "", err
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", "", err
	}
	out := secretbox.Seal(nil, plain, &nonce, key)
	return encode(nonce[:]), encode(out), nil
}

func openBox(key *[32]byte, nonceB64, boxB64 string) (json.RawMessage, error) {
	nonceRaw, err := decode(nonceB64)
	if err != nil || len(nonceRaw) != 24 {
		return nil, errors.New("nonce inválido")
	}
	boxRaw, err := decode(boxB64)
	if err != nil {
		return nil, err
	}
	var nonce [24]byte
	copy(nonce[:], nonceRaw)
	plain, ok := secretbox.Open(nil, boxRaw, &nonce, key)
	if !ok {
		return nil, errors.New("falha ao abrir a caixa")
	}
	return plain, nil
}

func encode(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0x0f]
	}
	return string(out)
}

func decode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, errors.New("hex inválido")
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi, err := fromHex(s[i*2])
		if err != nil {
			return nil, err
		}
		lo, err := fromHex(s[i*2+1])
		if err != nil {
			return nil, err
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func fromHex(b byte) (byte, error) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', nil
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, nil
	default:
		return 0, errors.New("hex inválido")
	}
}

// IdleRSS reports the shell process size. This runner only measures on Windows.
func IdleRSS() (uint64, error) {
	if runtime.GOOS != "windows" {
		return 0, errors.New("RSS da casca só é medido no Windows")
	}
	return idleRSSWindows()
}

func idleRSSWindows() (uint64, error) {
	return 0, errors.New("medição não executada neste processo")
}
