package proxy

import (
	"bytes"
	"context"
	"errors"
	"net/http"
)

// SendResult is the outcome of an in-process request (repeat / composer).
type SendResult struct {
	FlowID string
	Status int
	Header http.Header
	Body   []byte
	// Aborted is set when a rule or breakpoint aborted the request.
	Aborted bool
}

// capture records the flow id and marks it as composer traffic before the other hooks run.
type capture struct {
	NopHooks
	tags map[string]string
	id   string
}

func (c *capture) OnRequest(f *Flow) Decision {
	c.id = f.ID
	for k, v := range c.tags {
		f.SetTag(k, v)
	}
	return Decision{}
}

// responseBuffer is a minimal http.ResponseWriter that keeps the response in memory.
type responseBuffer struct {
	header http.Header
	status int
	body   bytes.Buffer
	limit  int
}

func (r *responseBuffer) Header() http.Header { return r.header }
func (r *responseBuffer) WriteHeader(s int) {
	if r.status == 0 {
		r.status = s
	}
}
func (r *responseBuffer) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if room := r.limit - r.body.Len(); room > 0 {
		if len(b) > room {
			r.body.Write(b[:room])
		} else {
			r.body.Write(b)
		}
	}
	return len(b), nil // keep reading so the flow records the full size
}
func (r *responseBuffer) Flush() {}

// Send runs req through hooks (recording, rules…) exactly like proxied traffic and
// returns the response. req must have an absolute URL. tags are set on the flow
// before the hooks see it (e.g. source=composer, repeat_of=<id>).
func (s *Server) Send(ctx context.Context, h Hooks, req *http.Request, tags map[string]string, bodyLimit int) (res SendResult, err error) {
	if !req.URL.IsAbs() || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
		return res, errors.New("URL must be absolute http(s)")
	}
	if s.isSelf(req.URL.Host, req.URL.Scheme) {
		return res, errors.New("refusing to send a request to the proxy itself")
	}
	c := &capture{tags: tags}
	w := &responseBuffer{header: http.Header{}, limit: bodyLimit}
	req = req.WithContext(ctx)
	if req.Header == nil {
		req.Header = http.Header{}
	}
	defer func() {
		// abortConn panics with ErrAbortHandler when it cannot hijack (no real connection here).
		if p := recover(); p != nil {
			if p != http.ErrAbortHandler {
				panic(p)
			}
			res = SendResult{FlowID: c.id, Aborted: true}
		}
	}()
	s.forwardWith(Chain{c, h}, w, req, req.URL.Scheme, "composer:0")
	return SendResult{FlowID: c.id, Status: w.status, Header: w.header, Body: w.body.Bytes()}, nil
}
