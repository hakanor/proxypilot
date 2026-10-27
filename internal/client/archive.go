package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/hakanor/proxypilot/internal/archive"
)

// Export streams /export/{har|session}; masked unless the client was created without redaction.
func (c *Client) Export(ctx context.Context, kind string, q url.Values) (io.ReadCloser, error) {
	if q == nil {
		q = url.Values{}
	}
	if !c.Redact {
		q.Set("redact", "false")
	}
	cl := c.unredacted() // redaction is controlled by the explicit parameter above
	req, err := cl.newRequest(ctx, http.MethodGet, "/export/"+kind, q, nil)
	if err != nil {
		return nil, err
	}
	resp, err := cl.send(req)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// Import uploads a HAR or session file.
func (c *Client) Import(ctx context.Context, r io.Reader) (archive.ImportResult, error) {
	req, err := c.newRequest(ctx, http.MethodPost, "/import", nil, r)
	if err != nil {
		return archive.ImportResult{}, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.send(req)
	if err != nil {
		return archive.ImportResult{}, err
	}
	defer resp.Body.Close()
	var res archive.ImportResult
	return res, json.NewDecoder(resp.Body).Decode(&res)
}
