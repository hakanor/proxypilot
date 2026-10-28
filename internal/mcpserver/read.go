package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/hakanor/proxypilot/internal/api"
	"github.com/hakanor/proxypilot/internal/client"
	"github.com/hakanor/proxypilot/internal/rules"
	"github.com/hakanor/proxypilot/internal/store"
)

// rowFormat names the fields of a list line, once per page.
const rowFormat = "id method status ms size type time url [×N repeats] [state= err= client= rules= gql=]"

// line renders a flow as one compact text line: about 60% fewer tokens than a JSON object.
func (s *Server) line(f *store.Flow) string {
	size := f.RespSize
	if size == 0 {
		size = f.BytesDown
	}
	status := "-"
	if f.Status != 0 {
		status = strconv.Itoa(f.Status)
	}
	typ := shortType(f.RespContentType)
	if typ == "" {
		typ = "-"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s %dms %s %s %s %s", s.ids.short(f.ID), f.Method, status, int(f.Timing.Total), bytesStr(size), typ,
		f.CreatedAt.Format(time.TimeOnly), clip(f.URL, 180))
	if f.State != store.StateCompleted {
		b.WriteString(" state=" + f.State)
	}
	if f.ErrorKind != "" {
		b.WriteString(" err=" + f.ErrorKind)
	}
	if f.ClientLabel != "" {
		b.WriteString(" client=" + strings.ReplaceAll(f.ClientLabel, " ", "_"))
	}
	if len(f.MatchedRules) > 0 {
		b.WriteString(" rules=" + strings.Join(f.MatchedRules, ","))
	}
	if g := strings.TrimSpace(f.Tags["gql_type"] + " " + f.Tags["gql_op"]); g != "" {
		b.WriteString(" gql=" + strings.ReplaceAll(g, " ", ":"))
	}
	return b.String()
}

func bytesStr(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10) + "B"
	case n < 1_000_000:
		return strconv.FormatFloat(float64(n)/1e3, 'f', 1, 64) + "K"
	}
	return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + "M"
}

func shortType(ct string) string {
	ct, _, _ = strings.Cut(ct, ";")
	ct = strings.TrimSpace(ct)
	if _, sub, ok := strings.Cut(ct, "/"); ok {
		return sub
	}
	return ct
}

type page struct {
	Format     string   `json:"format"`
	Requests   []string `json:"requests"`
	Count      int      `json:"count"`
	Grouped    int      `json:"grouped,omitempty"` // repeats folded into a ×N line
	More       bool     `json:"more"`
	NextCursor string   `json:"next_cursor,omitempty"`
	Hint       string   `json:"hint,omitempty"`
}

func (s *Server) list(ctx context.Context, k *conn, q url.Values, group bool) (page, error) {
	p, err := k.c.ListFlows(ctx, q)
	if err != nil {
		return page{}, err
	}
	flows, repeats := p.Flows, make([]int, len(p.Flows))
	if group {
		flows, repeats = groupRepeats(p.Flows)
	}
	lines := make([]string, len(flows))
	for i, f := range flows {
		lines[i] = s.line(f)
		if repeats[i] > 1 {
			lines[i] += " ×" + strconv.Itoa(repeats[i])
		}
	}
	kept, cut := fit(lines, MaxResponse-400)
	out := page{Format: rowFormat, Requests: kept, Count: len(kept), Grouped: len(p.Flows) - len(flows), More: p.HasMore || cut, NextCursor: p.NextCursor}
	if cut {
		out.NextCursor = ""
		out.Hint = "list was cut to fit 8 KB; use a smaller limit or a filter"
	} else if p.HasMore {
		out.Hint = "pass next_cursor as cursor for older requests"
	}
	return out, nil
}

// groupRepeats folds requests with the same method, URL, status and error (polling)
// into their newest one and counts them.
func groupRepeats(flows []*store.Flow) ([]*store.Flow, []int) {
	var out []*store.Flow
	var counts []int
	at := map[string]int{}
	for _, f := range flows {
		key := f.Method + " " + f.URL + " " + strconv.Itoa(f.Status) + " " + f.ErrorKind
		if i, ok := at[key]; ok {
			counts[i]++
			continue
		}
		at[key] = len(out)
		out = append(out, f)
		counts = append(counts, 1)
	}
	return out, counts
}

