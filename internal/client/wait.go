package client

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/hakanor/proxypilot/internal/store"
)

// ErrWaitTimeout means no matching flow completed before ctx's deadline.
var ErrWaitTimeout = errors.New("no matching flow before the timeout")

// URLMatcher matches a substring, or a * pattern over the whole URL (case-insensitive).
func URLMatcher(pattern string) func(string) bool {
	p := strings.ToLower(pattern)
	if !strings.Contains(p, "*") {
		return func(u string) bool { return strings.Contains(strings.ToLower(u), p) }
	}
	match := store.MatchHost(p) // same glob semantics
	return func(u string) bool { return match(strings.ToLower(u)) }
}

// WaitOptions select the flow WaitFlow returns.
type WaitOptions struct {
	URL    string // substring or * pattern (required)
	Method string // comma-separated
	Status string // 2xx, 404, 400-499
	Since  string // also accept flows completed this long before waiting ("10s")
}

// WaitFlow blocks until a matching flow completes and returns its id.
// The deadline comes from ctx; it returns ErrWaitTimeout when it passes.
func (c *Client) WaitFlow(ctx context.Context, o WaitOptions) (string, error) {
	match := URLMatcher(o.URL)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	q := url.Values{"events": {"flow.completed"}}
	if o.Method != "" {
		q.Set("method", o.Method)
	}
	if o.Status != "" {
		q.Set("status", o.Status)
	}
	matched := make(chan string, 2)
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- c.Stream(ctx, q, func(e StreamEvent) bool {
			if r, ok := e.Flow(); ok && match(r.URL) {
				matched <- r.ID
				return false
			}
			return true
		})
	}()

	// Flows that completed shortly before count too.
	if o.Since != "" {
		lq := url.Values{"since": {o.Since}, "state": {"completed"}, "limit": {"500"}}
		if o.Method != "" {
			lq.Set("method", o.Method)
		}
		if o.Status != "" {
			lq.Set("status", o.Status)
		}
		page, err := c.ListFlows(ctx, lq)
		if err != nil {
			return "", err
		}
		for _, f := range page.Flows {
			if match(f.URL) {
				return f.ID, nil
			}
		}
	}

	select {
	case id := <-matched:
		return id, nil
	case err := <-streamErr:
		select { // the stream may have matched right before ending
		case id := <-matched:
			return id, nil
		default:
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", ErrWaitTimeout
		}
		if err == nil {
			err = ctx.Err()
		}
		return "", err
	}
}
