package nats

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/sharedcode/joltrin/ai/verify"
)

// BarrierDecision is the structured event VerifyBridge publishes to NATS
// every time it runs a barrier check. It carries enough detail for an
// external subscriber to reconstruct what happened without re-deriving it
// from the workflow graph: which step, which workflow, whether it was
// allowed, and if not, which rule and which missing state blocked it.
type BarrierDecision struct {
	Time     time.Time     `json:"time"`
	Workflow string        `json:"workflow,omitempty"`
	Step     verify.StepID `json:"step"`
	// Allowed is true when the step passed the barrier (CheckSafety
	// returned nil). It is false both for a blocked *verify.Violation and
	// for a malformed request (an unknown step), Rule/MissingState
	// distinguish the two: a Violation always sets Rule, a malformed
	// request never does.
	Allowed bool `json:"allowed"`
	// Replayed is true when the decision came from
	// CheckAndCommitIdempotent's idempotency cache rather than a fresh
	// check, i.e. this event describes a retried call, not a new one.
	Replayed bool `json:"replayed,omitempty"`
	// Rule is the SafetyRule.Name that blocked this step, or the literal
	// "precondition" when the step's own precondition was not met. Empty
	// when Allowed is true or the request was malformed.
	Rule string `json:"rule,omitempty"`
	// MissingState is the State that still needs to be established before
	// this step can pass, taken from verify.Violation.MissingState. Empty
	// when Allowed is true or the request was malformed.
	MissingState verify.State `json:"missing_state,omitempty"`
	// Reason is the blocking error's message, verbatim, for a human
	// reading the event directly. Empty when Allowed is true.
	Reason string `json:"reason,omitempty"`
}

// DefaultSubject is the NATS subject a VerifyBridge publishes to when
// constructed with an empty workflow name.
const DefaultSubject = "joltrin.verify.decision"

// SubjectFor returns the NATS subject a VerifyBridge publishes
// BarrierDecision events to for the given workflow name. An empty name
// returns DefaultSubject.
func SubjectFor(workflow string) string {
	if workflow == "" {
		return DefaultSubject
	}
	return fmt.Sprintf("joltrin.verify.%s.decision", workflow)
}

// VerifyBridge wraps a *verify.Workflow and publishes a BarrierDecision
// event to NATS after every barrier check it runs. It does not change
// ai/verify's behavior: CheckSafety, CheckAndCommit, and
// CheckAndCommitIdempotent each call straight through to the identically
// named *verify.Workflow method and return its exact result unchanged; the
// NATS publish is a side effect on the way out. A publish failure is never
// allowed to change or block the barrier's own decision, see publish
// below.
//
// A VerifyBridge is only ever a decorator a caller opts into at its own
// call sites. Nothing in ai/verify, tools/mcpserver, or tools/a2aagent
// constructs or requires one.
type VerifyBridge struct {
	wf       *verify.Workflow
	nc       *nats.Conn
	workflow string
	subject  string

	// onPublishError, if set, is called with any error returned by the
	// underlying NATS publish. Optional: a VerifyBridge with no handler set
	// simply drops a publish failure, exactly like fire-and-forget metrics
	// or logging, rather than letting it surface as a barrier error.
	onPublishError func(error)
}

// NewVerifyBridge returns a VerifyBridge that runs barrier checks against
// wf and publishes a BarrierDecision for each one to nc, on the subject
// SubjectFor(workflowName). workflowName is a label only, used to build the
// subject and to tag published events; it does not have to match any name
// wf is registered under in a tools/runbookstore.Store.
func NewVerifyBridge(wf *verify.Workflow, nc *nats.Conn, workflowName string) *VerifyBridge {
	return &VerifyBridge{
		wf:       wf,
		nc:       nc,
		workflow: workflowName,
		subject:  SubjectFor(workflowName),
	}
}

// OnPublishError sets a handler called with any error the underlying NATS
// publish returns, and returns the bridge for chaining. Optional; without
// one, a publish failure is dropped silently.
func (b *VerifyBridge) OnPublishError(fn func(error)) *VerifyBridge {
	b.onPublishError = fn
	return b
}

// Subject returns the NATS subject this bridge publishes to.
func (b *VerifyBridge) Subject() string {
	return b.subject
}

// CheckSafety runs wf.CheckSafety(trace, next), publishes the resulting
// BarrierDecision, and returns wf.CheckSafety's exact result.
func (b *VerifyBridge) CheckSafety(trace *verify.Trace, next verify.StepID) error {
	err := b.wf.CheckSafety(trace, next)
	b.publish(next, false, err)
	return err
}

// CheckAndCommit runs wf.CheckAndCommit(trace, next), publishes the
// resulting BarrierDecision, and returns wf.CheckAndCommit's exact result.
func (b *VerifyBridge) CheckAndCommit(trace *verify.Trace, next verify.StepID) error {
	err := b.wf.CheckAndCommit(trace, next)
	b.publish(next, false, err)
	return err
}

// CheckAndCommitIdempotent runs
// wf.CheckAndCommitIdempotent(trace, next, idempotencyKey), publishes the
// resulting BarrierDecision (with Replayed set from the same call), and
// returns wf.CheckAndCommitIdempotent's exact result.
func (b *VerifyBridge) CheckAndCommitIdempotent(trace *verify.Trace, next verify.StepID, idempotencyKey string) (replayed bool, err error) {
	replayed, err = b.wf.CheckAndCommitIdempotent(trace, next, idempotencyKey)
	b.publish(next, replayed, err)
	return replayed, err
}

func (b *VerifyBridge) publish(step verify.StepID, replayed bool, err error) {
	ev := BarrierDecision{
		Time:     time.Now().UTC(),
		Workflow: b.workflow,
		Step:     step,
		Allowed:  err == nil,
		Replayed: replayed,
	}
	if err != nil {
		ev.Reason = err.Error()
		var violation *verify.Violation
		if errors.As(err, &violation) {
			ev.Rule = violation.Rule
			ev.MissingState = violation.MissingState
		}
	}

	data, err := json.Marshal(ev)
	if err != nil {
		// A BarrierDecision is a fixed, JSON-safe shape; this would only
		// fail if that shape changes to include something unmarshalable.
		// Report it the same way a publish failure is reported, rather than
		// silently dropping a bug.
		if b.onPublishError != nil {
			b.onPublishError(fmt.Errorf("nats: marshal barrier decision: %w", err))
		}
		return
	}

	if err := b.nc.Publish(b.subject, data); err != nil && b.onPublishError != nil {
		b.onPublishError(fmt.Errorf("nats: publish barrier decision: %w", err))
	}
}
