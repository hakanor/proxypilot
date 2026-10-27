package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hakanor/proxypilot/internal/ca"
)

type recHooks struct {
	NopHooks
	mu        sync.Mutex
	completed []*Flow
	errored   []*Flow
	done      chan *Flow
}

func newRecHooks() *recHooks { return &recHooks{done: make(chan *Flow, 16)} }

func (h *recHooks) OnComplete(f *Flow) {
	h.mu.Lock()
	h.completed = append(h.completed, f)
	h.mu.Unlock()
	h.done <- f
}

func (h *recHooks) OnError(f *Flow, _ error) {
	h.mu.Lock()
	h.errored = append(h.errored, f)
	h.mu.Unlock()
	h.done <- f
}

func (h *recHooks) wait(t *testing.T) *Flow {
	t.Helper()
	select {
	case f := <-h.done:
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for hook")
		return nil
	}
}

func startProxy(t *testing.T, opts Options) (*Server, *url.URL) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Serve(ctx, ln) }()
	t.Cleanup(cancel)
	u, _ := url.Parse("http://" + ln.Addr().String())
	return s, u
}

func proxyClient(proxyURL *url.URL, roots *x509.CertPool) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: roots},
		},
	}
}

func TestRemoveHopHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Connection", "X-Secret, keep-alive")
	h.Set("X-Secret", "1")
	h.Set("Proxy-Connection", "keep-alive")
	h.Set("Keep-Alive", "timeout=5")
	h.Set("Upgrade", "h2c")
	h.Set("X-Keep", "yes")
	removeHopHeaders(h)
	for _, k := range []string{"Connection", "X-Secret", "Proxy-Connection", "Keep-Alive", "Upgrade"} {
		if h.Get(k) != "" {
			t.Errorf("%s not removed", k)
		}
	}
	if h.Get("X-Keep") != "yes" {
		t.Error("end-to-end header removed")
	}
}

func TestHTTPForward(t *testing.T) {
	var gotHeader http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("echo:" + string(b)))
	}))
	defer upstream.Close()

	hooks := newRecHooks()
	_, pu := startProxy(t, Options{Hooks: hooks})
	req, _ := http.NewRequest("POST", upstream.URL+"/x?y=1", strings.NewReader("hello"))
	req.Header.Set("Proxy-Connection", "keep-alive")
	req.Header.Set("Connection", "X-Drop")
	req.Header.Set("X-Drop", "1")
	resp, err := proxyClient(pu, nil).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "echo:hello" {
		t.Fatalf("body = %q", body)
	}
	if gotHeader.Get("Proxy-Connection") != "" || gotHeader.Get("X-Drop") != "" {
		t.Fatalf("hop headers reached upstream: %v", gotHeader)
	}

	f := hooks.wait(t)
	if f.ErrorKind != "" {
		t.Fatalf("unexpected error kind %s", f.ErrorKind)
	}
	if f.BytesUp != 5 || f.BytesDown != int64(len("echo:hello")) {
		t.Fatalf("byte counts up=%d down=%d", f.BytesUp, f.BytesDown)
	}
	if b, ok := f.RespBody.InMemory(); !ok || string(b) != "echo:hello" {
		t.Fatalf("captured response = %q", b)
	}
	if b, _ := f.ReqBody.InMemory(); string(b) != "hello" {
		t.Fatalf("captured request = %q", b)
	}
}

func TestBodySpillsToDisk(t *testing.T) {
	big := strings.Repeat("x", 4096)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(big)) }))
	defer upstream.Close()
	hooks := newRecHooks()
	_, pu := startProxy(t, Options{Hooks: hooks, InlineLimit: 1024, TempDir: t.TempDir()})
	resp, err := proxyClient(pu, nil).Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	f := hooks.wait(t)
	defer f.RespBody.Close()
	if _, ok := f.RespBody.InMemory(); ok || f.RespBody.SpillPath() == "" {
		t.Fatal("large body did not spill to disk")
	}
	if f.RespBody.Size() != int64(len(big)) {
		t.Fatalf("size = %d", f.RespBody.Size())
	}
}

func TestUnreachableUpstream502(t *testing.T) {
	hooks := newRecHooks()
	_, pu := startProxy(t, Options{Hooks: hooks})
	resp, err := proxyClient(pu, nil).Get("http://127.0.0.1:1/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	f := hooks.wait(t)
	if f.ErrorKind != ErrUpstreamConnect {
		t.Fatalf("error kind = %s", f.ErrorKind)
	}
}

func TestProxyLoopRejected(t *testing.T) {
	_, pu := startProxy(t, Options{})
	resp, err := proxyClient(pu, nil).Get(pu.String() + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// --- HTTPS ---

func mitmSetup(t *testing.T) (*ca.RootCA, *ca.Issuer, *httptest.Server, *x509.CertPool) {
	t.Helper()
	root, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	iss, err := ca.NewIssuer(root, 100)
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secure:" + r.URL.Path))
	}))
	t.Cleanup(upstream.Close)
	upPool := x509.NewCertPool()
	upPool.AddCert(upstream.Certificate())
	return root, iss, upstream, upPool
}

