// Package mcpserver exposes SOP runbooks to MCP clients (an LLM agent, an
// orchestration framework, another service) as three tools: read_sop,
// validate_step, and execute_step. execute_step is gated by
// ai/verify's barrier certificate: a step only actually executes if the
// safety check passes first, an agent cannot skip a precondition by
// asserting it did, the check is enforced server-side against the trace
// this server holds, not against whatever the agent claims.
//
// Built on github.com/mark3labs/mcp-go rather than a hand-rolled JSON-RPC
// transport, that library implements the actual MCP wire protocol
// (initialize, tools/list, tools/call, both stdio and streamable-HTTP
// transports) to spec; this package's job is just the three tool handlers.
// The runbook state itself lives in tools/runbookstore.Store, shared with
// tools/a2aagent, so a step executed through either protocol is visible to
// the other.
package mcpserver

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/sharedcode/joltrin/ai/verify"
	"github.com/sharedcode/joltrin/tools/runbookstore"
)

// New builds an MCP server with read_sop, validate_step, and execute_step
// registered against store.
func New(store *runbookstore.Store) *server.MCPServer {
	s := server.NewMCPServer("joltrin-runbook-server", "0.1.0")

	s.AddTool(
		mcp.NewTool("read_sop",
			mcp.WithDescription("Read the steps, preconditions, and postconditions of a registered SOP runbook."),
			mcp.WithString("workflow", mcp.Required(), mcp.Description("Name the runbook was registered under.")),
		),
		readSOPHandler(store),
	)

	s.AddTool(
		mcp.NewTool("validate_step",
			mcp.WithDescription("Check whether a step would be safe to execute right now, without executing it. Does not modify the trace."),
			mcp.WithString("workflow", mcp.Required(), mcp.Description("Name of the runbook.")),
			mcp.WithString("trace_id", mcp.Required(), mcp.Description("Identifies this execution's trace; steps already executed under this ID are what preconditions are checked against.")),
			mcp.WithString("step", mcp.Required(), mcp.Description("ID of the step to validate.")),
		),
		validateStepHandler(store),
	)

	s.AddTool(
		mcp.NewTool("execute_step",
			mcp.WithDescription("Execute a step. Blocked server-side if the safety barrier check fails, an agent cannot bypass this by asserting a precondition was met."),
			mcp.WithString("workflow", mcp.Required(), mcp.Description("Name of the runbook.")),
			mcp.WithString("trace_id", mcp.Required(), mcp.Description("Identifies this execution's trace.")),
			mcp.WithString("step", mcp.Required(), mcp.Description("ID of the step to execute.")),
		),
		executeStepHandler(store),
	)

	return s
}

func readSOPHandler(store *runbookstore.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name := req.GetString("workflow", "")
		wf, ok := store.Workflow(name)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("unknown workflow %q", name)), nil
		}
		return mcp.NewToolResultStructuredOnly(describeWorkflow(wf)), nil
	}
}

func validateStepHandler(store *runbookstore.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name := req.GetString("workflow", "")
		stepID := req.GetString("step", "")
		traceID := req.GetString("trace_id", "")

		wf, ok := store.Workflow(name)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("unknown workflow %q", name)), nil
		}
		trace := store.TraceFor(traceID)

		if err := wf.CheckSafety(trace, verify.StepID(stepID)); err != nil {
			return mcp.NewToolResultStructuredOnly(map[string]any{
				"safe":   false,
				"reason": err.Error(),
			}), nil
		}
		return mcp.NewToolResultStructuredOnly(map[string]any{
			"safe": true,
		}), nil
	}
}

func executeStepHandler(store *runbookstore.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name := req.GetString("workflow", "")
		stepID := req.GetString("step", "")
		traceID := req.GetString("trace_id", "")

		wf, ok := store.Workflow(name)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("unknown workflow %q", name)), nil
		}
		trace := store.TraceFor(traceID)

		// The barrier certificate: verify before acting, never act then
		// verify. A failed check here means the step never executes, full
		// stop, regardless of what the calling agent asserted about its own
		// prior actions. CheckAndCommit holds the trace lock across both
		// halves, so a concurrent request on the same trace_id cannot land
		// between the check and the commit.
		if err := wf.CheckAndCommit(trace, verify.StepID(stepID)); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("blocked by safety barrier: %s", err.Error())), nil
		}

		return mcp.NewToolResultStructuredOnly(map[string]any{
			"executed": stepID,
			"trace":    trace.ExecutedSteps(),
		}), nil
	}
}

// describeWorkflow renders a Workflow's steps into a plain map, since
// verify.Step's fields are already exported and JSON-friendly, this is
// mostly about presenting them in a stable, tool-result-shaped form rather
// than exposing the internal Workflow struct layout directly.
func describeWorkflow(wf *verify.Workflow) map[string]any {
	steps := make(map[string]any, len(wf.Steps))
	for id, step := range wf.Steps {
		steps[string(id)] = map[string]any{
			"requires":    step.Requires,
			"establishes": step.Establishes,
		}
	}
	safety := make([]map[string]any, 0, len(wf.Safety))
	for _, r := range wf.Safety {
		safety = append(safety, map[string]any{
			"name":      r.Name,
			"forbidden": r.Forbidden,
			"requires":  r.Requires,
		})
	}
	return map[string]any{
		"steps":  steps,
		"safety": safety,
	}
}
