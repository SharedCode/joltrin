// Package verify checks a finite operational workflow (a runbook: a set of
// steps with preconditions and postconditions) against two classes of
// property before letting an agent execute a step:
//
//   - Safety (precedence): a forbidden state must never occur unless a
//     required state occurred first. Example: "the production database can
//     never be dropped unless a backup was already validated."
//   - Reachability: a target state must remain reachable from wherever
//     execution currently stands. Example: "a rollback path must always
//     exist, even after a partial failure."
//
// What this is: an explicit-state graph checker over a finite workflow.
// Safety checking is the "P is preceded by Q" pattern from Dwyer, Avrunin,
// and Corbett's property specification patterns (1999), the same
// specification family most LTL model checkers verify against; here it's
// checked directly against an accumulated state set rather than compiled
// into a temporal-logic formula and run through a model checker. Reachability
// checking is plain graph reachability (BFS) over the step graph.
//
// What this is not: a general-purpose LTL/CTL model checker. There is no
// temporal-operator parser, no Büchi automaton construction, no support for
// infinite-trace properties or probabilistic model checking. For a finite
// runbook with an enumerable set of states, that generality isn't needed to
// get a correct answer, an explicit precondition/postcondition graph is
// exactly the right level of formalism, and a much smaller trusted
// computing base to verify by reading the source.
package verify

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// State is a named fact that becomes true once some step establishes it.
// States are opaque strings from this package's point of view, e.g.
// "backup_validated" or "prod_db_dropped".
type State string

// StepID identifies one step in a Workflow.
type StepID string

// Step is one unit of a workflow. It requires a set of States to already
// hold (its precondition) and, once it completes, establishes a set of new
// States (its postcondition).
type Step struct {
	ID          StepID
	Requires    []State
	Establishes []State
}

// SafetyRule is a precedence property: Forbidden must never become
// established unless Requires was already established first.
type SafetyRule struct {
	Name      string
	Forbidden State
	Requires  State
}

// ReachabilityRule asserts that Target must remain reachable, via some
// sequence of steps, from every state in the workflow graph. Used to verify
// properties like "rollback is always reachable."
type ReachabilityRule struct {
	Name   string
	Target State
}

// Workflow is the full transition graph for one runbook: its steps, plus
// the safety and reachability properties every execution trace must satisfy.
type Workflow struct {
	Steps        map[StepID]Step
	Safety       []SafetyRule
	Reachability []ReachabilityRule
}

// NewWorkflow builds a Workflow from a step list, indexing steps by ID.
// Returns an error if two steps share an ID.
func NewWorkflow(steps []Step, safety []SafetyRule, reachability []ReachabilityRule) (*Workflow, error) {
	index := make(map[StepID]Step, len(steps))
	for _, s := range steps {
		if _, exists := index[s.ID]; exists {
			return nil, fmt.Errorf("verify: duplicate step ID %q", s.ID)
		}
		index[s.ID] = s
	}
	return &Workflow{Steps: index, Safety: safety, Reachability: reachability}, nil
}

// maxIdempotencyKeysPerTrace bounds how many distinct idempotency keys one
// Trace retains, evicting the oldest first, the same reasoning as
// runbookstore.DefaultMaxTraces: tools/a2aagent exposes traces over the
// network, so a per-key cache needs a cap or a client can grow it without
// limit just by sending fresh keys. A legitimate caller retrying a handful
// of steps needs nowhere near this many. Eviction here is safe in the same
// direction runbookstore's trace eviction is: an evicted key just reverts
// that one retry to pre-idempotency behavior (it re-executes and may
// duplicate a trace entry), not to something less safe than before this
// mechanism existed.
const maxIdempotencyKeysPerTrace = 256

