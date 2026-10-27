package rules

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hakanor/proxypilot/internal/events"
	"github.com/hakanor/proxypilot/internal/proxy"
)

func spec(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func body(s string) json.RawMessage { return spec(map[string]any{"body": s}) }

func flowFor(t *testing.T, method, rawURL string, hdr map[string]string) *proxy.Flow {
	t.Helper()
	req := httptest.NewRequest(method, rawURL, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	u, _ := url.Parse(rawURL)
	f := &proxy.Flow{Request: req, Scheme: u.Scheme, Host: u.Host}
	return f
}

func ids(cs []*compiled) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func TestMatching(t *testing.T) {
	e, _ := New("", nil, nil)
	add := func(name string, m Matcher, prio int) {
		t.Helper()
		if _, err := e.Add(Rule{Name: name, Type: TypeMapLocal, Enabled: true, Match: m, Spec: body("x"), Priority: prio}, 0); err != nil {
			t.Fatal(err)
		}
	}
	add("users wildcard", Matcher{URL: "https://api.x.com/users/*"}, 0)
	add("any host feed", Matcher{URL: "*/v2/feed*", Methods: []string{"get"}}, 0)
	add("regex orders", Matcher{URL: `^https://api\.x\.com/orders/\d+$`, Regex: true}, 5)
	add("header json", Matcher{URL: "api.x.com", Headers: map[string]string{"Accept": "application/*"}}, 1)
	add("high prio users", Matcher{URL: "https://api.x.com/users/*"}, 10)

	cases := []struct {
		method, url string
		hdr         map[string]string
		want        string
	}{
		{"GET", "https://api.x.com/users/42", nil, "high prio users,users wildcard"},
		{"GET", "https://API.X.COM:443/users/42", nil, "high prio users,users wildcard"},
		{"GET", "http://api.x.com/users/42", nil, ""}, // scheme is part of the pattern
		{"GET", "https://cdn.y.com/v2/feed?page=2", nil, "any host feed"},
		{"POST", "https://cdn.y.com/v2/feed", nil, ""}, // method filter
		{"GET", "https://api.x.com/orders/77", nil, "regex orders"},
		{"GET", "https://api.x.com/orders/77/items", nil, ""},
		{"GET", "https://api.x.com/other", map[string]string{"Accept": "application/json"}, "header json"},
		{"GET", "https://api.x.com/other", map[string]string{"Accept": "text/html"}, ""},
		{"GET", "https://api.x.com/orders/1", map[string]string{"Accept": "application/json"}, "regex orders,header json"},
	}
	for _, c := range cases {
		got := strings.Join(ids(e.Match(flowFor(t, c.method, c.url, c.hdr))), ",")
		if got != c.want {
			t.Errorf("%s %s %v: got %q, want %q", c.method, c.url, c.hdr, got, c.want)
		}
	}
}

func TestValidation(t *testing.T) {
	e, _ := New("", nil, nil)
	bad := []Rule{
		{Type: "map_local"}, // no source
		{Type: "map_local", Spec: spec(map[string]any{"body": "x", "file": "y"})},
		{Type: "rewrite", Spec: body("x")},
		{Type: "nope"},
		{Type: "map_local", Spec: body("x"), Owner: "agent:"},
		{Type: "map_local", Spec: body("x"), Match: Matcher{URL: "(", Regex: true}},
		{Type: "map_local", Spec: spec(map[string]any{"body": "x", "mode": "half"})},
	}
	for i, r := range bad {
		if _, err := e.Add(r, 0); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: want ErrInvalid, got %v", i, err)
		}
	}
}

// proxyWith starts a proxy whose hooks are the engine and returns a client using it.
func proxyWith(t *testing.T, e *Engine) *http.Client {
	t.Helper()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	srv := proxy.New(proxy.Options{Hooks: e})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Serve(ctx, ln)
	pu, _ := url.Parse("http://" + ln.Addr().String())
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
}

func get(t *testing.T, c *http.Client, u string) (int, string, http.Header) {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestMapLocalThroughProxy(t *testing.T) {
	var upstreamHits atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("X-Upstream", "yes")
		w.WriteHeader(201)
		io.WriteString(w, "from server")
	}))
	defer up.Close()
	dir := t.TempDir()
	file := filepath.Join(dir, "mock.json")
	os.WriteFile(file, []byte(`{"v":1}`), 0o644)
	os.MkdirAll(filepath.Join(dir, "site", "js"), 0o755)
	os.WriteFile(filepath.Join(dir, "site", "js", "app.js"), []byte("console.log(1)"), 0o644)
	os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("nope"), 0o644)

	e, _ := New("", nil, nil)
	c := proxyWith(t, e)
	mustAdd := func(r Rule) {
		t.Helper()
		r.Enabled = true
		if _, err := e.Add(r, 0); err != nil {
			t.Fatal(err)
		}
	}
	mustAdd(Rule{Type: TypeMapLocal, Match: Matcher{URL: up.URL + "/feed"}, Spec: spec(map[string]any{"file": file, "status": 203, "headers": map[string]string{"X-Mock": "1"}})})
	mustAdd(Rule{Type: TypeMapLocal, Match: Matcher{URL: up.URL + "/static/*"}, Spec: spec(map[string]any{"dir": filepath.Join(dir, "site")})})
	mustAdd(Rule{Type: TypeMapLocal, Match: Matcher{URL: up.URL + "/body-only"}, Spec: spec(map[string]any{"body": "patched", "mode": "body_only"})})

	// Full mode never reaches the server; file content, status, headers and content type apply.
	st, b, h := get(t, c, up.URL+"/feed")
	if st != 203 || b != `{"v":1}` || h.Get("X-Mock") != "1" || h.Get("Content-Type") != "application/json" || upstreamHits.Load() != 0 {
		t.Fatalf("map local: %d %q %v hits=%d", st, b, h, upstreamHits.Load())
	}
	// The file is read on every request.
	os.WriteFile(file, []byte(`{"v":2}`), 0o644)
	if _, b, _ := get(t, c, up.URL+"/feed"); b != `{"v":2}` {
		t.Fatalf("file change not picked up: %q", b)
	}
	// Folder mapping, and no escaping the folder.
	if st, b, h := get(t, c, up.URL+"/static/js/app.js?v=3"); st != 200 || b != "console.log(1)" || !strings.Contains(h.Get("Content-Type"), "javascript") {
		t.Fatalf("dir: %d %q %v", st, b, h)
	}
	if st, b, _ := get(t, c, up.URL+"/static/..%2Fsecret.txt"); st != 404 || strings.Contains(b, "nope") {
		t.Fatalf("traversal: %d %q", st, b)
	}
	// body_only goes upstream and keeps its status and headers.
	st, b, h = get(t, c, up.URL+"/body-only")
	if st != 201 || b != "patched" || h.Get("X-Upstream") != "yes" || upstreamHits.Load() != 1 {
		t.Fatalf("body_only: %d %q %v hits=%d", st, b, h, upstreamHits.Load())
	}
	// A missing file explains itself.
	os.Remove(file)
	if st, b, _ := get(t, c, up.URL+"/feed"); st != 404 || !strings.Contains(b, "Map Local") {
		t.Fatalf("missing file: %d %q", st, b)
	}
	// Disabled rules are skipped; hits are counted.
	for _, r := range e.List() {
		if r.Match.URL == up.URL+"/feed" {
			if r.Hits != 3 {
				t.Errorf("hits = %d, want 3", r.Hits)
			}
			off := false
			e.Toggle(r.ID, &off)
		}
	}
	if _, b, _ := get(t, c, up.URL+"/feed"); b != "from server" {
		t.Fatalf("disabled rule still applied: %q", b)
	}
}

