package proxy

import (
	"net"
	"sort"
	"strings"
	"sync"
)

// SSLPolicy decides per CONNECT host whether to intercept or tunnel: only hosts on the
// allowlist are decrypted (like Proxyman's SSL Proxying list), everything else is tunneled
// untouched. The denylist always wins. Safe for concurrent use; API updates it at runtime.
type SSLPolicy struct {
	mu      sync.RWMutex
	allow   []string
	deny    []string
	learned map[string]string // host → reason (e.g. upstream requires client cert)
	// favorites: also intercept the popular and pinned hosts (kept in auto by the daemon).
	favorites bool
	auto      map[string]bool
}

func NewSSLPolicy(allow, deny []string) *SSLPolicy {
	p := &SSLPolicy{learned: map[string]string{}}
	p.Set(allow, deny)
	return p
}

// Set replaces both lists.
func (p *SSLPolicy) Set(allow, deny []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.allow = normalizePatterns(allow)
	p.deny = normalizePatterns(deny)
}

// PolicySnapshot is the serializable state (API / status).
type PolicySnapshot struct {
	Allowlist []string          `json:"allowlist"`
	Denylist  []string          `json:"denylist"`
	Learned   map[string]string `json:"learned_tunnels"`
	Favorites bool              `json:"include_favorites"`
	// FavoriteHosts: the popular and pinned hosts include_favorites adds right now.
	FavoriteHosts []string `json:"favorite_hosts,omitempty"`
}

// SetFavorites turns decrypting the popular+pinned hosts on or off.
func (p *SSLPolicy) SetFavorites(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.favorites = on
}

// SetFavoriteHosts replaces the popular+pinned host set (exact host names).
func (p *SSLPolicy) SetFavoriteHosts(hosts []string) {
	m := make(map[string]bool, len(hosts))
	for _, h := range normalizePatterns(hosts) {
		m[h] = true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.auto = m
}

func (p *SSLPolicy) Snapshot() PolicySnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	l := make(map[string]string, len(p.learned))
	for k, v := range p.learned {
		l[k] = v
	}
	fav := make([]string, 0, len(p.auto))
	for h := range p.auto {
		fav = append(fav, h)
	}
	sort.Strings(fav)
	return PolicySnapshot{Allowlist: append([]string{}, p.allow...), Denylist: append([]string{}, p.deny...), Learned: l, Favorites: p.favorites, FavoriteHosts: fav}
}

// Why a CONNECT is tunneled instead of decrypted (stored as the flow's tunnel_reason tag).
const (
	TunnelNotAllowed = "not_allowlisted" // SSL Proxying is not enabled for the host
	TunnelDenylist   = "denylist"        // on the "never decrypt" list
	TunnelLearned    = "learned"         // rejected the certificate repeatedly, or needs a client certificate
)

// ShouldIntercept takes "host:port" (or a bare host).
func (p *SSLPolicy) ShouldIntercept(hostport string) bool { return p.TunnelReason(hostport) == "" }

// TunnelReason is "" when the host is decrypted, otherwise one of the Tunnel* reasons.
func (p *SSLPolicy) TunnelReason(hostport string) string {
	host, port := splitHostPortLower(hostport)
	p.mu.RLock()
	defer p.mu.RUnlock()
	if matchAny(p.deny, host, port) {
		return TunnelDenylist
	}
	if _, ok := p.learned[host]; ok {
		return TunnelLearned
	}
	if matchAny(p.allow, host, port) || p.favorites && p.auto[host] {
		return ""
	}
	return TunnelNotAllowed
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
