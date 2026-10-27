package ca

import (
	"container/list"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// LeafIssuer returns a TLS certificate for a host, signed by the root CA.
type LeafIssuer interface {
	Get(host string) (*tls.Certificate, error)
}

const (
	// Apple rejects TLS server certificates valid for more than 398 days.
	leafValidity     = 397 * 24 * time.Hour
	defaultCacheSize = 1000
)

// Issuer is the default LeafIssuer: one shared ECDSA P-256 key, one
// certificate per host, LRU-cached, concurrent misses coalesced.
type Issuer struct {
	root *RootCA
	key  *ecdsa.PrivateKey
	max  int

	group singleflight.Group

	mu    sync.Mutex
	gen   uint64
	ll    *list.List
	items map[string]*list.Element
}

type cacheEntry struct {
	host string
	cert *tls.Certificate
}

func NewIssuer(root *RootCA, cacheSize int) (*Issuer, error) {
	if cacheSize <= 0 {
		cacheSize = defaultCacheSize
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Issuer{root: root, key: key, max: cacheSize, gen: root.Generation(), ll: list.New(), items: map[string]*list.Element{}}, nil
}

// normalizeHost strips port, brackets and trailing dot, lowercases.
func normalizeHost(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimSuffix(strings.Trim(h, "[]"), ".")
	return strings.ToLower(h)
}

func (i *Issuer) Get(host string) (*tls.Certificate, error) {
	host = normalizeHost(host)
	if host == "" {
		return nil, errors.New("empty host")
	}
	if c := i.lookup(host); c != nil {
		return c, nil
	}
	v, err, _ := i.group.Do(host, func() (any, error) {
		if c := i.lookup(host); c != nil {
			return c, nil
		}
		gen := i.root.Generation()
		c, err := i.issue(host)
		if err != nil {
			return nil, err
		}
		i.store(host, c, gen)
		return c, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*tls.Certificate), nil
}

// GetCertificate plugs into tls.Config. fallbackHost is used when the client
// sends no SNI (e.g. connecting by IP); it is usually the CONNECT host.
func (i *Issuer) GetCertificate(fallbackHost string) func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		host := hello.ServerName
		if host == "" {
			host = fallbackHost
		}
		return i.Get(host)
	}
}

// Len reports cached entries (for tests and status).
func (i *Issuer) Len() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.resetIfRotated()
	return i.ll.Len()
}

func (i *Issuer) lookup(host string) *tls.Certificate {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.resetIfRotated()
	if el, ok := i.items[host]; ok {
		i.ll.MoveToFront(el)
		return el.Value.(*cacheEntry).cert
	}
	return nil
}

func (i *Issuer) store(host string, c *tls.Certificate, gen uint64) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.resetIfRotated()
	if gen != i.gen { // CA rotated while we were signing; don't cache a stale leaf
		return
	}
	if el, ok := i.items[host]; ok {
		el.Value.(*cacheEntry).cert = c
		i.ll.MoveToFront(el)
		return
	}
	i.items[host] = i.ll.PushFront(&cacheEntry{host: host, cert: c})
	for i.ll.Len() > i.max {
		old := i.ll.Back()
		i.ll.Remove(old)
		delete(i.items, old.Value.(*cacheEntry).host)
	}
}

// resetIfRotated drops every leaf after a CA regenerate. Caller holds mu.
func (i *Issuer) resetIfRotated() {
	if g := i.root.Generation(); g != i.gen {
		i.gen = g
		i.ll.Init()
		i.items = map[string]*list.Element{}
	}
}

func (i *Issuer) issue(host string) (*tls.Certificate, error) {
	rootCert, rootKey, _ := i.root.Current()
	serial, err := randSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:   serial,
		Subject:        pkix.Name{CommonName: host, Organization: []string{"ProxyPilot"}},
		NotBefore:      now.Add(-24 * time.Hour),
		NotAfter:       now.Add(leafValidity),
		KeyUsage:       x509.KeyUsageDigitalSignature,
		ExtKeyUsage:    []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		AuthorityKeyId: rootCert.SubjectKeyId,
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, rootCert, &i.key.PublicKey, rootKey)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{
		Certificate: [][]byte{der, rootCert.Raw},
		PrivateKey:  i.key,
		Leaf:        leaf,
	}, nil
}
