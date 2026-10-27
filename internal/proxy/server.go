package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Options configure a Server. Zero values get sensible defaults.
type Options struct {
	Addr        string // listen address, e.g. 127.0.0.1:9090
	Hooks       Hooks
	Logger      *slog.Logger
	InlineLimit int64  // body capture kept in memory up to this size
	TempDir     string // spill directory for large bodies

	// HTTPS interception. If Issuer is nil every CONNECT is tunneled.
	Issuer CertIssuer
	Policy *SSLPolicy

	// DisableH2 keeps both sides on HTTP/1.1 (--disable-h2).
	DisableH2 bool
	// UpstreamTLS lets tests trust a fake upstream; nil means system roots.
	UpstreamTLS *tls.Config

	// DirectHandler answers requests sent to the proxy itself (non-proxy form,
	// e.g. http://<lan-ip>:9090/): the certificate page for phones.
	DirectHandler http.Handler

	// Unrecorded reports upstream host:port values that are proxied but never
	// passed to Hooks (the control API's own address).
	Unrecorded func(hostport string) bool
}

// CertIssuer is the part of ca.Issuer the proxy needs.
type CertIssuer interface {
	GetCertificate(fallbackHost string) func(*tls.ClientHelloInfo) (*tls.Certificate, error)
}

type Server struct {
	opts      Options
	hooks     Hooks
	log       *slog.Logger
	transport *http.Transport
	httpSrv   *http.Server

	mu         sync.Mutex
	ln         net.Listener
	lanAddr    net.Addr        // extra all-interfaces listener (SetLAN), nil when off
	selfAddr   map[string]bool // host:port strings that point at this proxy
	rejections map[string]int  // client_tls_rejected count per host
	inner      map[*http.Server]struct{}
	tunnels    sync.WaitGroup
	closing    atomic.Bool
}

func New(opts Options) *Server {
	if opts.Hooks == nil {
		opts.Hooks = NopHooks{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.InlineLimit <= 0 {
		opts.InlineLimit = 256 * 1024
	}
	if opts.TempDir == "" {
		opts.TempDir = os.TempDir()
	}
	if opts.Policy == nil {
		opts.Policy = NewSSLPolicy(ModeAll, nil, nil)
	}
	s := &Server{opts: opts, hooks: opts.Hooks, log: opts.Logger}
	s.transport = &http.Transport{
		Proxy:                 nil, // never chain to the system proxy (which may be us)
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:       opts.UpstreamTLS,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   32,
		DisableCompression:    true,            // forward content-encoding untouched; the store decodes
		ForceAttemptHTTP2:     !opts.DisableH2, // h2 upstream via ALPN when the server offers it
	}
	s.httpSrv = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(opts.Logger.Handler(), slog.LevelDebug),
	}
	return s
}

// Start listens and serves until ctx is cancelled or Shutdown is called.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

// Serve uses an existing listener (tests use port 0).
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	m := newMultiListener(ln)
	s.mu.Lock()
	s.ln = m
	s.selfAddr = selfAddresses(ln.Addr())
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(sctx)
	}()
	s.log.Info("proxy listening", "addr", ln.Addr().String())
	err := s.httpSrv.Serve(m)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Addr is the bound address (useful with port 0).
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// Shutdown stops accepting, waits for in-flight requests and tunnels.
func (s *Server) Shutdown(ctx context.Context) error {
	if !s.closing.CompareAndSwap(false, true) {
		return nil
	}
	err := s.httpSrv.Shutdown(ctx)
	for _, in := range s.innerServers() { // intercepted HTTPS connections
		_ = in.Shutdown(ctx)
	}
	done := make(chan struct{})
	go func() { s.tunnels.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	s.transport.CloseIdleConnections()
	return err
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}
	if !r.URL.IsAbs() || strings.EqualFold(r.URL.Hostname(), MagicHost) {
		s.handleDirect(w, r)
		return
	}
	if s.isSelf(r.URL.Host, r.URL.Scheme) {
		f := newFlow(r.RemoteAddr, r.URL.Scheme)
		f.Request, f.Host = r, r.URL.Host
		f.ErrorKind = ErrProxyLoop
		s.hooks.OnError(f, errors.New("request targets the proxy itself"))
		http.Error(w, "ProxyPilot: refusing to proxy a request to itself", http.StatusBadRequest)
		return
	}
	s.forward(w, r, r.URL.Scheme, r.RemoteAddr)
}

