// Package mcpserver exposes SOP runbooks to MCP clients (an LLM agent, an
// orchestration framework, another service) as three tools: read_sop,
// validate_step, and execute_step. execute_step is gated by
// verify's barrier certificate: a step only actually executes if the
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
	"errors"
	"fmt"
	"sort"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
	"github.com/sharedcode/joltrin/v5/verify"
)

// New builds an MCP server with read_sop, validate_step, and execute_step
// registered against store. With no options it behaves exactly as it always
// has; WithMemory adds the optional record of earlier blocks.
func New(store *runbookstore.Store, opts ...Option) *server.MCPServer {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}
	var serverOpts []server.ServerOption
	if cfg.log != nil {
		if text := instructions(store, cfg.log); text != "" {
			serverOpts = append(serverOpts, server.WithInstructions(text))
		}
		cfg.writeLessons(store)
	}
	s := server.NewMCPServer("joltrin-runbook-server", "0.1.0", serverOpts...)

	s.AddTool(
		mcp.NewTool("read_sop",
			mcp.WithDescription("Read the steps, preconditions, and postconditions of a registered SOP runbook."),
			mcp.WithString("workflow", mcp.Required(), mcp.Description("Name the runbook was registered under.")),
		),
		readSOPHandler(store),
	)

	s.AddTool(
		mcp.NewTool("validate_step",
			mcp.WithDescription("Check whether a step would be safe to execute right now, without executing it. Does not modify the trace. Never returns a bare error: an unknown workflow or step comes back with the server's actual inventory, and a blocked step comes back with the missing state and which registered steps would establish it."),
			mcp.WithString("workflow", mcp.Required(), mcp.Description("Name of the runbook.")),
			mcp.WithString("trace_id", mcp.Required(), mcp.Description("Identifies this execution's trace; steps already executed under this ID are what preconditions are checked against.")),
			mcp.WithString("step", mcp.Required(), mcp.Description("ID of the step to validate.")),
			mcp.WithOutputSchema[ValidateStepResult](),
		),
		validateStepHandler(store),
	)

	s.AddTool(
		mcp.NewTool("execute_step",
			mcp.WithDescription("Execute a step. Blocked server-side if the safety barrier check fails, an agent cannot bypass this by asserting a precondition was met. Never returns a bare error: an unknown workflow or step comes back with the server's actual inventory, and a blocked step comes back with the missing state and which registered steps would establish it. Safe to retry: pass idempotency_key and a retried call after a dropped or timed-out response returns the original outcome instead of executing (or re-checking) again."),
			mcp.WithString("workflow", mcp.Required(), mcp.Description("Name of the runbook.")),
			mcp.WithString("trace_id", mcp.Required(), mcp.Description("Identifies this execution's trace.")),
			mcp.WithString("step", mcp.Required(), mcp.Description("ID of the step to execute.")),
			mcp.WithString("idempotency_key", mcp.Description("Optional. A unique ID for this specific call, chosen by the caller. Retrying with the same key after a lost response returns the original result rather than executing the step again. A key names one call: reusing it for a different step is refused, and a replayed blocked result carries a hint to use a new key after running the missing steps. Omit it and every call is treated as new, matching the pre-existing behavior.")),
			mcp.WithOutputSchema[ExecuteStepResult](),
		),
		executeStepHandler(store, cfg),
	)

	// read_lessons exists only when memory is on, so a server without it shows
	// the same three tools it always has.
	if cfg.log != nil {
		s.AddTool(
			mcp.NewTool("read_lessons",
				mcp.WithDescription("List what earlier runs on this server got blocked on and the order of steps that works. Call it before you start a task. It is advice only: the barrier still checks every call."),
				mcp.WithString("workflow", mcp.Description("Optional. Only lessons for this runbook.")),
				mcp.WithOutputSchema[ReadLessonsResult](),
			),
			readLessonsHandler(store, cfg),
		)
	}

	return s
}

func readLessonsHandler(store *runbookstore.Store, cfg *config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		lessons := collectLessons(store, cfg.log, req.GetString("workflow", ""))
		if lessons == nil {
			lessons = []Lesson{} // an empty list, not null
		}
		return mcp.NewToolResultStructuredOnly(ReadLessonsResult{Lessons: lessons}), nil
	}
}

func readSOPHandler(store *runbookstore.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name := req.GetString("workflow", "")
		wf, ok := store.Workflow(name)
		if !ok {
			return unknownWorkflowResult(store, name), nil
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
			return unknownWorkflowResult(store, name), nil
		}
		if _, ok := wf.Steps[verify.StepID(stepID)]; !ok {
			return unknownStepResult(wf, name, stepID), nil
		}
		if traceID == "" {
			return missingTraceResult(), nil
		}
		trace := store.TraceFor(traceID)

		err := wf.CheckSafety(trace, verify.StepID(stepID))
		if err == nil {
			return mcp.NewToolResultStructuredOnly(ValidateStepResult{Safe: true}), nil
		}
		var v *verify.Violation
		if !errors.As(err, &v) {
			// Not reachable given the step-existence check above already
			// rules out the only non-Violation error CheckSafety returns
			// (unknown step). Fail closed to the same malformed-request
			// shape rather than a bare error if that invariant ever changes.
			return unknownStepResult(wf, name, stepID), nil
		}
		return mcp.NewToolResultStructuredOnly(ValidateStepResult{
			Safe:   false,
			Reason: blockReason(wf, v),
		}), nil
	}
}