func (s *Server) registerRead() {
	ro := mcp.WithReadOnlyHintAnnotation(true)

	s.add(mcp.NewTool("get_status",
		mcp.WithDescription("Daemon state: proxy address, flow count, active rules, paused breakpoints, whether recording."),
		ro,
	), func(ctx context.Context, k *conn, _ mcp.CallToolRequest) (any, error) {
		st, err := k.c.Status(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"running": st.Running, "proxy": st.ProxyAddr, "flows": st.Flows, "recording": st.Recording,
			"active_rules": st.ActiveRules, "paused_breakpoints": st.PendingBreakpoints, "version": st.Version,
			"you": s.owner, "read_only": s.opts.ReadOnly,
		}, nil
	})

	s.add(mcp.NewTool("summarize_traffic",
		mcp.WithDescription("Start here. Totals, status groups, failing endpoints grouped (with the last flow id of each) and the slowest request for a time window."),
		mcp.WithString("since", mcp.Description(`Window like "5m", "1h", or "all"`), mcp.DefaultString("5m")),
		ro,
	), func(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
		since := req.GetString("since", "5m")
		if since == "all" {
			since = ""
		}
		sum, err := k.c.Summary(ctx, since)
		if err != nil {
			return nil, err
		}
		for i := range sum.Failures {
			sum.Failures[i].LastID = s.ids.short(sum.Failures[i].LastID)
		}
		if sum.Slowest != nil {
			sum.Slowest.ID = s.ids.short(sum.Slowest.ID)
		}
		failures, cut := fit(sum.Failures, 4000)
		hosts, _ := fit(sum.TopHosts, 1500)
		out := map[string]any{
			"total": sum.Total, "errors": sum.Errors, "by_status": sum.ByStatus, "by_error_kind": sum.ByError,
			"top_hosts": hosts, "bytes_up": sum.BytesUp, "bytes_down": sum.BytesDown, "avg_ms": int(sum.AvgMs),
			"failures": failures, "slowest": sum.Slowest,
		}
		if cut {
			out["hint"] = "more failure groups exist; use list_requests with status or errors_only"
		}
		return out, nil
	})

	s.add(mcp.NewTool("list_requests",
		mcp.WithDescription("Recent requests, newest first, one text line each (see format). Filter rather than paging."),
		mcp.WithNumber("limit", mcp.Description("Max rows (default 20, max 100)"), mcp.DefaultNumber(20)),
		mcp.WithString("host", mcp.Description("Host, wildcards allowed: *.example.com")),
		mcp.WithString("method", mcp.Description("GET, POST… comma separated")),
		mcp.WithString("status", mcp.Description("2xx, 404, 400-499, 4xx,5xx")),
		mcp.WithString("type", mcp.Description("Response content type: json, html, image…")),
		mcp.WithBoolean("errors_only", mcp.Description("Only failed requests (status >= 400 or transport errors)")),
		mcp.WithString("since", mcp.Description(`Only newer than, e.g. "10m"`)),
		mcp.WithString("gql_op", mcp.Description("GraphQL operation name")),
		mcp.WithString("cursor", mcp.Description("next_cursor from a previous call")),
		mcp.WithBoolean("group", mcp.Description("Fold repeats (same method, URL, status) into one ×N line"), mcp.DefaultBool(true)),
		ro,
	), func(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(min(max(req.GetInt("limit", 20), 1), 100)))
		for _, k := range []string{"host", "method", "status", "type", "since", "cursor", "gql_op"} {
			if v := req.GetString(k, ""); v != "" {
				q.Set(k, v)
			}
		}
		if req.GetBool("errors_only", false) {
			q.Set("has_error", "true")
		}
		return s.list(ctx, k, q, req.GetBool("group", true))
	})

	s.add(mcp.NewTool("search_traffic",
		mcp.WithDescription("Find requests whose URL, headers or body contain some text."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Text to look for")),
		mcp.WithString("in", mcp.Description("url (default), headers or body"), mcp.Enum("url", "headers", "body")),
		mcp.WithNumber("limit", mcp.DefaultNumber(20)),
		ro,
	), func(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
		text, err := req.RequireString("query")
		if err != nil {
			return nil, err
		}
		q := url.Values{"q": {text}, "in": {req.GetString("in", "url")}, "limit": {strconv.Itoa(min(max(req.GetInt("limit", 20), 1), 100))}}
		return s.list(ctx, k, q, true)
	})

	s.add(mcp.NewTool("get_request",
		mcp.WithDescription("One request: timing, headers and a decoded body excerpt. Use path to pick part of a JSON body, offset to read on."),
		mcp.WithString("id", mcp.Required()),
		mcp.WithString("body", mcp.Description("Which body to include"), mcp.Enum("response", "request", "both", "none"), mcp.DefaultString("response")),
		mcp.WithNumber("max_body_bytes", mcp.Description("Body excerpt size (default 2048, max 6000)"), mcp.DefaultNumber(2048)),
		mcp.WithNumber("offset", mcp.Description("Start of the excerpt in the body, for reading the rest"), mcp.DefaultNumber(0)),
		mcp.WithString("path", mcp.Description("JSON body: return only this part, e.g. $.items[0] or $.user.plan")),
		mcp.WithBoolean("all_headers", mcp.Description("Also show noise headers (date, server, CSP, sec-ch-*…), hidden by default")),
		ro,
	), s.getRequest)

	s.registerDiff(ro)

	s.add(mcp.NewTool("get_websocket_messages",
		mcp.WithDescription("Messages of a WebSocket (status 101) request, oldest first. dir up = app → server, down = server → app. Page with after = previous next."),
		mcp.WithString("id", mcp.Required(), mcp.Description("The WebSocket request id")),
		mcp.WithNumber("after", mcp.Description("Return messages after this seq"), mcp.DefaultNumber(0)),
		mcp.WithNumber("limit", mcp.Description("Max messages (default 50, max 200)"), mcp.DefaultNumber(50)),
		mcp.WithNumber("max_text", mcp.Description("Characters kept per message (default 500)"), mcp.DefaultNumber(500)),
		ro,
	), func(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
		id, err := s.flowID(ctx, k, req, "id")
		if err != nil {
			return nil, err
		}
		p, err := k.c.Messages(ctx, id, int64(req.GetInt("after", 0)), min(max(req.GetInt("limit", 50), 1), 200))
		if err != nil {
			return nil, err
		}
		n := min(max(req.GetInt("max_text", 500), 20), 4000)
		out := make([]map[string]any, 0, len(p.Messages))
		for _, m := range p.Messages {
			e := map[string]any{"seq": m.Seq, "dir": m.Dir, "type": m.Opcode, "size": m.Size, "at": m.At}
			if m.Text != nil {
				e["text"] = clip(*m.Text, n)
			} else if m.Base64 != "" {
				e["base64"] = clip(m.Base64, n)
			}
			out = append(out, e)
		}
		return map[string]any{"messages": out, "next": p.Next, "total": p.Total}, nil
	})

	s.add(mcp.NewTool("wait_for_request",
		mcp.WithDescription("Block until a request whose URL matches completes, then return it. Start this, then trigger the action in the app."),
		mcp.WithString("url", mcp.Required(), mcp.Description("URL substring or * pattern, e.g. */checkout*")),
		mcp.WithNumber("timeout_seconds", mcp.Required(), mcp.Description("1..300")),
		mcp.WithString("method"),
		mcp.WithString("status", mcp.Description("e.g. 2xx")),
		mcp.WithNumber("since_seconds", mcp.Description("Also accept a request that completed this many seconds before the call (in case the action already ran)")),
		ro,
	), func(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
		u, err := req.RequireString("url")
		if err != nil {
			return nil, err
		}
		secs, err := req.RequireInt("timeout_seconds")
		if err != nil || secs < 1 || secs > 300 {
			return nil, errorf("timeout_seconds must be 1..300")
		}
		o := client.WaitOptions{URL: u, Method: req.GetString("method", ""), Status: req.GetString("status", "")}
		if ss := req.GetInt("since_seconds", 0); ss > 0 {
			o.Since = strconv.Itoa(ss) + "s"
		}
		wctx, cancel := context.WithTimeout(ctx, time.Duration(secs)*time.Second)
		defer cancel()
		id, err := k.c.WaitFlow(wctx, o)
		if errors.Is(err, client.ErrWaitTimeout) {
			return map[string]any{"matched": false, "hint": "nothing matched in time; check the URL pattern with list_requests"}, nil
		}
		if err != nil {
			return nil, err
		}
		d, err := k.c.GetFlow(ctx, id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"matched": true, "request": s.line(&d.Flow), "hint": "call get_request with this id for headers and body"}, nil
	})

	s.add(mcp.NewTool("list_rules",
		mcp.WithDescription("All rules in evaluation order. You may only change rules whose owner is you (see get_status)."),
		ro,
	), func(ctx context.Context, k *conn, _ mcp.CallToolRequest) (any, error) {
		list, err := k.c.ListRules(ctx, "")
		if err != nil {
			return nil, err
		}
		type ruleRow struct {
			ID, Name, Type, Owner, Match string
			Enabled                      bool
			Expires                      string `json:",omitempty"`
			Hits                         int64
			Yours                        bool
		}
		rows := make([]ruleRow, len(list))
		for i, r := range list {
			m := r.Match.URL
			if len(r.Match.Methods) > 0 {
				m = strings.Join(r.Match.Methods, ",") + " " + m
			}
			rr := ruleRow{ID: r.ID, Name: clip(r.Name, 80), Type: r.Type, Owner: r.Owner, Match: clip(m, 160), Enabled: r.Enabled, Hits: r.Hits, Yours: r.Owner == s.owner}
			if r.ExpiresAt != nil {
				rr.Expires = time.Until(*r.ExpiresAt).Round(time.Second).String()
			}
			rows[i] = rr
		}
		kept, cut := fit(rows, MaxResponse-200)
		return map[string]any{"rules": kept, "truncated": cut}, nil
	})

	s.add(mcp.NewTool("wait_for_breakpoint",
		mcp.WithDescription("Block until one of your breakpoints pauses a flow, then return it for resume_breakpoint."),
		mcp.WithNumber("timeout_seconds", mcp.Required(), mcp.Description("1..300")),
		mcp.WithString("url", mcp.Description("URL substring or * pattern")),
		mcp.WithString("phase", mcp.Enum("request", "response")),
		ro,
	), s.waitBreakpoint)
}

