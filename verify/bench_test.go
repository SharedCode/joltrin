package verify

import (
	"fmt"
	"testing"
)

// benchWorkflow is a chain of n steps, each requiring the state the one before
// it establishes, with one safety rule on the last step. It is the shape of a
// real runbook, long enough that a check does real work.
func benchWorkflow(b *testing.B, n int) (*Workflow, []StepID) {
	b.Helper()
	steps := make([]Step, n)
	ids := make([]StepID, n)
	for i := 0; i < n; i++ {
		id := StepID(fmt.Sprintf("step_%03d", i))
		ids[i] = id
		steps[i] = Step{ID: id, Establishes: []State{State(fmt.Sprintf("state_%03d", i))}}
		if i > 0 {
			steps[i].Requires = []State{State(fmt.Sprintf("state_%03d", i-1))}
		}
	}
	rules := []SafetyRule{{Name: "last-needs-first", Forbidden: State(fmt.Sprintf("state_%03d", n-1)), Requires: "state_000"}}
	wf, err := NewWorkflow(steps, rules, nil)
	if err != nil {
		b.Fatal(err)
	}
	return wf, ids
}

// BenchmarkCheckSafetyAllowed is the read-only check on the path that passes.
func BenchmarkCheckSafetyAllowed(b *testing.B) {
	wf, ids := benchWorkflow(b, 50)
	trace := NewTrace()
	for _, id := range ids[:49] {
		if err := wf.CheckAndCommit(trace, id); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := wf.CheckSafety(trace, ids[49]); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCheckSafetyBlocked is the same check on the path that blocks, which
// builds a Violation and its message.
func BenchmarkCheckSafetyBlocked(b *testing.B) {
	wf, ids := benchWorkflow(b, 50)
	trace := NewTrace()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := wf.CheckSafety(trace, ids[49]); !IsViolation(err) {
			b.Fatal("want a violation")
		}
	}
}

// BenchmarkCheckAndCommitRun commits a whole 50-step run into a fresh trace.
func BenchmarkCheckAndCommitRun(b *testing.B) {
	wf, ids := benchWorkflow(b, 50)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		trace := NewTrace()
		for _, id := range ids {
			if err := wf.CheckAndCommit(trace, id); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkCheckAndCommitIdempotentReplay is a retry that hits the key cache.
func BenchmarkCheckAndCommitIdempotentReplay(b *testing.B) {
	wf, ids := benchWorkflow(b, 50)
	trace := NewTrace()
	if _, err := wf.CheckAndCommitIdempotent(trace, ids[0], "k"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if replayed, err := wf.CheckAndCommitIdempotent(trace, ids[0], "k"); !replayed || err != nil {
			b.Fatal("want a replay")
		}
	}
}

// BenchmarkCheckSafetyParallel is many runs checking at once, each on its own
// trace, which is how a server with many agents behaves.
func BenchmarkCheckSafetyParallel(b *testing.B) {
	wf, ids := benchWorkflow(b, 50)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		trace := NewTrace()
		for pb.Next() {
			_ = wf.CheckSafety(trace, ids[0])
		}
	})
}

// BenchmarkCheckSafetySharedTrace is many callers on one trace, which measures
// the cost of the trace lock.
func BenchmarkCheckSafetySharedTrace(b *testing.B) {
	wf, ids := benchWorkflow(b, 50)
	trace := NewTrace()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = wf.CheckSafety(trace, ids[0])
		}
	})
}

// BenchmarkVerifyReachability is the registration-time check on a long chain.
func BenchmarkVerifyReachability(b *testing.B) {
	wf, ids := benchWorkflow(b, 50)
	wf.Reachability = []ReachabilityRule{{Name: "end", Target: State(fmt.Sprintf("state_%03d", len(ids)-1))}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := wf.VerifyReachability(); err != nil {
			b.Fatal(err)
		}
	}
}
