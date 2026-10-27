package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/hakanor/proxypilot/internal/ca"
	"github.com/hakanor/proxypilot/internal/events"
	"github.com/hakanor/proxypilot/internal/proxy"
	"github.com/hakanor/proxypilot/internal/store"
)

const testToken = "test-token"

type env struct {
	srv   *Server
	base  string
	store *store.Store
	bus   *events.Bus
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	root, err := ca.LoadOrCreate(dir + "/ca")
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	s := New(Deps{
		Store: st, Bus: bus, CA: root, Token: testToken,
		Policy: proxy.NewSSLPolicy(proxy.ModeAll, nil, nil),
		Status: func() StatusInfo { return StatusInfo{Running: true} },
	}, Options{Addr: "127.0.0.1:0"})
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.Serve(ctx)
	return &env{srv: s, base: "http://" + s.Addr().String(), store: st, bus: bus}
}

func (e *env) do(t *testing.T, method, path string, body io.Reader, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, e.base+path, body)
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

var auth = map[string]string{"Authorization": "Bearer " + testToken}

func decode(t *testing.T, r *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func TestSecurity(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		name   string
		method string
		path   string
		hdr    map[string]string
		want   int
	}{
		{"no token", "GET", "/api/v1/status", nil, 401},
		{"wrong token", "GET", "/api/v1/status", map[string]string{"Authorization": "Bearer nope"}, 401},
		{"ok", "GET", "/api/v1/status", auth, 200},
		{"dns rebinding", "GET", "/api/v1/status", map[string]string{"Authorization": "Bearer " + testToken, "Host": "evil.com"}, 421},
		{"foreign origin", "DELETE", "/api/v1/flows", map[string]string{"Authorization": "Bearer " + testToken, "Origin": "https://evil.com"}, 403},
		{"cookie without origin on write", "DELETE", "/api/v1/flows", map[string]string{"Cookie": cookieName + "=" + testToken}, 403},
		{"cookie read", "GET", "/api/v1/status", map[string]string{"Cookie": cookieName + "=" + testToken}, 200},
		{"cors preflight", "OPTIONS", "/api/v1/flows", map[string]string{"Origin": "https://evil.com"}, 403},
		{"rules unavailable", "GET", "/api/v1/rules", auth, 501},
		{"unknown", "GET", "/api/v1/nope", auth, 404},
	}
	for _, c := range cases {
		if got := e.do(t, c.method, c.path, nil, c.hdr).StatusCode; got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}

	// Same-origin cookie write is allowed.
	origin := "http://" + e.srv.Addr().String()
	if got := e.do(t, "DELETE", "/api/v1/flows", nil, map[string]string{"Cookie": cookieName + "=" + testToken, "Origin": origin}).StatusCode; got != 200 {
		t.Errorf("same-origin cookie write: %d", got)
	}

	// Token exchange sets an HttpOnly cookie and strips the token from the URL.
	r := e.do(t, "GET", "/?token="+testToken, nil, nil)
	if r.StatusCode != http.StatusFound || r.Header.Get("Location") != "/" {
		t.Fatalf("token exchange: %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	if sc := r.Header.Get("Set-Cookie"); !strings.Contains(sc, "HttpOnly") || !strings.Contains(sc, "SameSite=Strict") {
		t.Fatalf("cookie flags: %s", sc)
	}
	if got := e.do(t, "GET", "/?token=bad", nil, nil).StatusCode; got != 401 {
		t.Errorf("bad token exchange: %d", got)
	}
}

func TestRemoteBindRejected(t *testing.T) {
	s := New(Deps{}, Options{Addr: "0.0.0.0:0"})
	if err := s.Listen(); err != ErrRemoteBind {
		t.Fatalf("got %v", err)
	}
}

func TestListLimits(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 520; i++ {
		e.store.Insert(context.Background(), &store.Flow{Method: "GET", Host: "a.test", URL: "http://a.test/"})
	}
	var p flowPage
	decode(t, e.do(t, "GET", "/api/v1/flows", nil, auth), &p)
	if len(p.Flows) != 50 || !p.HasMore || p.NextCursor == "" {
		t.Fatalf("default page: %d has_more=%v", len(p.Flows), p.HasMore)
	}
	decode(t, e.do(t, "GET", "/api/v1/flows?limit=100000", nil, auth), &p)
	if len(p.Flows) != 500 || p.Limit != 500 {
		t.Fatalf("max page: %d limit=%d", len(p.Flows), p.Limit)
	}
	if got := e.do(t, "GET", "/api/v1/flows?status=abc", nil, auth).StatusCode; got != 400 {
		t.Fatalf("bad filter: %d", got)
	}
}

func TestFlowDetailBodyAndRedact(t *testing.T) {
	e := newEnv(t)
	f := &store.Flow{Method: "POST", Host: "a.test", URL: "http://a.test/login?api_key=abc&x=1", Query: "api_key=abc&x=1",
		RespContentType: "application/json", ReqHeaders: []store.Header{{Name: "Authorization", Value: "Bearer s3cret"}, {Name: "Accept", Value: "*/*"}}}
	e.store.Insert(context.Background(), f)
	body := []byte(`{"password":"hunter2","user":"hakan"}`)
	e.store.PutBody(context.Background(), f.ID, store.SideResp, "", int64(len(body)), func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(string(body))), nil
	})

	var got map[string]any
	decode(t, e.do(t, "GET", "/api/v1/flows/"+f.ID+"?redact=true", nil, auth), &got)
	hs := got["req_headers"].([]any)
	if !strings.HasPrefix(hs[0].(map[string]any)["value"].(string), "Bearer [redacted") || hs[1].(map[string]any)["value"] != "*/*" {
		t.Fatalf("headers not redacted: %v", hs)
	}
	if u := got["url"].(string); strings.Contains(u, "api_key=abc&") || !strings.HasSuffix(u, "&x=1") {
		t.Fatalf("query not redacted: %v", got["url"])
	}

	var b BodyResponse
	decode(t, e.do(t, "GET", "/api/v1/flows/"+f.ID+"/body/resp?max=10", nil, auth), &b)
	if b.Returned != 10 || !b.Truncated || b.Size != int64(len(body)) || b.Encoding != "utf8" {
		t.Fatalf("truncated body: %+v", b)
	}
	decode(t, e.do(t, "GET", "/api/v1/flows/"+f.ID+"/body/resp?redact=true", nil, auth), &b)
	if strings.Contains(b.Data, "hunter2") || !strings.Contains(b.Data, "hakan") {
		t.Fatalf("body redaction: %s", b.Data)
	}
	if got := e.do(t, "GET", "/api/v1/flows/nope", nil, auth).StatusCode; got != 404 {
		t.Fatalf("missing flow: %d", got)
	}
	var er map[string]map[string]string
	decode(t, e.do(t, "GET", "/api/v1/flows/nope", nil, auth), &er)
	if er["error"]["code"] != "not_found" {
		t.Fatalf("error format: %v", er)
	}
}

