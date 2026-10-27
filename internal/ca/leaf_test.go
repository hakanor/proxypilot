package ca

import (
	"crypto/x509"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestIssuer(t *testing.T, size int) (*RootCA, *Issuer) {
	t.Helper()
	r, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	i, err := NewIssuer(r, size)
	if err != nil {
		t.Fatal(err)
	}
	return r, i
}

func TestLeafCached(t *testing.T) {
	_, i := newTestIssuer(t, 10)
	a, err := i.Get("api.example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	b, err := i.Get("API.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("second call was not served from cache")
	}
}

func TestLeafVerifiesAgainstRoot(t *testing.T) {
	r, i := newTestIssuer(t, 10)
	for _, host := range []string{"api.example.com", "10.0.0.7"} {
		c, err := i.Get(host)
		if err != nil {
			t.Fatal(err)
		}
		pool := x509.NewCertPool()
		pool.AddCert(r.Certificate())
		_, err = c.Leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		if c.Leaf.NotAfter.Sub(c.Leaf.NotBefore) > 398*24*time.Hour {
			t.Fatalf("%s: validity too long", host)
		}
	}
}

func TestLeafSingleflight(t *testing.T) {
	_, i := newTestIssuer(t, 10)
	var issued atomic.Int32
	var wg sync.WaitGroup
	results := make(chan any, 100)
	for n := 0; n < 100; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := i.Get("same.example.com")
			if err != nil {
				t.Error(err)
				return
			}
			results <- c
		}()
	}
	wg.Wait()
	close(results)
	var first any
	for c := range results {
		if first == nil {
			first = c
		} else if c != first {
			issued.Add(1)
		}
	}
	if issued.Load() != 0 {
		t.Fatalf("%d calls got a different certificate; expected one issuance", issued.Load())
	}
}

func TestLeafLRUAndRotation(t *testing.T) {
	r, i := newTestIssuer(t, 2)
	for _, h := range []string{"a.test", "b.test", "c.test"} {
		if _, err := i.Get(h); err != nil {
			t.Fatal(err)
		}
	}
	if i.Len() != 2 {
		t.Fatalf("cache len = %d, want 2", i.Len())
	}
	if err := r.Regenerate(); err != nil {
		t.Fatal(err)
	}
	if i.Len() != 0 {
		t.Fatal("cache not cleared after CA regenerate")
	}
	c, err := i.Get("a.test")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(r.Certificate())
	if _, err := c.Leaf.Verify(x509.VerifyOptions{DNSName: "a.test", Roots: pool}); err != nil {
		t.Fatalf("leaf not signed by new CA: %v", err)
	}
}
