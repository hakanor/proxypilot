package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultUpstreamBypass never goes through the upstream proxy.
var DefaultUpstreamBypass = []string{"localhost", "*.localhost", "*.local"}

// upstream is a second proxy that ProxyPilot sends its outgoing traffic through
// (a company proxy, Burp, …). Loopback and private addresses always go direct.
type upstream struct {
	u      *url.URL
	bypass []string
}

// ParseUpstream checks an upstream proxy URL: http://[user:pass@]host[:port].
func ParseUpstream(raw string) (*url.URL, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("upstream proxy: %w", err)
	}
	if u.Scheme != "http" || u.Hostname() == "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("upstream proxy must look like http://[user:pass@]host:port")
	}
	if u.Port() == "" {
		u.Host = net.JoinHostPort(u.Hostname(), "80")
	}
	return u, nil
}

// SetUpstream sends outgoing traffic through raw ("" turns it off). bypass adds
// host patterns (api.x.com, *.x.com) that keep going direct.
func (s *Server) SetUpstream(raw string, bypass []string) error {
	if raw == "" {
		s.up.Store(nil)
		return nil
	}
	u, err := ParseUpstream(raw)
	if err != nil {
		return err
	}
	s.up.Store(&upstream{u: u, bypass: normalizePatterns(append(append([]string{}, DefaultUpstreamBypass...), bypass...))})
	return nil
}

// UpstreamHost is the upstream proxy's host:port ("" when direct); never the credentials.
func (s *Server) UpstreamHost() string {
	if up := s.up.Load(); up != nil {
		return up.u.Host
	}
	return ""
}

// upstreamFor returns the upstream proxy for hostport, or nil to connect directly.
func (s *Server) upstreamFor(hostport string) *url.URL {
	up := s.up.Load()
	if up == nil {
		return nil
	}
	host, port := splitHostPortLower(hostport)
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return nil
	}
	if matchAny(up.bypass, host, port) {
		return nil
	}
	return up.u
}

func (s *Server) proxyFunc(r *http.Request) (*url.URL, error) {
	hp := r.URL.Host
	if r.URL.Port() == "" {
		port := "80"
		if r.URL.Scheme == "https" || r.URL.Scheme == "wss" {
			port = "443"
		}
		hp = net.JoinHostPort(r.URL.Hostname(), port)
	}
	if u := s.upstreamFor(hp); u != nil {
		return u, nil
	}
	return nil, nil
}

// dial opens a TCP connection to hostport, through the upstream proxy (CONNECT) when one is set.
func (s *Server) dial(ctx context.Context, hostport string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	u := s.upstreamFor(hostport)
	if u == nil {
		return d.DialContext(ctx, "tcp", hostport)
	}
	c, err := d.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return nil, fmt.Errorf("upstream proxy %s: %w", u.Host, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	} else {
		_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	}
	req := "CONNECT " + hostport + " HTTP/1.1\r\nHost: " + hostport + "\r\n"
	if u.User != nil {
		pass, _ := u.User.Password()
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(u.User.Username()+":"+pass)) + "\r\n"
	}
	if _, err := c.Write([]byte(req + "\r\n")); err != nil {
		c.Close()
		return nil, fmt.Errorf("upstream proxy %s: %w", u.Host, err)
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("upstream proxy %s: %w", u.Host, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.Close()
		return nil, fmt.Errorf("upstream proxy %s refused CONNECT %s: %s", u.Host, hostport, resp.Status)
	}
	_ = c.SetDeadline(time.Time{})
	return prefixConn(c, br), nil
}