func TestSSLPolicyEndpoint(t *testing.T) {
	e := newEnv(t)
	r := e.do(t, "PUT", "/api/v1/ssl-policy", strings.NewReader(`{"mode":"allowlist","allowlist":["*.invio.com"],"denylist":["*.apple.com"]}`), auth)
	if r.StatusCode != 200 {
		t.Fatalf("put: %d", r.StatusCode)
	}
	var p proxy.PolicySnapshot
	decode(t, e.do(t, "GET", "/api/v1/ssl-policy", nil, auth), &p)
	if p.Mode != proxy.ModeAllowlist || len(p.Allowlist) != 1 {
		t.Fatalf("policy: %+v", p)
	}
	if got := e.do(t, "PUT", "/api/v1/ssl-policy", strings.NewReader(`{"mode":"sometimes"}`), auth).StatusCode; got != 400 {
		t.Fatalf("bad mode: %d", got)
	}
}

func dialWS(t *testing.T, e *env) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws://"+e.srv.Addr().String()+"/api/v1/stream", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + testToken}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func readMsg(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

func TestStreamFilteredSubscription(t *testing.T) {
	e := newEnv(t)
	c := dialWS(t, e)
	c.Write(context.Background(), websocket.MessageText, []byte(`{"type":"subscribe","events":["flow.*"],"filter":{"host":"*.example.com","status":">=400"}}`))
	if m := readMsg(t, c); m["type"] != "subscribed" {
		t.Fatalf("got %v", m)
	}
	e.bus.Publish(events.FlowCompleted, &store.Flow{ID: "ok", Host: "api.example.com", Status: 200})
	e.bus.Publish(events.FlowCompleted, &store.Flow{ID: "other", Host: "api.other.com", Status: 500})
	e.bus.Publish(events.RuleChanged, map[string]string{"id": "r1"})
	e.bus.Publish(events.FlowFailed, &store.Flow{ID: "match", Host: "api.example.com", Status: 503})

	m := readMsg(t, c)
	if m["type"] != "batch" {
		t.Fatalf("got %v", m)
	}
	evs := m["events"].([]any)
	if len(evs) != 1 {
		t.Fatalf("want only the matching event, got %v", evs)
	}
	if id := evs[0].(map[string]any)["data"].(map[string]any)["id"]; id != "match" {
		t.Fatalf("wrong event %v", id)
	}
}

