package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	clientHandshakeTimeout = 15 * time.Second
	// After this many client_tls_rejected on one host, later CONNECTs to it are
	// tunneled (pinned apps keep working) and a suggestion tag is set (the
	// recorder turns it into a `suggestion` event: "add to denylist?").
	rejectSuggestThreshold = 3
)

// handleConnect implements the CONNECT flow:
//  1. decide intercept vs tunnel from the SSL policy,
//     2a. tunnel: dial upstream first (so failures become a 502), then splice bytes,
//     2b. intercept: hijack, reply 200, terminate TLS with a leaf for the host and
//     serve the decrypted HTTP/1.1 requests through forward().
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	target := hostPort(r.Host, "https")
	if s.isSelf(target, "https") {
		http.Error(w, "ProxyPilot: refusing to CONNECT to itself", http.StatusBadRequest)
		return
	}
	intercept := s.opts.Issuer != nil && s.opts.Policy.ShouldIntercept(target)
	if !intercept {
		s.tunnel(w, r, target)
		return
	}

	conn, brw, err := hijack(w)
	if err != nil {
		s.log.Debug("hijack failed", "err", err)
		return
	}
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		conn.Close()
		return
	}
	s.tunnels.Add(1)
	go func() {
		defer s.tunnels.Done()
		s.intercept(prefixConn(conn, brw.Reader), r.RemoteAddr, target)
	}()
}

func hijack(w http.ResponseWriter) (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer cannot hijack")
	}
	return hj.Hijack()
}

// tunnel passes raw bytes in both directions and records one Flow with counts.
func (s *Server) tunnel(w http.ResponseWriter, r *http.Request, target string) {
	f := newFlow(r.RemoteAddr, "https")
	f.Host, f.Tunneled, f.Request = target, true, r
	hooks := s.hooks
	if s.unrecorded(target) {
		hooks = NopHooks{}
	}

	up, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(r.Context(), "tcp", target)
	if err != nil {
		f.ErrorKind, f.Err = classifyUpstreamErr(err), err
		f.Timing.Total = time.Since(f.Started)
		hooks.OnError(f, err)
		http.Error(w, "ProxyPilot: cannot reach "+target+": "+err.Error(), http.StatusBadGateway)
		return
	}
	f.Timing.Connect = time.Since(f.Started)

	conn, brw, err := hijack(w)
	if err != nil {
		up.Close()
		return
	}
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		conn.Close()
		up.Close()
		return
	}
	s.tunnels.Add(1)
	go func() {
		defer s.tunnels.Done()
		client := prefixConn(conn, brw.Reader)
		splice(client, up, &f.BytesUp, &f.BytesDown)
		f.Timing.Total = time.Since(f.Started)
		hooks.OnComplete(f)
	}()
}

// splice copies a↔b until both directions finish.
func splice(a, b net.Conn, aToB, bToA *int64) {
	var wg sync.WaitGroup
	cp := func(dst, src net.Conn, n *int64) {
		defer wg.Done()
		c, _ := io.Copy(dst, src)
		atomic.AddInt64(n, c)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}
	wg.Add(2)
	go cp(b, a, aToB)
	go cp(a, b, bToA)
	wg.Wait()
	a.Close()
	b.Close()
}

// intercept terminates the client's TLS and serves requests over it.
func (s *Server) intercept(conn net.Conn, clientAddr, target string) {
	host, _ := splitHostPortLower(target)

	// Some clients send plain HTTP through CONNECT (e.g. ws:// via an HTTP proxy).
	pc := conn.(*prefixedConn)
	first, err := pc.r.Peek(1)
	if err != nil {
		conn.Close()
		return
	}
	if first[0] != 0x16 { // not a TLS handshake record
		s.serveConn(conn, clientAddr, target, "http")
		return
	}

	start := time.Now()
	tconn := tls.Server(conn, &tls.Config{
		GetCertificate: s.opts.Issuer.GetCertificate(host),
		NextProtos:     s.clientProtos(),
		MinVersion:     tls.VersionTLS10,
	})
	ctx, cancel := context.WithTimeout(context.Background(), clientHandshakeTimeout)
	err = tconn.HandshakeContext(ctx)
	cancel()
	if err != nil {
		f := newFlow(clientAddr, "https")
		f.Started, f.Host = start, target
		f.ErrorKind, f.Err = classifyClientTLSErr(err), err
		f.Timing.Total = time.Since(start)
		if f.ErrorKind == ErrClientTLSRejected {
			if n := s.countRejection(host); n >= rejectSuggestThreshold {
				s.opts.Policy.LearnTunnel(target, "client rejected the ProxyPilot certificate (SSL pinning or CA not trusted)")
				f.SetTag("suggestion", "ssl_denylist")
				f.SetTag("rejections", strconv.Itoa(n))
			}
		}
		s.hooks.OnError(f, err)
		tconn.Close()
		return
	}
	s.serveConn(tconn, clientAddr, target, "https")
}

// clientProtos is what the MITM side offers over ALPN. With "h2" negotiated, the
// http.Server in serveConn speaks HTTP/2 on that *tls.Conn by itself (one handler
// call per stream), so rules and capture work the same as for HTTP/1.1.
func (s *Server) clientProtos() []string {
	if s.opts.DisableH2 {
		return []string{"http/1.1"}
	}
	return []string{"h2", "http/1.1"}
}