// getRequest renders one flow within the 8 KB budget.
func (s *Server) getRequest(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
	id, err := s.flowID(ctx, k, req, "id")
	if err != nil {
		return nil, err
	}
	d, err := k.c.GetFlow(ctx, id)
	if err != nil {
		return nil, err
	}
	maxBody := min(max(req.GetInt("max_body_bytes", 2048), 64), 6000)
	offset := max(req.GetInt("offset", 0), 0)
	all, hidden := req.GetBool("all_headers", false), 0
	headers := func(hs []store.Header) map[string]string {
		m := map[string]string{}
		for _, h := range hs {
			if !all && noisyHeader(h.Name) {
				hidden++
				continue
			}
			v := clip(h.Value, 300)
			if old, ok := m[h.Name]; ok {
				v = old + ", " + v
			}
			m[h.Name] = v
		}
		return m
	}
	out := map[string]any{
		"request": s.line(&d.Flow),
		"timing_ms": map[string]int{
			"dns": int(d.Timing.DNS), "connect": int(d.Timing.Connect), "tls": int(d.Timing.TLS), "ttfb": int(d.Timing.TTFB), "total": int(d.Timing.Total),
		},
		"protocol":         d.Protocol,
		"request_headers":  headers(d.ReqHeaders),
		"response_headers": headers(d.RespHeaders),
	}
	if hidden > 0 {
		out["hidden_headers"] = hidden
	}
	if d.ErrorMsg != "" {
		out["error_message"] = clip(d.ErrorMsg, 400)
	}
	path := req.GetString("path", "")
	which := req.GetString("body", "response")
	sides := map[string][]string{"response": {"resp"}, "request": {"req"}, "both": {"req", "resp"}}[which]
	per := maxBody
	if len(sides) == 2 {
		per = maxBody / 2
	}
	for _, side := range sides {
		limit := int64(offset + per)
		if path != "" {
			limit = pathBodyMax
		}
		b, err := k.c.GetBody(ctx, id, side, limit)
		if err != nil {
			return nil, err
		}
		key := map[string]string{"req": "request_body", "resp": "response_body"}[side]
		if path != "" && b.Size > 0 && b.Encoding != "base64" {
			sel, err := selectJSON(b, path)
			if err != nil {
				out[key] = map[string]any{"size": b.Size, "error": err.Error()}
				continue
			}
			b.Data, b.Size = sel, int64(len(sel))
		}
		if b.Size == 0 {
			out[key] = map[string]any{"size": 0}
			continue
		}
		body := map[string]any{"size": b.Size, "content_type": b.ContentType}
		if b.Encoding == "base64" {
			body["binary"] = true
			body["hint"] = "binary body; not shown"
		} else {
			data := b.Data
			start := offset
			for start < len(data) && data[start]&0xC0 == 0x80 { // don't start inside a UTF-8 rune
				start++
			}
			if start < len(data) {
				data = data[start:]
			} else {
				data = ""
			}
			data = clip(data, per)
			body["offset"], body["text"] = offset, data
			if path != "" {
				body["path"] = path
			}
			if end := offset + len(data); int64(end) < b.Size {
				body["more"] = true
				body["hint"] = "call again with offset=" + strconv.Itoa(end) + " to read on"
			}
		}
		out[key] = body
	}
	// Headers can be huge (cookies); drop the largest until it fits.
	for size(out) > MaxResponse-100 {
		if !shrinkHeaders(out) {
			break
		}
	}
	return out, nil
}

