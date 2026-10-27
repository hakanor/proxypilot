package client

import (
	"context"
	"net/http"

	"github.com/hakanor/proxypilot/internal/api"
	"github.com/hakanor/proxypilot/internal/proxy"
)

func (c *Client) Throttle(ctx context.Context) (api.ThrottleInfo, error) {
	var t api.ThrottleInfo
	return t, c.doJSON(ctx, http.MethodGet, "/throttle", nil, nil, &t)
}

func (c *Client) SetThrottle(ctx context.Context, s proxy.ThrottleSettings) (api.ThrottleInfo, error) {
	var t api.ThrottleInfo
	return t, c.doJSON(ctx, http.MethodPut, "/throttle", nil, s, &t)
}