// Trace is the ordered record of steps executed so far in one run of a
// Workflow, plus the accumulated set of States those steps established.
//
// A Trace is shared: tools/runbookstore hands the same *Trace to every
// request carrying the same trace_id, and those requests can arrive
// concurrently on different protocols (MCP, A2A) against the same store.
// The mutex below guards Executed, Holds, and commits for that reason;
// without it, one goroutine committing while another checks is a data race
// on Holds, which the Go runtime turns into an unrecoverable "concurrent
// map read and map write" process abort, i.e. a remote kill switch on any
// server built from this package. Callers must not touch the fields
// directly; use CheckSafety, Commit, CheckAndCommit,
// CheckAndCommitIdempotent, and ExecutedSteps.
type Trace struct {
	mu       sync.Mutex
	Executed []StepID
	Holds    map[State]bool
	// commits caches the outcome of a CheckAndCommitIdempotent call by its
	// idempotency key, so a retried call with the same key gets back the
	// exact original outcome (success or the same Violation) instead of
	// being recomputed against however the trace has changed since. That is
	// what actually makes a retry safe: the caller cannot get a different
	// answer to the same question just by asking twice, and a step never
	// gets double-committed to Executed because its client timed out and
	// retried. commitOrder tracks insertion order for the eviction above.
	commits     map[string]error
	commitOrder []string
}

// NewTrace starts an empty execution trace.
func NewTrace() *Trace {
	return &Trace{Holds: make(map[State]bool), commits: make(map[string]error)}
}

// ExecutedSteps returns a copy of the steps committed to this trace so far.
// A copy, not the live slice, so a caller can render it (into an MCP tool
// result or an A2A artifact) while another request is still appending.
func (t *Trace) ExecutedSteps() []StepID {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]StepID, len(t.Executed))
	copy(out, t.Executed)
	return out
}

// Violation is returned when a step is blocked by the barrier: either its
// precondition has not been established in this trace yet, or a SafetyRule
// forbids the state it would establish. It is distinct from a malformed
// request (an unknown step, say) so a caller can tell "blocked, but could
// succeed later" apart from "this can never succeed", which is exactly the
// distinction A2A draws between input-required and failed.
type Violation struct {
	Rule    string
	Message string
	// MissingState is the specific State that has not been established yet
	// in this trace: the step's own precondition for a "precondition"
	// Violation, or the SafetyRule's Requires state for a rule violation.
	// Exposed as its own field, not just folded into Message, so a caller
	// (tools/mcpserver, tools/a2aagent) can look up which steps would
	// establish it via Workflow.StepsThatEstablish and hand the agent a
	// concrete next action instead of a string to parse.
	MissingState State
}

func (v *Violation) Error() string { return v.Message }

// IsViolation reports whether err is a barrier Violation as opposed to a
// malformed-request error.
func IsViolation(err error) bool {
	var v *Violation
	return errors.As(err, &v)
}

// CheckSafety is the barrier certificate: before executing `next`, verify
// that doing so would not establish a Forbidden state without its Requires
// state already holding. Returns nil if `next` is safe to execute, or an
// error identifying which SafetyRule it would violate and why.
func (w *Workflow) CheckSafety(trace *Trace, next StepID) error {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return w.checkSafetyLocked(trace, next)
}

// checkSafetyLocked is CheckSafety's body, minus the locking, so
// CheckAndCommit can run the check and the commit under one acquisition
// rather than releasing between them.
func (w *Workflow) checkSafetyLocked(trace *Trace, next StepID) error {
	step, ok := w.Steps[next]
	if !ok {
		return fmt.Errorf("verify: unknown step %q", next)
	}

	// Precondition check: every state this step requires must already hold.
	for _, req := range step.Requires {
		if !trace.Holds[req] {
			return &Violation{
				Rule:         "precondition",
				Message:      fmt.Sprintf("step %q requires state %q, which has not been established in this trace", next, req),
				MissingState: req,
			}
		}
	}

	// Safety check: for every state this step would establish, verify no
	// SafetyRule forbids establishing it without its Requires state already
	// holding.
	for _, est := range step.Establishes {
		for _, rule := range w.Safety {
			if rule.Forbidden != est {
				continue
			}
			if !trace.Holds[rule.Requires] {
				return &Violation{
					Rule: rule.Name,
					Message: fmt.Sprintf(
						"step %q would establish forbidden state %q without required state %q first (rule: %s)",
						next, est, rule.Requires, rule.Name,
					),
					MissingState: rule.Requires,
				}
			}
		}
	}

	return nil
}

