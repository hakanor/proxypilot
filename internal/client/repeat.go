package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/hakanor/proxypilot/internal/api"
	"github.com/hakanor/proxypilot/internal/repeat"
)

func (c *Client) Repeat(ctx context.Context, id string, in api.RepeatRequest) (repeat.Summary, error) {
	var s repeat.Summary
	return s, c.unredacted().doJSON(ctx, http.MethodPost, "/flows/"+url.PathEscape(id)+"/repeat", nil, in, &s)
}

func (c *Client) Compose(ctx context.Context, in api.ComposeRequest) (repeat.Summary, error) {
	var s repeat.Summary
	return s, c.unredacted().doJSON(ctx, http.MethodPost, "/compose", nil, in, &s)
}

// Curl returns a flow as a cURL command; masked unless the client was created without redaction.
func (c *Client) Curl(ctx context.Context, id string) (string, bool, error) {
	var out struct {
		Curl   string `json:"curl"`
		Binary bool   `json:"binary_body"`
	}
	err := c.doJSON(ctx, http.MethodGet, "/flows/"+url.PathEscape(id)+"/curl", nil, nil, &out)
	return out.Curl, out.Binary, err
}
