// Package mcpserver exposes ProxyPilot to AI agents over MCP (stdio). It talks to the
// daemon through the control API client, so it never touches the store directly.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/hakanor/proxypilot/internal/client"
	"github.com/hakanor/proxypilot/internal/version"
)

// MaxResponse keeps every tool answer small enough for an agent's context.
const MaxResponse = 8 << 10

type Options struct {
	// ReadOnly registers no tool that changes anything.
	ReadOnly bool
	// Agent names the owner of rules this server creates: "agent:<Agent>".
	Agent string
	// AllowSecrets turns redaction off. Only the user can set it (a CLI flag); agents cannot.
	AllowSecrets bool
}

type Server struct {
	dial  Dialer
	opts  Options
	owner string
	mcp   *server.MCPServer

	mu   sync.Mutex
	conn *conn
	ids  ids
}

// conn is a pair of API clients: c masks secrets (unless --allow-secrets); raw never
// does and is only used to build edits that are sent back to the daemon, never returned.
type conn struct {
	c, raw *client.Client
}

// Dialer connects to the daemon. It is called lazily and again after the daemon
// went away, so the MCP server can start before the daemon and survive restarts.
type Dialer func() (c, raw *client.Client, err error)

func (s *Server) connect() (*conn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		return s.conn, nil
	}
	c, raw, err := s.dial()
	if err != nil {
		return nil, err
	}
	s.conn = &conn{c: c, raw: raw}
	return s.conn, nil
}

func (s *Server) drop(k *conn) {
	s.mu.Lock()
	if s.conn == k {
		s.conn = nil
	}
	s.mu.Unlock()
}

const instructions = `ProxyPilot is an HTTP(S)/WebSocket debugging proxy. Use it to see what an app really sends and receives.

Recommended flow:
1. summarize_traffic first: it groups failures and names the slowest request, so you rarely need the full list.
2. list_requests / search_traffic to find a flow (one line each; ids are short), then get_request. For big JSON bodies pass path (e.g. $.items[0]) instead of reading it all.
3. To check a change: wait_for_request (start it, trigger the action in the app, it returns the flow).
4. To mock: add_map_local. To edit traffic: add_rewrite. To point at another server: add_map_remote. To intercept: set_breakpoint, wait_for_breakpoint, resume_breakpoint.

Rules you create are owned by you, expire after 1h by default, and cannot touch the user's rules.
Secrets (Authorization, cookies, tokens, passwords) are masked as "Bearer eyJh…[redacted, N chars]"; that is expected.
To reproduce: repeat_request (optionally edited, count up to 10).`

func New(dial Dialer, o Options) *Server {
	if o.Agent == "" {
		o.Agent = "agent"
	}
	s := &Server{dial: dial, opts: o, owner: "agent:" + o.Agent}
	s.mcp = server.NewMCPServer("proxypilot", version.Version,
		server.WithToolCapabilities(false),
		server.WithInstructions(instructions),
	)
	s.registerRead()
	if !o.ReadOnly {
		s.registerWrite()
	}
	return s
}

// MCP returns the underlying server (stdio serving, tests).
func (s *Server) MCP() *server.MCPServer { return s.mcp }

// ServeStdio blocks serving MCP on stdin/stdout.
func (s *Server) ServeStdio() error { return server.ServeStdio(s.mcp) }

type handler func(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error)

// add registers a tool whose handler returns a value that is sent as compact JSON.
func (s *Server) add(t mcp.Tool, h handler) {
	s.mcp.AddTool(t, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		k, err := s.connect()
		if err != nil {
			return mcp.NewToolResultError(explain(err)), nil
		}
		v, err := h(ctx, k, req)
		if errors.Is(err, client.ErrNoDaemon) {
			s.drop(k) // the daemon may come back on another port
		}
		if err != nil {
			return mcp.NewToolResultError(explain(err)), nil
		}
		b, err := json.Marshal(v)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(b) > MaxResponse {
			// Handlers trim their own output; this guard keeps the contract if one misses.
			return mcp.NewToolResultError("response over 8 KB; narrow the query (smaller limit, max_body_bytes, or a filter)"), nil
		}
		return mcp.NewToolResultText(string(b)), nil
	})
}

// explain turns client errors into actionable messages for the agent.
func explain(err error) string {
	var ae *client.APIError
	switch {
	case errors.Is(err, client.ErrNoDaemon):
		return "ProxyPilot is not running. Ask the user to run `proxypilot start --detach` (or `proxypilot ui`)."
	case errors.Is(err, client.ErrUnauthorized):
		return "The control API rejected the token. Ask the user to restart `proxypilot mcp`."
	case errors.As(err, &ae):
		return fmt.Sprintf("%s: %s", ae.Code, ae.Message)
	}
	return err.Error()
}

// fit keeps the first n items of a list that, marshalled, stays under the budget.
func fit[T any](items []T, budget int) ([]T, bool) {
	for n := len(items); n > 0; n-- {
		b, _ := json.Marshal(items[:n])
		if len(b) <= budget {
			return items[:n], n < len(items)
		}
	}
	return nil, len(items) > 0
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && cut < len(s) && (s[cut]&0xC0) == 0x80 { // keep UTF-8 valid
		cut--
	}
	return s[:cut] + "…"
}

// flowID reads a request id argument, short or full. A short id this process has
// not shown (e.g. after an MCP restart) is looked up among the latest 2000 flows.
func (s *Server) flowID(ctx context.Context, k *conn, req mcp.CallToolRequest, key string) (string, error) {
	id, err := req.RequireString(key)
	if err != nil {
		return "", err
	}
	if full, err := s.ids.resolve(id); err == nil {
		return full, nil
	}
	q := url.Values{"limit": {"500"}}
	for range 4 {
		p, err := k.c.ListFlows(ctx, q)
		if err != nil {
			return "", err
		}
		for _, f := range p.Flows {
			s.ids.short(f.ID)
		}
		if full, err := s.ids.resolve(id); err == nil || !p.HasMore {
			return full, err
		}
		q.Set("cursor", p.NextCursor)
	}
	return s.ids.resolve(id)
}

func errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }

func trimAll(xs []string) []string {
	out := xs[:0]
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}
