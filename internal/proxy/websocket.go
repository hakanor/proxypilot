package proxy

import (
	"bufio"
	"bytes"
	"compress/flate"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// WebSocket capture: the handshake goes through the hooks like any request (rules can
// mock or block it); after 101 the proxy relays bytes untouched in both directions and
// parses a copy of the frames into messages for MessageHook.

// WSMessage is one reassembled WebSocket message.
type WSMessage struct {
	Dir       string // "up" client → server, "down" server → client
	At        time.Time
	Opcode    string // text | binary | close | ping | pong
	Size      int64  // payload size (decompressed when permessage-deflate)
	Payload   []byte // at most WSPayloadLimit bytes
	Truncated bool
}

// MessageHook receives WebSocket messages; hooks in a Chain that implement it get them.
type MessageHook interface {
	OnMessage(f *Flow, m WSMessage)
}

// WSPayloadLimit caps how much of each message is kept; the relay itself is never limited.
const WSPayloadLimit = 64 << 10

func (c Chain) OnMessage(f *Flow, m WSMessage) {
	for _, h := range c {
		if mh, ok := h.(MessageHook); ok {
			mh.OnMessage(f, m)
		}
	}
}

func isWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") && headerHasToken(r.Header, "Connection", "upgrade")
}

func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, t := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

// forwardWebSocket handles an Upgrade: websocket request (f.Request already set up).
func (s *Server) forwardWebSocket(h Hooks, w http.ResponseWriter, r *http.Request, f *Flow) {
	out := f.Request
	// The handshake needs its hop headers back (forwardWith stripped them).
	out.Header.Set("Connection", "Upgrade")
	out.Header.Set("Upgrade", r.Header.Get("Upgrade"))

	switch d := h.OnRequest(f); d.Action {
	case Abort:
		f.ErrorKind = ErrAborted
		h.OnError(f, errors.New("aborted by rule"))
		abortConn(w)
		return
	case Respond:
		if d.Response != nil {
			f.Response = d.Response
			s.writeResponse(h, w, f, d.Response)
			return
		}
	}

	up, err := s.dialUpstream(r.Context(), f.Scheme, f.Host)
	if err != nil {
		s.wsFail(h, w, f, err)
		return
	}
	if err := out.Write(up); err != nil {
		up.Close()
		s.wsFail(h, w, f, err)
		return
	}
	upr := bufio.NewReader(up)
	resp, err := http.ReadResponse(upr, out)
	if err != nil {
		up.Close()
		s.wsFail(h, w, f, err)
		return
	}
	f.Timing.TTFB = time.Since(f.Started)
	f.Response = resp
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer up.Close()
		h.OnResponse(f)
		s.writeResponse(h, w, f, f.Response)
		return
	}
	h.OnResponse(f) // observers; a 101 can't be rewritten

	client, brw, err := hijack(w)
	if err != nil {
		up.Close()
		s.wsFail(h, w, f, err)
		return
	}
	resp.Body = nil
	var head bytes.Buffer
	_ = resp.Write(&head) // status line + headers, no body
	if _, err := client.Write(head.Bytes()); err != nil {
		client.Close()
		up.Close()
		return
	}
	f.Timing.Total = time.Since(f.Started)
	f.SetTag("websocket", "open")
	h.OnComplete(f) // the handshake is done; messages follow under the same flow

	deflate := strings.Contains(strings.ToLower(resp.Header.Get("Sec-WebSocket-Extensions")), "permessage-deflate")
	var wg sync.WaitGroup
	relay := func(dst net.Conn, src io.Reader, dir string) {
		defer wg.Done()
		p := &wsParser{dir: dir, deflate: deflate, emit: func(m WSMessage) { s.emitMessage(h, f, m) }}
		_, _ = io.Copy(dst, io.TeeReader(src, p))
		// One side ended: close both so the other copy returns too.
		dst.Close()
	}
	wg.Add(2)
	go relay(up, brw.Reader, "up")
	go relay(client, upr, "down")
	wg.Wait()
	client.Close()
	up.Close()
	f.SetTag("websocket", "closed")
}

func (s *Server) emitMessage(h Hooks, f *Flow, m WSMessage) {
	if mh, ok := h.(MessageHook); ok {
		mh.OnMessage(f, m)
	}
}

func (s *Server) wsFail(h Hooks, w http.ResponseWriter, f *Flow, err error) {
	f.ErrorKind = classifyUpstreamErr(err)
	f.Err = err
	f.Timing.Total = time.Since(f.Started)
	h.OnError(f, err)
	http.Error(w, fmt.Sprintf("ProxyPilot: websocket upstream %s failed (%s): %v", f.Host, f.ErrorKind, err), http.StatusBadGateway)
}