// serveConn runs an http.Server on a single already-established connection,
// so keep-alive, chunking and pipelining behave exactly like the front door
// (and HTTP/2, when the TLS handshake picked it).
func (s *Server) serveConn(conn net.Conn, clientAddr, target, scheme string) {
	ln := newOneConnListener(conn)
	var connState func(net.Conn, http.ConnState)
	if _, ok := conn.(*tls.Conn); ok {
		ln = newRawOneConnListener(conn)
		connState = func(_ net.Conn, st http.ConnState) {
			if st == http.StateClosed || st == http.StateHijacked {
				ln.done()
			}
		}
	}
	srv := &http.Server{
		ConnState: connState,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.URL.Scheme = scheme
			if r.URL.Host == "" {
				r.URL.Host = r.Host
			}
			if r.URL.Host == "" {
				r.URL.Host = target
			}
			s.forwardIntercepted(w, r, scheme, clientAddr, target)
		}),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          s.httpSrv.ErrorLog,
	}
	s.trackInner(srv, true)
	defer s.trackInner(srv, false)
	_ = srv.Serve(ln)
	<-ln.closed
}

func (s *Server) forwardIntercepted(w http.ResponseWriter, r *http.Request, scheme, clientAddr, target string) {
	if s.isSelf(r.URL.Host, scheme) {
		http.Error(w, "ProxyPilot: refusing to proxy a request to itself", http.StatusBadRequest)
		return
	}
	s.forwardWithLearn(w, r, scheme, clientAddr, target)
}

// forwardWithLearn wraps forward and, when the upstream asks for a client
// certificate, makes the next CONNECT to that host a plain tunnel.
func (s *Server) forwardWithLearn(w http.ResponseWriter, r *http.Request, scheme, clientAddr, target string) {
	lw := &learnHooks{Hooks: s.hooks, onErr: func(err error) {
		if scheme == "https" && requiresClientCert(err) {
			s.opts.Policy.LearnTunnel(target, "upstream requires a client certificate (mTLS)")
			s.log.Info("mTLS upstream, tunneling from now on", "host", target)
		}
	}}
	s.forwardWith(lw, w, r, scheme, clientAddr)
}

type learnHooks struct {
	Hooks
	onErr func(error)
}

// OnMessage passes WebSocket messages through (embedding Hooks alone would drop them).
func (l *learnHooks) OnMessage(f *Flow, m WSMessage) {
	if mh, ok := l.Hooks.(MessageHook); ok {
		mh.OnMessage(f, m)
	}
}

func (l *learnHooks) OnError(f *Flow, err error) {
	l.onErr(err)
	l.Hooks.OnError(f, err)
}

func requiresClientCert(err error) bool {
	if err == nil {
		return false
	}
	m := err.Error()
	return strings.Contains(m, "certificate required") || strings.Contains(m, "handshake failure") && strings.Contains(m, "remote error")
}

func classifyClientTLSErr(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrTimeout
	}
	m := err.Error()
	// The client aborted after seeing our certificate: untrusted CA or pinning.
	if strings.Contains(m, "remote error: tls: bad certificate") ||
		strings.Contains(m, "remote error: tls: unknown certificate authority") ||
		strings.Contains(m, "remote error: tls: unknown certificate") ||
		strings.Contains(m, "remote error: tls: certificate unknown") ||
		strings.Contains(m, "remote error: tls: access denied") ||
		errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, net.ErrClosed) {
		return ErrClientTLSRejected
	}
	return ErrClientTLS
}

func (s *Server) countRejection(host string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rejections == nil {
		s.rejections = map[string]int{}
	}
	s.rejections[host]++
	return s.rejections[host]
}

func (s *Server) trackInner(srv *http.Server, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inner == nil {
		s.inner = map[*http.Server]struct{}{}
	}
	if add {
		s.inner[srv] = struct{}{}
	} else {
		delete(s.inner, srv)
	}
}

func (s *Server) innerServers() []*http.Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*http.Server, 0, len(s.inner))
	for srv := range s.inner {
		out = append(out, srv)
	}
	return out
}

// prefixedConn replays bytes the HTTP server already buffered.
type prefixedConn struct {
	net.Conn
	r *bufio.Reader
}

func prefixConn(c net.Conn, r *bufio.Reader) net.Conn {
	if r == nil {
		r = bufio.NewReader(c)
	}
	return &prefixedConn{Conn: c, r: r}
}

func (p *prefixedConn) Read(b []byte) (int, error) { return p.r.Read(b) }

func (p *prefixedConn) CloseWrite() error {
	if cw, ok := p.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return p.Conn.Close()
}

// oneConnListener hands out a single connection, then blocks until it closes.
type oneConnListener struct {
	conn   net.Conn
	once   sync.Once
	closed chan struct{}
	served bool
	mu     sync.Mutex
}

func newOneConnListener(c net.Conn) *oneConnListener {
	l := &oneConnListener{closed: make(chan struct{})}
	l.conn = &notifyConn{Conn: c, onClose: l.done}
	return l
}

// newRawOneConnListener hands out c itself: http.Server only switches to HTTP/2 when
// the connection is a *tls.Conn. The server's ConnState must call done when it ends.
func newRawOneConnListener(c net.Conn) *oneConnListener {
	return &oneConnListener{conn: c, closed: make(chan struct{})}
}

func (l *oneConnListener) done() { l.once.Do(func() { close(l.closed) }) }

func (l *oneConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if !l.served {
		l.served = true
		l.mu.Unlock()
		return l.conn, nil
	}
	l.mu.Unlock()
	<-l.closed
	return nil, net.ErrClosed
}

func (l *oneConnListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *oneConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

type notifyConn struct {
	net.Conn
	onClose   func()
	closeOnce sync.Once
}

func (c *notifyConn) Close() error {
	err := c.Conn.Close()
	c.closeOnce.Do(c.onClose)
	return err
}
