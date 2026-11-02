package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerPrompts adds ready-made tasks that clients show as slash commands.
func (s *Server) registerPrompts() {
	add := func(name, desc, arg, argDesc, text string) {
		s.mcp.AddPrompt(mcp.NewPrompt(name, mcp.WithPromptDescription(desc), mcp.WithArgument(arg, mcp.ArgumentDescription(argDesc))),
			func(_ context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return mcp.NewGetPromptResult(desc, []mcp.PromptMessage{
					mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(text+"\n\n"+arg+": "+req.Params.Arguments[arg])),
				}), nil
			})
	}
	add("investigate_failure", "Find out why a request or feature fails", "what", "The failing feature or endpoint",
		"Investigate this failure with ProxyPilot. Run summarize_traffic, find the failing request, and read it with get_request. Find a working request to the same endpoint and compare them with diff_flows. Report the cause and the evidence. Change nothing.")
	add("mock_endpoint", "Mock an endpoint to test the app", "endpoint", "URL pattern and the response you want",
		"Mock this endpoint with ProxyPilot. Look at a real response with search_traffic and get_request so the mock keeps its shape, then add it with add_map_local. Ask me to trigger the action, confirm it with wait_for_request, and call clear_agent_rules when done.")
	add("check_change", "Verify that an app change sends the right request", "expected", "What the request should look like",
		"Verify this with ProxyPilot. Start wait_for_request, ask me to trigger the action, then read the request with get_request and compare it with what is expected. Report any difference.")
}

func (s *Server) exportHAR(ctx context.Context, k *conn, req mcp.CallToolRequest) (any, error) {
	var ids []string
	for _, id := range req.GetStringSlice("ids", nil) {
		full, err := s.ids.resolve(id)
		if err != nil {
			return nil, err
		}
		ids = append(ids, full)
	}
	if len(ids) == 0 {
		return nil, errorf("ids must list at least one request")
	}
	b, err := k.c.ExportHAR(ctx, ids)
	if err != nil {
		return nil, err
	}
	// Always a new file in the temp folder: an agent must not choose where this writes.
	f, err := os.CreateTemp("", "proxypilot-"+time.Now().Format("20060102-150405")+"-*.har")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		return nil, err
	}
	return map[string]any{"file": filepath.Clean(f.Name()), "bytes": len(b), "requests": len(ids), "secrets_masked": true}, nil
}