func TestStreamSlowClientDoesNotBlock(t *testing.T) {
	e := newEnv(t)
	c := dialWS(t, e)
	c.Write(context.Background(), websocket.MessageText, []byte(`{"type":"subscribe","events":["*"]}`))
	readMsg(t, c) // subscribed; then never read again

	done := make(chan struct{})
	go func() {
		big := strings.Repeat("x", 2048)
		for i := 0; i < 20000; i++ {
			e.bus.Publish(events.FlowCompleted, &store.Flow{ID: big})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publishing blocked by a slow stream client")
	}
}

func TestNDJSONStream(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest("GET", e.base+"/api/v1/stream/ndjson?events=flow.*&status=5xx", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("content type %s", resp.Header.Get("Content-Type"))
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		e.bus.Publish(events.FlowCompleted, &store.Flow{ID: "a", Status: 200})
		e.bus.Publish(events.FlowCompleted, &store.Flow{ID: "b", Status: 502})
	}()
	sc := bufio.NewScanner(resp.Body)
	if !sc.Scan() {
		t.Fatal("no line")
	}
	var ev wireEvent
	json.Unmarshal(sc.Bytes(), &ev)
	if ev.Type != events.FlowCompleted || ev.Data.(map[string]any)["id"] != "b" {
		t.Fatalf("line: %s", sc.Text())
	}
}

func TestSummaryRedactsSlowestURL(t *testing.T) {
	e := newEnv(t)
	e.store.Insert(context.Background(), &store.Flow{Method: "GET", Host: "a.test", URL: "https://a.test/x?api_key=abcdefghijklmnopqrstuvwxyz",
		State: store.StateCompleted, Timing: store.Timing{Total: 900}})
	var s store.Summary
	decode(t, e.do(t, "GET", "/api/v1/summary?redact=true", nil, auth), &s)
	if s.Slowest == nil || strings.Contains(s.Slowest.URL, "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("slowest URL leaked: %+v", s.Slowest)
	}
}

func TestNDJSONRedacts(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest("GET", e.base+"/api/v1/stream/ndjson?redact=true", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	go func() {
		time.Sleep(50 * time.Millisecond)
		e.bus.Publish(events.FlowCompleted, &store.Flow{ID: "a", URL: "https://a/x?token=abcdefghijklmnopqrstuvwxyz"})
	}()
	sc := bufio.NewScanner(resp.Body)
	sc.Scan()
	if strings.Contains(sc.Text(), "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("stream leaked: %s", sc.Text())
	}
}