func TestTTLPersistenceAndOwners(t *testing.T) {
	dir := t.TempDir()
	bus := events.NewBus()
	sub := bus.Subscribe(nil, 64)
	defer sub.Cancel()
	e, _ := New(dir, bus, nil)
	now := time.Now()
	e.now = func() time.Time { return now }

	user, _ := e.Add(Rule{Name: "mine", Type: TypeMapLocal, Enabled: true, Spec: body("a")}, 0)
	agent, _ := e.Add(Rule{Name: "agent", Type: TypeMapLocal, Enabled: true, Owner: "agent:claude", Spec: body("b")}, 30*time.Second)
	e.Add(Rule{Name: "agent2", Type: TypeMapLocal, Enabled: true, Owner: "agent:cursor", Spec: body("c")}, 0)
	if e.Active() != 3 {
		t.Fatalf("active = %d", e.Active())
	}

	// Only user rules are persisted.
	e2, err := New(dir, nil, nil)
	if err != nil || len(e2.List()) != 1 || e2.List()[0].ID != user.ID {
		t.Fatalf("reload: %v %+v", err, e2.List())
	}

	now = now.Add(31 * time.Second)
	if n := e.Expire(); n != 1 {
		t.Fatalf("expired %d", n)
	}
	if _, err := e.Get(agent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired rule still there")
	}
	sawExpired := false
	for len(sub.C) > 0 {
		ev := <-sub.C
		if m, ok := ev.Data.(map[string]any); ok && ev.Type == events.RuleChanged && m["action"] == "expired" {
			sawExpired = true
		}
	}
	if !sawExpired {
		t.Fatal("no rule.changed expired event")
	}
	if n, _ := e.DeleteOwner("agent:*"); n != 1 || len(e.List()) != 1 {
		t.Fatalf("DeleteOwner agent:* removed %d, left %d", n, len(e.List()))
	}
}

func TestNoRulesIsCheap(t *testing.T) {
	e, _ := New("", nil, nil)
	f := flowFor(t, "GET", "https://x.com/", nil)
	if d := e.OnRequest(f); d.Action != proxy.Continue {
		t.Fatal("unexpected decision")
	}
	if allocs := testing.AllocsPerRun(100, func() { e.OnRequest(f) }); allocs != 0 {
		t.Fatalf("OnRequest with no rules allocates %v", allocs)
	}
}

func TestCDPPatterns(t *testing.T) {
	e, _ := New("", nil, nil)
	if r, s := e.Patterns(); len(r)+len(s) != 0 {
		t.Fatalf("no rules should mean no interception: %v %v", r, s)
	}
	e.Add(Rule{Type: TypeMapLocal, Enabled: true, Match: Matcher{URL: "https://api.x.com/v2/feed?x=*"}, Spec: body("x")}, 0)
	e.Add(Rule{Type: TypeBreakpoint, Enabled: true, Phase: PhaseResponse, Match: Matcher{URL: "cdn.x.com"}}, 0)
	req, resp := e.Patterns()
	if len(req) != 1 || req[0] != `https://api.x.com/v2/feed\?x=*` || len(resp) != 1 || resp[0] != "*://cdn.x.com*" {
		t.Fatalf("patterns: %v %v", req, resp)
	}
	e.Add(Rule{Type: TypeRewrite, Enabled: true, Phase: PhaseBoth, Match: Matcher{URL: `^https://.*`, Regex: true}, Spec: steps(Step{Type: StepSetHeader, Name: "a", Value: "b"})}, 0)
	if req, resp := e.Patterns(); len(req) != 1 || req[0] != "*" || len(resp) != 1 || resp[0] != "*" {
		t.Fatalf("regex rule should widen to *: %v %v", req, resp)
	}
}
