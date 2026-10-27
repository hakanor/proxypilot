package client

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/hakanor/proxypilot/internal/api"
	"github.com/hakanor/proxypilot/internal/rules"
)

// doJSON sends body (if any) as JSON and decodes the response into out (if any).
func (c *Client) doJSON(ctx context.Context, method, path string, q url.Values, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := c.newRequest(ctx, method, path, q, r)
	if err != nil {
		return err
	}
	resp, err := c.send(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) ListRules(ctx context.Context, owner string) ([]*rules.Rule, error) {
	q := url.Values{}
	if owner != "" {
		q.Set("owner", owner)
	}
	var out struct {
		Rules []*rules.Rule `json:"rules"`
	}
	err := c.doJSON(ctx, http.MethodGet, "/rules", q, nil, &out)
	return out.Rules, err
}

func (c *Client) GetRule(ctx context.Context, id string) (*rules.Rule, error) {
	var r rules.Rule
	return &r, c.doJSON(ctx, http.MethodGet, "/rules/"+url.PathEscape(id), nil, nil, &r)
}

func (c *Client) AddRule(ctx context.Context, in api.RuleInput) (*rules.Rule, error) {
	var r rules.Rule
	return &r, c.doJSON(ctx, http.MethodPost, "/rules", nil, in, &r)
}

func (c *Client) DeleteRule(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/rules/"+url.PathEscape(id), nil, nil, nil)
}

// DeleteRules removes rules whose owner matches pattern ("agent:*").
func (c *Client) DeleteRules(ctx context.Context, owner string) (int, error) {
	var out struct {
		Deleted int `json:"deleted"`
	}
	err := c.doJSON(ctx, http.MethodDelete, "/rules", url.Values{"owner": {owner}}, nil, &out)
	return out.Deleted, err
}

func (c *Client) ToggleRule(ctx context.Context, id string, enabled *bool) (*rules.Rule, error) {
	var r rules.Rule
	var body any
	if enabled != nil {
		body = map[string]bool{"enabled": *enabled}
	}
	return &r, c.doJSON(ctx, http.MethodPost, "/rules/"+url.PathEscape(id)+"/toggle", nil, body, &r)
}

// MapLocalFromFlow saves a recorded response to disk and creates a Map Local rule for it.
func (c *Client) MapLocalFromFlow(ctx context.Context, flowID, owner, ttl string) (*rules.Rule, string, error) {
	var out struct {
		Rule *rules.Rule `json:"rule"`
		File string      `json:"file"`
	}
	err := c.doJSON(ctx, http.MethodPost, "/flows/"+url.PathEscape(flowID)+"/map-local", nil, map[string]string{"owner": owner, "ttl": ttl}, &out)
	return out.Rule, out.File, err
}
