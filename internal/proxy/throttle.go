package proxy

import (
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// ThrottleProfile is a named network condition: bandwidth in kbit/s (0 = unlimited)
// and a delay added before each request goes upstream.
type ThrottleProfile struct {
	Name      string `json:"name"`
	DownKbps  int    `json:"down_kbps"`
	UpKbps    int    `json:"up_kbps"`
	LatencyMs int    `json:"latency_ms"`
}

// ThrottleProfiles are the presets; "custom" takes its numbers from the settings.
var ThrottleProfiles = []ThrottleProfile{
	{Name: "edge", DownKbps: 240, UpKbps: 200, LatencyMs: 840},
	{Name: "3g", DownKbps: 780, UpKbps: 330, LatencyMs: 200},
	{Name: "lte", DownKbps: 12000, UpKbps: 5000, LatencyMs: 50},
	{Name: "bad-wifi", DownKbps: 1000, UpKbps: 500, LatencyMs: 400},
}

// LookupThrottleProfile finds a preset by name.
func LookupThrottleProfile(name string) (ThrottleProfile, bool) {
	for _, p := range ThrottleProfiles {
		if p.Name == strings.ToLower(name) {
			return p, true
		}
	}
	return ThrottleProfile{}, false
}

// ThrottleSettings is GET/PUT /api/v1/throttle. Hosts limits throttling to matching
// hosts ("api.example.com", "*.example.com"); empty means all traffic.
type ThrottleSettings struct {
	Enabled bool `json:"enabled"`
	ThrottleProfile
	Hosts []string `json:"hosts,omitempty"`
}

// Throttle slows matching flows down to a network profile: a fixed delay before the
// request leaves, then request and response bodies paced to the profile's bandwidth.
// Each flow is paced on its own, like one connection on a slow link.
type Throttle struct {
	NopHooks
	mu sync.RWMutex
	s  ThrottleSettings
}

func (t *Throttle) Settings() ThrottleSettings {
	t.mu.RLock()
	defer t.mu.RUnlock()
	s := t.s
	s.Hosts = append([]string(nil), s.Hosts...)
	return s
}

func (t *Throttle) Set(s ThrottleSettings) {
	t.mu.Lock()
	t.s = s
	t.mu.Unlock()
}

func (t *Throttle) active(f *Flow) (ThrottleSettings, bool) {
	s := t.Settings()
	if !s.Enabled || f.Request == nil {
		return s, false
	}
	if len(s.Hosts) == 0 {
		return s, true
	}
	host := f.Request.URL.Hostname()
	if host == "" {
		host, _, _ = net.SplitHostPort(f.Request.Host)
	}
	return s, hostMatches(strings.ToLower(host), s.Hosts)
}

func hostMatches(host string, patterns []string) bool {
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == host || (strings.HasPrefix(p, "*.") && strings.HasSuffix(host, p[1:])) {
			return true
		}
		if ok, _ := path.Match(p, host); ok {
			return true
		}
	}
	return false
}

func (t *Throttle) OnRequest(f *Flow) Decision {
	s, ok := t.active(f)
	if !ok {
		return Decision{}
	}
	if s.LatencyMs > 0 {
		select {
		case <-time.After(time.Duration(s.LatencyMs) * time.Millisecond):
		case <-f.Request.Context().Done():
		}
	}
	if s.UpKbps > 0 && f.Request.Body != nil && f.Request.Body != http.NoBody {
		f.Request.Body = newPacedBody(f.Request.Body, s.UpKbps)
	}
	f.SetTag("throttle", s.Name)
	return Decision{}
}

func (t *Throttle) OnResponse(f *Flow) Decision {
	s, ok := t.active(f)
	if !ok || f.Response == nil || f.Response.Body == nil || s.DownKbps <= 0 {
		return Decision{}
	}
	f.Response.Body = newPacedBody(f.Response.Body, s.DownKbps)
	return Decision{}
}

// pacedBody hands out at most kbps worth of bytes per unit of time.
type pacedBody struct {
	io.ReadCloser
	bps   float64 // bytes per second
	start time.Time
	n     int64
}

func newPacedBody(rc io.ReadCloser, kbps int) *pacedBody {
	return &pacedBody{ReadCloser: rc, bps: float64(kbps) * 1000 / 8}
}

func (p *pacedBody) Read(b []byte) (int, error) {
	if p.start.IsZero() {
		p.start = time.Now()
	}
	// Small reads keep the pace smooth instead of one burst then a long sleep.
	if max := int(p.bps / 10); max > 0 && len(b) > max {
		b = b[:max]
	}
	n, err := p.ReadCloser.Read(b)
	p.n += int64(n)
	if due := p.start.Add(time.Duration(float64(p.n) / p.bps * float64(time.Second))); time.Until(due) > 0 {
		time.Sleep(time.Until(due))
	}
	return n, err
}
