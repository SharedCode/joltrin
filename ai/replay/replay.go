// Package replay closes the loop between ai/ledger and ai/verify: it records
// verification-barrier decisions into a run's ledger in a canonical shape,
// and replays a run's recorded decision history against a candidate
// verify.Workflow to catch behavioral drift before a workflow definition
// change ships.
//
// This is deliberately narrow in scope. Practices like this go by different
// names in different corners of the industry, replay testing in durable
// execution systems, shadow evaluation for policy or guardrail changes, WAF
// rule shadow mode, but the underlying idea is the same one applied here:
// before trusting a new decision policy with live traffic, run it against
// what a real trace of past traffic actually did and diff the outcomes. Here
// "past traffic" is an ai/ledger run's recorded verification_decision
// events, and "the new decision policy" is a candidate verify.Workflow.
//
// What this is not: a live shadow-traffic mirror or an A/B testing
// framework. It replays already-recorded, completed history entirely
// offline, no real tool ever runs again, which is what makes it safe to run
// against arbitrary past runs on demand.
package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sharedcode/joltrin/ai/ledger"
	"github.com/sharedcode/joltrin/ai/verify"
)

// DecisionPayload is the canonical JSON shape recorded on a
// ledger.EventVerificationDecision event. Recording decisions in this shape
// (via RecordDecision) is what lets a later Harness reconstruct exactly what
// was decided at the time without needing the original process, or even the
// original workflow definition, still available.
type DecisionPayload struct {
	Step         verify.StepID `json:"step"`
	Allowed      bool          `json:"allowed"`
	Rule         string        `json:"rule,omitempty"`
	MissingState verify.State  `json:"missing_state,omitempty"`
	Reason       string        `json:"reason,omitempty"`
}

// RecordDecision checks whether step is safe to execute against wf given
// trace's history so far, commits it into trace if allowed exactly like
// verify.Workflow.CheckAndCommit, and appends a verification_decision event
// to runID's ledger recording the outcome either way.
//
// It returns the same error CheckAndCommit would (nil, a *verify.Violation,
// or a malformed-request error for an unknown step), so a caller already
// gating execution on wf.CheckAndCommit(trace, step) can switch to this
// function unchanged and get an auditable, replayable record for free.
//
// If the ledger append itself fails, that failure is returned wrapped
// instead of the barrier outcome, so a ledger outage is never mistaken for a
// clean, recorded decision.
func RecordDecision(ctx context.Context, l *ledger.Ledger, runID string, wf *verify.Workflow, trace *verify.Trace, step verify.StepID) error {
	checkErr := wf.CheckAndCommit(trace, step)

	payload := DecisionPayload{Step: step, Allowed: checkErr == nil}
	var v *verify.Violation
	switch {
	case errors.As(checkErr, &v):
		payload.Rule = v.Rule
		payload.MissingState = v.MissingState
		payload.Reason = v.Message
	case checkErr != nil:
		payload.Reason = checkErr.Error()
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		// An encoding bug in this package must not look like a clean,
		// recorded decision to the caller.
		return fmt.Errorf("replay: encoding decision for step %q: %w", step, err)
	}
	if _, err := l.Append(ctx, runID, ledger.EventVerificationDecision, raw); err != nil {
		return fmt.Errorf("replay: barrier decided allowed=%v for step %q but failed to record it: %w", payload.Allowed, step, err)
	}
	return checkErr
}

// Divergence describes one recorded step whose outcome under the candidate
// workflow differs from what was actually decided when the run executed.
type Divergence struct {
	Seq               uint64
	Step              verify.StepID
	OriginallyAllowed bool
	CandidateAllowed  bool
	// CandidateReason explains the candidate's decision when it disagrees
	// with the original: the candidate's violation message, or why an
	// originally-allowed step is no longer even a known step in candidate.
	CandidateReason string
}

