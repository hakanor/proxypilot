package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hakanor/proxypilot/internal/config"
	"github.com/hakanor/proxypilot/internal/daemon"
)

type harness struct {
	dir      string
	proxy    *url.URL
	upstream *httptest.Server
}

// startDaemon runs a real daemon in-process on random ports.
func startDaemon(t *testing.T) *harness {
	t.Helper()
	cfg := config.Defaults()
	cfg.DataDir = t.TempDir()
	cfg.Proxy.Listen, cfg.API.Listen = "127.0.0.1:0", "127.0.0.1:0"
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan *daemon.Daemon, 1)
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), daemon.RunOptions{}, func(d *daemon.Daemon) { ready <- d })
	}()
	var d *daemon.Daemon
	select {
	case d = <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon not ready")
	}
	t.Cleanup(func() { cancel(); <-done })

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"user":"hakan","access_token":"eyJhbGciOiJIUzI1NiJ9.verylongtokenvalue"}`))
		case "/big":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(strings.Repeat("abcdefgh", 2048)))
		case "/fail":
			w.WriteHeader(500)
		case "/echo":
			w.Write([]byte(r.Header.Get("Authorization") + "|" + r.Header.Get("X-Test")))
		default:
			w.Write([]byte("ok"))
		}
	}))
	t.Cleanup(up.Close)
	pu, _ := url.Parse("http://" + d.Proxy.Addr().String())
	return &harness{dir: cfg.DataDir, proxy: pu, upstream: up}
}

func (h *harness) get(t *testing.T, path string, hdr map[string]string) {
	t.Helper()
	req, _ := http.NewRequest("GET", h.upstream.URL+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(h.proxy)}}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// run executes the CLI in-process and returns stdout, stderr and the exit code.
func run(t *testing.T, dataDir string, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	app := &App{Out: &out, ErrOut: &errOut}
	root := NewRootCmd(app)
	root.SetArgs(append([]string{"--data-dir", dataDir, "--log-level", "error"}, args...))
	err := root.Execute()
	if err != nil {
		errOut.WriteString(err.Error())
	}
	return out.String(), errOut.String(), codeFor(err)
}

func waitForFlows(t *testing.T, dir string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, _, code := run(t, dir, "status", "--json")
		var s struct{ Flows int }
		if code == 0 && json.Unmarshal([]byte(out), &s) == nil && s.Flows >= n {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("expected %d flows", n)
}

func TestNoDaemonExitCodes(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"status"}, {"list"}, {"summary"}, {"show", "x"}} {
		if _, _, code := run(t, dir, args...); code != ExitNoDaemon {
			t.Errorf("%v: exit %d, want %d", args, code, ExitNoDaemon)
		}
	}
	if _, _, code := run(t, dir, "clear"); code != ExitUsage {
		t.Errorf("clear without --yes: exit %d", code)
	}
}

func TestListShowRedactionAndWait(t *testing.T) {
	h := startDaemon(t)
	h.get(t, "/login?api_key=abcdefghijklmnopqrstuvwxyz", map[string]string{"Authorization": "Bearer supersecrettokenvalue123"})
	h.get(t, "/big", nil)
	h.get(t, "/fail", nil)
	waitForFlows(t, h.dir, 3)

	out, _, code := run(t, h.dir, "list", "--json", "--status", "5xx")
	if code != 0 {
		t.Fatalf("list exit %d", code)
	}
	var page struct {
		Flows []struct {
			ID, URL string
			Status  int
		}
		HasMore bool `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil || len(page.Flows) != 1 || page.Flows[0].Status != 500 {
		t.Fatalf("list --status 5xx: %s", out)
	}

	out, _, _ = run(t, h.dir, "list", "--json", "--limit", "10")
	json.Unmarshal([]byte(out), &page)
	ids := map[string]string{}
	for _, f := range page.Flows {
		u, _ := url.Parse(f.URL)
		ids[u.Path] = f.ID
	}

	// Redaction is on by default for headers, query and JSON body.
	out, _, code = run(t, h.dir, "show", ids["/login"], "--body", "--json")
	if code != 0 {
		t.Fatalf("show exit %d", code)
	}
	for _, secret := range []string{"supersecrettokenvalue123", "abcdefghijklmnopqrstuvwxyz", "verylongtokenvalue"} {
		if strings.Contains(out, secret) {
			t.Errorf("secret %q leaked with default redaction", secret)
		}
	}
	if !strings.Contains(out, "redacted") || !strings.Contains(out, "hakan") {
		t.Errorf("unexpected show output: %s", out)
	}
	out, errOut, _ := run(t, h.dir, "--no-redact", "show", ids["/login"], "--json")
	if !strings.Contains(out, "supersecrettokenvalue123") || !strings.Contains(errOut, "warning") {
		t.Errorf("--no-redact did not reveal or warn")
	}

	// Large bodies are truncated by default and say so.
	out, _, _ = run(t, h.dir, "show", ids["/big"], "--body", "--side", "resp", "--no-color")
	if !strings.Contains(out, "truncated") || !strings.Contains(out, "4.0 KB of 16.0 KB") {
		t.Errorf("show --body truncation note missing:\n%s", out)
	}
	out, _, _ = run(t, h.dir, "show", ids["/big"], "--raw")
	if len(out) != 16384 {
		t.Errorf("--raw returned %d bytes", len(out))
	}

	// Summary groups failures.
	out, _, _ = run(t, h.dir, "summary", "--no-color")
	if !strings.Contains(out, "3 requests") || !strings.Contains(out, "/fail") || !strings.Contains(out, "×1") {
		t.Errorf("summary:\n%s", out)
	}

	// Search in headers.
	out, _, _ = run(t, h.dir, "--no-redact", "search", "supersecret", "--in", "headers", "--json")
	json.Unmarshal([]byte(out), &page)
	if len(page.Flows) != 1 {
		t.Errorf("header search: %s", out)
	}

	// wait: success when the request arrives, exit 5 on timeout.
	go func() {
		time.Sleep(300 * time.Millisecond)
		h.get(t, "/checkout/done", nil)
	}()
	out, _, code = run(t, h.dir, "wait", "--url", "*/checkout/*", "--timeout", "5s", "--json")
	if code != 0 || !strings.Contains(out, `"matched": true`) || !strings.Contains(out, "/checkout/done") {
		t.Fatalf("wait success: exit %d\n%s", code, out)
	}
	_, _, code = run(t, h.dir, "wait", "--url", "never-happens", "--timeout", "300ms")
	if code != ExitTimeout {
		t.Fatalf("wait timeout exit %d, want %d", code, ExitTimeout)
	}
	_, _, code = run(t, h.dir, "wait", "--url", "/big", "--since", "1m", "--timeout", "2s")
	if code != 0 {
		t.Fatalf("wait --since should match an earlier flow, exit %d", code)
	}
	if _, _, code := run(t, h.dir, "show", "does-not-exist"); code != ExitNotFound {
		t.Fatalf("show missing: exit %d", code)
	}
	if _, _, code := run(t, h.dir, "clear", "--yes"); code != 0 {
		t.Fatalf("clear exit %d", code)
	}
}

