// Package client talks to the control API. The CLI, the MCP server and the TUI
// use it; none of them touches the store or proxy packages directly.
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hakanor/proxypilot/internal/api"
	"github.com/hakanor/proxypilot/internal/daemon"
	"github.com/hakanor/proxypilot/internal/store"
)

// Error kinds the CLI maps to exit codes.
var (
	ErrNoDaemon     = errors.New("proxypilot is not running (start it with `proxypilot start --detach`)")
	ErrUnauthorized = errors.New("unauthorized: token rejected")
	ErrNotFound     = errors.New("not found")
)

// APIError is an error response from the API.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func (e *APIError) Unwrap() error {
	switch e.Status {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	}
	return nil
}

type Client struct {
	Base   string // http://127.0.0.1:9091/api/v1
	Token  string
	Redact bool // send ?redact=true (CLI and MCP default)
	HTTP   *http.Client
}

// Options locate the daemon. Empty fields are discovered from the data dir.
type Options struct {
	DataDir string
	APIAddr string
	Token   string
	Redact  bool
}

// New resolves the API address (flag > running daemon state) and token (flag > token file).
func New(o Options) (*Client, error) {
	addr := o.APIAddr
	if addr == "" {
		st, err := daemon.Running(o.DataDir)
		if err != nil {
			return nil, ErrNoDaemon
		}
		addr = st.APIAddr
	}
	tok := o.Token
	if tok == "" {
		t, err := daemon.ReadToken(o.DataDir)
		if err != nil {
			return nil, fmt.Errorf("%w (no token file in %s; pass --token)", ErrUnauthorized, o.DataDir)
		}
		tok = t
	}
	return &Client{
		Base: "http://" + addr + "/api/v1", Token: tok, Redact: o.Redact,
		HTTP: &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}},
	}, nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, q url.Values, body io.Reader) (*http.Request, error) {
	if q == nil {
		q = url.Values{}
	}
	if c.Redact {
		q.Set("redact", "true")
	}
	u := c.Base + path
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) send(req *http.Request) (*http.Response, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		var ne net.Error
		var oe *net.OpError
		if errors.As(err, &oe) || (errors.As(err, &ne) && !ne.Timeout()) {
			return nil, fmt.Errorf("%w: %v", ErrNoDaemon, err)
		}
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&e)
		if e.Error.Code == "" {
			e.Error.Code, e.Error.Message = "http_"+fmt.Sprint(resp.StatusCode), resp.Status
		}
		return nil, &APIError{Status: resp.StatusCode, Code: e.Error.Code, Message: e.Error.Message}
	}
	return resp, nil
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	req, err := c.newRequest(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return err
	}
	resp, err := c.send(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Status(ctx context.Context) (api.StatusInfo, error) {
	var s api.StatusInfo
	return s, c.getJSON(ctx, "/status", nil, &s)
}

// FlowPage mirrors GET /flows.
type FlowPage struct {
	Flows      []*store.Flow `json:"flows"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
	Limit      int           `json:"limit"`
}

func (c *Client) ListFlows(ctx context.Context, q url.Values) (FlowPage, error) {
	var p FlowPage
	return p, c.getJSON(ctx, "/flows", q, &p)
}

// FlowDetail mirrors GET /flows/{id}.
type FlowDetail struct {
	store.Flow
	Bodies struct {
		Req  *store.BodyInfo `json:"req,omitempty"`
		Resp *store.BodyInfo `json:"resp,omitempty"`
	} `json:"bodies"`
}

func (c *Client) GetFlow(ctx context.Context, id string) (FlowDetail, error) {
	var d FlowDetail
	return d, c.getJSON(ctx, "/flows/"+url.PathEscape(id), nil, &d)
}

func (c *Client) GetBody(ctx context.Context, id, side string, max int64) (api.BodyResponse, error) {
	var b api.BodyResponse
	q := url.Values{"max": {fmt.Sprint(max)}}
	return b, c.getJSON(ctx, "/flows/"+url.PathEscape(id)+"/body/"+side, q, &b)
}

// RawBody streams the full decoded body (never redacted: it is the exact payload).
func (c *Client) RawBody(ctx context.Context, id, side string) (io.ReadCloser, error) {
	plain := *c
	plain.Redact = false
	req, err := plain.newRequest(ctx, http.MethodGet, "/flows/"+url.PathEscape(id)+"/body/"+side, url.Values{"raw": {"true"}}, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.send(req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *Client) Summary(ctx context.Context, since string) (store.Summary, error) {
	var s store.Summary
	q := url.Values{}
	if since != "" {
		q.Set("since", since)
	}
	return s, c.getJSON(ctx, "/summary", q, &s)
}

func (c *Client) Clear(ctx context.Context) error {
	req, err := c.newRequest(ctx, http.MethodDelete, "/flows", nil, nil)
	if err != nil {
		return err
	}
	resp, err := c.send(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// StreamEvent is one NDJSON line from /stream/ndjson.
type StreamEvent struct {
	Seq   uint64          `json:"seq"`
	Type  string          `json:"type"`
	Time  time.Time       `json:"time"`
	Data  json.RawMessage `json:"data,omitempty"`
	Count uint64          `json:"count,omitempty"` // for type=dropped
}

// Flow decodes Data as a flow row.
func (e StreamEvent) Flow() (api.FlowRow, bool) {
	var r api.FlowRow
	if !strings.HasPrefix(e.Type, "flow.") || json.Unmarshal(e.Data, &r) != nil {
		return r, false
	}
	return r, true
}

// Stream calls fn for each event until ctx ends, fn returns false, or the stream breaks.
func (c *Client) Stream(ctx context.Context, q url.Values, fn func(StreamEvent) bool) error {
	req, err := c.newRequest(ctx, http.MethodGet, "/stream/ndjson", q, nil)
	if err != nil {
		return err
	}
	resp, err := c.send(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		var e StreamEvent
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if !fn(e) {
			return nil
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return sc.Err()
}