// pathBodyMax caps how much of a body is parsed to select a JSON path.
const pathBodyMax = 1 << 20

// selectJSON returns the compact JSON at path in a body.
func selectJSON(b api.BodyResponse, path string) (string, error) {
	if b.Truncated {
		return "", errorf("body over 1 MB; read it with offset instead of path")
	}
	var doc any
	if err := json.Unmarshal([]byte(b.Data), &doc); err != nil {
		return "", errorf("body is not JSON; drop path")
	}
	v, err := rules.JSONGet(doc, path)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(v)
	return string(out), err
}

// noisyHeader is a header that rarely helps debugging; get_request hides these
// unless all_headers is set.
func noisyHeader(name string) bool {
	n := strings.ToLower(name)
	switch n {
	case "date", "server", "etag", "last-modified", "age", "vary", "via", "accept-ranges", "connection", "keep-alive",
		"alt-svc", "strict-transport-security", "content-security-policy", "content-security-policy-report-only",
		"x-content-type-options", "x-frame-options", "x-xss-protection", "referrer-policy", "permissions-policy",
		"report-to", "reporting-endpoints", "nel", "expect-ct", "cross-origin-opener-policy", "cross-origin-embedder-policy",
		"cross-origin-resource-policy", "x-cache", "x-cache-hits", "x-served-by", "x-timer", "cf-ray", "cf-cache-status",
		"accept-encoding", "accept-language", "upgrade-insecure-requests", "priority", "dnt", "pragma":
		return true
	}
	return strings.HasPrefix(n, "sec-ch-") || strings.HasPrefix(n, "sec-fetch-") || strings.HasPrefix(n, "x-amz-cf-")
}