// CheckAndCommit is the barrier certificate as a single atomic operation:
// it verifies next is safe and, only if it is, commits it, without
// releasing the trace lock in between. Prefer it over a CheckSafety call
// followed by a Commit call, which leaves a window where a concurrent
// request can commit against the same trace between the two.
//
// A blocked step returns a *Violation (see IsViolation); a malformed one
// (unknown step) returns a plain error, and neither mutates the trace.
//
// CheckAndCommit does not deduplicate retries: calling it twice for the
// same step appends twice to Executed. Callers reachable over a protocol a
// client might retry after a timeout (tools/mcpserver, tools/a2aagent)
// should use CheckAndCommitIdempotent instead.
func (w *Workflow) CheckAndCommit(trace *Trace, next StepID) error {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return w.checkAndCommitLocked(trace, next)
}

// CheckAndCommitIdempotent is CheckAndCommit, made safe to retry: if
// idempotencyKey is non-empty and this trace has already processed that
// exact key, it returns the original outcome (replayed=true) without
// re-checking safety or re-committing, whether that original outcome was a
// success or a blocked *Violation. A caller that timed out waiting for a
// response and is not sure whether the first attempt landed can retry with
// the same key and get back the true answer to "what actually happened,"
// not a fresh recomputation that might disagree with it.
//
// An empty idempotencyKey disables the cache for that call and behaves
// exactly like CheckAndCommit (replayed is always false), so this is
// purely additive: a caller that never passes a key sees no behavior
// change from adding this method.
func (w *Workflow) CheckAndCommitIdempotent(trace *Trace, next StepID, idempotencyKey string) (replayed bool, err error) {
	trace.mu.Lock()
	defer trace.mu.Unlock()

	if idempotencyKey != "" {
		if cached, ok := trace.commits[idempotencyKey]; ok {
			return true, cached
		}
	}

	err = w.checkAndCommitLocked(trace, next)

	if idempotencyKey != "" {
		if _, exists := trace.commits[idempotencyKey]; !exists {
			for len(trace.commitOrder) >= maxIdempotencyKeysPerTrace {
				oldest := trace.commitOrder[0]
				trace.commitOrder = trace.commitOrder[1:]
				delete(trace.commits, oldest)
			}
			trace.commitOrder = append(trace.commitOrder, idempotencyKey)
		}
		trace.commits[idempotencyKey] = err
	}
	return false, err
}

// checkAndCommitLocked is CheckAndCommit's body, minus the locking, shared
// with CheckAndCommitIdempotent so both run the check-then-commit sequence
// under exactly one lock acquisition.
func (w *Workflow) checkAndCommitLocked(trace *Trace, next StepID) error {
	if err := w.checkSafetyLocked(trace, next); err != nil {
		return err
	}
	return w.commitLocked(trace, next)
}

// Commit records that `next` executed successfully, advancing the trace:
// its established states now hold. Callers must call CheckSafety(trace,
// next) and get a nil error before calling Commit; Commit itself does not
// re-check safety, it assumes the caller already gated execution on it,
// exactly the barrier-certificate pattern: verify, then act, never act then
// verify.
//
// Any caller that can be reached concurrently for the same trace should use
// CheckAndCommit instead: this method takes the trace lock, but a separate
// CheckSafety call releases it before Commit reacquires it, leaving a
// window in which another request can commit against the same trace.
func (w *Workflow) Commit(trace *Trace, next StepID) error {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return w.commitLocked(trace, next)
}

func (w *Workflow) commitLocked(trace *Trace, next StepID) error {
	step, ok := w.Steps[next]
	if !ok {
		return fmt.Errorf("verify: unknown step %q", next)
	}
	trace.Executed = append(trace.Executed, next)
	for _, est := range step.Establishes {
		trace.Holds[est] = true
	}
	return nil
}

// StepsThatEstablish returns the IDs of every step in the workflow whose
// Establishes list includes state, sorted for a deterministic result (map
// iteration order is not). Intended for a caller that just got a Violation
// back from CheckSafety: Violation.MissingState names what's missing,
// StepsThatEstablish(that state) names which step(s) would actually set it,
// so a blocked response can point at a concrete next action instead of just
// naming the gap.
func (w *Workflow) StepsThatEstablish(state State) []StepID {
	var ids []StepID
	for id, step := range w.Steps {
		for _, est := range step.Establishes {
			if est == state {
				ids = append(ids, id)
				break
			}
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