func TestHTTPSIntercept(t *testing.T) {
	root, iss, upstream, upPool := mitmSetup(t)
	hooks := newRecHooks()
	_, pu := startProxy(t, Options{Hooks: hooks, Issuer: iss, UpstreamTLS: &tls.Config{RootCAs: upPool}})

	ours := x509.NewCertPool()
	ours.AddCert(root.Certificate())
	resp, err := proxyClient(pu, ours).Get(upstream.URL + "/hello")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "secure:/hello" {
		t.Fatalf("body = %q", b)
	}
	if resp.TLS == nil || resp.TLS.PeerCertificates[0].Issuer.CommonName != root.Certificate().Subject.CommonName {
		t.Fatal("response was not served with a ProxyPilot leaf")
	}
	f := hooks.wait(t)
	if f.Scheme != "https" || f.Tunneled {
		t.Fatalf("flow scheme=%s tunneled=%v", f.Scheme, f.Tunneled)
	}
	if b, _ := f.RespBody.InMemory(); string(b) != "secure:/hello" {
		t.Fatalf("decrypted body not captured: %q", b)
	}
}

func TestDenylistTunnels(t *testing.T) {
	_, iss, upstream, upPool := mitmSetup(t)
	hooks := newRecHooks()
	policy := NewSSLPolicy(ModeAll, nil, []string{"127.0.0.1"})
	_, pu := startProxy(t, Options{Hooks: hooks, Issuer: iss, Policy: policy})

	// The client trusts only the real upstream cert: works only if tunneled.
	c := proxyClient(pu, upPool)
	resp, err := c.Get(upstream.URL + "/t")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	c.Transport.(*http.Transport).CloseIdleConnections()

	f := hooks.wait(t)
	if !f.Tunneled {
		t.Fatal("expected a tunneled flow")
	}
	if f.BytesUp == 0 || f.BytesDown == 0 {
		t.Fatalf("tunnel bytes not counted: up=%d down=%d", f.BytesUp, f.BytesDown)
	}
}

func TestClientRejectsCertificate(t *testing.T) {
	_, iss, upstream, upPool := mitmSetup(t)
	hooks := newRecHooks()
	_, pu := startProxy(t, Options{Hooks: hooks, Issuer: iss, UpstreamTLS: &tls.Config{RootCAs: upPool}})

	// Client does not trust the ProxyPilot CA → handshake fails on its side.
	_, err := proxyClient(pu, x509.NewCertPool()).Get(upstream.URL)
	if err == nil {
		t.Fatal("expected client TLS failure")
	}
	f := hooks.wait(t)
	if f.ErrorKind != ErrClientTLSRejected {
		t.Fatalf("error kind = %s (%v)", f.ErrorKind, f.Err)
	}
}

func TestRepeatedRejectionTunnelsHost(t *testing.T) {
	_, iss, upstream, upPool := mitmSetup(t)
	hooks := newRecHooks()
	_, pu := startProxy(t, Options{Hooks: hooks, Issuer: iss, UpstreamTLS: &tls.Config{RootCAs: upPool}})

	// A pinned app: trusts the real server, never the ProxyPilot CA.
	pinned := func() *http.Client { return proxyClient(pu, upPool) }
	for i := 0; i < rejectSuggestThreshold; i++ {
		if _, err := pinned().Get(upstream.URL); err == nil {
			t.Fatalf("request %d: expected the pinned client to reject the certificate", i)
		}
		hooks.wait(t)
	}
	resp, err := pinned().Get(upstream.URL)
	if err != nil {
		t.Fatalf("after %d rejections the host should be tunneled: %v", rejectSuggestThreshold, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestUpstreamBadCertificate(t *testing.T) {
	root, iss, upstream, _ := mitmSetup(t)
	hooks := newRecHooks()
	// Proxy uses system roots, which do not include httptest's self-signed cert.
	_, pu := startProxy(t, Options{Hooks: hooks, Issuer: iss})
	ours := x509.NewCertPool()
	ours.AddCert(root.Certificate())
	resp, err := proxyClient(pu, ours).Get(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	f := hooks.wait(t)
	if f.ErrorKind != ErrUpstreamTLS {
		t.Fatalf("error kind = %s (%v)", f.ErrorKind, f.Err)
	}
}
