// Package repeat re-sends recorded requests (optionally edited) and sends composed
// ones, through the same proxy pipeline as live traffic. Results are recorded with
// source=composer.
package repeat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hakanor/proxypilot/internal/proxy"
	"github.com/hakanor/proxypilot/internal/store"
)

// Limits keep a repeat from turning into an accidental load test.
const (
	MaxCount       = 50
	MaxConcurrency = 10
	resultBody     = 64 << 10
)

var ErrLimit = fmt.Errorf("count must be 1..%d and concurrency 1..%d", MaxCount, MaxConcurrency)

// Request is a composed request (also the editable form of a recorded one).
type Request struct {
	Method  string         `json:"method"`
	URL     string         `json:"url"`
	Headers []store.Header `json:"headers"`
	Body    string         `json:"body,omitempty"`
}

// Edits change a recorded request before it is repeated.
type Edits struct {
	Method        string            `json:"method,omitempty"`
	URL           string            `json:"url,omitempty"`
	SetHeaders    map[string]string `json:"set_headers,omitempty"`
	RemoveHeaders []string          `json:"remove_headers,omitempty"`
	SetQuery      map[string]string `json:"set_query,omitempty"`
	Body          *string           `json:"body,omitempty"`
}

type Options struct {
	Count       int  `json:"count"`
	Concurrency int  `json:"concurrency"`
	BypassRules bool `json:"bypass_rules"`
}

// Result is one sent request.
type Result struct {
	FlowID  string  `json:"flow_id"`
	Status  int     `json:"status,omitempty"`
	Ms      float64 `json:"ms"`
	Aborted bool    `json:"aborted,omitempty"`
	Error   string  `json:"error,omitempty"`
}

// Summary aggregates a batch.
type Summary struct {
	Results  []Result       `json:"results"`
	ByStatus map[string]int `json:"by_status"`
	AvgMs    float64        `json:"avg_ms"`
}

type Repeater struct {
	Proxy *proxy.Server
	Store *store.Store
	// Hooks is the full pipeline; Bypass skips rules (recording only).
	Hooks, Bypass proxy.Hooks
}

// hop-by-hop and framing headers that must not be replayed verbatim.
var skipHeaders = map[string]bool{"Content-Length": true, "Transfer-Encoding": true, "Connection": true, "Proxy-Connection": true, "Keep-Alive": true, "Accept-Encoding": false}

// FromFlow turns a recorded flow into an editable request. Bodies are stored decoded,
// so Content-Encoding is dropped.
func (r *Repeater) FromFlow(ctx context.Context, id string) (Request, error) {
	f, err := r.Store.Get(ctx, id)
	if err != nil {
		return Request{}, err
	}
	if f.State == store.StateTunneled || f.Method == http.MethodConnect {
		return Request{}, errors.New("this flow is an HTTPS tunnel (not decrypted), there is no request to repeat")
	}
	req := Request{Method: f.Method, URL: f.URL}
	for _, h := range f.ReqHeaders {
		ck := http.CanonicalHeaderKey(h.Name)
		if skipHeaders[ck] || ck == "Content-Encoding" {
			continue
		}
		req.Headers = append(req.Headers, store.Header{Name: h.Name, Value: h.Value})
	}
	if rc, _, err := r.Store.OpenBody(ctx, id, store.SideReq); err == nil {
		b, _ := io.ReadAll(io.LimitReader(rc, 16<<20))
		rc.Close()
		req.Body = string(b)
	}
	return req, nil
}

// Apply returns req with edits.
func (e Edits) Apply(req Request) (Request, error) {
	if e.Method != "" {
		req.Method = strings.ToUpper(e.Method)
	}
	if e.URL != "" {
		req.URL = e.URL
	}
	if len(e.SetQuery) > 0 {
		u, err := url.Parse(req.URL)
		if err != nil {
			return req, err
		}
		q := u.Query()
		for k, v := range e.SetQuery {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		req.URL = u.String()
	}
	drop := map[string]bool{}
	for _, h := range e.RemoveHeaders {
		drop[http.CanonicalHeaderKey(h)] = true
	}
	for k := range e.SetHeaders {
		drop[http.CanonicalHeaderKey(k)] = true
	}
	var hs []store.Header
	for _, h := range req.Headers {
		if !drop[http.CanonicalHeaderKey(h.Name)] {
			hs = append(hs, h)
		}
	}
	for k, v := range e.SetHeaders {
		hs = append(hs, store.Header{Name: k, Value: v})
	}
	req.Headers = hs
	if e.Body != nil {
		req.Body = *e.Body
	}
	return req, nil
}

func (req Request) build() (*http.Request, error) {
	m := strings.ToUpper(strings.TrimSpace(req.Method))
	if m == "" {
		m = http.MethodGet
	}
	u, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || !u.IsAbs() || u.Host == "" {
		return nil, fmt.Errorf("invalid URL %q (needs http:// or https://)", req.URL)
	}
	var body io.Reader = http.NoBody
	if req.Body != "" {
		body = strings.NewReader(req.Body)
	}
	hr, err := http.NewRequest(m, u.String(), body)
	if err != nil {
		return nil, err
	}
	for _, h := range req.Headers {
		if h.Name == "" || skipHeaders[http.CanonicalHeaderKey(h.Name)] {
			continue
		}
		if strings.EqualFold(h.Name, "Host") {
			hr.Host = h.Value
			continue
		}
		hr.Header.Add(h.Name, h.Value)
	}
	if req.Body != "" {
		hr.ContentLength = int64(len(req.Body))
		hr.Header.Set("Content-Length", strconv.Itoa(len(req.Body)))
	}
	return hr, nil
}

// Send runs req count times with the given concurrency.
func (r *Repeater) Send(ctx context.Context, req Request, o Options, tags map[string]string) (Summary, error) {
	if o.Count == 0 {
		o.Count = 1
	}
	if o.Concurrency == 0 {
		o.Concurrency = 1
	}
	if o.Count < 1 || o.Count > MaxCount || o.Concurrency < 1 || o.Concurrency > MaxConcurrency {
		return Summary{}, ErrLimit
	}
	if _, err := req.build(); err != nil {
		return Summary{}, err
	}
	h := r.Hooks
	if o.BypassRules {
		h = r.Bypass
	}
	if tags == nil {
		tags = map[string]string{}
	}
	tags["source"] = store.SourceComposer
	results := make([]Result, o.Count)
	sem := make(chan struct{}, o.Concurrency)
	var wg sync.WaitGroup
	for i := 0; i < o.Count; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			hr, _ := req.build() // fresh body per send
			start := time.Now()
			res, err := r.Proxy.Send(ctx, h, hr, tags, resultBody)
			out := Result{FlowID: res.FlowID, Status: res.Status, Aborted: res.Aborted, Ms: float64(time.Since(start).Microseconds()) / 1000}
			if err != nil {
				out.Error = err.Error()
			}
			results[i] = out
		}(i)
	}
	wg.Wait()
	sum := Summary{Results: results, ByStatus: map[string]int{}}
	for _, x := range results {
		key := "error"
		if x.Status > 0 {
			key = strconv.Itoa(x.Status/100) + "xx"
		}
		sum.ByStatus[key]++
		sum.AvgMs += x.Ms
	}
	sum.AvgMs /= float64(len(results))
	return sum, nil
}
