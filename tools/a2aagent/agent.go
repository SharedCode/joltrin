// Package a2aagent exposes the same SOP runbook execution capability as
// tools/mcpserver, over the Agent2Agent (A2A) protocol instead of MCP: a
// task-delegation shape for orchestrators that speak A2A rather than MCP's
// tool-call shape. Both packages share one tools/runbookstore.Store, so a
// step executed through either protocol advances the same trace the other
// can see.
//
// Built on the official github.com/a2aproject/a2a-go SDK, not a hand-rolled
// implementation of the wire protocol.
//
// One correction versus the brief that requested this package: the A2A spec
// (and this SDK) serves the agent card at /.well-known/agent-card.json
// (see a2asrv.WellKnownAgentCardPath), not /.well-known/agent.json. Wired up
// per the SDK's actual constant below, not the brief's guess at the path.
package a2aagent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"github.com/a2aproject/a2a-go/a2asrv/eventqueue"

	"github.com/sharedcode/joltrin/ai/verify"
	"github.com/sharedcode/joltrin/tools/runbookstore"
)

// AgentCard describes this agent's one skill for A2A discovery. Callers
// typically serve this from a2asrv.NewStaticAgentCardHandler at
// a2asrv.WellKnownAgentCardPath; see NewMux in http.go for the wiring.
func AgentCard(baseURL string) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:               "sop-runbook-agent",
		Description:        "Executes SOP runbook steps, gated by a safety and reachability barrier certificate (ai/verify) so a step can never run out of order or into an unrecoverable state.",
		URL:                baseURL,
		Version:            "0.1.0",
		PreferredTransport: a2a.TransportProtocolJSONRPC,
		Capabilities:       a2a.AgentCapabilities{},
		DefaultInputModes:  []string{"application/json"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Skills: []a2a.AgentSkill{
			{
				ID:          "execute_step",
				Name:        "Execute SOP runbook step",
				Description: "Delegate execution of one runbook step. Blocked (task moves to input-required, not failed) if the step's precondition hasn't been established yet in this trace.",
				Examples: []string{
					`{"workflow":"db-maintenance","trace_id":"incident-42","step":"drop_prod_db"}`,
				},
			},
		},
	}
}

// stepRequest is the structured payload this agent expects as a DataPart on
// the delegating message.
type stepRequest struct {
	Workflow string
	TraceID  string
	Step     string
}

// Executor implements a2asrv.AgentExecutor, delegating the actual safety
// check and commit to the same ai/verify.Workflow logic tools/mcpserver
// uses, against the same shared runbookstore.Store.
type Executor struct {
	Store *runbookstore.Store
}

// NewExecutor returns an Executor backed by store.
func NewExecutor(store *runbookstore.Store) *Executor {
	return &Executor{Store: store}
}