func TestParseBytesAndFormat(t *testing.T) {
	for in, want := range map[string]int64{"4096": 4096, "4kb": 4096, "1.5MB": 1572864, "2m": 2 << 20, "10b": 10} {
		if got, err := parseBytes(in); err != nil || got != want {
			t.Errorf("parseBytes(%q) = %d, %v", in, got, err)
		}
	}
	if _, err := parseBytes("lots"); err == nil {
		t.Error("parseBytes accepted garbage")
	}
	if formatBytes(1536) != "1.5 KB" || formatBytes(500) != "500 B" || formatBytes(3<<20) != "3.0 MB" {
		t.Error("formatBytes")
	}
}

func (h *harness) body(t *testing.T, path string) (int, string) {
	t.Helper()
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(h.proxy)}}
	resp, err := c.Get(h.upstream.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestRuleCommands(t *testing.T) {
	h := startDaemon(t)
	out, errOut, code := run(t, h.dir, "rule", "add", "map-local", "--url", h.upstream.URL+"/login*", "--status", "418", "--body", `{"mocked":true}`, "--json")
	if code != 0 {
		t.Fatalf("add: exit %d %s", code, errOut)
	}
	var r struct{ ID string }
	json.Unmarshal([]byte(out), &r)

	if st, b := h.body(t, "/login?x=1"); st != 418 || b != `{"mocked":true}` {
		t.Fatalf("map local not applied: %d %q", st, b)
	}
	out, _, _ = run(t, h.dir, "rule", "list", "--json")
	if !strings.Contains(out, `"hits": 1`) {
		t.Errorf("hits not counted: %s", out)
	}
	// The flow records which rule matched.
	waitForFlows(t, h.dir, 1)
	out, _, _ = run(t, h.dir, "list", "--json")
	if !strings.Contains(out, r.ID) {
		t.Errorf("matched_rules missing from flow: %s", out)
	}

	if _, _, code := run(t, h.dir, "rule", "toggle", r.ID, "--off"); code != 0 {
		t.Fatalf("toggle exit %d", code)
	}
	if _, b := h.body(t, "/login"); strings.Contains(b, "mocked") {
		t.Fatal("disabled rule still applied")
	}

	// Agent rules: TTL and clear by owner.
	if _, _, code := run(t, h.dir, "rule", "add", "map-local", "--url", "*/never", "--body", "x", "--owner", "agent:claude", "--ttl", "10m"); code != 0 {
		t.Fatalf("agent add exit %d", code)
	}
	out, _, _ = run(t, h.dir, "rule", "clear", "--owner", "agent", "--json")
	if !strings.Contains(out, `"deleted": 1`) {
		t.Fatalf("clear agent: %s", out)
	}
	if _, _, code := run(t, h.dir, "rule", "add", "map-local", "--url", "x"); code != ExitUsage {
		t.Fatalf("missing source: exit %d", code)
	}
	if _, _, code := run(t, h.dir, "rule", "add", "map-local", "--url", "(", "--regex", "--body", "x"); code != ExitUsage {
		t.Fatalf("bad regex: exit %d", code)
	}

	// --from copies a recorded response into a file-backed rule.
	h.get(t, "/big", nil)
	waitForFlows(t, h.dir, 3)
	out, _, _ = run(t, h.dir, "list", "--json", "--limit", "1")
	var page struct{ Flows []struct{ ID string } }
	json.Unmarshal([]byte(out), &page)
	out, errOut, code = run(t, h.dir, "rule", "add", "map-local", "--from", page.Flows[0].ID, "--json")
	if code != 0 || !strings.Contains(out, "maplocal") {
		t.Fatalf("--from: exit %d %s %s", code, out, errOut)
	}
	if st, b := h.body(t, "/big"); st != 200 || len(b) != 16384 {
		t.Fatalf("--from rule: %d %d bytes", st, len(b))
	}
}

func TestBreakpointCommands(t *testing.T) {
	h := startDaemon(t)
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(h.proxy)}, Timeout: 10 * time.Second}
	type res struct {
		status int
		body   string
	}
	fire := func(path string) chan res {
		ch := make(chan res, 1)
		go func() {
			req, _ := http.NewRequest("GET", h.upstream.URL+path, nil)
			req.Header.Set("Authorization", "Bearer realsecrettoken123456")
			resp, err := c.Do(req)
			if err != nil {
				ch <- res{body: err.Error()}
				return
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			ch <- res{resp.StatusCode, string(b)}
		}()
		return ch
	}

	// Request phase: the edit keeps the real (unmasked) Authorization.
	if _, e, code := run(t, h.dir, "rule", "add", "breakpoint", "--url", h.upstream.URL+"/echo", "--phase", "request"); code != 0 {
		t.Fatalf("add: %d %s", code, e)
	}
	done := fire("/echo")
	out, _, code := run(t, h.dir, "breakpoint", "wait", "--timeout", "5s", "--json")
	if code != 0 {
		t.Fatalf("wait exit %d", code)
	}
	if strings.Contains(out, "realsecrettoken123456") {
		t.Error("wait output leaked the token (redaction should be on)")
	}
	var p struct{ ID string }
	json.Unmarshal([]byte(out), &p)
	if _, e, code := run(t, h.dir, "breakpoint", "resume", p.ID, "--set-header", "X-Test:edited"); code != 0 {
		t.Fatalf("resume: %d %s", code, e)
	}
	if r := <-done; r.body != "Bearer realsecrettoken123456|edited" {
		t.Fatalf("server saw %q", r.body)
	}
	if _, _, code := run(t, h.dir, "breakpoint", "resume", p.ID); code != ExitError {
		t.Fatalf("second resume: exit %d", code)
	}
	run(t, h.dir, "rule", "clear", "--owner", "all")

	// Response phase: change the status.
	run(t, h.dir, "rule", "add", "breakpoint", "--url", h.upstream.URL+"/login", "--phase", "response")
	done = fire("/login")
	out, _, _ = run(t, h.dir, "breakpoint", "wait", "--timeout", "5s", "--json", "--phase", "response")
	json.Unmarshal([]byte(out), &p)
	if _, e, code := run(t, h.dir, "breakpoint", "resume", p.ID, "--status", "500", "--body", "nope"); code != 0 {
		t.Fatalf("resume response: %d %s", code, e)
	}
	if r := <-done; r.status != 500 || r.body != "nope" {
		t.Fatalf("client got %+v", r)
	}
	if _, _, code := run(t, h.dir, "breakpoint", "wait", "--timeout", "200ms"); code != ExitTimeout {
		t.Fatalf("wait timeout exit %d", code)
	}
}
