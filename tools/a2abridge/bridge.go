// Package a2abridge lets an MCP client (Claude Desktop, Claude Code, or any
// other MCP-speaking agent) drive a runbook served over Agent2Agent (A2A)
// by tools/a2aagent, without speaking A2A itself. Claude has no native A2A
// client; MCP is the protocol it actually knows. This package exposes the
// remote agent's execute_step skill as an MCP tool of the same name and
// translates each call into a real A2A task delegation over the wire
// (resolving the agent's card, then a2aclient.SendMessage), translating the
// resulting Task state back into an MCP tool result:
//
//   - TaskStateCompleted becomes a structured success result carrying the
//     step executed and any artifacts the remote agent attached.
//   - TaskStateInputRequired becomes a tool error prefixed "blocked by
//     safety barrier", the same "blocked, not failed" distinction
//     tools/a2aagent itself makes: the task is well-formed and could still
//     succeed once its precondition is met.
//   - TaskStateFailed becomes a tool error carrying the agent's own
//     failure reason (unknown workflow, malformed request, etc).
//
// Built on the official a2aclient (github.com/a2aproject/a2a-go/a2aclient),
// not a hand-rolled JSON-RPC client, the same principle tools/a2aagent and
// tools/mcpserver already follow for their own protocol libraries.
package a2abridge

import (
	"context"
	"fmt"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2aclient"
	"github.com/a2aproject/a2a-go/a2aclient/agentcard"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// New resolves the A2A agent card at agentBaseURL (a GET against
// agentBaseURL + /.well-known/agent-card.json) and returns an MCP server
// exposing that agent's execute_step skill as an MCP tool of the same name.
// Fails fast if the card can't be resolved or no compatible transport can
// be established: a bridge pointed at an unreachable or misconfigured
// agent should never look alive at start-up.
func New(ctx context.Context, agentBaseURL string) (*server.MCPServer, error) {
	card, err := agentcard.DefaultResolver.Resolve(ctx, agentBaseURL)
	if err != nil {
		return nil, fmt.Errorf("a2abridge: resolve agent card at %s: %w", agentBaseURL, err)
	}
	return NewFromCard(ctx, card)
}

// NewFromCard builds the bridge from an already-resolved agent card,
// exported so tests (and callers who already have the card, e.g. from a
// prior discovery step) can skip the network round-trip New makes.
func NewFromCard(ctx context.Context, card *a2a.AgentCard) (*server.MCPServer, error) {
	c, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		return nil, fmt.Errorf("a2abridge: connect to agent %q: %w", card.Name, err)
	}
	return newServer(card, c), nil
}

func newServer(card *a2a.AgentCard, c *a2aclient.Client) *server.MCPServer {
	s := server.NewMCPServer("sop-a2a-bridge", "0.1.0")

	s.AddTool(
		mcp.NewTool("execute_step",
			mcp.WithDescription(fmt.Sprintf(
				"Delegate execution of one runbook step to the remote A2A agent %q over the network, gated by that agent's own safety barrier. Blocked (task moves to input-required, not failed) if the step's precondition hasn't been established yet in this trace.",
				card.Name,
			)),
			mcp.WithString("workflow", mcp.Required(), mcp.Description("Name of the runbook registered on the remote agent.")),
			mcp.WithString("trace_id", mcp.Required(), mcp.Description("Identifies this execution's trace on the remote agent.")),
			mcp.WithString("step", mcp.Required(), mcp.Description("ID of the step to execute.")),
			mcp.WithString("idempotency_key", mcp.Description("Optional. Forwarded to the remote agent as-is. Retrying with the same key after a lost or timed-out response returns the original outcome instead of executing the step again on the remote agent.")),
		),
		executeStepHandler(c),
	)

	return s
}

func executeStepHandler(c *a2aclient.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		workflow := req.GetString("workflow", "")
		traceID := req.GetString("trace_id", "")
		step := req.GetString("step", "")
		idempotencyKey := req.GetString("idempotency_key", "")
		if workflow == "" || traceID == "" || step == "" {
			return mcp.NewToolResultError("workflow, trace_id, and step are all required"), nil
		}

		data := map[string]any{
			"workflow": workflow,
			"trace_id": traceID,
			"step":     step,
		}
		if idempotencyKey != "" {
			data["idempotency_key"] = idempotencyKey
		}
		result, err := c.SendMessage(ctx, &a2a.MessageSendParams{
			Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.DataPart{Data: data}),
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("a2abridge: sending step %q to remote agent: %v", step, err)), nil
		}

		task, ok := result.(*a2a.Task)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("a2abridge: remote agent returned a %T for step %q, expected a Task", result, step)), nil
		}

		switch task.Status.State {
		case a2a.TaskStateCompleted:
			return mcp.NewToolResultStructuredOnly(map[string]any{
				"executed":  step,
				"artifacts": artifactData(task),
			}), nil
		case a2a.TaskStateInputRequired:
			return mcp.NewToolResultError(fmt.Sprintf("blocked by safety barrier: %s", statusText(task))), nil
		case a2a.TaskStateFailed:
			return mcp.NewToolResultError(fmt.Sprintf("remote agent reported failure: %s", statusText(task))), nil
		default:
			return mcp.NewToolResultError(fmt.Sprintf("a2abridge: unexpected task state %q for step %q", task.Status.State, step)), nil
		}
	}
}

// statusText extracts the human-readable reason a2aagent attached to a
// non-completed task's status message, falling back to the bare state name
// if the agent didn't attach one.
func statusText(task *a2a.Task) string {
	if task.Status.Message != nil {
		for _, part := range task.Status.Message.Parts {
			if tp, ok := part.(a2a.TextPart); ok {
				return tp.Text
			}
		}
	}
	return string(task.Status.State)
}

// artifactData flattens a completed task's artifacts into a slice of their
// DataPart contents, mirroring how tools/mcpserver's own execute_step
// result shapes its "trace" field, so a caller sees the same information
// regardless of which protocol actually executed the step.
func artifactData(task *a2a.Task) []map[string]any {
	out := make([]map[string]any, 0, len(task.Artifacts))
	for _, artifact := range task.Artifacts {
		// Artifacts is []*Artifact, and a remote agent answering
		// "artifacts":[null] decodes to a nil element. Dereferencing that
		// would panic and take the bridge (an MCP server) down on input a
		// peer controls, so skip it.
		if artifact == nil {
			continue
		}
		for _, part := range artifact.Parts {
			if dp, ok := part.(a2a.DataPart); ok {
				out = append(out, dp.Data)
			}
		}
	}
	return out
}
