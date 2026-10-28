package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/hakanor/proxypilot/internal/api"
	"github.com/hakanor/proxypilot/internal/breakpoints"
	"github.com/hakanor/proxypilot/internal/client"
	"github.com/hakanor/proxypilot/internal/proxy"
	"github.com/hakanor/proxypilot/internal/repeat"
	"github.com/hakanor/proxypilot/internal/rules"
)

const defaultTTL = "1h"

func size(v any) int {
	b, _ := json.Marshal(v)
	return len(b)
}

// shrinkHeaders drops the longest header value from a get_request result.
func shrinkHeaders(out map[string]any) bool {
	var bestMap map[string]string
	bestKey, bestLen := "", 0
	for _, k := range []string{"request_headers", "response_headers"} {
		m, _ := out[k].(map[string]string)
		for hk, hv := range m {
			if len(hv) > bestLen && hv != "[dropped: too long]" {
				bestMap, bestKey, bestLen = m, hk, len(hv)
			}
		}
	}
	if bestMap == nil || bestLen < 40 {
		return false
	}
	bestMap[bestKey] = "[dropped: too long]"
	return true
}

func ttlArg(req mcp.CallToolRequest) (string, error) {
	ttl := req.GetString("ttl", defaultTTL)
	d, err := time.ParseDuration(ttl)
	if err != nil || d <= 0 || d > 24*time.Hour {
		return "", errorf(`ttl must be a duration up to 24h, e.g. "30m"`)
	}
	return ttl, nil
}

func ruleOut(r *rules.Rule) map[string]any {
	out := map[string]any{"id": r.ID, "name": r.Name, "type": r.Type, "owner": r.Owner, "match": r.Match.URL}
	if r.ExpiresAt != nil {
		out["expires_at"] = r.ExpiresAt.Format(time.RFC3339)
	}
	return out
}

// own returns the rule if it belongs to this agent, otherwise an explanation.
func (s *Server) own(ctx context.Context, k *conn, id string) (*rules.Rule, error) {
	r, err := k.c.GetRule(ctx, id)
	if err != nil {
		return nil, err
	}
	if r.Owner != s.owner {
		return nil, errorf("rule %s belongs to %s; you can only change your own rules (%s)", id, r.Owner, s.owner)
	}
	return r, nil
}