// dialUpstream opens the raw connection for an upgraded request (TLS for https/wss).
func (s *Server) dialUpstream(ctx context.Context, scheme, hostport string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, err := s.transport.DialContext(ctx, "tcp", hostport)
	if err != nil || scheme != "https" {
		return c, err
	}
	host, _, _ := net.SplitHostPort(hostport)
	cfg := &tls.Config{}
	if s.transport.TLSClientConfig != nil {
		cfg = s.transport.TLSClientConfig.Clone()
	}
	cfg.ServerName = host
	cfg.NextProtos = []string{"http/1.1"} // WebSocket over h1 only
	tc := tls.Client(c, cfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return tc, nil
}

// wsParser reads the frames one direction carries (fed as a byte stream through Write)
// and reassembles messages. It never blocks or fails the relay: on a parse error it
// stops parsing and the bytes keep flowing.
type wsParser struct {
	dir     string
	deflate bool
	emit    func(WSMessage)

	buf    []byte
	broken bool

	// A fragmented message being collected.
	msgOp   byte
	msgRSV1 bool
	msgBuf  []byte
	msgSize int64
	msgTrnc bool

	dict []byte // last 32 KB of decompressed output (permessage-deflate context takeover)
}

func (p *wsParser) Write(b []byte) (int, error) {
	if p.broken {
		return len(b), nil
	}
	p.buf = append(p.buf, b...)
	for {
		n, ok := p.frame()
		if !ok {
			break
		}
		p.buf = p.buf[n:]
	}
	// Keep the buffer bounded: a frame header plus at most one capped payload.
	if len(p.buf) > 4*WSPayloadLimit+14 && !p.broken {
		p.skipLarge()
	}
	return len(b), nil
}

// frame parses one complete frame from p.buf; ok=false when more bytes are needed.
func (p *wsParser) frame() (int, bool) {
	b := p.buf
	if len(b) < 2 {
		return 0, false
	}
	fin, rsv1, op := b[0]&0x80 != 0, b[0]&0x40 != 0, b[0]&0x0f
	masked := b[1]&0x80 != 0
	n := int64(b[1] & 0x7f)
	i := 2
	switch n {
	case 126:
		if len(b) < 4 {
			return 0, false
		}
		n, i = int64(binary.BigEndian.Uint16(b[2:4])), 4
	case 127:
		if len(b) < 10 {
			return 0, false
		}
		n, i = int64(binary.BigEndian.Uint64(b[2:10])), 10
	}
	var key []byte
	if masked {
		if len(b) < i+4 {
			return 0, false
		}
		key, i = b[i:i+4], i+4
	}
	if n < 0 || int64(len(b)-i) < n {
		return 0, false
	}
	payload := append([]byte(nil), b[i:i+int(n)]...)
	if masked {
		for j := range payload {
			payload[j] ^= key[j%4]
		}
	}
	p.handle(fin, rsv1, op, payload)
	return i + int(n), true
}

// skipLarge gives up on frames too big to buffer: parsing stops for this direction.
func (p *wsParser) skipLarge() {
	p.broken = true
	p.buf = nil
	p.emit(WSMessage{Dir: p.dir, At: time.Now(), Opcode: "binary", Truncated: true, Payload: []byte("(message too large to capture; later messages in this direction are not recorded)")})
}

func (p *wsParser) handle(fin, rsv1 bool, op byte, payload []byte) {
	if op >= 0x8 { // control frames are never fragmented
		p.out(op, false, payload, int64(len(payload)), false)
		return
	}
	if op != 0 { // first frame of a message
		p.msgOp, p.msgRSV1, p.msgBuf, p.msgSize, p.msgTrnc = op, rsv1, nil, 0, false
	}
	p.msgSize += int64(len(payload))
	if room := 4*WSPayloadLimit - len(p.msgBuf); room > 0 {
		if len(payload) > room {
			payload, p.msgTrnc = payload[:room], true
		}
		p.msgBuf = append(p.msgBuf, payload...)
	} else {
		p.msgTrnc = true
	}
	if fin {
		p.out(p.msgOp, p.msgRSV1, p.msgBuf, p.msgSize, p.msgTrnc)
		p.msgBuf = nil
	}
}

func (p *wsParser) out(op byte, compressed bool, payload []byte, size int64, truncated bool) {
	if compressed && p.deflate && !truncated {
		if d, err := p.inflate(payload); err == nil {
			payload, size = d, int64(len(d))
		}
	}
	if len(payload) > WSPayloadLimit {
		payload, truncated = payload[:WSPayloadLimit], true
	}
	p.emit(WSMessage{Dir: p.dir, At: time.Now(), Opcode: opName(op), Size: size, Payload: payload, Truncated: truncated})
}

func (p *wsParser) inflate(b []byte) ([]byte, error) {
	r := flate.NewReaderDict(io.MultiReader(bytes.NewReader(b), bytes.NewReader([]byte{0, 0, 0xff, 0xff})), p.dict)
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, 16<<20))
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	p.dict = append(p.dict, out...)
	if len(p.dict) > 32<<10 {
		p.dict = append([]byte(nil), p.dict[len(p.dict)-32<<10:]...)
	}
	return out, nil
}

func opName(op byte) string {
	switch op {
	case 1:
		return "text"
	case 2:
		return "binary"
	case 8:
		return "close"
	case 9:
		return "ping"
	case 10:
		return "pong"
	}
	return fmt.Sprintf("op%d", op)
}
