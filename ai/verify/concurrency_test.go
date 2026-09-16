package verify

import (
	"sync"
	"testing"
)

// Test_ConcurrentSameTrace is the regression test for the data race that
// made every server built on this package remotely killable: two requests
// carrying the same trace_id land on the same *Trace, and one committing
// while the other checks raced on Trace.Holds. The Go runtime turns that
// into "fatal error: concurrent map read and map write", which recover()
// cannot catch, so a single pair of concurrent same-trace requests took the
// whole process down. Run under -race.
func Test_ConcurrentSameTrace(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	trace := NewTrace()

	const goroutines = 8
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, step := range []StepID{"take_backup", "validate_backup", "drop_prod_db"} {
				// Mix of the read-only and the atomic paths, which is what
				// validate_step and execute_step do respectively.
				_ = wf.CheckSafety(trace, step)
				_ = wf.CheckAndCommit(trace, step)
				_ = trace.ExecutedSteps()
			}
		}()
	}
	wg.Wait()

	// The barrier must still hold under concurrency: prod can only appear in
	// the trace if a validated backup was established first.
	var sawDrop, sawValidate bool
	for _, step := range trace.ExecutedSteps() {
		switch step {
		case "validate_backup":
			sawValidate = true
		case "drop_prod_db":
			if !sawValidate {
				t.Fatal("drop_prod_db committed before validate_backup: the barrier was bypassed under concurrency")
			}
			sawDrop = true
		}
	}
	if !sawDrop {
		t.Fatal("expected drop_prod_db to eventually commit once its precondition held")
	}
}

// Test_CheckAndCommit_LeavesTraceUntouchedWhenBlocked confirms the atomic
// path has no side effects on a blocked step: a rejected step must not
// appear in the trace, or the "verify, then act" guarantee is cosmetic.
func Test_CheckAndCommit_LeavesTraceUntouchedWhenBlocked(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	trace := NewTrace()

	err := wf.CheckAndCommit(trace, "drop_prod_db")
	if err == nil {
		t.Fatal("expected drop_prod_db to be blocked with an empty trace")
	}
	if !IsViolation(err) {
		t.Fatalf("expected a *Violation, got %T: %v", err, err)
	}
	if got := trace.ExecutedSteps(); len(got) != 0 {
		t.Fatalf("a blocked step must not advance the trace, got %v", got)
	}
}

// Test_ConcurrentIdempotentRetries_CommitExactlyOnce is the race-safety
// proof for CheckAndCommitIdempotent: several goroutines racing to commit
// the same step with the same idempotency key (simulating a client that
// fired a retry concurrently with its original request still in flight,
// not just sequentially after a timeout) must produce exactly one commit.
// Only the lock held across the cache lookup and the cache write, the same
// lock CheckAndCommit itself uses, makes that true; checking and writing
// the cache under separate acquisitions would let two goroutines both see
// "no key yet" and both commit. Run under -race.
func Test_ConcurrentIdempotentRetries_CommitExactlyOnce(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	trace := NewTrace()

	const goroutines = 16
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = wf.CheckAndCommitIdempotent(trace, "take_backup", "concurrent-retry-1")
		}()
	}
	wg.Wait()

	if got := trace.ExecutedSteps(); len(got) != 1 {
		t.Fatalf("expected exactly one commit across %d concurrent retries of the same key, got %d: %v", goroutines, len(got), got)
	}
}

// Test_IsViolation_DistinguishesMalformedFromBlocked is what lets the A2A
// executor answer input-required (retryable) versus failed (never
// succeeds); collapsing the two would make a typo look like a barrier hit.
func Test_IsViolation_DistinguishesMalformedFromBlocked(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	trace := NewTrace()

	if err := wf.CheckAndCommit(trace, "no_such_step"); err == nil {
		t.Fatal("expected an unknown step to error")
	} else if IsViolation(err) {
		t.Fatalf("an unknown step is malformed, not a barrier violation: %v", err)
	}
}
