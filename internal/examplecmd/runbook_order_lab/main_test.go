package runbookorderlab

import (
	"reflect"
	"testing"

	"github.com/sharedcode/joltrin/v5/verify"
)

func TestAcceptedOrdersShortestFirst(t *testing.T) {
	wf, err := newWorkflow()
	if err != nil {
		t.Fatal(err)
	}
	orders := acceptedOrders(wf)
	if len(orders) != 3 {
		t.Fatalf("accepted orders = %d, want 3: %v", len(orders), orders)
	}
	want := []verify.StepID{"take_backup", "validate_backup", "restore_from_backup"}
	if !reflect.DeepEqual(orders[0], want) {
		t.Fatalf("shortest order = %v, want %v", orders[0], want)
	}
	for _, o := range orders {
		if !replay(wf, o) {
			t.Fatalf("order %v was returned but the verifier rejects it", o)
		}
	}
}

func TestNaiveOrderIsBlockedByVerifier(t *testing.T) {
	wf, err := newWorkflow()
	if err != nil {
		t.Fatal(err)
	}
	tr := verify.NewTrace()
	naive := []verify.StepID{"drop_prod_db", "take_backup", "validate_backup", "restore_from_backup"}
	blocked := 0
	for _, id := range naive {
		if err := wf.CheckAndCommit(tr, id); err != nil {
			if !verify.IsViolation(err) {
				t.Fatalf("step %s: unexpected error type: %v", id, err)
			}
			blocked++
		}
	}
	if blocked != 1 {
		t.Fatalf("blocked = %d, want 1", blocked)
	}
	if got := len(tr.ExecutedSteps()); got != 3 {
		t.Fatalf("committed = %d, want 3", got)
	}
}

func BenchmarkAcceptedOrders(b *testing.B) {
	wf, err := newWorkflow()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		acceptedOrders(wf)
	}
}
