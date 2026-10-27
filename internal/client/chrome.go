package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/hakanor/proxypilot/internal/cdp"
)

func (c *Client) ChromeStatus(ctx context.Context) (cdp.Status, error) {
	var s cdp.Status
	return s, c.doJSON(ctx, http.MethodGet, "/chrome", nil, nil, &s)
}

func (c *Client) ChromeLaunch(ctx context.Context, startURL string) (cdp.Status, error) {
	var s cdp.Status
	return s, c.doJSON(ctx, http.MethodPost, "/chrome/launch", nil, map[string]string{"url": startURL}, &s)
}

func (c *Client) ChromeAttach(ctx context.Context, addr string) (cdp.Status, error) {
	var s cdp.Status
	return s, c.doJSON(ctx, http.MethodPost, "/chrome/attach", nil, map[string]string{"addr": addr}, &s)
}

func (c *Client) ChromeDetach(ctx context.Context) (cdp.Status, error) {
	var s cdp.Status
	return s, c.doJSON(ctx, http.MethodPost, "/chrome/detach", nil, nil, &s)
}

func (c *Client) ChromeWatch(ctx context.Context, tabID string, watch bool) (cdp.Tab, error) {
	var t cdp.Tab
	return t, c.doJSON(ctx, http.MethodPut, "/chrome/tabs/"+url.PathEscape(tabID), nil, map[string]bool{"watch": watch}, &t)
}
