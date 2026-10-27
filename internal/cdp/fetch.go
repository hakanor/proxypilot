package cdp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/hakanor/proxypilot/internal/proxy"
)

type pausedEvent struct {
	RequestID          string        `json:"requestId"`
	NetworkID          string        `json:"networkId"`
	Request            cdpRequest    `json:"request"`
	ResponseStatusCode int           `json:"responseStatusCode"`
	ResponseHeaders    []fetchHeader `json:"responseHeaders"`
	ResponseError      string        `json:"responseErrorReason"`
}

type fetchHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func toHTTPHeader(h map[string]string) http.Header {
	out := http.Header{}
	for k, v := range h {
		for _, line := range strings.Split(v, "\n") {
			out.Add(k, line)
		}
	}
	return out
}

func fromHTTPHeader(h http.Header) []fetchHeader {
	out := []fetchHeader{}
	for k, vs := range h {
		for _, v := range vs {
			out = append(out, fetchHeader{Name: k, Value: v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// paused runs a Fetch.requestPaused through the rules engine. Whatever happens
// (rule error, panic, Chrome closing) the request is continued, fulfilled or failed.
func (m *Manager) paused(conn *Conn, session string, t *Tab, raw json.RawMessage) {
	var p pausedEvent
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	resolved := false
	ctx := context.Background()
	call := func(method string, params map[string]any) {
		params["requestId"] = p.RequestID
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := conn.Call(cctx, session, method, params, nil); err != nil && err != ErrClosed {
			m.log().Debug("chrome "+method+" failed", "err", err)
		}
		resolved = true
	}
	defer func() {
		if r := recover(); r != nil {
			m.log().Error("chrome rule handling panicked; continuing the request", "panic", fmt.Sprint(r))
		}
		if !resolved {
			call("Fetch.continueRequest", map[string]any{})
		}
	}()
	if m.Rules == nil {
		return
	}

	u, err := url.Parse(p.Request.URL)
	if err != nil {
		return
	}
	var body io.Reader = http.NoBody
	if p.Request.PostData != "" {
		body = strings.NewReader(p.Request.PostData)
	}
	req, err := http.NewRequest(p.Request.Method, p.Request.URL, body)
	if err != nil {
		return
	}
	req.Header = toHTTPHeader(p.Request.Headers)
	f := proxy.NewFlow("chrome:0", u.Scheme)
	f.Host = u.Host
	if u.Port() == "" {
		f.Host = u.Hostname() + map[string]string{"https": ":443"}[u.Scheme]
		if !strings.Contains(f.Host, ":") {
			f.Host += ":80"
		}
	}
	f.Request = req
	key := session + "|" + p.NetworkID
	remember := func() {
		tags := f.Tags()
		if len(tags) == 0 {
			return
		}
		m.mu.Lock()
		if m.matched[key] == nil {
			m.matched[key] = map[string]string{}
		}
		for k, v := range tags {
			if k == "matched_rules" && m.matched[key][k] != "" && !strings.Contains(m.matched[key][k], v) {
				v = m.matched[key][k] + "," + v
			}
			m.matched[key][k] = v
		}
		m.mu.Unlock()
	}

	if p.ResponseStatusCode == 0 && p.ResponseError == "" {
		// Request stage.
		beforeURL, beforeMethod, beforeHdr := req.URL.String(), req.Method, fmt.Sprint(req.Header)
		d := m.Rules.OnRequest(f)
		remember()
		switch d.Action {
		case proxy.Respond:
			call("Fetch.fulfillRequest", fulfillParams(d.Response))
		case proxy.Abort:
			call("Fetch.failRequest", map[string]any{"errorReason": "Aborted"})
		default:
			params := map[string]any{}
			if f.Request.URL.String() != beforeURL {
				params["url"] = f.Request.URL.String()
			}
			if f.Request.Method != beforeMethod {
				params["method"] = f.Request.Method
			}
			if fmt.Sprint(f.Request.Header) != beforeHdr {
				params["headers"] = fromHTTPHeader(f.Request.Header)
			}
			if f.Request.Body != nil && f.Request.Body != http.NoBody && f.Tags()["rewritten"] == "request" {
				b, _ := io.ReadAll(io.LimitReader(f.Request.Body, 16<<20))
				params["postData"] = base64.StdEncoding.EncodeToString(b)
			}
			call("Fetch.continueRequest", params)
		}
		return
	}

	// Response stage: fetch the body only when a rule may need it.
	resp := &http.Response{StatusCode: p.ResponseStatusCode, Status: fmt.Sprintf("%d %s", p.ResponseStatusCode, http.StatusText(p.ResponseStatusCode)),
		Header: http.Header{}, Request: req, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1}
	for _, h := range p.ResponseHeaders {
		resp.Header.Add(h.Name, h.Value)
	}
	var b struct {
		Body          string `json:"body"`
		Base64Encoded bool   `json:"base64Encoded"`
	}
	bctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = conn.Call(bctx, session, "Fetch.getResponseBody", map[string]string{"requestId": p.RequestID}, &b)
	cancel()
	if err != nil {
		return // deferred continue
	}
	data := []byte(b.Body)
	if b.Base64Encoded {
		data, _ = base64.StdEncoding.DecodeString(b.Body)
	}
	// Chrome hands over the decoded body; the encoding header no longer applies.
	resp.Header.Del("Content-Encoding")
	resp.Body = io.NopCloser(bytes.NewReader(data))
	resp.ContentLength = int64(len(data))
	f.Response = resp
	m.Rules.TagMatches(f)
	d := m.Rules.OnResponse(f)
	remember()
	switch d.Action {
	case proxy.Respond:
		call("Fetch.fulfillRequest", fulfillParams(d.Response))
	case proxy.Abort:
		call("Fetch.failRequest", map[string]any{"errorReason": "Aborted"})
	default:
		call("Fetch.continueRequest", map[string]any{})
	}
}

func fulfillParams(r *http.Response) map[string]any {
	var data []byte
	if r.Body != nil {
		data, _ = io.ReadAll(io.LimitReader(r.Body, 64<<20))
		r.Body.Close()
	}
	h := r.Header.Clone()
	h.Del("Content-Length")
	h.Del("Transfer-Encoding")
	return map[string]any{
		"responseCode":    r.StatusCode,
		"responseHeaders": fromHTTPHeader(h),
		"body":            base64.StdEncoding.EncodeToString(data),
	}
}
