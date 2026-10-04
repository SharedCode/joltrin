package verify

import (
	"errors"
	"reflect"
	"testing"
)

// A key names one call. Reusing a key for a different step must never return
// the first step's answer for the second one, because an agent chooses its own
// keys and could otherwise get a step approved that was never checked.

func Test_Idempotent_KeyFromASuccessfulCallCannotApproveADestructiveStep(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	trace := NewTrace()

	if _, err := wf.CheckAndCommitIdempotent(trace, "take_backup", "k"); err != nil {
		t.Fatalf("take_backup should run: %v", err)
	}
	// No validated backup exists. The key from the successful call above must not approve drop_prod_db.
	replayed, err := wf.CheckAndCommitIdempotent(trace, "drop_prod_db", "k")
	if replayed {
		t.Error("a key used for another step must not be reported as a replay")
	}
	if !IsKeyReused(err) {
		t.Fatalf("want a KeyReusedError, got %v", err)
	}
	if IsViolation(err) {
		t.Error("a reused key is not a barrier violation")
	}
	var e *KeyReusedError
	if !errors.As(err, &e) || e.UsedFor != "take_backup" || e.Requested != "drop_prod_db" || e.Key != "k" {
		t.Errorf("unexpected error details: %+v", e)
	}
	if got := trace.ExecutedSteps(); !reflect.DeepEqual(got, []StepID{"take_backup"}) {
		t.Errorf("trace = %v, want only take_backup: the refused call must change nothing", got)
	}
	// The barrier still decides drop_prod_db on its own merits.
	if err := wf.CheckSafety(trace, "drop_prod_db"); !IsViolation(err) {
		t.Errorf("drop_prod_db must still be blocked, got %v", err)
	}
}

func Test_Idempotent_KeyFromABlockedCallCannotHideAnAllowedStep(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	trace := NewTrace()

	if _, err := wf.CheckAndCommitIdempotent(trace, "drop_prod_db", "k"); !IsViolation(err) {
		t.Fatalf("drop_prod_db should be blocked first, got %v", err)
	}
	// take_backup is allowed. It must run, not return the earlier block.
	replayed, err := wf.CheckAndCommitIdempotent(trace, "take_backup", "k")
	if replayed || !IsKeyReused(err) {
		t.Fatalf("want a refused reuse, got replayed=%v err=%v", replayed, err)
	}
	if got := trace.ExecutedSteps(); len(got) != 0 {
		t.Errorf("trace = %v, want empty", got)
	}
	// With its own key it runs.
	if _, err := wf.CheckAndCommitIdempotent(trace, "take_backup", "k2"); err != nil {
		t.Fatalf("take_backup with a new key should run: %v", err)
	}
}

func Test_Idempotent_RefusedReuseDoesNotChangeWhatTheKeyReplays(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	trace := NewTrace()

	if _, err := wf.CheckAndCommitIdempotent(trace, "take_backup", "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := wf.CheckAndCommitIdempotent(trace, "validate_backup", "k"); !IsKeyReused(err) {
		t.Fatalf("want a refused reuse, got %v", err)
	}
	// The key still replays its original call, once, with no new commit.
	replayed, err := wf.CheckAndCommitIdempotent(trace, "take_backup", "k")
	if !replayed || err != nil {
		t.Errorf("the original call should replay, got replayed=%v err=%v", replayed, err)
	}
	if got := trace.ExecutedSteps(); !reflect.DeepEqual(got, []StepID{"take_backup"}) {
		t.Errorf("trace = %v, want [take_backup] exactly once", got)
	}
}

func Test_Idempotent_KeysAreIndependentAcrossTraces(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	a, b := NewTrace(), NewTrace()
	if _, err := wf.CheckAndCommitIdempotent(a, "take_backup", "k"); err != nil {
		t.Fatal(err)
	}
	// The same key in another trace is a different call.
	if _, err := wf.CheckAndCommitIdempotent(b, "validate_backup", "k"); !IsViolation(err) {
		t.Errorf("a key from another trace must not matter, got %v", err)
	}
}
