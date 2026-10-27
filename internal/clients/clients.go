// Package clients tracks the devices talking to the proxy (by IP): first contact,
// a guessed name, and whether HTTPS decryption works for them. It drives the
// "Device reached the proxy → certificate trusted" checklist and custom names.
package clients

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hakanor/proxypilot/internal/events"
	"github.com/hakanor/proxypilot/internal/proxy"
)

const NamesFile = "clients.json"

// TLS states.
const (
	TLSUnknown  = ""         // no HTTPS seen yet
	TLSOK       = "ok"       // at least one request was decrypted
	TLSRejected = "rejected" // the client refused our certificate
	TLSTunneled = "tunneled" // HTTPS passed through without decryption (policy)
)

type Client struct {
	IP        string    `json:"ip"`
	Name      string    `json:"name"`             // custom name, else the guess
	Guess     string    `json:"guess"`            // from User-Agent
	Custom    bool      `json:"custom,omitempty"` // Name was set by the user
	UserAgent string    `json:"user_agent,omitempty"`
	Local     bool      `json:"local"` // this Mac (loopback)
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Requests  int64     `json:"requests"`
	TLS       string    `json:"tls"`
	// RejectedHost is the last host whose certificate the client refused.
	RejectedHost string `json:"rejected_host,omitempty"`
}

// Tracker is a proxy.Hooks observer; it never changes traffic.
type Tracker struct {
	proxy.NopHooks
	bus   *events.Bus
	path  string
	guess func(ua string) string
	now   func() time.Time

	mu      sync.Mutex
	clients map[string]*Client
	names   map[string]string // ip → custom name (persisted)
}

func New(dataDir string, bus *events.Bus, guess func(string) string) *Tracker {
	t := &Tracker{bus: bus, guess: guess, now: time.Now, clients: map[string]*Client{}, names: map[string]string{}}
	if dataDir != "" {
		t.path = filepath.Join(dataDir, NamesFile)
		if b, err := os.ReadFile(t.path); err == nil {
			_ = json.Unmarshal(b, &t.names)
		}
	}
	return t
}

func ipOf(addr string) string {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return h
}

func isLocal(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && p.IsLoopback()
}

// seen updates the client for f and returns a copy plus whether it is new.
func (t *Tracker) seen(f *proxy.Flow, update func(c *Client)) {
	ip := ipOf(f.ClientAddr)
	if net.ParseIP(ip) == nil {
		return // composer and other in-process traffic
	}
	ua := ""
	if f.Request != nil {
		ua = f.Request.Header.Get("User-Agent")
	}
	t.mu.Lock()
	c, ok := t.clients[ip]
	if !ok {
		c = &Client{IP: ip, Local: isLocal(ip), FirstSeen: t.now()}
		t.clients[ip] = c
	}
	c.LastSeen = t.now()
	// Prefer a browser/app UA over CONNECT-less guesses.
	if ua != "" && (c.UserAgent == "" || len(ua) > len(c.UserAgent) && c.Guess == "") {
		c.UserAgent = ua
		if t.guess != nil {
			c.Guess = t.guess(ua)
		}
	}
	t.applyName(c)
	update(c)
	snap := *c
	t.mu.Unlock()
	if !ok && t.bus != nil {
		t.bus.Publish(events.DeviceConnected, &snap)
	}
}

func (t *Tracker) applyName(c *Client) {
	if n, ok := t.names[c.IP]; ok && n != "" {
		c.Name, c.Custom = n, true
		return
	}
	c.Name, c.Custom = c.Guess, false
	if c.Name == "" {
		c.Name = c.IP
	}
}

func (t *Tracker) OnRequest(f *proxy.Flow) proxy.Decision {
	t.seen(f, func(c *Client) {
		c.Requests++
		if f.Scheme == "https" && !f.Tunneled {
			c.TLS = TLSOK
		}
	})
	return proxy.Decision{}
}

func (t *Tracker) OnComplete(f *proxy.Flow) {
	if f.Tunneled {
		t.seen(f, func(c *Client) {
			if c.TLS == TLSUnknown {
				c.TLS = TLSTunneled
			}
		})
	}
}

func (t *Tracker) OnError(f *proxy.Flow, _ error) {
	if f.ErrorKind != proxy.ErrClientTLSRejected {
		return
	}
	t.seen(f, func(c *Client) {
		host, _, _ := net.SplitHostPort(f.Host)
		c.RejectedHost = host
		if c.TLS != TLSOK {
			c.TLS = TLSRejected
		}
	})
}

// Name returns the display name for an IP ("" if unknown), used to label flows.
func (t *Tracker) Name(addr string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.names[ipOf(addr)]
}

// Get returns the client for an address (IP or ip:port).
func (t *Tracker) Get(addr string) (Client, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c, ok := t.clients[ipOf(addr)]
	if !ok {
		return Client{IP: ipOf(addr)}, false
	}
	return *c, true
}

// List returns known clients, most recent first.
func (t *Tracker) List() []Client {
	t.mu.Lock()
	out := make([]Client, 0, len(t.clients))
	for _, c := range t.clients {
		out = append(out, *c)
	}
	t.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}

var ErrInvalidIP = errors.New("invalid IP address")

// Rename sets (or with "" clears) a custom name for ip and persists it.
func (t *Tracker) Rename(ip, name string) (Client, error) {
	if net.ParseIP(ip) == nil {
		return Client{}, ErrInvalidIP
	}
	name = strings.TrimSpace(name)
	if len(name) > 64 {
		name = name[:64]
	}
	t.mu.Lock()
	if name == "" {
		delete(t.names, ip)
	} else {
		t.names[ip] = name
	}
	c, ok := t.clients[ip]
	if !ok {
		c = &Client{IP: ip, Local: isLocal(ip)}
	} else {
		t.applyName(c)
	}
	if !ok {
		t.applyName(c)
	}
	snap := *c
	var err error
	if t.path != "" {
		b, _ := json.MarshalIndent(t.names, "", "  ")
		err = os.WriteFile(t.path, b, 0o600)
	}
	t.mu.Unlock()
	return snap, err
}
