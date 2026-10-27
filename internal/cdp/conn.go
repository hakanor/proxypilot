// Package cdp captures Chrome traffic over the DevTools Protocol and applies
// ProxyPilot rules through the Fetch domain, without a proxy or certificate.
package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Conn is one browser-level CDP websocket with flattened target sessions.
type Conn struct {
	ws     *websocket.Conn
	nextID atomic.Int64
	onEvt  func(method, session string, params json.RawMessage)

	mu      sync.Mutex
	pending map[int64]chan reply
	closed  chan struct{}
	err     error
}

type reply struct {
	Result json.RawMessage
	Err    *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
}

type message struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// BrowserWS asks a debugging endpoint (http://127.0.0.1:9222) for the browser websocket URL.
func BrowserWS(ctx context.Context, addr string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/json/version", nil)
	resp, err := (&http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}).Do(req)
	if err != nil {
		return "", fmt.Errorf("no Chrome debugging endpoint on %s (start Chrome with --remote-debugging-port): %w", addr, err)
	}
	defer resp.Body.Close()
	var v struct {
		WS string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || v.WS == "" {
		return "", fmt.Errorf("%s did not return a browser websocket URL", addr)
	}
	return v.WS, nil
}

// Dial connects to a browser websocket; onEvent runs on the read goroutine and must not block.
func Dial(ctx context.Context, wsURL string, onEvent func(method, session string, params json.RawMessage)) (*Conn, error) {
	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: &http.Client{Transport: &http.Transport{Proxy: nil}}})
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(256 << 20) // response bodies arrive in one message
	c := &Conn{ws: ws, onEvt: onEvent, pending: map[int64]chan reply{}, closed: make(chan struct{})}
	go c.read()
	return c, nil
}

func (c *Conn) read() {
	defer func() {
		c.mu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
		close(c.closed)
	}()
	for {
		_, b, err := c.ws.Read(context.Background())
		if err != nil {
			c.mu.Lock()
			c.err = err
			c.mu.Unlock()
			return
		}
		var m message
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		if m.ID != 0 {
			c.mu.Lock()
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- reply{Result: m.Result, Err: m.Error}
			}
			continue
		}
		if m.Method != "" && c.onEvt != nil {
			c.onEvt(m.Method, m.SessionID, m.Params)
		}
	}
}

// Done is closed when the browser connection ends (Chrome quit or detach).
func (c *Conn) Done() <-chan struct{} { return c.closed }

var ErrClosed = errors.New("chrome connection closed")

// Call sends a command and decodes the result into out (if not nil).
func (c *Conn) Call(ctx context.Context, session, method string, params, out any) error {
	id := c.nextID.Add(1)
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}
	b, _ := json.Marshal(message{ID: id, Method: method, SessionID: session, Params: raw})
	ch := make(chan reply, 1)
	c.mu.Lock()
	select {
	case <-c.closed:
		c.mu.Unlock()
		return ErrClosed
	default:
	}
	c.pending[id] = ch
	c.mu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.ws.Write(wctx, websocket.MessageText, b); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if r.Err != nil {
			return fmt.Errorf("%s: %s", method, r.Err.Message)
		}
		if out != nil && len(r.Result) > 0 {
			return json.Unmarshal(r.Result, out)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	}
}

func (c *Conn) Close() error { return c.ws.Close(websocket.StatusNormalClosure, "") }
