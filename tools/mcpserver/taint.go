package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/sharedcode/joltrin/v5/taint"
)

// WithTaint adds a check_tool_call tool that asks a taint.Barrier whether a
// tool call may send labeled data where it is going. Without this option the
// server's tool list is unchanged.
//
// An agent that calls the tool itself is checking its own plan, so treat the
// answer as advice. To enforce it, run the same barrier from a hook or proxy
// the agent cannot skip, such as "sop-mcp-server taint check".
func WithTaint(b *taint.Barrier) Option {
	return func(c *config) { c.taint = b }
}

func addTaintTool(s *server.MCPServer, b *taint.Barrier) {
	s.AddTool(
		mcp.NewTool("check_tool_call",
			mcp.WithDescription("Before you call a tool that sends data somewhere, ask whether it may. Returns allowed or blocked with a reason code and which labeled values (by handle, never the value) were found in the arguments, in any common encoding. A blocked call must not be made. Do not retry it with the data encoded differently."),
			mcp.WithString("tool", mcp.Required(), mcp.Description("Name of the tool you intend to call.")),
			mcp.WithString("destination", mcp.Description("Where the call sends data: a URL, host[:port] or absolute file path. Omit for a call that sends nowhere.")),
			mcp.WithObject("args", mcp.Description("The arguments you intend to pass.")),
			mcp.WithOutputSchema[taint.Verdict](),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			call := taint.Call{
				Tool:        req.GetString("tool", ""),
				Destination: req.GetString("destination", ""),
				Args:        req.GetArguments()["args"],
				Actor:       "mcp",
			}
			if call.Tool == "" {
				return mcp.NewToolResultError("tool is required"), nil
			}
			v, _ := b.Check(ctx, call)
			return mcp.NewToolResultStructuredOnly(v), nil
		},
	)
}
