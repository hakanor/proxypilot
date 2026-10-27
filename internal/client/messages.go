package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/hakanor/proxypilot/internal/api"
)

// Messages pages through a WebSocket flow's messages (after = last seq seen).
func (c *Client) Messages(ctx context.Context, id string, after int64, limit int) (api.MessagesPage, error) {
	q := url.Values{"after": {strconv.FormatInt(after, 10)}, "limit": {strconv.Itoa(limit)}}
	var p api.MessagesPage
	return p, c.doJSON(ctx, http.MethodGet, "/flows/"+url.PathEscape(id)+"/messages", q, nil, &p)
}
