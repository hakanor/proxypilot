package breakpoints

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/hakanor/proxypilot/internal/proxy"
	"github.com/hakanor/proxypilot/internal/store"
)

// Options come from the breakpoint rule.
type Options struct {
	RuleID, RuleName, Owner string
	Timeout                 time.Duration
	OnTimeout               string
}

func headersOf(h http.Header) []Header {
	out := []Header{}
	for k, vs := range h {
		for _, v := range vs {
			out = append(out, Header{Name: k, Value: v})
		}
	}
	sortHeaders(out)
	return out
}

func sortHeaders(hs []Header) {
	for i := 1; i < len(hs); i++ {
		for j := i; j > 0 && hs[j].Name < hs[j-1].Name; j-- {
			hs[j], hs[j-1] = hs[j-1], hs[j]
		}
	}
}

func toHeader(hs []Header) http.Header {
	h := http.Header{}
	for _, x := range hs {
		if x.Name != "" {
			h.Add(x.Name, x.Value)
		}
	}
	return h
}

// readBody buffers up to MaxBody and returns a reader that replays everything.
func readBody(rc io.ReadCloser) ([]byte, io.ReadCloser, bool, error) {
	if rc == nil || rc == http.NoBody {
		return nil, http.NoBody, false, nil
	}
	buf, err := io.ReadAll(io.LimitReader(rc, MaxBody+1))
	if err != nil {
		return nil, nil, false, err
	}
	if len(buf) > MaxBody {
		// Too big to edit: replay what we read, then the rest of the stream.
		return buf[:MaxBody], struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(buf), rc), rc}, true, nil
	}
	rc.Close()
	return buf, io.NopCloser(bytes.NewReader(buf)), false, nil
}

func encodeBody(b []byte) (string, string) {
	if utf8.Valid(b) {
		return string(b), ""
	}
	return "", base64.StdEncoding.EncodeToString(b)
}

func decodeBody(text, b64 string) ([]byte, error) {
	if b64 != "" {
		return base64.StdEncoding.DecodeString(b64)
	}
	return []byte(text), nil
}

// PauseRequest holds f before it is sent upstream and applies the decision to f.Request.
func (m *Manager) PauseRequest(f *proxy.Flow, o Options) proxy.Decision {
	req := f.Request
	body, replay, truncated, err := readBody(req.Body)
	if err != nil {
		return proxy.Decision{} // the client stopped sending; let the proxy report it
	}
	req.Body = replay
	text, b64 := encodeBody(body)
	p := &Paused{
		FlowID: f.ID, Phase: PhaseRequest, RuleID: o.RuleID, RuleName: o.RuleName, Owner: o.Owner,
		Source: "proxy", Client: f.ClientAddr, OnTimeout: o.OnTimeout, Truncated: truncated,
		Request: Request{Method: req.Method, URL: req.URL.String(), Headers: headersOf(req.Header), Body: text, BodyBase64: b64},
	}
	res := m.Pause(req.Context(), p, o.Timeout)
	f.SetTag("breakpoint", p.ID)
	if res.TimedOut {
		f.SetTag("breakpoint_timed_out", "true")
	}
	switch res.Action {
	case ActionAbort:
		// A 502, not a closed connection: clients retry idempotent requests on a
		// dropped connection, which would hit the breakpoint again.
		f.SetTag("breakpoint_aborted", "request")
		return proxy.Decision{Action: proxy.Respond, Response: abortedResponse(req)}
	case ActionModify:
		if res.Request != nil {
			if err := applyRequest(f, res.Request, truncated); err != nil {
				return proxy.Decision{Action: proxy.Respond, Response: errorResponse(req, err)}
			}
			f.SetTag("breakpoint_modified", "request")
		}
	}
	return proxy.Decision{}
}

// applyRequest edits the outgoing request in place (the proxy sends this object).
func applyRequest(f *proxy.Flow, e *Request, truncated bool) error {
	req := f.Request
	if e.Method != "" {
		req.Method = e.Method
	}
	if e.URL != "" && e.URL != req.URL.String() {
		u, err := url.Parse(e.URL)
		if err != nil || !u.IsAbs() {
			return fmt.Errorf("invalid URL %q", e.URL)
		}
		if u.Scheme != f.Scheme {
			return fmt.Errorf("cannot change the scheme from %s to %s", f.Scheme, u.Scheme)
		}
		req.URL, req.Host = u, u.Host
		f.Host = u.Host
		if u.Port() == "" {
			port := "80"
			if u.Scheme == "https" {
				port = "443"
			}
			f.Host = u.Host + ":" + port
		}
	}
	if e.Headers != nil {
		req.Header = toHeader(e.Headers)
	}
	if !truncated {
		b, err := decodeBody(e.Body, e.BodyBase64)
		if err != nil {
			return fmt.Errorf("body_base64: %w", err)
		}
		req.Body = io.NopCloser(bytes.NewReader(b))
		req.ContentLength = int64(len(b))
		req.Header.Del("Transfer-Encoding")
		if len(b) > 0 || req.Header.Get("Content-Length") != "" {
			req.Header.Set("Content-Length", strconv.Itoa(len(b)))
		}
	}
	return nil
}

