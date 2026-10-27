package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// h2Upstream is a TLS server that speaks HTTP/2 and echoes the protocol it saw.
func h2Upstream(t *testing.T) (*httptest.Server, *x509.CertPool) {
	up := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("upstream saw " + r.Proto))
	}))
	up.EnableHTTP2 = true
	up.StartTLS()
	t.Cleanup(up.Close)
	pool := x509.NewCertPool()
	pool.AddCert(up.Certificate())
	return up, pool
}

func TestHTTP2BothSides(t *testing.T) {
	root, iss, _, _ := mitmSetup(t)
	up, upPool := h2Upstream(t)
	hooks := newRecHooks()
	_, pu := startProxy(t, Options{Hooks: hooks, Issuer: iss, UpstreamTLS: &tls.Config{RootCAs: upPool}})

	ours := x509.NewCertPool()
	ours.AddCert(root.Certificate())
	c := proxyClient(pu, ours)
	c.Transport.(*http.Transport).ForceAttemptHTTP2 = true
	resp, err := c.Get(up.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Proto != "HTTP/2.0" {
		t.Errorf("client side spoke %s, want HTTP/2.0", resp.Proto)
	}
	if string(b) != "upstream saw HTTP/2.0" {
		t.Errorf("upstream side: %q", b)
	}
	f := hooks.wait(t)
	if f.Request.Proto != "HTTP/2.0" {
		t.Errorf("flow request proto %s", f.Request.Proto)
	}
}

func TestDisableH2(t *testing.T) {
	root, iss, _, _ := mitmSetup(t)
	up, upPool := h2Upstream(t)
	hooks := newRecHooks()
	_, pu := startProxy(t, Options{Hooks: hooks, Issuer: iss, UpstreamTLS: &tls.Config{RootCAs: upPool}, DisableH2: true})

	ours := x509.NewCertPool()
	ours.AddCert(root.Certificate())
	c := proxyClient(pu, ours)
	c.Transport.(*http.Transport).ForceAttemptHTTP2 = true
	resp, err := c.Get(up.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Proto != "HTTP/1.1" || string(b) != "upstream saw HTTP/1.1" {
		t.Errorf("client %s, upstream %q", resp.Proto, b)
	}
	hooks.wait(t)
}
