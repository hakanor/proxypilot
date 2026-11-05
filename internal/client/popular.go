package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/hakanor/proxypilot/internal/proxy"
	"github.com/hakanor/proxypilot/internal/store"
)

// Popular lists the hosts the user works on most, each with its top paths.
func (c *Client) Popular(ctx context.Context, limit, paths int) ([]store.PopularHost, error) {
	var out struct {
		Hosts []store.PopularHost `json:"hosts"`
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}, "paths": {strconv.Itoa(paths)}}
	return out.Hosts, c.getJSON(ctx, "/popular", q, &out)
}

func (c *Client) ClearPopular(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodDelete, "/popular", nil, nil, nil)
}

// SSLPolicy returns which hosts are decrypted (mode, allowlist, denylist, favorite hosts).
func (c *Client) SSLPolicy(ctx context.Context) (proxy.PolicySnapshot, error) {
	var p proxy.PolicySnapshot
	return p, c.getJSON(ctx, "/ssl-policy", nil, &p)
}
