package clients

import (
	"errors"
	"net/http"
	"testing"

	"github.com/hakanor/proxypilot/internal/proxy"
)

func flow(addr, scheme, ua string) *proxy.Flow {
	req, _ := http.NewRequest("GET", scheme+"://x.test/", nil)
	req.Header.Set("User-Agent", ua)
	return &proxy.Flow{ClientAddr: addr, Scheme: scheme, Request: req, Host: "x.test:443"}
}

func TestTLSStatesAndNames(t *testing.T) {
	dir := t.TempDir()
	tr := New(dir, nil, func(ua string) string { return "Phone" })
	f := flow("192.168.1.20:5000", "https", "ua")
	f.ErrorKind = proxy.ErrClientTLSRejected
	tr.OnError(f, errors.New("bad cert"))
	if c, _ := tr.Get("192.168.1.20"); c.TLS != TLSRejected || c.RejectedHost != "x.test" || c.Local {
		t.Fatalf("rejected: %+v", c)
	}
	tr.OnRequest(flow("192.168.1.20:5001", "https", "ua"))
	if c, _ := tr.Get("192.168.1.20"); c.TLS != TLSOK || c.Name != "Phone" {
		t.Fatalf("ok: %+v", c)
	}
	if _, err := tr.Rename("nope", "x"); !errors.Is(err, ErrInvalidIP) {
		t.Fatal("invalid ip accepted")
	}
	tr.Rename("192.168.1.20", "Hakan's iPhone")
	// Names persist across restarts.
	tr2 := New(dir, nil, nil)
	if tr2.Name("192.168.1.20:1") != "Hakan's iPhone" {
		t.Fatal("name not persisted")
	}
	tr.Rename("192.168.1.20", "")
	if c, _ := tr.Get("192.168.1.20"); c.Custom || c.Name != "Phone" {
		t.Fatalf("cleared name: %+v", c)
	}
}