// MagicHost is answered by the proxy itself (the certificate page), never forwarded.
const MagicHost = "proxypilot.cert"

func (s *Server) handleDirect(w http.ResponseWriter, r *http.Request) {
	if s.opts.DirectHandler != nil {
		s.opts.DirectHandler.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "ProxyPilot proxy is running. Configure this address as your HTTP proxy.")
}

// forward runs one exchange: hooks, upstream round trip, streamed response.
// It is shared by plain HTTP and intercepted HTTPS (scheme = https).
func (s *Server) forward(w http.ResponseWriter, r *http.Request, scheme, clientAddr string) {
	s.forwardWith(s.hooks, w, r, scheme, clientAddr)
}

func (s *Server) unrecorded(hostport string) bool {
	return s.opts.Unrecorded != nil && s.opts.Unrecorded(strings.ToLower(hostport))
}

func (s *Server) forwardWith(h Hooks, w http.ResponseWriter, r *http.Request, scheme, clientAddr string) {
	f := newFlow(clientAddr, scheme)
	f.Host = hostPort(r.URL.Host, scheme)
	if s.unrecorded(f.Host) {
		h = NopHooks{}
	}
	f.ReqBody = newBody(s.opts.InlineLimit, s.opts.TempDir)
	f.RespBody = newBody(s.opts.InlineLimit, s.opts.TempDir)

	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.URL.Scheme = scheme
	removeHopHeaders(out.Header)
	if r.Body != nil && r.Body != http.NoBody {
		out.Body = &teeBody{r: r.Body, capture: f.ReqBody, n: &f.BytesUp}
	}
	f.Request = out
	if isWebSocket(r) {
		s.forwardWebSocket(h, w, r, f)
		return
	}

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

	var trace timingTrace
	ctx := httptrace.WithClientTrace(out.Context(), trace.clientTrace(f.Started))
	resp, err := s.transport.RoundTrip(out.WithContext(ctx))
	trace.apply(&f.Timing)
	if err != nil {
		f.ErrorKind = classifyUpstreamErr(err)
		f.Err = err
		f.Timing.Total = time.Since(f.Started)
		h.OnError(f, err)
		if r.Context().Err() != nil {
			return // client went away
		}
		http.Error(w, fmt.Sprintf("ProxyPilot: upstream %s failed (%s): %v", f.Host, f.ErrorKind, err), http.StatusBadGateway)
		return
	}
	f.Response = resp

	switch d := h.OnResponse(f); d.Action {
	case Abort:
		resp.Body.Close()
		f.ErrorKind = ErrAborted
		h.OnError(f, errors.New("aborted by rule"))
		abortConn(w)
		return
	case Respond:
		if d.Response != nil {
			resp.Body.Close()
			f.Response = d.Response
		}
	}
	s.writeResponse(h, w, f, f.Response)
}

// writeResponse streams resp to the client, capturing a copy.
func (s *Server) writeResponse(h Hooks, w http.ResponseWriter, f *Flow, resp *http.Response) {
	if resp.Body == nil {
		resp.Body = http.NoBody
	}
	defer resp.Body.Close()
	removeHopHeaders(resp.Header)
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	body := &teeBody{r: resp.Body, capture: f.RespBody, n: &f.BytesDown}
	_, err := copyFlush(w, body)
	f.Timing.Total = time.Since(f.Started)
	if err != nil {
		f.ErrorKind = ErrClientAborted
		if errors.Is(err, errUpstreamRead) {
			f.ErrorKind = ErrUpstreamConnect
		}
		f.Err = err
		h.OnError(f, err)
		return
	}
	h.OnComplete(f)
}

var errUpstreamRead = errors.New("upstream read")

