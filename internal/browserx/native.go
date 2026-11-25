package browserx

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const maxNativeFrame = 1 << 20

// LaunchedAsNativeHost reconhece o executável que o Chrome sobe pelo manifesto.
// O Chrome não passa argumentos de aplicação; o nome do binário é o modo.
func LaunchedAsNativeHost(args []string) bool {
	if len(args) == 0 {
		return false
	}
	base := strings.ToLower(filepath.Base(args[0]))
	base = strings.TrimSuffix(base, ".exe")
	return base == "hivemind-browser-host"
}

// RunNativeHost é o processo stdio do Chrome. Ele lê o token do arquivo 0600
// e abre o WebSocket em 127.0.0.1. A extensão não vê o token.
func RunNativeHost() error {
	return serveNative(context.Background(), os.Stdin, os.Stdout)
}

func serveNative(ctx context.Context, in io.Reader, out io.Writer) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	if !probe(cfg.Port) {
		deadline := time.Now().Add(8 * time.Second)
		for !probe(cfg.Port) && time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(400 * time.Millisecond):
			}
		}
		if !probe(cfg.Port) {
			return fmt.Errorf("HiveMind não está escutando em 127.0.0.1:%d. Abra a IDE ou rode hivemind browser serve", cfg.Port)
		}
	}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	conn, _, err := websocket.Dial(dctx, fmt.Sprintf("ws://127.0.0.1:%d/browser", cfg.Port), &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	cancel()
	if err != nil {
		return fmt.Errorf("não conectou na ponte local do Chrome")
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	conn.SetReadLimit(maxNativeFrame)
	hctx, hcancel := context.WithTimeout(ctx, 5*time.Second)
	err = writeRaw(hctx, conn, Message{Type: "hello", Role: "extension", Token: cfg.Token})
	hcancel()
	if err != nil {
		return fmt.Errorf("não conectou na ponte local do Chrome")
	}
	rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
	_, data, err := conn.Read(rctx)
	rcancel()
	if err != nil {
		return fmt.Errorf("a ponte local recusou o host")
	}
	var hello Message
	if json.Unmarshal(data, &hello) != nil || !hello.OK {
		return fmt.Errorf("a ponte local recusou o host")
	}
	if err := writeFrame(out, data); err != nil {
		return err
	}
	errc := make(chan error, 2)
	go func() { errc <- pumpSocketToNative(ctx, conn, out) }()
	go func() { errc <- pumpNativeToSocket(ctx, in, conn) }()
	err = <-errc
	if err == io.EOF {
		return nil
	}
	return err
}

func pumpSocketToNative(ctx context.Context, conn *websocket.Conn, out io.Writer) error {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if err := writeFrame(out, data); err != nil {
			return err
		}
	}
}

func pumpNativeToSocket(ctx context.Context, in io.Reader, conn *websocket.Conn) error {
	for {
		data, err := readFrame(in)
		if err != nil {
			return err
		}
		var m Message
		if json.Unmarshal(data, &m) == nil && m.Type == "hello" {
			// O hello autenticado já foi enviado por este host. O da extensão
			// não carrega o token e não deve ser reencaminhado.
			continue
		}
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			return err
		}
	}
}

func readFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.NativeEndian.Uint32(hdr[:])
	if n == 0 || n > maxNativeFrame {
		return nil, fmt.Errorf("quadro native messaging inválido")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeFrame(w io.Writer, payload []byte) error {
	if len(payload) == 0 || len(payload) > maxNativeFrame {
		return fmt.Errorf("quadro native messaging inválido")
	}
	var hdr [4]byte
	binary.NativeEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}
