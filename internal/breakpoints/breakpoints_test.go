package breakpoints_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hakanor/proxypilot/internal/breakpoints"
	"github.com/hakanor/proxypilot/internal/proxy"
	"github.com/hakanor/proxypilot/internal/rules"
)

type env struct {
	bp      *breakpoints.Manager
	rules   *rules.Engine
	client  *http.Client
	up      *httptest.Server
	seenHdr chan string
}

func setup(t *testing.T) *env {
	t.Helper()
	e := &env{bp: breakpoints.New(nil), seenHdr: make(chan string, 10)}
	e.rules, _ = rules.New("", nil, nil)
	e.rules.SetPauser(e.bp)
	e.up = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.seenHdr <- r.Header.Get("X-Test")
		if r.URL.Path == "/gz" {
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			zw.Write([]byte(`{"ok":true}`))
			zw.Close()
			return
		}
		b, _ := io.ReadAll(r.Body)
		io.WriteString(w, "upstream:"+string(b))
	}))
	t.Cleanup(e.up.Close)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	srv := proxy.New(proxy.Options{Hooks: e.rules})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Serve(ctx, ln)
	pu, _ := url.Parse("http://" + ln.Addr().String())
	e.client = &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu), DisableCompression: true}, Timeout: 10 * time.Second}
	return e
}

func (e *env) breakpoint(t *testing.T, pattern string, phase rules.Phase, spec string) {
	t.Helper()
	if _, err := e.rules.Add(rules.Rule{Type: rules.TypeBreakpoint, Enabled: true, Match: rules.Matcher{URL: e.up.URL + pattern}, Phase: phase, Spec: json.RawMessage(spec)}, 0); err != nil {
		t.Fatal(err)
	}
}

type result struct {
	status int
	body   string
	hdr    http.Header
	err    error
}

func (e *env) async(method, path, body string) chan result {
	ch := make(chan result, 1)
	go func() {
		req, _ := http.NewRequest(method, e.up.URL+path, strings.NewReader(body))
		req.Header.Set("X-Test", "original")
		resp, err := e.client.Do(req)
		if err != nil {
			ch <- result{err: err}
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		ch <- result{status: resp.StatusCode, body: string(b), hdr: resp.Header}
	}()
	return ch
}

func (e *env) waitPaused(t *testing.T) *breakpoints.Paused {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := e.bp.Wait(ctx, nil)
	if err != nil {
		t.Fatal("nothing paused")
	}
	return p
}

func TestModifyRequestHeaderAndBody(t *testing.T) {
	e := setup(t)
	e.breakpoint(t, "/pay", rules.PhaseRequest, "")
	res := e.async("POST", "/pay", "amount=1")
	p := e.waitPaused(t)
	if p.Phase != "request" || p.Request.Method != "POST" || p.Request.Body != "amount=1" {
		t.Fatalf("paused: %+v", p)
	}
	edited := p.Request
	for i, h := range edited.Headers {
		if h.Name == "X-Test" {
			edited.Headers[i].Value = "edited"
		}
	}
	edited.Body = "amount=999"
	if err := e.bp.Resume(p.ID, breakpoints.Resolution{Action: "modify", Request: &edited}); err != nil {
		t.Fatal(err)
	}
	r := <-res
	if r.err != nil || r.body != "upstream:amount=999" {
		t.Fatalf("result: %+v", r)
	}
	if got := <-e.seenHdr; got != "edited" {
		t.Fatalf("server saw X-Test=%q", got)
	}
	// A second decision is rejected.
	if err := e.bp.Resume(p.ID, breakpoints.Resolution{Action: "continue"}); !errors.Is(err, breakpoints.ErrAlreadyResolved) {
		t.Fatalf("second resume: %v", err)
	}
}

func TestModifyResponseStatusAndOtherTrafficFlows(t *testing.T) {
	e := setup(t)
	e.breakpoint(t, "/gz", rules.PhaseResponse, "")
	res := e.async("GET", "/gz", "")
	p := e.waitPaused(t)
	if p.Phase != "response" || p.Response.Body != `{"ok":true}` || p.Response.Encoding != "gzip" {
		t.Fatalf("paused response (should be decoded): %+v", p.Response)
	}
	// Unrelated traffic is not blocked while the breakpoint waits.
	if r := <-e.async("GET", "/other", ""); r.err != nil || r.body != "upstream:" {
		t.Fatalf("other traffic: %+v", r)
	}
	<-e.seenHdr
	<-e.seenHdr
	edited := *p.Response
	edited.Status = 500
	edited.Body = `{"ok":false}`
	if err := e.bp.Resume(p.ID, breakpoints.Resolution{Action: "modify", Response: &edited}); err != nil {
		t.Fatal(err)
	}
	r := <-res
	if r.status != 500 || r.body != `{"ok":false}` || r.hdr.Get("Content-Encoding") != "" {
		t.Fatalf("client got %d %q %v", r.status, r.body, r.hdr)
	}
}

func TestTimeoutContinuesAndAbort(t *testing.T) {
	e := setup(t)
	e.breakpoint(t, "/slow", rules.PhaseRequest, `{"timeout":"300ms"}`)
	start := time.Now()
	r := <-e.async("GET", "/slow", "")
	if r.err != nil || r.body != "upstream:" || time.Since(start) > 3*time.Second {
		t.Fatalf("timeout did not continue: %+v after %v", r, time.Since(start))
	}
	<-e.seenHdr
	if e.bp.Count() != 0 {
		t.Fatal("timed out breakpoint still pending")
	}

	e.breakpoint(t, "/abort", rules.PhaseRequest, "")
	res := e.async("GET", "/abort", "")
	p := e.waitPaused(t)
	e.bp.Resume(p.ID, breakpoints.Resolution{Action: "abort"})
	if r := <-res; r.status != 502 || !strings.Contains(r.body, "aborted at a breakpoint") {
		t.Fatalf("request-phase abort: %+v", r)
	}
	if err := e.bp.Resume("bp_nope", breakpoints.Resolution{Action: "continue"}); !errors.Is(err, breakpoints.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if err := e.bp.Resume(p.ID, breakpoints.Resolution{Action: "explode"}); !errors.Is(err, breakpoints.ErrInvalid) {
		t.Fatalf("bad action: %v", err)
	}
}

func TestReleaseAllOnShutdown(t *testing.T) {
	e := setup(t)
	e.breakpoint(t, "/hold", rules.PhaseRequest, `{"timeout":"10m"}`)
	res := e.async("GET", "/hold", "")
	e.waitPaused(t)
	if n := e.bp.ReleaseAll(); n != 1 {
		t.Fatalf("released %d", n)
	}
	if r := <-res; r.err != nil || r.body != "upstream:" {
		t.Fatalf("released flow: %+v", r)
	}
}

func TestExtendAndTakeOver(t *testing.T) {
	m := breakpoints.New(nil)
	done := make(chan breakpoints.Resolution, 1)
	go func() {
		done <- m.Pause(context.Background(), &breakpoints.Paused{Phase: "request", Owner: "agent:claude"}, 200*time.Millisecond)
	}()
	p, _ := m.Wait(context.Background(), nil)
	if _, err := m.Extend(p.ID, time.Second); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.TakeOver(p.ID, ""); got.Owner != "user" {
		t.Fatalf("owner after take over: %s", got.Owner)
	}
	select {
	case <-done:
		t.Fatal("extended breakpoint timed out at the original deadline")
	case <-time.After(500 * time.Millisecond):
	}
	if r := <-done; !r.TimedOut || r.Action != "continue" {
		t.Fatalf("result: %+v", r)
	}
}
