package runbookstore

import (
	"fmt"
	"sync"
	"testing"

	"github.com/sharedcode/joltrin/ai/verify"
)

func Test_WorkflowNames_SortedAndEmptyWhenNoneRegistered(t *testing.T) {
	store := New()
	if got := store.WorkflowNames(); len(got) != 0 {
		t.Fatalf("expected no names on an empty store, got %v", got)
	}

	trivial := func() *verify.Workflow {
		wf, err := verify.NewWorkflow([]verify.Step{{ID: "noop"}}, nil, nil)
		if err != nil {
			t.Fatalf("NewWorkflow: %v", err)
		}
		return wf
	}
	for _, name := range []string{"zeta", "alpha", "mid"} {
		if err := store.RegisterWorkflow(name, trivial()); err != nil {
			t.Fatalf("RegisterWorkflow(%q): %v", name, err)
		}
	}

	got := store.WorkflowNames()
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected sorted %v, got %v", want, got)
		}
	}
}

// Test_TraceFor_EvictsOldestPastCap is the regression test for the
// unbounded-memory finding: a trace was allocated per unseen trace_id and
// never released, so any client reaching a protocol server could grow the
// store without limit just by varying trace_id.
func Test_TraceFor_EvictsOldestPastCap(t *testing.T) {
	store := NewWithMaxTraces(4)

	for i := 0; i < 100; i++ {
		store.TraceFor(fmt.Sprintf("incident-%d", i))
	}

	if got := store.TraceCount(); got != 4 {
		t.Fatalf("expected the store to hold at most 4 traces, got %d", got)
	}
}

// Test_TraceFor_EvictionFailsClosed pins the safety direction of eviction:
// a recycled trace_id must come back empty, so a step needing an
// established precondition is blocked rather than waved through.
func Test_TraceFor_EvictionFailsClosed(t *testing.T) {
	wf, err := DBMaintenanceWorkflow()
	if err != nil {
		t.Fatalf("DBMaintenanceWorkflow: %v", err)
	}
	store := NewWithMaxTraces(2)
	if err := store.RegisterWorkflow("db-maintenance", wf); err != nil {
		t.Fatalf("RegisterWorkflow: %v", err)
	}

	trace := store.TraceFor("incident-original")
	for _, step := range []verify.StepID{"take_backup", "validate_backup"} {
		if err := wf.CheckAndCommit(trace, step); err != nil {
			t.Fatalf("setup %s: %v", step, err)
		}
	}
	if err := wf.CheckSafety(trace, "drop_prod_db"); err != nil {
		t.Fatalf("precondition should hold before eviction: %v", err)
	}

	// Push the original out of the store.
	store.TraceFor("incident-a")
	store.TraceFor("incident-b")
	store.TraceFor("incident-c")

	revived := store.TraceFor("incident-original")
	if err := wf.CheckSafety(revived, "drop_prod_db"); err == nil {
		t.Fatal("an evicted trace must come back empty so the drop is blocked, not allowed")
	}
}

func Test_TraceFor_ConcurrentAccessIsSafe(t *testing.T) {
	store := NewWithMaxTraces(16)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				store.TraceFor(fmt.Sprintf("incident-%d", j))
				_ = store.TraceCount()
			}
		}(i)
	}
	wg.Wait()

	if got := store.TraceCount(); got > 16 {
		t.Fatalf("cap breached under concurrency: %d traces", got)
	}
}
