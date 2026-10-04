package verify

import (
	"errors"
	"testing"
)

func attemptsOf(t *testing.T, err error) int {
	t.Helper()
	var v *Violation
	if !errors.As(err, &v) {
		t.Fatalf("want a *Violation, got %v", err)
	}
	return v.Attempts
}

func Test_Violation_Attempts_CountsRepeatedBlocksOfOneStep(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	tr := NewTrace()

	// An agent hammering the same blocked step with a fresh idempotency key
	// each time is not a retry of a lost response, so each call counts.
	for i, key := range []string{"a", "b", "c"} {
		_, err := wf.CheckAndCommitIdempotent(tr, "drop_prod_db", key)
		if got := attemptsOf(t, err); got != i+1 {
			t.Errorf("call %d: Attempts = %d, want %d", i+1, got, i+1)
		}
	}

	// A different step blocked for its own reason starts its own count.
	_, err := wf.CheckAndCommitIdempotent(tr, "restore_from_backup", "d")
	if got := attemptsOf(t, err); got != 1 {
		t.Errorf("other step: Attempts = %d, want 1", got)
	}
}

func Test_Violation_Attempts_ReplayDoesNotCount(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	tr := NewTrace()

	wf.CheckAndCommitIdempotent(tr, "drop_prod_db", "k1")
	replayed, err := wf.CheckAndCommitIdempotent(tr, "drop_prod_db", "k1")
	if !replayed || attemptsOf(t, err) != 1 {
		t.Fatalf("replayed=%v Attempts=%d, want the original outcome with Attempts 1", replayed, attemptsOf(t, err))
	}
	_, err = wf.CheckAndCommitIdempotent(tr, "drop_prod_db", "k2")
	if got := attemptsOf(t, err); got != 2 {
		t.Errorf("after a replay, the next real block has Attempts %d, want 2", got)
	}
}

func Test_Violation_Attempts_ValidateDoesNotCount(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	tr := NewTrace()

	if got := attemptsOf(t, wf.CheckSafety(tr, "drop_prod_db")); got != 0 {
		t.Errorf("CheckSafety is read-only, Attempts = %d, want 0", got)
	}
	_, err := wf.CheckAndCommitIdempotent(tr, "drop_prod_db", "")
	if got := attemptsOf(t, err); got != 1 {
		t.Errorf("a dry run must not count: Attempts = %d, want 1", got)
	}
}

func Test_Violation_Attempts_PerTraceNotShared(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	one, two := NewTrace(), NewTrace()

	wf.CheckAndCommit(one, "drop_prod_db")
	wf.CheckAndCommit(one, "drop_prod_db")
	_, err := wf.CheckAndCommitIdempotent(two, "drop_prod_db", "")
	if got := attemptsOf(t, err); got != 1 {
		t.Errorf("a second trace starts at 1, got %d", got)
	}
}

func Test_NextAction_PointsAtEstablishingStepsOrStops(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	err := wf.CheckSafety(NewTrace(), "drop_prod_db")
	var v *Violation
	if !errors.As(err, &v) {
		t.Fatalf("want a *Violation, got %v", err)
	}
	if got := wf.NextAction(v); got != NextRunEstablishingSteps {
		t.Errorf("NextAction = %q, want %q: validate_backup establishes the missing state", got, NextRunEstablishingSteps)
	}

	// A step that needs a state nothing establishes cannot be fixed by the
	// caller running other steps.
	orphan, err := NewWorkflow([]Step{{ID: "orphan", Requires: []State{"never_established"}}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = orphan.CheckSafety(NewTrace(), "orphan")
	if !errors.As(err, &v) {
		t.Fatalf("want a *Violation, got %v", err)
	}
	if got := orphan.NextAction(v); got != NextStopAndAsk {
		t.Errorf("NextAction = %q, want %q: no step establishes the missing state", got, NextStopAndAsk)
	}
}
