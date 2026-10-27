// Package archive exports and imports traffic: HAR 1.2 (Chrome DevTools,
// Proxyman, Charles…) and ProxyPilot session files.
package archive

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hakanor/proxypilot/internal/redact"
	"github.com/hakanor/proxypilot/internal/store"
	"github.com/hakanor/proxypilot/internal/version"
)

// MaxBody caps each exported body; larger bodies are cut and flagged in a comment.
const MaxBody = 10 << 20

// MaxFlows caps one export.
const MaxFlows = 20000

type HAR struct {
	Log Log `json:"log"`
}

type Log struct {
	Version string  `json:"version"`
	Creator Creator `json:"creator"`
	Pages   []any   `json:"pages"`
	Entries []Entry `json:"entries"`
	Comment string  `json:"comment,omitempty"`
}

type Creator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type NV struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Entry struct {
	StartedDateTime string   `json:"startedDateTime"`
	Time            float64  `json:"time"`
	Request         Request  `json:"request"`
	Response        Response `json:"response"`
	Cache           struct{} `json:"cache"`
	Timings         Timings  `json:"timings"`
	Comment         string   `json:"comment,omitempty"`
	// ProxyPilot extensions (underscore prefix is the HAR convention).
	ID     string            `json:"_id,omitempty"`
	Source string            `json:"_source,omitempty"`
	Client string            `json:"_client,omitempty"`
	Tags   map[string]string `json:"_tags,omitempty"`
	Error  string            `json:"_error,omitempty"`
}

type Request struct {
	Method      string    `json:"method"`
	URL         string    `json:"url"`
	HTTPVersion string    `json:"httpVersion"`
	Cookies     []NV      `json:"cookies"`
	Headers     []NV      `json:"headers"`
	QueryString []NV      `json:"queryString"`
	PostData    *PostData `json:"postData,omitempty"`
	HeadersSize int       `json:"headersSize"`
	BodySize    int64     `json:"bodySize"`
}

type PostData struct {
	MimeType string `json:"mimeType"`
	Text     string `json:"text"`
	Encoding string `json:"encoding,omitempty"` // non-standard but widely read: base64
}

type Response struct {
	Status      int     `json:"status"`
	StatusText  string  `json:"statusText"`
	HTTPVersion string  `json:"httpVersion"`
	Cookies     []NV    `json:"cookies"`
	Headers     []NV    `json:"headers"`
	Content     Content `json:"content"`
	RedirectURL string  `json:"redirectURL"`
	HeadersSize int     `json:"headersSize"`
	BodySize    int64   `json:"bodySize"`
}

type Content struct {
	Size        int64  `json:"size"`
	Compression int64  `json:"compression,omitempty"`
	MimeType    string `json:"mimeType"`
	Text        string `json:"text,omitempty"`
	Encoding    string `json:"encoding,omitempty"`
}

type Timings struct {
	Blocked float64 `json:"blocked"`
	DNS     float64 `json:"dns"`
	Connect float64 `json:"connect"`
	SSL     float64 `json:"ssl"`
	Send    float64 `json:"send"`
	Wait    float64 `json:"wait"`
	Receive float64 `json:"receive"`
}

func nv(hs []store.Header) []NV {
	out := make([]NV, 0, len(hs))
	for _, h := range hs {
		out = append(out, NV{Name: h.Name, Value: h.Value})
	}
	return out
}

func cookies(hs []store.Header, response bool) []NV {
	out := []NV{}
	for _, h := range hs {
		switch {
		case !response && strings.EqualFold(h.Name, "Cookie"):
			for _, c := range strings.Split(h.Value, ";") {
				if k, v, ok := strings.Cut(strings.TrimSpace(c), "="); ok {
					out = append(out, NV{Name: k, Value: v})
				}
			}
		case response && strings.EqualFold(h.Name, "Set-Cookie"):
			first, _, _ := strings.Cut(h.Value, ";")
			if k, v, ok := strings.Cut(strings.TrimSpace(first), "="); ok {
				out = append(out, NV{Name: k, Value: v})
			}
		}
	}
	return out
}

func httpVersion(p string) string {
	if p == "" {
		return "HTTP/1.1"
	}
	return p
}

// body encodes a body as text or base64 (HAR content.encoding).
func encodeBody(b []byte) (text, encoding string) {
	if utf8.Valid(b) {
		return string(b), ""
	}
	return base64.StdEncoding.EncodeToString(b), "base64"
}

// ExportOptions select and mask what is exported.
type ExportOptions struct {
	Filter store.Filter
	// Redact masks secrets (headers, query, JSON/form bodies). Nil exports as recorded.
	Redact *redact.Redactor
	// Progress is called after each flow (done, total) for long exports.
	Progress func(done, total int)
}