// PauseResponse holds the upstream response before it reaches the client.
func (m *Manager) PauseResponse(f *proxy.Flow, o Options) proxy.Decision {
	resp := f.Response
	body, replay, truncated, err := readBody(resp.Body)
	if err != nil {
		return proxy.Decision{}
	}
	resp.Body = replay // "continue" sends the original bytes
	// Show the decoded body; an edited body is sent decoded (Content-Encoding dropped).
	encoding := resp.Header.Get("Content-Encoding")
	if encoding != "" && !truncated {
		if dec, err := store.Decode(body, encoding); err == nil {
			body = dec
		} else {
			encoding = "" // undecodable: edit the raw bytes
		}
	}
	text, b64 := encodeBody(body)
	req := f.Request
	p := &Paused{
		FlowID: f.ID, Phase: PhaseResponse, RuleID: o.RuleID, RuleName: o.RuleName, Owner: o.Owner,
		Source: "proxy", Client: f.ClientAddr, OnTimeout: o.OnTimeout, Truncated: truncated,
		Request:  Request{Method: req.Method, URL: req.URL.String(), Headers: headersOf(req.Header)},
		Response: &Response{Status: resp.StatusCode, Headers: headersOf(resp.Header), Body: text, BodyBase64: b64, Encoding: encoding},
	}
	ctx := req.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	res := m.Pause(ctx, p, o.Timeout)
	f.SetTag("breakpoint", p.ID)
	if res.TimedOut {
		f.SetTag("breakpoint_timed_out", "true")
	}
	switch res.Action {
	case ActionAbort:
		return proxy.Decision{Action: proxy.Abort}
	case ActionModify:
		if res.Response != nil {
			out, err := buildResponse(resp, res.Response, truncated)
			if err != nil {
				return proxy.Decision{Action: proxy.Respond, Response: errorResponse(req, err)}
			}
			f.SetTag("breakpoint_modified", "response")
			return proxy.Decision{Action: proxy.Respond, Response: out}
		}
	}
	return proxy.Decision{}
}

func buildResponse(orig *http.Response, e *Response, truncated bool) (*http.Response, error) {
	out := *orig
	status := e.Status
	if status == 0 {
		status = orig.StatusCode
	}
	if status < 100 || status > 599 {
		return nil, fmt.Errorf("status %d out of range", status)
	}
	out.StatusCode, out.Status = status, fmt.Sprintf("%d %s", status, http.StatusText(status))
	if e.Headers != nil {
		out.Header = toHeader(e.Headers)
	} else {
		out.Header = orig.Header.Clone()
	}
	if !truncated {
		b, err := decodeBody(e.Body, e.BodyBase64)
		if err != nil {
			return nil, fmt.Errorf("body_base64: %w", err)
		}
		if orig.Body != nil {
			orig.Body.Close()
		}
		out.Body = io.NopCloser(bytes.NewReader(b))
		out.ContentLength = int64(len(b))
		out.Header.Del("Transfer-Encoding")
		out.Header.Set("Content-Length", strconv.Itoa(len(b)))
		// The edited body is what the client gets, byte for byte (it was shown decoded).
		if e.Encoding != "" || orig.Header.Get("Content-Encoding") != "" && e.Headers == nil {
			out.Header.Del("Content-Encoding")
		}
	}
	return &out, nil
}

func abortedResponse(req *http.Request) *http.Response {
	r := errorResponse(req, fmt.Errorf("request aborted at a breakpoint; it was not sent to the server"))
	r.StatusCode, r.Status = http.StatusBadGateway, "502 Bad Gateway"
	return r
}

func errorResponse(req *http.Request, err error) *http.Response {
	b := []byte("ProxyPilot breakpoint: " + err.Error() + "\n")
	return &http.Response{
		StatusCode: http.StatusBadRequest, Status: "400 Bad Request", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{"Content-Type": {"text/plain; charset=utf-8"}, "Content-Length": {strconv.Itoa(len(b))}},
		Body:   io.NopCloser(bytes.NewReader(b)), ContentLength: int64(len(b)), Request: req,
	}
}