// copyFlush copies and flushes after each chunk so streaming responses
// (SSE, long polling, chunked downloads) reach the client immediately.
func copyFlush(w http.ResponseWriter, r io.Reader) (int64, error) {
	fl, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			wn, werr := w.Write(buf[:n])
			total += int64(wn)
			if werr != nil {
				return total, werr
			}
			if fl != nil {
				fl.Flush()
			}
		}
		if rerr == io.EOF {
			return total, nil
		}
		if rerr != nil {
			return total, fmt.Errorf("%w: %v", errUpstreamRead, rerr)
		}
	}
}

func abortConn(w http.ResponseWriter) {
	if hj, ok := w.(http.Hijacker); ok {
		if c, _, err := hj.Hijack(); err == nil {
			c.Close()
			return
		}
	}
	panic(http.ErrAbortHandler)
}

func classifyUpstreamErr(err error) string {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return ErrTimeout
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrTimeout
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return ErrUpstreamTLS
	}
	var recErr tls.RecordHeaderError
	if errors.As(err, &recErr) || strings.Contains(err.Error(), "tls:") {
		return ErrUpstreamTLS
	}
	return ErrUpstreamConnect
}

func hostPort(host, scheme string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	if scheme == "https" {
		return net.JoinHostPort(strings.Trim(host, "[]"), "443")
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), "80")
}

// isSelf reports whether host:port resolves to this proxy's listener.
func (s *Server) isSelf(host, scheme string) bool {
	hp := strings.ToLower(hostPort(host, scheme))
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selfAddr[hp]
}

func selfAddresses(a net.Addr) map[string]bool {
	out := map[string]bool{}
	tcp, ok := a.(*net.TCPAddr)
	if !ok {
		return out
	}
	port := fmt.Sprint(tcp.Port)
	add := func(h string) { out[strings.ToLower(net.JoinHostPort(h, port))] = true }
	add("localhost")
	add("127.0.0.1")
	add("::1")
	if !tcp.IP.IsUnspecified() {
		add(tcp.IP.String())
		return out
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, ia := range addrs {
			if ipn, ok := ia.(*net.IPNet); ok {
				add(ipn.IP.String())
			}
		}
	}
	if h, err := os.Hostname(); err == nil {
		add(h)
	}
	return out
}

// timingTrace records httptrace callbacks relative to the flow start.
type timingTrace struct {
	mu                            sync.Mutex
	start                         time.Time
	dnsStart, connStart, tlsStart time.Time
	dns, connect, tls, ttfb       time.Duration
	reused                        bool
}

func (t *timingTrace) clientTrace(start time.Time) *httptrace.ClientTrace {
	t.start = start
	now := func() time.Time { return time.Now() }
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { t.mu.Lock(); t.dnsStart = now(); t.mu.Unlock() },
		DNSDone: func(httptrace.DNSDoneInfo) {
			t.mu.Lock()
			if !t.dnsStart.IsZero() {
				t.dns = now().Sub(t.dnsStart)
			}
			t.mu.Unlock()
		},
		ConnectStart: func(string, string) { t.mu.Lock(); t.connStart = now(); t.mu.Unlock() },
		ConnectDone: func(string, string, error) {
			t.mu.Lock()
			if !t.connStart.IsZero() {
				t.connect = now().Sub(t.connStart)
			}
			t.mu.Unlock()
		},
		TLSHandshakeStart: func() { t.mu.Lock(); t.tlsStart = now(); t.mu.Unlock() },
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			t.mu.Lock()
			if !t.tlsStart.IsZero() {
				t.tls = now().Sub(t.tlsStart)
			}
			t.mu.Unlock()
		},
		GotConn: func(i httptrace.GotConnInfo) { t.mu.Lock(); t.reused = i.Reused; t.mu.Unlock() },
		GotFirstResponseByte: func() {
			t.mu.Lock()
			t.ttfb = now().Sub(t.start)
			t.mu.Unlock()
		},
	}
}

func (t *timingTrace) apply(dst *Timing) {
	t.mu.Lock()
	defer t.mu.Unlock()
	dst.DNS, dst.Connect, dst.TLS, dst.TTFB, dst.Reused = t.dns, t.connect, t.tls, t.ttfb, t.reused
}