func (s *Server) registerWrite() {
	s.add(mcp.NewTool("add_map_local",
		mcp.WithDescription("Mock responses: matching requests get this content instead of (or, with mode=body_only, on top of) the server's answer. Expires after ttl (default 1h)."),
		mcp.WithString("url", mcp.Required(), mcp.Description("URL pattern with * wildcards, e.g. https://api.example.com/v2/feed*")),
		mcp.WithArray("methods", mcp.WithStringItems(), mcp.Description("Only these methods (default any)")),
		mcp.WithString("graphql_operation", mcp.Description("Only this GraphQL operation (mock one query of a /graphql endpoint)")),
		mcp.WithNumber("status", mcp.DefaultNumber(200)),
		mcp.WithString("body", mcp.Description("Response body text (use this or file)")),
		mcp.WithString("file", mcp.Description("Absolute path of a file to serve; read on every request")),
		mcp.WithString("content_type", mcp.Description("Default: guessed from the body or file extension")),
		mcp.WithObject("headers", mcp.Description("Extra response headers {name: value}")),
		mcp.WithString("mode", mcp.Enum("full", "body_only"), mcp.DefaultString("full")),
		mcp.WithString("name"),
		mcp.WithString("ttl", mcp.DefaultString(defaultTTL)),
	), func(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
		u, err := req.RequireString("url")
		if err != nil {
			return nil, err
		}
		ttl, err := ttlArg(req)
		if err != nil {
			return nil, err
		}
		spec := map[string]any{"status": req.GetInt("status", 200), "mode": req.GetString("mode", "full")}
		switch body, file := req.GetString("body", ""), req.GetString("file", ""); {
		case file != "" && body != "":
			return nil, errorf("pass body or file, not both")
		case file != "":
			spec["file"] = file
		default:
			spec["body"] = body
		}
		headers := map[string]string{}
		if h, ok := req.GetArguments()["headers"].(map[string]any); ok {
			for k, v := range h {
				if sv, ok := v.(string); ok {
					headers[k] = sv
				}
			}
		}
		if ct := req.GetString("content_type", ""); ct != "" {
			headers["Content-Type"] = ct
		}
		if len(headers) > 0 {
			spec["headers"] = headers
		}
		raw, _ := json.Marshal(spec)
		r, err := k.c.AddRule(ctx, api.RuleInput{
			Rule: rules.Rule{Name: req.GetString("name", ""), Type: rules.TypeMapLocal, Enabled: true, Owner: s.owner,
				Match: rules.Matcher{URL: u, Methods: trimAll(req.GetStringSlice("methods", nil)), GraphQLOperation: req.GetString("graphql_operation", "")}, Spec: raw},
			TTL: ttl,
		})
		if err != nil {
			return nil, err
		}
		return ruleOut(r), nil
	})

	s.add(mcp.NewTool("add_rewrite",
		mcp.WithDescription("Edit matching traffic in flight. Steps run in order. Types: set_header/add_header {name,value}, remove_header {name}, "+
			"set_query {name,value} / remove_query {name} (request), replace_body {pattern,value,regex}, json_set {path:\"$.user.isPremium\", value:\"true\"} (value is JSON text), "+
			"json_remove {path}, set_status {status} (response), set_url {value} / set_path {value} (request). Compressed bodies are decoded first."),
		mcp.WithString("url", mcp.Required(), mcp.Description("URL pattern with * wildcards")),
		mcp.WithString("phase", mcp.Enum("request", "response", "both"), mcp.DefaultString("response")),
		mcp.WithArray("steps", mcp.Required(), mcp.Description("Ordered steps, e.g. [{\"type\":\"json_set\",\"path\":\"$.user.isPremium\",\"value\":\"true\"}]")),
		mcp.WithArray("methods", mcp.WithStringItems()),
		mcp.WithString("name"),
		mcp.WithString("ttl", mcp.DefaultString(defaultTTL)),
	), func(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
		u, err := req.RequireString("url")
		if err != nil {
			return nil, err
		}
		ttl, err := ttlArg(req)
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(req.GetArguments()["steps"])
		var steps []rules.Step
		if err := json.Unmarshal(raw, &steps); err != nil || len(steps) == 0 {
			return nil, errorf("steps must be a non-empty list of step objects")
		}
		spec, _ := json.Marshal(rules.RewriteSpec{Steps: steps})
		r, err := k.c.AddRule(ctx, api.RuleInput{
			Rule: rules.Rule{Name: req.GetString("name", ""), Type: rules.TypeRewrite, Enabled: true, Owner: s.owner,
				Phase: rules.Phase(req.GetString("phase", "response")), Match: rules.Matcher{URL: u, Methods: trimAll(req.GetStringSlice("methods", nil))}, Spec: spec},
			TTL: ttl,
		})
		if err != nil {
			return nil, err
		}
		return ruleOut(r), nil
	})

	s.add(mcp.NewTool("add_map_remote",
		mcp.WithDescription("Send matching requests to another server, e.g. prod → staging or a local dev server. A trailing * on `to` keeps the rest of the original path."),
		mcp.WithString("url", mcp.Required(), mcp.Description("URL pattern, e.g. https://api.example.com/*")),
		mcp.WithString("to", mcp.Required(), mcp.Description("Target, e.g. https://staging.example.com/* or http://localhost:3000")),
		mcp.WithBoolean("preserve_host_header", mcp.Description("Keep the original Host header")),
		mcp.WithString("name"),
		mcp.WithString("ttl", mcp.DefaultString(defaultTTL)),
	), func(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
		u, err := req.RequireString("url")
		if err != nil {
			return nil, err
		}
		to, err := req.RequireString("to")
		if err != nil {
			return nil, err
		}
		ttl, err := ttlArg(req)
		if err != nil {
			return nil, err
		}
		spec, _ := json.Marshal(rules.MapRemoteSpec{To: to, PreserveHostHeader: req.GetBool("preserve_host_header", false)})
		r, err := k.c.AddRule(ctx, api.RuleInput{
			Rule: rules.Rule{Name: req.GetString("name", ""), Type: rules.TypeMapRemote, Enabled: true, Owner: s.owner, Match: rules.Matcher{URL: u}, Spec: spec},
			TTL:  ttl,
		})
		if err != nil {
			return nil, err
		}
		return ruleOut(r), nil
	})

	s.add(mcp.NewTool("repeat_request",
		mcp.WithDescription("Send a recorded request again, optionally edited (rules apply unless bypass_rules). Returns the new ids with status and time."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Flow id to repeat")),
		mcp.WithObject("set_headers", mcp.Description("{name: value} to set")),
		mcp.WithArray("remove_headers", mcp.WithStringItems()),
		mcp.WithObject("set_query", mcp.Description("{name: value} query parameters to set")),
		mcp.WithString("body", mcp.Description("Replacement body")),
		mcp.WithString("method"),
		mcp.WithString("url"),
		mcp.WithNumber("count", mcp.Description("Times to send (1..10)"), mcp.DefaultNumber(1)),
		mcp.WithNumber("concurrency", mcp.Description("How many at once (1..5)"), mcp.DefaultNumber(1)),
		mcp.WithBoolean("bypass_rules"),
	), func(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
		id, err := s.flowID(ctx, k, req, "id")
		if err != nil {
			return nil, err
		}
		count, conc := req.GetInt("count", 1), req.GetInt("concurrency", 1)
		if count < 1 || count > 10 || conc < 1 || conc > 5 {
			return nil, errorf("count must be 1..10 and concurrency 1..5")
		}
		args := req.GetArguments()
		strMap := func(key string) map[string]string {
			m, ok := args[key].(map[string]any)
			if !ok {
				return nil
			}
			out := map[string]string{}
			for k, v := range m {
				if sv, ok := v.(string); ok {
					out[k] = sv
				}
			}
			return out
		}
		in := api.RepeatRequest{Options: repeat.Options{Count: count, Concurrency: conc, BypassRules: req.GetBool("bypass_rules", false)}}
		in.Method, in.URL = req.GetString("method", ""), req.GetString("url", "")
		in.SetHeaders, in.SetQuery = strMap("set_headers"), strMap("set_query")
		in.RemoveHeaders = req.GetStringSlice("remove_headers", nil)
		if _, ok := args["body"]; ok {
			b := req.GetString("body", "")
			in.Body = &b
		}
		sum, err := k.c.Repeat(ctx, id, in)
		if err != nil {
			return nil, err
		}
		for i := range sum.Results {
			sum.Results[i].FlowID = s.ids.short(sum.Results[i].FlowID)
		}
		return sum, nil
	})

	s.add(mcp.NewTool("set_breakpoint",
		mcp.WithDescription("Pause matching requests (before the server) or responses (before the app); then wait_for_breakpoint and resume_breakpoint. Unanswered flows continue after timeout."),
		mcp.WithString("url", mcp.Required(), mcp.Description("URL pattern with * wildcards")),
		mcp.WithString("phase", mcp.Enum("request", "response", "both"), mcp.DefaultString("request")),
		mcp.WithArray("methods", mcp.WithStringItems()),
		mcp.WithString("graphql_operation", mcp.Description("Only this GraphQL operation")),
		mcp.WithString("timeout", mcp.Description(`How long a flow waits for your decision, e.g. "60s" (max 30m)`), mcp.DefaultString("60s")),
		mcp.WithString("name"),
		mcp.WithString("ttl", mcp.DefaultString(defaultTTL)),
	), func(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
		u, err := req.RequireString("url")
		if err != nil {
			return nil, err
		}
		ttl, err := ttlArg(req)
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(map[string]string{"timeout": req.GetString("timeout", "60s")})
		r, err := k.c.AddRule(ctx, api.RuleInput{
			Rule: rules.Rule{Name: req.GetString("name", ""), Type: rules.TypeBreakpoint, Enabled: true, Owner: s.owner,
				Phase: rules.Phase(req.GetString("phase", "request")), Match: rules.Matcher{URL: u, Methods: trimAll(req.GetStringSlice("methods", nil)), GraphQLOperation: req.GetString("graphql_operation", "")}, Spec: raw},
			TTL: ttl,
		})
		if err != nil {
			return nil, err
		}
		return ruleOut(r), nil
	})

	s.add(mcp.NewTool("resume_breakpoint",
		mcp.WithDescription("Continue, edit (modify) or abort a flow paused by your breakpoint. Edits apply to the paused phase (request or response)."),
		mcp.WithString("id", mcp.Required(), mcp.Description("Breakpoint id (bp_…)")),
		mcp.WithString("action", mcp.Required(), mcp.Enum("continue", "modify", "abort")),
		mcp.WithNumber("status", mcp.Description("Response phase: new status code")),
		mcp.WithObject("set_headers", mcp.Description("{name: value} to set")),
		mcp.WithArray("remove_headers", mcp.WithStringItems()),
		mcp.WithString("body", mcp.Description("Replacement body")),
		mcp.WithString("method", mcp.Description("Request phase: new method")),
		mcp.WithString("url", mcp.Description("Request phase: new URL (same scheme)")),
	), s.resumeBreakpoint)

	s.add(mcp.NewTool("remove_rule",
		mcp.WithDescription("Delete one of your rules."),
		mcp.WithString("id", mcp.Required()),
		mcp.WithDestructiveHintAnnotation(true),
	), func(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
		id, err := req.RequireString("id")
		if err != nil {
			return nil, err
		}
		if _, err := s.own(ctx, k, id); err != nil {
			return nil, err
		}
		if err := k.c.DeleteRule(ctx, id); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": id}, nil
	})

	s.add(mcp.NewTool("clear_agent_rules",
		mcp.WithDescription("Delete every rule you created. Do this when you are done mocking or intercepting."),
		mcp.WithDestructiveHintAnnotation(true),
	), func(ctx context.Context, k *conn, _ mcp.CallToolRequest) (any, error) {
		n, err := k.c.DeleteRules(ctx, s.owner)
		if err != nil {
			return nil, err
		}
		return map[string]any{"deleted": n, "owner": s.owner}, nil
	})

	s.add(mcp.NewTool("set_throttle",
		mcp.WithDescription("Simulate a slow network (latency, then limited bandwidth) to reproduce timeouts and slow-loading bugs. Affects everyone using the proxy: set enabled=false when done."),
		mcp.WithBoolean("enabled", mcp.Required()),
		mcp.WithString("profile", mcp.Enum("edge", "3g", "lte", "bad-wifi", "custom"), mcp.Description("Preset; custom uses down_kbps, up_kbps, latency_ms")),
		mcp.WithNumber("down_kbps", mcp.Description("custom: download kbit/s (0 = unlimited)")),
		mcp.WithNumber("up_kbps", mcp.Description("custom: upload kbit/s (0 = unlimited)")),
		mcp.WithNumber("latency_ms", mcp.Description("custom: delay added before each request")),
		mcp.WithArray("hosts", mcp.WithStringItems(), mcp.Description(`Only throttle these hosts ("api.example.com", "*.example.com"); omit for all traffic`)),
	), func(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
		in := proxy.ThrottleSettings{Enabled: req.GetBool("enabled", false), Hosts: req.GetStringSlice("hosts", nil)}
		in.Name = req.GetString("profile", "3g")
		if in.Name == "custom" {
			in.DownKbps, in.UpKbps, in.LatencyMs = req.GetInt("down_kbps", 0), req.GetInt("up_kbps", 0), req.GetInt("latency_ms", 0)
		}
		if !in.Enabled {
			in = proxy.ThrottleSettings{}
		}
		return k.c.SetThrottle(ctx, in)
	})

	s.add(mcp.NewTool("clear_session",
		mcp.WithDescription("Delete all recorded traffic (the user's too). Only do this when the user asked for a clean slate."),
		mcp.WithDestructiveHintAnnotation(true),
	), func(ctx context.Context, k *conn, _ mcp.CallToolRequest) (any, error) {
		if err := k.c.Clear(ctx); err != nil {
			return nil, err
		}
		return map[string]any{"cleared": true}, nil
	})
}

// pausedOut renders a paused flow (redacted unless --allow-secrets) within the budget.
func (s *Server) pausedOut(p *breakpoints.Paused) map[string]any {
	hdrs := func(hs []breakpoints.Header) map[string]string {
		m := map[string]string{}
		for _, h := range hs {
			m[h.Name] = clip(h.Value, 200)
		}
		return m
	}
	out := map[string]any{
		"id": p.ID, "flow_id": s.ids.short(p.FlowID), "phase": p.Phase, "rule": p.RuleName, "owner": p.Owner,
		"seconds_left": int(time.Until(p.ExpiresAt).Seconds()), "on_timeout": p.OnTimeout,
		"request": map[string]any{"method": p.Request.Method, "url": clip(p.Request.URL, 300), "headers": hdrs(p.Request.Headers), "body": clip(p.Request.Body, 2000)},
	}
	if p.Response != nil {
		out["response"] = map[string]any{"status": p.Response.Status, "headers": hdrs(p.Response.Headers), "body": clip(p.Response.Body, 2500)}
	}
	return out
}

func (s *Server) waitBreakpoint(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
	secs, err := req.RequireInt("timeout_seconds")
	if err != nil || secs < 1 || secs > 300 {
		return nil, errorf("timeout_seconds must be 1..300")
	}
	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	u, phase := req.GetString("url", ""), req.GetString("phase", "")
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return map[string]any{"paused": false, "hint": "nothing paused in time; is the breakpoint rule matching? see list_rules"}, nil
		}
		p, err := k.c.WaitBreakpoint(ctx, left, u, phase)
		var ae *client.APIError
		if errors.As(err, &ae) && ae.Status == 408 {
			continue
		}
		if err != nil {
			return nil, err
		}
		if p.Owner != s.owner {
			// Someone else's breakpoint (the user decides it); wait for the next one.
			select {
			case <-time.After(200 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if list, err := k.c.ListBreakpoints(ctx); err == nil {
				for _, x := range list {
					if x.Owner == s.owner && (u == "" || client.URLMatcher(u)(x.Request.URL)) && (phase == "" || x.Phase == phase) {
						return s.pausedOut(x), nil
					}
				}
			}
			continue
		}
		return s.pausedOut(p), nil
	}
}

func (s *Server) resumeBreakpoint(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return nil, err
	}
	action, err := req.RequireString("action")
	if err != nil {
		return nil, err
	}
	// Edits are built from the unredacted original so masked secrets are never sent upstream.
	p, err := k.raw.GetBreakpoint(ctx, id, true)
	if err != nil {
		return nil, err
	}
	if p.Owner != s.owner {
		return nil, errorf("breakpoint %s is held by %s; only they can resume it", id, p.Owner)
	}
	res := breakpoints.Resolution{Action: action, By: s.owner}
	args := req.GetArguments()
	edit := func(hs []breakpoints.Header) []breakpoints.Header {
		for _, name := range req.GetStringSlice("remove_headers", nil) {
			hs = dropHeader(hs, name)
		}
		if m, ok := args["set_headers"].(map[string]any); ok {
			for k, v := range m {
				if sv, ok := v.(string); ok {
					hs = append(dropHeader(hs, k), breakpoints.Header{Name: k, Value: sv})
				}
			}
		}
		return hs
	}
	if action == breakpoints.ActionModify {
		_, hasBody := args["body"]
		if p.Phase == breakpoints.PhaseRequest {
			r := p.Request
			if m := req.GetString("method", ""); m != "" {
				r.Method = strings.ToUpper(m)
			}
			if u := req.GetString("url", ""); u != "" {
				r.URL = u
			}
			r.Headers = edit(r.Headers)
			if hasBody {
				r.Body, r.BodyBase64 = req.GetString("body", ""), ""
			}
			res.Request = &r
		} else {
			r := *p.Response
			if st := req.GetInt("status", 0); st != 0 {
				r.Status = st
			}
			r.Headers = edit(r.Headers)
			if hasBody {
				r.Body, r.BodyBase64 = req.GetString("body", ""), ""
			}
			res.Response = &r
		}
	}
	if err := k.c.ResumeBreakpoint(ctx, id, res); err != nil {
		return nil, err
	}
	return map[string]any{"resumed": id, "action": action}, nil
}

func dropHeader(hs []breakpoints.Header, name string) []breakpoints.Header {
	out := hs[:0:0]
	for _, h := range hs {
		if !strings.EqualFold(h.Name, name) {
			out = append(out, h)
		}
	}
	return out
}
