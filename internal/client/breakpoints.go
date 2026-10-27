package client

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/hakanor/proxypilot/internal/breakpoints"
)

// unredacted is a copy that never asks for masking (needed to edit and send back).
func (c *Client) unredacted() *Client {
	cp := *c
	cp.Redact = false
	return &cp
}

func (c *Client) ListBreakpoints(ctx context.Context) ([]*breakpoints.Paused, error) {
	var out struct {
		Breakpoints []*breakpoints.Paused `json:"breakpoints"`
	}
	err := c.doJSON(ctx, http.MethodGet, "/breakpoints", nil, nil, &out)
	return out.Breakpoints, err
}

// GetBreakpoint returns a paused flow; raw=true skips redaction (for editing).
func (c *Client) GetBreakpoint(ctx context.Context, id string, raw bool) (*breakpoints.Paused, error) {
	cl := c
	if raw {
		cl = c.unredacted()
	}
	var p breakpoints.Paused
	return &p, cl.doJSON(ctx, http.MethodGet, "/breakpoints/"+url.PathEscape(id), nil, nil, &p)
}

// WaitBreakpoint long-polls for the first paused flow; ErrTimeout-like APIError (408) when none.
func (c *Client) WaitBreakpoint(ctx context.Context, timeout time.Duration, urlPattern, phase string) (*breakpoints.Paused, error) {
	q := url.Values{"timeout": {timeout.String()}}
	if urlPattern != "" {
		q.Set("url", urlPattern)
	}
	if phase != "" {
		q.Set("phase", phase)
	}
	var p breakpoints.Paused
	return &p, c.doJSON(ctx, http.MethodGet, "/breakpoints/wait", q, nil, &p)
}

func (c *Client) ResumeBreakpoint(ctx context.Context, id string, r breakpoints.Resolution) error {
	return c.doJSON(ctx, http.MethodPost, "/breakpoints/"+url.PathEscape(id)+"/resume", nil, r, nil)
}

func (c *Client) ExtendBreakpoint(ctx context.Context, id string, seconds int) (*breakpoints.Paused, error) {
	var p breakpoints.Paused
	return &p, c.doJSON(ctx, http.MethodPost, "/breakpoints/"+url.PathEscape(id)+"/extend", nil, map[string]int{"seconds": seconds}, &p)
}
