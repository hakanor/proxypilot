package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type wsHooks struct {
	recHooks
	mu   sync.Mutex
	msgs []WSMessage
}

func (h *wsHooks) OnMessage(_ *Flow, m WSMessage) {
	h.mu.Lock()
	h.msgs = append(h.msgs, m)
	h.mu.Unlock()
}

func (h *wsHooks) messages() []WSMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]WSMessage(nil), h.msgs...)
}

func echoServer(t *testing.T, mode websocket.CompressionMode) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: mode, CompressionThreshold: 1})
		if err != nil {
			return
		}
		defer c.CloseNow()
		c.SetReadLimit(1 << 20)
		for {
			typ, b, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if err := c.Write(r.Context(), typ, append([]byte("echo:"), b...)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWebSocketCapture(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode websocket.CompressionMode
	}{
		{"plain", websocket.CompressionDisabled},
		{"deflate-context-takeover", websocket.CompressionContextTakeover},
		{"deflate-no-context", websocket.CompressionNoContextTakeover},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hooks := &wsHooks{recHooks: *newRecHooks()}
			_, pu := startProxy(t, Options{Hooks: hooks})
			srv := echoServer(t, tc.mode)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			c, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{
				HTTPClient:           proxyClient(pu, nil),
				CompressionMode:      tc.mode,
				CompressionThreshold: 1,
			})
			if err != nil {
				t.Fatalf("dial through proxy: %v", err)
			}
			c.SetReadLimit(1 << 20)
			if resp.StatusCode != http.StatusSwitchingProtocols {
				t.Fatalf("status %d", resp.StatusCode)
			}
			f := hooks.wait(t) // handshake completes the flow
			if f.Response.StatusCode != 101 {
				t.Fatalf("flow status %d", f.Response.StatusCode)
			}

			big := strings.Repeat("abcdefgh", 20000) // 160 KB: crosses the payload cap
			for _, m := range []string{"hello", `{"n":1}`, "hello", big} {
				if err := c.Write(ctx, websocket.MessageText, []byte(m)); err != nil {
					t.Fatal(err)
				}
				_, b, err := c.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if string(b) != "echo:"+m {
					t.Fatalf("echo mismatch (%d bytes)", len(b))
				}
			}
			c.Close(websocket.StatusNormalClosure, "bye")

			deadline := time.Now().Add(3 * time.Second)
			for len(hooks.messages()) < 9 && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			var up, down []WSMessage
			for _, m := range hooks.messages() {
				if m.Opcode != "text" {
					continue
				}
				if m.Dir == "up" {
					up = append(up, m)
				} else {
					down = append(down, m)
				}
			}
			if len(up) != 4 || len(down) != 4 {
				t.Fatalf("got %d up / %d down text messages: %+v", len(up), len(down), hooks.messages())
			}
			if string(up[1].Payload) != `{"n":1}` || string(down[2].Payload) != "echo:hello" {
				t.Errorf("payloads: up[1]=%q down[2]=%q", up[1].Payload, down[2].Payload)
			}
			if up[3].Size != int64(len(big)) || !up[3].Truncated || len(up[3].Payload) != WSPayloadLimit {
				t.Errorf("big message: size %d truncated %v kept %d", up[3].Size, up[3].Truncated, len(up[3].Payload))
			}
		})
	}
}

func TestWebSocketParserSplitWrites(t *testing.T) {
	// A masked "hi" frame fed one byte at a time still parses.
	frame := []byte{0x81, 0x82, 1, 2, 3, 4, 'h' ^ 1, 'i' ^ 2}
	var got []WSMessage
	p := &wsParser{dir: "up", emit: func(m WSMessage) { got = append(got, m) }}
	for _, b := range frame {
		p.Write([]byte{b})
	}
	if len(got) != 1 || string(got[0].Payload) != "hi" {
		t.Fatalf("got %+v", got)
	}
}

func TestLearnHooksPassMessages(t *testing.T) {
	inner := &wsHooks{recHooks: *newRecHooks()}
	var h Hooks = &learnHooks{Hooks: inner, onErr: func(error) {}}
	mh, ok := h.(MessageHook)
	if !ok {
		t.Fatal("learnHooks does not forward WebSocket messages")
	}
	mh.OnMessage(&Flow{}, WSMessage{Dir: "up", Opcode: "text"})
	if len(inner.messages()) != 1 {
		t.Fatal("message lost")
	}
}