func (e *Executor) Execute(ctx context.Context, reqCtx *a2asrv.RequestContext, queue eventqueue.Queue) error {
	if reqCtx.StoredTask == nil {
		if err := queue.Write(ctx, a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateSubmitted, nil)); err != nil {
			return fmt.Errorf("a2aagent: write submitted: %w", err)
		}
	}
	if err := queue.Write(ctx, a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateWorking, nil)); err != nil {
		return fmt.Errorf("a2aagent: write working: %w", err)
	}

	req, err := parseStepRequest(reqCtx.Message)
	if err != nil {
		return e.fail(ctx, reqCtx, queue, err.Error())
	}

	wf, ok := e.Store.Workflow(req.Workflow)
	if !ok {
		// A typo or a guess, not something establishing a missing
		// precondition could ever fix, so the correction is the server's
		// actual inventory, not just "no." Mirrors
		// tools/mcpserver.unknownWorkflowResult's reasoning for the same case.
		return e.fail(ctx, reqCtx, queue, fmt.Sprintf(
			"unknown workflow %q (available: %s)", req.Workflow, strings.Join(e.Store.WorkflowNames(), ", "),
		))
	}
	if _, ok := wf.Steps[verify.StepID(req.Step)]; !ok {
		steps := make([]string, 0, len(wf.Steps))
		for id := range wf.Steps {
			steps = append(steps, string(id))
		}
		sort.Strings(steps)
		return e.fail(ctx, reqCtx, queue, fmt.Sprintf(
			"unknown step %q in workflow %q (available: %s)", req.Step, req.Workflow, strings.Join(steps, ", "),
		))
	}
	trace := e.Store.TraceFor(req.TraceID)

	// The same barrier certificate tools/mcpserver's execute_step tool
	// gates on: verify before acting, never act then verify. CheckAndCommit
	// does both under one trace lock, so a concurrent request on the same
	// trace_id cannot slip in between the check and the commit.
	if err := wf.CheckAndCommit(trace, verify.StepID(req.Step)); err != nil {
		var v *verify.Violation
		if !errors.As(err, &v) {
			// The step-existence check above already rules out the only
			// non-Violation error CheckAndCommit returns (unknown step).
			// Fail closed rather than treat an unexpected error as a
			// recoverable block if that invariant ever changes.
			return e.fail(ctx, reqCtx, queue, err.Error())
		}
		// Not a failure: the task is well-formed and could still succeed
		// once its precondition is met, that's exactly what
		// TaskStateInputRequired means, "paused, waiting on something
		// before it can proceed", as opposed to TaskStateFailed's "this
		// task cannot succeed." The DataPart carries the same fields
		// tools/mcpserver's execute_step returns in its Blocked result, so
		// an A2A caller gets the identical structured signal an MCP caller
		// would, not just a text string to parse.
		established := wf.StepsThatEstablish(v.MissingState)
		establishedIDs := make([]string, len(established))
		for i, id := range established {
			establishedIDs[i] = string(id)
		}
		event := a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateInputRequired,
			a2a.NewMessageForTask(a2a.MessageRoleAgent, reqCtx,
				a2a.TextPart{Text: fmt.Sprintf("blocked by safety barrier: %s", err.Error())},
				a2a.DataPart{Data: map[string]any{
					"blocked_by":           v.Rule,
					"missing_state":        string(v.MissingState),
					"message":              v.Message,
					"established_by_steps": establishedIDs,
				}},
			))
		event.Final = true
		if werr := queue.Write(ctx, event); werr != nil {
			return fmt.Errorf("a2aagent: write input-required: %w", werr)
		}
		return nil
	}

	// The task store this SDK defaults to gob-encodes artifact data
	// internally, which panics-to-error on an unregistered named type
	// inside an any-typed map value (verify.StepID is a named string type).
	// Flatten to plain strings before handing this off, found by this
	// package's own end-to-end test, not by inspection.
	executed := trace.ExecutedSteps()
	executedIDs := make([]string, len(executed))
	for i, id := range executed {
		executedIDs[i] = string(id)
	}
	artifactEvent := a2a.NewArtifactEvent(reqCtx, a2a.DataPart{
		Data: map[string]any{
			"executed": req.Step,
			"trace":    executedIDs,
		},
	})
	if err := queue.Write(ctx, artifactEvent); err != nil {
		return fmt.Errorf("a2aagent: write artifact: %w", err)
	}

	completed := a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateCompleted, nil)
	completed.Final = true
	if err := queue.Write(ctx, completed); err != nil {
		return fmt.Errorf("a2aagent: write completed: %w", err)
	}
	return nil
}

func (e *Executor) Cancel(ctx context.Context, reqCtx *a2asrv.RequestContext, queue eventqueue.Queue) error {
	event := a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateCanceled, nil)
	event.Final = true
	return queue.Write(ctx, event)
}

func (e *Executor) fail(ctx context.Context, reqCtx *a2asrv.RequestContext, queue eventqueue.Queue, reason string) error {
	event := a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateFailed,
		a2a.NewMessageForTask(a2a.MessageRoleAgent, reqCtx, a2a.TextPart{Text: reason}))
	event.Final = true
	return queue.Write(ctx, event)
}

// parseStepRequest extracts a stepRequest from the first DataPart on msg.
func parseStepRequest(msg *a2a.Message) (stepRequest, error) {
	if msg == nil {
		return stepRequest{}, fmt.Errorf("a2aagent: no message on request")
	}
	for _, part := range msg.Parts {
		dp, ok := part.(a2a.DataPart)
		if !ok {
			continue
		}
		var req stepRequest
		if v, ok := dp.Data["workflow"].(string); ok {
			req.Workflow = v
		}
		if v, ok := dp.Data["trace_id"].(string); ok {
			req.TraceID = v
		}
		if v, ok := dp.Data["step"].(string); ok {
			req.Step = v
		}
		if req.Workflow == "" || req.TraceID == "" || req.Step == "" {
			return stepRequest{}, fmt.Errorf("a2aagent: message data part must include workflow, trace_id, and step")
		}
		return req, nil
	}
	return stepRequest{}, fmt.Errorf("a2aagent: message has no data part with workflow/trace_id/step")
}
