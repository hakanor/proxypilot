package proxy

import (
	"fmt"
	"net"
	"strings"
	"sync"
)

type SSLMode string

const (
	ModeAll       SSLMode = "all"       // intercept everything except the denylist
	ModeAllowlist SSLMode = "allowlist" // intercept only the allowlist (minus the denylist)
	ModeOff       SSLMode = "off"       // tunnel everything
)

// SSLPolicy decides per CONNECT host whether to intercept or tunnel.
// The denylist always wins. Safe for concurrent use; API updates it at runtime.
type SSLPolicy struct {
	mu      sync.RWMutex
	mode    SSLMode
	allow   []string
	deny    []string
	learned map[string]string // host → reason (e.g. upstream requires client cert)
}

func NewSSLPolicy(mode SSLMode, allow, deny []string) *SSLPolicy {
	p := &SSLPolicy{learned: map[string]string{}}
	p.Set(mode, allow, deny)
	return p
}

func ParseSSLMode(s string) (SSLMode, error) {
	switch m := SSLMode(strings.ToLower(s)); m {
	case ModeAll, ModeAllowlist, ModeOff:
		return m, nil
	}
	return "", fmt.Errorf("invalid ssl mode %q (all|allowlist|off)", s)
}

// Set replaces mode and lists.
func (p *SSLPolicy) Set(mode SSLMode, allow, deny []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mode = mode
	p.allow = normalizePatterns(allow)
	p.deny = normalizePatterns(deny)
}

// PolicySnapshot is the serializable state (API / status).
type PolicySnapshot struct {
	Mode      SSLMode           `json:"mode"`
	Allowlist []string          `json:"allowlist"`
	Denylist  []string          `json:"denylist"`
	Learned   map[string]string `json:"learned_tunnels"`
}

func (p *SSLPolicy) Snapshot() PolicySnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	l := make(map[string]string, len(p.learned))
	for k, v := range p.learned {
		l[k] = v
	}
	return PolicySnapshot{Mode: p.mode, Allowlist: append([]string{}, p.allow...), Denylist: append([]string{}, p.deny...), Learned: l}
}

// ShouldIntercept takes "host:port" (or a bare host).
func (p *SSLPolicy) ShouldIntercept(hostport string) bool {
	host, port := splitHostPortLower(hostport)
	p.mu.RLock()
	defer p.mu.RUnlock()
	if matchAny(p.deny, host, port) {
		return false
	}
	if _, ok := p.learned[host]; ok {
		return false
	}
	switch p.mode {
	case ModeOff:
		return false
	case ModeAllowlist:
		return matchAny(p.allow, host, port)
	default:
		return true
	}
}

// LearnTunnel makes future CONNECTs to host bypass interception
// (used when the upstream demands a client certificate).
func (p *SSLPolicy) LearnTunnel(hostport, reason string) {
	host, _ := splitHostPortLower(hostport)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.learned[host] = reason
}

// ForgetTunnel lets host be intercepted again (it is re-learned if it keeps rejecting).
func (p *SSLPolicy) ForgetTunnel(host string) {
	host, _ = splitHostPortLower(host)
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.learned, host)
}

// AddDeny appends a pattern to the denylist (e.g. after repeated client_tls_rejected).
func (p *SSLPolicy) AddDeny(pattern string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	for _, d := range p.deny {
		if d == pattern {
			return
		}
	}
	p.deny = append(p.deny, pattern)
}

func normalizePatterns(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func splitHostPortLower(hp string) (host, port string) {
	if h, p, err := net.SplitHostPort(hp); err == nil {
		return strings.ToLower(strings.TrimSuffix(h, ".")), p
	}
	return strings.ToLower(strings.TrimSuffix(strings.Trim(hp, "[]"), ".")), ""
}

func matchAny(patterns []string, host, port string) bool {
	for _, p := range patterns {
		if matchPattern(p, host, port) {
			return true
		}
	}
	return false
}

// matchPattern: "api.example.com", "*.example.com" (subdomains only),
// "*" (everything), optionally with ":port".
func matchPattern(pattern, host, port string) bool {
	pHost, pPort := pattern, ""
	if i := strings.LastIndex(pattern, ":"); i > 0 && !strings.Contains(pattern[:i], ":") {
		pHost, pPort = pattern[:i], pattern[i+1:]
	}
	if pPort != "" && pPort != port {
		return false
	}
	switch {
	case pHost == "*":
		return true
	case strings.HasPrefix(pHost, "*."):
		return strings.HasSuffix(host, pHost[1:])
	default:
		return host == pHost
	}
}
