package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/hakanor/proxypilot/internal/diff"
)

// Each of the six change lists gets this much of the 8 KB answer.
const diffListBudget = 1100

func (s *Server) registerDiff(ro mcp.ToolOption) {
	s.add(mcp.NewTool("diff_flows",
		mcp.WithDescription("What changed from request a to request b: method, host, path, each query parameter, headers, status, and bodies (JSON by path like $.items[0].price, text by line). Use it to compare a working request with a failing one, or before and after an app change. Only differences are returned; secrets stay masked."),
		mcp.WithString("a", mcp.Required(), mcp.Description("Id of the first (e.g. working) request")),
		mcp.WithString("b", mcp.Required(), mcp.Description("Id of the second (e.g. failing) request")),
		ro,
	), s.diffFlows)
}

type diffSide struct {
	Line    []diff.Change `json:"line,omitempty"`
	Headers []diff.Change `json:"headers,omitempty"`
	Body    diffBody      `json:"body"`
	More    int           `json:"more,omitempty"` // changes left out to stay under 8 KB
}

type diffBody struct {
	Mode      string        `json:"mode"`
	Changes   []diff.Change `json:"changes,omitempty"`
	Lines     []diff.Line   `json:"lines,omitempty"`
	Truncated bool          `json:"truncated,omitempty"`
}

func (s *Server) diffFlows(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
	a, err := s.flowID(req, "a")
	if err != nil {
		return nil, err
	}
	b, err := s.flowID(req, "b")
	if err != nil {
		return nil, err
	}
	d, err := k.c.Diff(ctx, a, b)
	if err != nil {
		return nil, err
	}
	d.A.URL, d.B.URL = clip(d.A.URL, 180), clip(d.B.URL, 180)
	d.A.ID, d.B.ID = s.ids.short(d.A.ID), s.ids.short(d.B.ID)
	return map[string]any{
		"a": d.A, "b": d.B, "same": d.Same,
		"request": compactSide(d.Request), "response": compactSide(d.Response),
	}, nil
}

func compactSide(sd diff.Side) diffSide {
	out := diffSide{Body: diffBody{Mode: sd.Body.Mode, Truncated: sd.Body.Truncated}}
	var more int
	out.Line, more = fitChanges(sd.Line)
	out.More += more
	out.Headers, more = fitChanges(sd.Headers)
	out.More += more
	out.Body.Changes, more = fitChanges(sd.Body.Changes)
	out.More += more
	lines := make([]diff.Line, len(sd.Body.Lines))
	for i, l := range sd.Body.Lines {
		lines[i] = diff.Line{Op: l.Op, Text: clip(l.Text, 160)}
	}
	kept, cut := fit(lines, diffListBudget)
	out.Body.Lines = kept
	if cut {
		out.More += len(lines) - len(kept)
	}
	return out
}

func fitChanges(cs []diff.Change) ([]diff.Change, int) {
	short := make([]diff.Change, len(cs))
	for i, c := range cs {
		short[i] = diff.Change{Path: clip(c.Path, 120), Kind: c.Kind, A: clip(c.A, 160), B: clip(c.B, 160)}
	}
	kept, _ := fit(short, diffListBudget)
	return kept, len(short) - len(kept)
}
