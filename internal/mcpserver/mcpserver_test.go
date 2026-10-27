package mcpserver_test

import (
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

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/hakanor/proxypilot/internal/client"
	"github.com/hakanor/proxypilot/internal/config"
	"github.com/hakanor/proxypilot/internal/daemon"
	"github.com/hakanor/proxypilot/internal/mcpserver"
)

type env struct {
	dir   string
	proxy *url.URL
	up    *httptest.Server
	d     *daemon.Daemon
}

func startDaemon(t *testing.T) *env {
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
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/big" {
			w.Write([]byte(`{"data":"` + strings.Repeat("x", 50000) + `"}`))
			return
		}
		if r.URL.Path == "/fail" {
			w.WriteHeader(500)
		}
		w.Write([]byte(`{"access_token":"supersecretvalue1234567890","user":"hakan"}`))
	}))
	t.Cleanup(up.Close)
	pu, _ := url.Parse("http://" + d.Proxy.Addr().String())
	return &env{dir: cfg.DataDir, proxy: pu, up: up, d: d}
}

func (e *env) get(t *testing.T, path string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", e.up.URL+path, nil)
	req.Header.Set("Authorization", "Bearer realsecrettoken987654321")
	resp, err := (&http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(e.proxy)}, Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func connect(t *testing.T, dir string, o mcpserver.Options) *mcpclient.Client {
	t.Helper()
	dial := func() (*client.Client, *client.Client, error) {
		raw, err := client.New(client.Options{DataDir: dir})
		if err != nil {
			return nil, nil, err
		}
		c, err := client.New(client.Options{DataDir: dir, Redact: !o.AllowSecrets})
		return c, raw, err
	}
	srv := mcpserver.New(dial, o)
	c, err := mcpclient.NewInProcessClient(srv.MCP())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1"}
	if _, err := c.Initialize(ctx, init); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func call(t *testing.T, c *mcpclient.Client, name string, args map[string]any) (string, bool) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := c.CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := ""
	for _, ct := range res.Content {
		if tc, ok := ct.(mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if len(text) > mcpserver.MaxResponse {
		t.Errorf("%s returned %d bytes (> 8 KB)", name, len(text))
	}
	return text, res.IsError
}

func toolNames(t *testing.T, c *mcpclient.Client) map[string]bool {
	t.Helper()
	res, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, tl := range res.Tools {
		out[tl.Name] = true
	}
	return out
}

func TestToolsEndToEnd(t *testing.T) {
	e := startDaemon(t)
	c := connect(t, e.dir, mcpserver.Options{Agent: "claude"})

	names := toolNames(t, c)
	for _, n := range []string{"get_status", "summarize_traffic", "list_requests", "get_request", "search_traffic", "wait_for_request",
		"add_map_local", "add_rewrite", "add_map_remote", "repeat_request", "set_breakpoint", "wait_for_breakpoint", "resume_breakpoint", "list_rules", "remove_rule", "clear_agent_rules", "clear_session"} {
		if !names[n] {
			t.Errorf("tool %s missing", n)
		}
	}

	e.get(t, "/login")
	e.get(t, "/fail")
	e.get(t, "/big")
	time.Sleep(200 * time.Millisecond)

	out, isErr := call(t, c, "summarize_traffic", map[string]any{"since": "all"})
	if isErr || !strings.Contains(out, `"total":3`) || !strings.Contains(out, "/fail") {
		t.Fatalf("summary: %s", out)
	}
	out, _ = call(t, c, "list_requests", map[string]any{"status": "5xx"})
	var page struct {
		Requests []struct{ ID, URL string }
	}
	json.Unmarshal([]byte(out), &page)
	if len(page.Requests) != 1 || !strings.HasSuffix(page.Requests[0].URL, "/fail") {
		t.Fatalf("list 5xx: %s", out)
	}

	// Secrets are masked in headers and bodies.
	out, _ = call(t, c, "list_requests", nil)
	json.Unmarshal([]byte(out), &page)
	var loginID, bigID string
	for _, r := range page.Requests {
		if strings.HasSuffix(r.URL, "/login") {
			loginID = r.ID
		}
		if strings.HasSuffix(r.URL, "/big") {
			bigID = r.ID
		}
	}
	out, _ = call(t, c, "get_request", map[string]any{"id": loginID})
	if strings.Contains(out, "realsecrettoken987654321") || strings.Contains(out, "supersecretvalue1234567890") || !strings.Contains(out, "redacted") || !strings.Contains(out, "hakan") {
		t.Fatalf("get_request redaction: %s", out)
	}
	// Big bodies are excerpts with a next offset.
	out, _ = call(t, c, "get_request", map[string]any{"id": bigID, "max_body_bytes": 6000})
	if !strings.Contains(out, `"more":true`) || !strings.Contains(out, "offset=6000") {
		t.Fatalf("big body hint: %.300s", out)
	}
	out, _ = call(t, c, "get_request", map[string]any{"id": bigID, "offset": 6000, "max_body_bytes": 100})
	if !strings.Contains(out, `"offset":6000`) {
		t.Fatalf("offset read: %.300s", out)
	}

	// Map Local owned by the agent, with a TTL.
	out, isErr = call(t, c, "add_map_local", map[string]any{"url": e.up.URL + "/mocked*", "status": 418, "body": `{"mock":true}`})
	if isErr || !strings.Contains(out, `"owner":"agent:claude"`) || !strings.Contains(out, "expires_at") {
		t.Fatalf("add_map_local: %s", out)
	}
	if st, b := e.get(t, "/mocked/1"); st != 418 || b != `{"mock":true}` {
		t.Fatalf("mock not applied: %d %s", st, b)
	}

	// Rewrite a JSON field in the response.
	out, isErr = call(t, c, "add_rewrite", map[string]any{"url": e.up.URL + "/login*", "phase": "response",
		"steps": []any{map[string]any{"type": "json_set", "path": "$.user", "value": `"agent"`}}})
	if isErr {
		t.Fatalf("add_rewrite: %s", out)
	}
	var rw struct{ ID string }
	json.Unmarshal([]byte(out), &rw)
	if _, b := e.get(t, "/login?rw=1"); !strings.Contains(b, `"user":"agent"`) {
		t.Fatalf("rewrite not applied: %s", b)
	}
	if out, isErr := call(t, c, "add_rewrite", map[string]any{"url": "x", "steps": []any{map[string]any{"type": "nope"}}}); !isErr {
		t.Fatalf("bad step accepted: %s", out)
	}
	call(t, c, "remove_rule", map[string]any{"id": rw.ID})

	// The agent cannot touch the user's rules.
	ac, _ := client.New(client.Options{DataDir: e.dir})
	userRule, err := ac.AddRule(context.Background(), apiRule(e.up.URL+"/user*"))
	if err != nil {
		t.Fatal(err)
	}
	if out, isErr := call(t, c, "remove_rule", map[string]any{"id": userRule.ID}); !isErr || !strings.Contains(out, "only change your own") {
		t.Fatalf("remove user rule: %v %s", isErr, out)
	}
	out, _ = call(t, c, "clear_agent_rules", nil)
	if !strings.Contains(out, `"deleted":1`) {
		t.Fatalf("clear_agent_rules: %s", out)
	}
	if _, err := ac.GetRule(context.Background(), userRule.ID); err != nil {
		t.Fatal("user rule was removed")
	}

	// Breakpoint round trip: response status edited to 503; the real token reaches the server.
	call(t, c, "set_breakpoint", map[string]any{"url": e.up.URL + "/login*", "phase": "response"})
	done := make(chan int, 1)
	go func() { st, _ := e.get(t, "/login"); done <- st }()
	out, _ = call(t, c, "wait_for_breakpoint", map[string]any{"timeout_seconds": 5})
	if strings.Contains(out, "realsecrettoken987654321") {
		t.Fatal("wait_for_breakpoint leaked the token")
	}
	var p struct{ ID string }
	json.Unmarshal([]byte(out), &p)
	if out, isErr := call(t, c, "resume_breakpoint", map[string]any{"id": p.ID, "action": "modify", "status": 503}); isErr {
		t.Fatalf("resume: %s", out)
	}
	if st := <-done; st != 503 {
		t.Fatalf("client got %d", st)
	}

	// repeat_request sends the login flow again with a header change; limits are enforced.
	out, isErr = call(t, c, "repeat_request", map[string]any{"id": loginID, "set_headers": map[string]any{"X-Repeat": "1"}, "count": 2, "bypass_rules": true})
	if isErr || !strings.Contains(out, `"2xx":2`) {
		t.Fatalf("repeat_request: %s", out)
	}
	if out, isErr := call(t, c, "repeat_request", map[string]any{"id": loginID, "count": 11}); !isErr {
		t.Fatalf("count limit not enforced: %s", out)
	}

	// wait_for_request returns the flow once it completes.
	go func() { time.Sleep(300 * time.Millisecond); e.get(t, "/later") }()
	out, _ = call(t, c, "wait_for_request", map[string]any{"url": "/later", "timeout_seconds": 5})
	if !strings.Contains(out, `"matched":true`) {
		t.Fatalf("wait_for_request: %s", out)
	}
	if out, isErr := call(t, c, "wait_for_request", map[string]any{"url": "x", "timeout_seconds": 999}); !isErr {
		t.Fatalf("timeout bound not enforced: %s", out)
	}
}

func TestReadOnlyAndNoDaemon(t *testing.T) {
	e := startDaemon(t)
	c := connect(t, e.dir, mcpserver.Options{ReadOnly: true})
	names := toolNames(t, c)
	for _, n := range []string{"add_map_local", "set_breakpoint", "resume_breakpoint", "remove_rule", "clear_agent_rules", "clear_session"} {
		if names[n] {
			t.Errorf("read-only server exposes %s", n)
		}
	}
	if !names["get_request"] {
		t.Error("read tools missing in read-only mode")
	}

	none := connect(t, t.TempDir(), mcpserver.Options{})
	out, isErr := call(t, none, "get_status", nil)
	if !isErr || !strings.Contains(out, "not running") {
		t.Fatalf("no daemon: %v %s", isErr, out)
	}
}
