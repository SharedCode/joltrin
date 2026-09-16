package mcpserver

import "github.com/sharedcode/joltrin/ai/verify"

// This file defines the structured (JSON) result shapes read_sop,
// validate_step, and execute_step return, and the tool output schemas
// mcp.WithOutputSchema derives from them (see server.go). A blocked or
// malformed call never returns a bare error string: it returns one of
// these, so a calling agent can branch on fields instead of parsing text,
// and always gets at least "what's missing" and "what would fix it," never
// just "no" or a flat 0/1 confidence with nothing behind it.
//
// A malformed request (unknown workflow or step) gets its own shape
// (UnknownWorkflowResult, UnknownStepResult) and is reported with
// CallToolResult.IsError true, since no precondition could ever make it
// succeed. A barrier block (BlockReason) is not an error: the request was
// well-formed and could succeed later once its precondition is met, so it
// is reported as a normal, non-error result with Safe/Executed false. This
// mirrors the distinction tools/a2aagent already draws between
// TaskStateFailed and TaskStateInputRequired for the same two cases.

// BlockReason explains why a step was not safe to execute: either its own
// precondition has not been established yet, or it would violate a named
// SafetyRule.
type BlockReason struct {
	// BlockedBy is "precondition" when the step's own Requires state hasn't
	// been established yet, or the name of the SafetyRule it would violate.
	BlockedBy string `json:"blocked_by" jsonschema_description:"'precondition' or the name of the violated safety rule."`
	// MissingState is the specific state that has not been established in
	// this trace yet.
	MissingState verify.State `json:"missing_state" jsonschema_description:"The state that must be established before this step is safe."`
	// Message is the human-readable form of the same fact, for logging or
	// display; agents should branch on the fields above, not parse this.
	Message string `json:"message" jsonschema_description:"Human-readable explanation of the block."`
	// EstablishedBy lists every registered step whose Establishes includes
	// MissingState, so the caller has a concrete next action rather than
	// just the name of a gap. Empty if no step in this workflow establishes
	// it, which itself is useful evidence: the workflow may need a step
	// added, not just a different call order.
	EstablishedBy []verify.StepID `json:"established_by_steps,omitempty" jsonschema_description:"Steps that would establish missing_state, if any are registered."`
}

// ValidateStepResult is validate_step's result for a well-formed request.
// Safe is always meaningful, and Reason is populated whenever Safe is false.
type ValidateStepResult struct {
	Safe   bool         `json:"safe" jsonschema_description:"True if the step is safe to execute right now."`
	Reason *BlockReason `json:"reason,omitempty" jsonschema_description:"Present only when safe is false."`
}

// ExecuteStepResult is execute_step's result for a well-formed request.
// Executed and Blocked are mutually exclusive and exhaustive: a blocked
// call never advances the trace, so Step and Trace are only meaningful
// when Executed is true, and Blocked is only present when it is false.
type ExecuteStepResult struct {
	Executed bool            `json:"executed" jsonschema_description:"True if the step ran and the trace advanced."`
	Step     string          `json:"step,omitempty" jsonschema_description:"The step ID that executed. Empty when executed is false."`
	Trace    []verify.StepID `json:"trace,omitempty" jsonschema_description:"Every step committed to this trace so far, in order, including this one. Empty when executed is false."`
	Blocked  *BlockReason    `json:"blocked,omitempty" jsonschema_description:"Present only when executed is false."`
	// Replayed is true when idempotency_key was supplied and had already
	// been seen for this trace: the rest of the result is the original
	// outcome (success or blocked), not a fresh check. Always false when no
	// idempotency_key was given.
	Replayed bool `json:"replayed" jsonschema_description:"True if this call was a retry recognized by idempotency_key: the result is the original outcome, replayed, not a fresh check."`
}

// UnknownWorkflowResult is returned instead of a plain error when a caller
// names a workflow this server has never heard of: a typo or a guess, not
// something establishing a missing precondition could ever fix, so it gets
// the server's actual inventory back instead of just "no."
type UnknownWorkflowResult struct {
	Error     string   `json:"error" jsonschema_description:"Human-readable description of the problem."`
	Workflow  string   `json:"workflow" jsonschema_description:"The workflow name that was requested."`
	Available []string `json:"available_workflows" jsonschema_description:"Workflow names actually registered on this server, sorted. Empty means none are registered yet."`
}

// UnknownStepResult is returned instead of a plain error when workflow is
// real but step is not one of its registered steps: also a malformed
// request, not a barrier block, so it is never reported alongside a real
// precondition gap.
type UnknownStepResult struct {
	Error    string          `json:"error" jsonschema_description:"Human-readable description of the problem."`
	Workflow string          `json:"workflow" jsonschema_description:"The workflow the step was requested against."`
	Step     string          `json:"step" jsonschema_description:"The step ID that was requested."`
	Steps    []verify.StepID `json:"available_steps" jsonschema_description:"Step IDs actually registered on this workflow, sorted."`
}

// StepDescription is one step's preconditions and postconditions, as
// returned by read_sop.
type StepDescription struct {
	Requires    []verify.State `json:"requires"`
	Establishes []verify.State `json:"establishes"`
}

// SafetyRuleDescription is one safety rule, as returned by read_sop.
type SafetyRuleDescription struct {
	Name      string       `json:"name"`
	Forbidden verify.State `json:"forbidden"`
	Requires  verify.State `json:"requires"`
}

// WorkflowDescription is read_sop's result for a well-formed request: the
// full step graph and safety rules for one registered runbook.
type WorkflowDescription struct {
	Steps  map[string]StepDescription `json:"steps"`
	Safety []SafetyRuleDescription    `json:"safety"`
}