// Kind classifies a Divergence for triage. A step the original safely
// executed that the candidate would now block is a regression risk, it
// could break an agent flow that used to work. A step the original
// correctly blocked that the candidate would now allow is a safety
// loosening, and deserves the most scrutiny of the two.
func (d Divergence) Kind() string {
	if d.OriginallyAllowed && !d.CandidateAllowed {
		return "newly_blocked"
	}
	return "newly_allowed"
}

// Report is the result of replaying one run's recorded verification history
// against a candidate workflow.
type Report struct {
	RunID         string
	StepsReplayed int
	Divergences   []Divergence
	// SkippedEvents counts verification_decision events whose payload did
	// not decode as a DecisionPayload (recorded by something other than
	// RecordDecision, or corrupted at the application-payload level).
	// Replay continues past them rather than aborting.
	SkippedEvents int
	// Incomplete is set when the run's ledger itself reported a gap or
	// corruption partway through (see ledger.Replay); Divergences and
	// StepsReplayed still reflect everything readable before that point.
	Incomplete bool
}

// Clean reports whether the candidate workflow agreed with every recorded
// decision in the run.
func (r *Report) Clean() bool { return len(r.Divergences) == 0 }

// Harness replays a run's recorded verification-decision history against a
// candidate verify.Workflow. A single Harness is safe for concurrent use
// across different runs; it holds no per-run state itself.
type Harness struct {
	ledger *ledger.Ledger
}

// NewHarness constructs a Harness backed by the ledger holding the run
// history to replay.
func NewHarness(l *ledger.Ledger) *Harness {
	return &Harness{ledger: l}
}

// ReplayAgainst replays runID's recorded verification_decision events, in
// sequence order, against candidate on a fresh verify.Trace, and reports any
// step whose allow/block outcome under candidate differs from what actually
// happened.
//
// A step the original run executed (allowed=true) is re-checked with
// candidate.CheckSafety; regardless of that outcome, it is then force-
// committed into the replay trace with candidate.Commit, because it is
// historical fact that this step did execute and later steps' preconditions
// must be evaluated against that same history, not against whatever the
// candidate would have preferred. A step the original run blocked
// (allowed=false) is only checked, never committed, since it never actually
// happened and must not affect state for later steps.
func (h *Harness) ReplayAgainst(ctx context.Context, runID string, candidate *verify.Workflow) (*Report, error) {
	if runID == "" {
		return nil, ledger.ErrInvalidRunID
	}
	if candidate == nil {
		return nil, errors.New("replay: candidate workflow must not be nil")
	}

	events, replayErr := h.ledger.Replay(ctx, runID)
	if replayErr != nil && len(events) == 0 {
		return nil, replayErr
	}

	report := &Report{RunID: runID, Incomplete: replayErr != nil}
	trace := verify.NewTrace()

	for _, ev := range events {
		if ev.Type != ledger.EventVerificationDecision {
			continue
		}
		var original DecisionPayload
		if err := json.Unmarshal(ev.Payload, &original); err != nil {
			report.SkippedEvents++
			continue
		}
		report.StepsReplayed++

		candidateErr := candidate.CheckSafety(trace, original.Step)
		candidateAllowed := candidateErr == nil

		if original.Allowed {
			// Historical fact: this step executed. Advance the replay trace
			// to match, independent of what the candidate would have done.
			_ = candidate.Commit(trace, original.Step)
		}

		if candidateAllowed == original.Allowed {
			continue
		}

		reason := ""
		var v *verify.Violation
		switch {
		case errors.As(candidateErr, &v):
			reason = v.Message
		case candidateErr != nil:
			reason = candidateErr.Error()
		}
		report.Divergences = append(report.Divergences, Divergence{
			Seq:               ev.Seq,
			Step:              original.Step,
			OriginallyAllowed: original.Allowed,
			CandidateAllowed:  candidateAllowed,
			CandidateReason:   reason,
		})
	}

	return report, nil
}