// collect lists flow ids matching the filter, oldest first.
func collect(ctx context.Context, st *store.Store, f store.Filter) ([]string, error) {
	var ids []string
	cursor := ""
	for len(ids) < MaxFlows {
		p, err := st.List(ctx, f, 500, cursor)
		if err != nil {
			return nil, err
		}
		for _, fl := range p.Flows {
			ids = append(ids, fl.ID)
		}
		if p.NextCursor == "" || len(p.Flows) == 0 {
			break
		}
		cursor = p.NextCursor
	}
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 { // List is newest first
		ids[i], ids[j] = ids[j], ids[i]
	}
	return ids, nil
}

// ExportHAR writes the selected flows as HAR 1.2.
func ExportHAR(ctx context.Context, st *store.Store, w io.Writer, o ExportOptions) (int, error) {
	ids, err := collect(ctx, st, o.Filter)
	if err != nil {
		return 0, err
	}
	h := HAR{Log: Log{Version: "1.2", Creator: Creator{Name: "ProxyPilot", Version: version.Version}, Pages: []any{}, Entries: []Entry{}}}
	if o.Redact != nil {
		h.Log.Comment = "Secrets are masked (Authorization, cookies, tokens, passwords)."
	}
	for i, id := range ids {
		f, err := st.Get(ctx, id)
		if err != nil {
			continue
		}
		if f.State == store.StateTunneled {
			continue // no request inside an undecrypted tunnel
		}
		if o.Redact != nil {
			f = o.Redact.Flow(f)
		}
		h.Log.Entries = append(h.Log.Entries, entry(ctx, st, f, o.Redact))
		if o.Progress != nil {
			o.Progress(i+1, len(ids))
		}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return len(h.Log.Entries), enc.Encode(h)
}

func entry(ctx context.Context, st *store.Store, f *store.Flow, rd *redact.Redactor) Entry {
	e := Entry{
		StartedDateTime: f.CreatedAt.UTC().Format(time.RFC3339Nano), Time: f.Timing.Total,
		ID: f.ID, Source: f.Source, Client: f.ClientLabel, Tags: f.Tags, Error: f.ErrorKind,
	}
	q := []NV{}
	if u, err := url.Parse(f.URL); err == nil {
		for k, vs := range u.Query() {
			for _, v := range vs {
				q = append(q, NV{Name: k, Value: v})
			}
		}
	}
	e.Request = Request{
		Method: f.Method, URL: f.URL, HTTPVersion: httpVersion(f.Protocol), Cookies: cookies(f.ReqHeaders, false),
		Headers: nv(f.ReqHeaders), QueryString: q, HeadersSize: -1, BodySize: f.ReqSize,
	}
	if b, truncated, _, err := st.GetBody(ctx, f.ID, store.SideReq, MaxBody); err == nil && len(b) > 0 {
		if rd != nil {
			b = rd.Body(b, f.ReqContentType)
		}
		text, enc := encodeBody(b)
		e.Request.PostData = &PostData{MimeType: f.ReqContentType, Text: text, Encoding: enc}
		if truncated {
			e.Comment = "request body truncated"
		}
	}
	status := f.Status
	e.Response = Response{
		Status: status, StatusText: http.StatusText(status), HTTPVersion: httpVersion(f.Protocol),
		Cookies: cookies(f.RespHeaders, true), Headers: nv(f.RespHeaders), HeadersSize: -1, BodySize: f.BytesDown,
		Content: Content{Size: f.RespSize, MimeType: f.RespContentType},
	}
	for _, hd := range f.RespHeaders {
		if strings.EqualFold(hd.Name, "Location") {
			e.Response.RedirectURL = hd.Value
		}
	}
	if f.RespSize > 0 && f.BytesDown > 0 && f.RespSize > f.BytesDown {
		e.Response.Content.Compression = f.RespSize - f.BytesDown
	}
	if b, truncated, _, err := st.GetBody(ctx, f.ID, store.SideResp, MaxBody); err == nil && len(b) > 0 {
		if rd != nil {
			b = rd.Body(b, f.RespContentType)
		}
		e.Response.Content.Text, e.Response.Content.Encoding = encodeBody(b)
		if truncated {
			e.Comment = strings.TrimPrefix(e.Comment+"; response body truncated", "; ")
		}
	}
	t := f.Timing
	wait := t.TTFB - t.DNS - t.Connect - t.TLS
	if wait < 0 {
		wait = t.TTFB
	}
	recv := t.Total - t.TTFB
	if recv < 0 {
		recv = 0
	}
	e.Timings = Timings{Blocked: -1, DNS: orMinus(t.DNS), Connect: orMinus(t.Connect + t.TLS), SSL: orMinus(t.TLS), Send: 0, Wait: max(wait, 0), Receive: recv}
	return e
}

func orMinus(v float64) float64 {
	if v <= 0 {
		return -1
	}
	return v
}

// ImportResult summarizes an import.
type ImportResult struct {
	Session  string `json:"session"`
	Imported int    `json:"imported"`
	Skipped  int    `json:"skipped"`
}

// ImportHAR reads a HAR into a new session (source=import).
func ImportHAR(ctx context.Context, st *store.Store, r io.Reader) (ImportResult, error) {
	var h HAR
	dec := json.NewDecoder(io.LimitReader(r, 2<<30))
	if err := dec.Decode(&h); err != nil {
		return ImportResult{}, fmt.Errorf("not a HAR file: %w", err)
	}
	if len(h.Log.Entries) == 0 && h.Log.Version == "" {
		return ImportResult{}, fmt.Errorf("not a HAR file: no log.entries")
	}
	sess, err := st.AddSession(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	res := ImportResult{Session: sess.ID}
	for _, e := range h.Log.Entries {
		if err := importEntry(ctx, st, sess.ID, e); err != nil {
			res.Skipped++
			continue
		}
		res.Imported++
	}
	return res, nil
}

func headers(list []NV) []store.Header {
	out := make([]store.Header, 0, len(list))
	for _, h := range list {
		if strings.HasPrefix(h.Name, ":") { // HTTP/2 pseudo headers from Chrome
			continue
		}
		out = append(out, store.Header{Name: h.Name, Value: h.Value})
	}
	return out
}

func headerValue(hs []store.Header, name string) string {
	for _, h := range hs {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

func decodeText(text, encoding string) []byte {
	if encoding == "base64" {
		if b, err := base64.StdEncoding.DecodeString(text); err == nil {
			return b
		}
	}
	return []byte(text)
}

func importEntry(ctx context.Context, st *store.Store, session string, e Entry) error {
	u, err := url.Parse(e.Request.URL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("bad url %q", e.Request.URL)
	}
	started, err := time.Parse(time.RFC3339Nano, e.StartedDateTime)
	if err != nil {
		started = time.Now()
	}
	f := &store.Flow{
		SessionID: session, Source: store.SourceImport, ClientAddr: "import", ClientLabel: e.Client,
		Method: strings.ToUpper(e.Request.Method), Scheme: u.Scheme, Host: u.Hostname(), Path: u.Path, Query: u.RawQuery, URL: e.Request.URL,
		Status: e.Response.Status, Protocol: e.Request.HTTPVersion, State: store.StateCompleted,
		ReqHeaders: headers(e.Request.Headers), RespHeaders: headers(e.Response.Headers),
		BytesDown: max(e.Response.BodySize, 0), CreatedAt: started, ErrorKind: e.Error,
		Tags: map[string]string{"imported_from": "har"},
	}
	for k, v := range e.Tags {
		f.Tags[k] = v
	}
	if e.Source != "" {
		f.Tags["original_source"] = e.Source
	}
	f.Port, _ = parsePort(u)
	f.ReqContentType = headerValue(f.ReqHeaders, "Content-Type")
	f.RespContentType = e.Response.Content.MimeType
	if f.RespContentType == "" {
		f.RespContentType = headerValue(f.RespHeaders, "Content-Type")
	}
	if e.Response.Status == 0 {
		f.State = store.StateFailed
		if f.ErrorKind == "" {
			f.ErrorKind = "upstream_connect_error"
		}
	}
	t := e.Timings
	pos := func(v float64) float64 { return max(v, 0) }
	f.Timing = store.Timing{DNS: pos(t.DNS), Connect: pos(t.Connect - pos(t.SSL)), TLS: pos(t.SSL), Total: e.Time}
	f.Timing.TTFB = pos(t.Blocked) + pos(t.DNS) + pos(t.Connect) + pos(t.Send) + pos(t.Wait)
	if err := st.Insert(ctx, f); err != nil {
		return err
	}
	put := func(side string, b []byte) {
		if len(b) == 0 {
			return
		}
		info, err := st.PutBody(ctx, f.ID, side, "", int64(len(b)), func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil })
		if err == nil {
			if side == store.SideReq {
				f.ReqSize = info.DecodedSize
			} else {
				f.RespSize = info.DecodedSize
			}
		}
	}
	if e.Request.PostData != nil {
		put(store.SideReq, decodeText(e.Request.PostData.Text, e.Request.PostData.Encoding))
	}
	put(store.SideResp, decodeText(e.Response.Content.Text, e.Response.Content.Encoding))
	return st.Update(ctx, f)
}

func parsePort(u *url.URL) (int, error) {
	if p := u.Port(); p != "" {
		var n int
		_, err := fmt.Sscanf(p, "%d", &n)
		return n, err
	}
	if u.Scheme == "https" {
		return 443, nil
	}
	return 80, nil
}
