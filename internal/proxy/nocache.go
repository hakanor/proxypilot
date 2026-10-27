package proxy

import "sync/atomic"

// NoCache is the "No caching" switch: while on, clients can't reuse anything they
// cached, so every request reaches the server and shows up in full. Requests lose
// their conditional headers and ask for a fresh copy; responses lose their
// validators and come back marked no-store.
type NoCache struct {
	NopHooks
	on atomic.Bool
}

func (n *NoCache) Enabled() bool     { return n.on.Load() }
func (n *NoCache) SetEnabled(v bool) { n.on.Store(v) }

func (n *NoCache) OnRequest(f *Flow) Decision {
	if !n.on.Load() || f.Request == nil {
		return Decision{}
	}
	h := f.Request.Header
	for _, k := range []string{"If-None-Match", "If-Modified-Since", "If-Match", "If-Unmodified-Since", "If-Range"} {
		h.Del(k)
	}
	h.Set("Cache-Control", "no-cache")
	h.Set("Pragma", "no-cache")
	return Decision{}
}

func (n *NoCache) OnResponse(f *Flow) Decision {
	if !n.on.Load() || f.Response == nil {
		return Decision{}
	}
	h := f.Response.Header
	for _, k := range []string{"ETag", "Last-Modified", "Expires", "Age"} {
		h.Del(k)
	}
	h.Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	h.Set("Pragma", "no-cache")
	h.Set("Expires", "0")
	return Decision{}
}
