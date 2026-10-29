// Command runbook_order_lab enumerates the orders in which a runbook can run
// and reports the shortest one. Every step is checked by the verifier, so an
// order only counts if the verifier accepts it.
package runbookorderlab

import (
	"fmt"
	"strings"

	"github.com/sharedcode/joltrin/v5/verify"
)

// barrierSteps is the same five-step workflow as examples/verify_barrier.
var barrierSteps = []verify.Step{
	{ID: "take_backup", Establishes: []verify.State{"backup_taken"}},
	{ID: "validate_backup", Requires: []verify.State{"backup_taken"}, Establishes: []verify.State{"backup_validated"}},
	{ID: "drop_prod_db", Requires: []verify.State{"backup_validated"}, Establishes: []verify.State{"prod_db_dropped"}},
	{ID: "restore_from_backup", Requires: []verify.State{"backup_validated"}, Establishes: []verify.State{"rollback_complete"}},
	{ID: "restore_from_backup_post_drop", Requires: []verify.State{"prod_db_dropped"}, Establishes: []verify.State{"rollback_complete"}},
}

const goal = verify.State("rollback_complete")

func newWorkflow() (*verify.Workflow, error) {
	return verify.NewWorkflow(barrierSteps,
		[]verify.SafetyRule{{Name: "no-drop-without-validated-backup", Forbidden: "prod_db_dropped", Requires: "backup_validated"}},
		[]verify.ReachabilityRule{{Name: "rollback-always-reachable", Target: goal}})
}

// replay runs seq through the verifier in a fresh trace. It reports whether
// every step was accepted.
func replay(wf *verify.Workflow, seq []verify.StepID) bool {
	tr := verify.NewTrace()
	for _, id := range seq {
		if err := wf.CheckAndCommit(tr, id); err != nil {
			return false
		}
	}
	return true
}

// acceptedOrders returns every order that the verifier accepts and that ends
// on a step establishing the goal, shortest first.
func acceptedOrders(wf *verify.Workflow) [][]verify.StepID {
	var out [][]verify.StepID
	var walk func(seq []verify.StepID)
	walk = func(seq []verify.StepID) {
		if n := len(seq); n > 0 && establishesGoal(wf, seq[n-1]) {
			out = append(out, append([]verify.StepID(nil), seq...))
			return
		}
		for _, s := range barrierSteps {
			if contains(seq, s.ID) {
				continue
			}
			next := append(append([]verify.StepID(nil), seq...), s.ID)
			if replay(wf, next) {
				walk(next)
			}
		}
	}
	walk(nil)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && len(out[j]) < len(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func establishesGoal(wf *verify.Workflow, id verify.StepID) bool {
	for _, st := range wf.Steps[id].Establishes {
		if st == goal {
			return true
		}
	}
	return false
}

func contains(seq []verify.StepID, id verify.StepID) bool {
	for _, x := range seq {
		if x == id {
			return true
		}
	}
	return false
}

func join(seq []verify.StepID) string {
	parts := make([]string, len(seq))
	for i, id := range seq {
		parts[i] = string(id)
	}
	return strings.Join(parts, " -> ")
}

func Main() {
	wf, err := newWorkflow()
	if err != nil {
		panic(err)
	}
	if err := wf.VerifyReachability(); err != nil {
		panic(err)
	}
	orders := acceptedOrders(wf)
	fmt.Printf("accepted orders to %s: %d\n", goal, len(orders))
	for _, o := range orders {
		fmt.Printf("%d steps: %s\n", len(o), join(o))
	}

	naive := []verify.StepID{"drop_prod_db", "take_backup", "validate_backup", "restore_from_backup"}
	tr := verify.NewTrace()
	blocked := 0
	for _, id := range naive {
		if err := wf.CheckAndCommit(tr, id); err != nil {
			blocked++
		}
	}
	fmt.Printf("naive order (%s): blocked=%d committed=%d\n", join(naive), blocked, len(tr.ExecutedSteps()))
}
