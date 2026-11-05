package proxy

import (
	"crypto/x509"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeUpstream is a second proxy: plain HTTP is answered by itself, CONNECT goes to target.
type fakeUpstream struct {
	mu     sync.Mutex
	seen   []string
	auth   string
	target string
}

func (f *fakeUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.seen = append(f.seen, r.Method+" "+r.RequestURI)
	f.mu.Unlock()
	if r.Header.Get("Proxy-Authorization") != f.auth {
		w.WriteHeader(http.StatusProxyAuthRequired)
		return
	}
	if r.Method != http.MethodConnect {
		io.WriteString(w, "via-upstream "+r.URL.String())
		return
	}
	up, err := net.Dial("tcp", f.target)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	c, brw, _ := w.(http.Hijacker).Hijack()
	io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
	go io.Copy(up, prefixConn(c, brw.Reader))
	go func() { io.Copy(c, up); c.Close() }()
}

func TestUpstreamProxy(t *testing.T) {
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "tls-ok") }))
	defer tlsSrv.Close()
	fake := &fakeUpstream{auth: "Basic " + base64.StdEncoding.EncodeToString([]byte("me:secret")), target: tlsSrv.Listener.Addr().String()}
	up := httptest.NewServer(fake)
	defer up.Close()

	s, pu := startProxy(t, Options{Hooks: newRecHooks(), Policy: NewSSLPolicy(ModeOff, nil, nil)})
	if err := s.SetUpstream("http://me:secret@"+up.Listener.Addr().String(), []string{"direct.example"}); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(tlsSrv.Certificate())
	c := proxyClient(pu, roots)

	// Plain HTTP goes to the upstream proxy in absolute form, with the credentials.
	resp, err := c.Get("http://example.com/a?b=1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "via-upstream http://example.com/a?b=1" {
		t.Fatalf("http: %q", b)
	}
	// HTTPS (tunneled) is a CONNECT through the upstream proxy.
	resp, err = c.Get("https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "tls-ok" {
		t.Fatalf("https: %q", b)
	}
	fake.mu.Lock()
	seen := append([]string{}, fake.seen...)
	fake.mu.Unlock()
	if len(seen) != 2 || seen[1] != "CONNECT example.com:443" {
		t.Fatalf("upstream saw %v", seen)
	}

	// Loopback, private and bypassed hosts go direct; off means direct for everything.
	for hp, via := range map[string]bool{"example.com:443": true, "127.0.0.1:80": false, "10.0.0.5:80": false, "localhost:80": false, "direct.example:443": false} {
		if got := s.upstreamFor(hp) != nil; got != via {
			t.Errorf("%s: via upstream = %v", hp, got)
		}
	}
	// Wrong credentials: the CONNECT fails with the upstream's status.
	if err := s.SetUpstream("http://me:wrong@"+up.Listener.Addr().String(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.dial(t.Context(), "example.com:443"); err == nil {
		t.Fatal("want an error for 407")
	}
	_ = s.SetUpstream("", nil)
	if s.upstreamFor("example.com:443") != nil {
		t.Fatal("upstream still set after off")
	}
	for _, bad := range []string{"socks5://x:1080", "http://", "http://x:1/path"} {
		if _, err := ParseUpstream(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