func executeStepHandler(store *runbookstore.Store, cfg *config) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name := req.GetString("workflow", "")
		stepID := req.GetString("step", "")
		traceID := req.GetString("trace_id", "")
		idempotencyKey := req.GetString("idempotency_key", "")

		wf, ok := store.Workflow(name)
		if !ok {
			return unknownWorkflowResult(store, name), nil
		}
		if _, ok := wf.Steps[verify.StepID(stepID)]; !ok {
			return unknownStepResult(wf, name, stepID), nil
		}
		if traceID == "" {
			return missingTraceResult(), nil
		}
		trace := store.TraceFor(traceID)

		// The barrier certificate: verify before acting, never act then
		// verify. A failed check here means the step never executes, full
		// stop, regardless of what the calling agent asserted about its own
		// prior actions. CheckAndCommitIdempotent holds the trace lock
		// across the check and the commit, so a concurrent request on the
		// same trace_id cannot land between the two, and (when
		// idempotency_key is set) a retried request gets the original
		// outcome back instead of being recomputed or double-committed.
		replayed, err := wf.CheckAndCommitIdempotent(trace, verify.StepID(stepID), idempotencyKey)
		var reused *verify.KeyReusedError
		if errors.As(err, &reused) {
			return keyReusedResult(reused), nil
		}
		if err == nil {
			return mcp.NewToolResultStructuredOnly(ExecuteStepResult{
				Executed: true,
				Step:     stepID,
				Trace:    trace.ExecutedSteps(),
				Replayed: replayed,
			}), nil
		}
		var v *verify.Violation
		if !errors.As(err, &v) {
			return unknownStepResult(wf, name, stepID), nil
		}
		if !replayed {
			cfg.recordBlock(store, name, traceID, stepID, wf, v)
		}
		res := ExecuteStepResult{
			Executed: false,
			Blocked:  blockReason(wf, v),
			Replayed: replayed,
		}
		if replayed {
			res.Hint = replayedBlockHint
		}
		return mcp.NewToolResultStructuredOnly(res), nil
	}
}

// replayedBlockHint is added to a blocked result that was replayed from an
// idempotency_key, so an agent that has fixed what was missing knows the
// answer is the old one and a new key will be checked afresh.
const replayedBlockHint = "This is the original blocked result, returned again because this idempotency_key was already used. If you have since run the steps in established_by_steps, retry with a new idempotency_key to get a fresh check."

// blockReason turns a *verify.Violation into the structured explanation
// validate_step and execute_step return: which check blocked it, what
// state is missing, and which registered steps (if any) would establish it.
func blockReason(wf *verify.Workflow, v *verify.Violation) *BlockReason {
	return &BlockReason{
		BlockedBy:     v.Rule,
		MissingState:  v.MissingState,
		Message:       v.Message,
		EstablishedBy: wf.StepsThatEstablish(v.MissingState),
	}
}

// unknownWorkflowResult reports a malformed request (a workflow name this
// server never registered) as an error result carrying the server's actual
// inventory, not a bare "unknown workflow" string: nothing the caller does
// with a trace can fix this, so the correction is which name to use instead.
func unknownWorkflowResult(store *runbookstore.Store, name string) *mcp.CallToolResult {
	r := UnknownWorkflowResult{
		Error:     fmt.Sprintf("unknown workflow %q", name),
		Workflow:  name,
		Available: store.WorkflowNames(),
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{mcp.NewTextContent(r.Error)},
		StructuredContent: r,
		IsError:           true,
	}
}

// missingTraceResult rejects a call with no trace_id. An empty id would put
// every caller that left it out into one shared trace, so steps one run had
// finished would count for another run's checks.
func missingTraceResult() *mcp.CallToolResult {
	return mcp.NewToolResultError("trace_id is required. It names this run: a step is checked against the steps already run under the same trace_id, so a call without one is refused instead of sharing a trace with other callers.")
}

// keyReusedResult reports an idempotency_key that was already used for a
// different step as an error result. Nothing was checked or run.
func keyReusedResult(e *verify.KeyReusedError) *mcp.CallToolResult {
	r := KeyReusedResult{
		Error:          e.Error(),
		IdempotencyKey: e.Key,
		UsedForStep:    e.UsedFor,
		RequestedStep:  e.Requested,
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{mcp.NewTextContent(r.Error)},
		StructuredContent: r,
		IsError:           true,
	}
}

// unknownStepResult reports a malformed request (a step ID not registered
// on an otherwise-known workflow) the same way: an error result carrying
// the workflow's actual step inventory.
func unknownStepResult(wf *verify.Workflow, workflowName, step string) *mcp.CallToolResult {
	steps := make([]verify.StepID, 0, len(wf.Steps))
	for id := range wf.Steps {
		steps = append(steps, id)
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i] < steps[j] })
	r := UnknownStepResult{
		Error:    fmt.Sprintf("unknown step %q in workflow %q", step, workflowName),
		Workflow: workflowName,
		Step:     step,
		Steps:    steps,
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{mcp.NewTextContent(r.Error)},
		StructuredContent: r,
		IsError:           true,
	}
}

// describeWorkflow renders a Workflow's steps into read_sop's result shape.
// verify.Step's fields are already exported and JSON-friendly, this is
// mostly about presenting them in a stable, documented shape rather than
// exposing the internal Workflow struct layout directly.
func describeWorkflow(wf *verify.Workflow) WorkflowDescription {
	steps := make(map[string]StepDescription, len(wf.Steps))
	for id, step := range wf.Steps {
		steps[string(id)] = StepDescription{
			Requires:    step.Requires,
			Establishes: step.Establishes,
		}
	}
	safety := make([]SafetyRuleDescription, 0, len(wf.Safety))
	for _, r := range wf.Safety {
		safety = append(safety, SafetyRuleDescription{
			Name:      r.Name,
			Forbidden: r.Forbidden,
			Requires:  r.Requires,
		})
	}
	return WorkflowDescription{Steps: steps, Safety: safety}
}
