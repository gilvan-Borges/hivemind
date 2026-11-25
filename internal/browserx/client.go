package browserx

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Client é um processo hivemind que não é o dono da porta. Ele fala o papel
// "agent": a aprovação já aconteceu neste processo, antes do Call.
type Client struct {
	port  int
	token string
	conn  *websocket.Conn

	mu      sync.Mutex
	writeMu sync.Mutex
	pending map[string]chan Message
	closed  bool
}

func dialAgent(ctx context.Context, port int, token string) (*Client, error) {
	if port <= 0 || !probe(port) {
		return nil, fmt.Errorf("nenhuma ponte do Chrome em 127.0.0.1:%d", port)
	}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dctx, fmt.Sprintf("ws://127.0.0.1:%d/browser", port), &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return nil, fmt.Errorf("não conectou na ponte local do Chrome")
	}
	conn.SetReadLimit(1 << 20)
	c := &Client{port: port, token: token, conn: conn, pending: map[string]chan Message{}}
	if err := c.write(dctx, Message{Type: "hello", Role: "agent", Token: token}); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "")
		return nil, fmt.Errorf("não conectou na ponte local do Chrome")
	}
	_, data, err := conn.Read(dctx)
	if err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, "")
		return nil, fmt.Errorf("não conectou na ponte local do Chrome")
	}
	var hello Message
	if json.Unmarshal(data, &hello) != nil || !hello.OK {
		_ = conn.Close(websocket.StatusPolicyViolation, "")
		return nil, fmt.Errorf("a ponte local recusou este processo")
	}
	go c.read(context.Background())
	return c, nil
}

func (c *Client) Port() int { return c.port }

func (c *Client) Healthy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil && !c.closed
}

func (c *Client) ExtensionOn() bool {
	if !c.Healthy() {
		return false
	}
	ok, extOn := probeHealth(c.port)
	return ok && extOn
}

func (c *Client) write(ctx context.Context, m Message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.mu.Lock()
	conn := c.conn
	closed := c.closed
	c.mu.Unlock()
	if conn == nil || closed {
		return fmt.Errorf("ponte fechada")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return conn.Write(ctx, websocket.MessageText, data)
}

func (c *Client) read(ctx context.Context) {
	defer c.Close()
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		var m Message
		if json.Unmarshal(data, &m) != nil || m.Type != "result" {
			continue
		}
		c.mu.Lock()
		ch := c.pending[m.ID]
		c.mu.Unlock()
		if ch != nil {
			select {
			case ch <- m:
			default:
			}
		}
	}
}

func (c *Client) Call(ctx context.Context, tool string, args map[string]any) (Message, error) {
	id := newID()
	ch := make(chan Message, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.write(ctx, Message{Type: "call", ID: id, Tool: tool, Args: args}); err != nil {
		return Message{}, fmt.Errorf("ponte local do Chrome caiu")
	}
	select {
	case <-ctx.Done():
		return Message{}, ctx.Err()
	case res := <-ch:
		res.Text = Redact(res.Text, c.token)
		return res, nil
	}
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	conn := c.conn
	pending := c.pending
	c.pending = make(map[string]chan Message)
	c.mu.Unlock()
	for id, ch := range pending {
		select {
		case ch <- Message{Type: "result", ID: id, OK: false, Text: "ponte local do Chrome fechada"}:
		default:
		}
	}
	if conn != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}
	return nil
}
