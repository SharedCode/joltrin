// Command failover_barrier shows the verification barrier on a data center
// failover: an agent that tries to stop the primary site before the standby is
// verified and traffic is confirmed on it is blocked, then the safe order runs.
package failoverbarrier

import (
	"fmt"
	"strings"

	"github.com/sharedcode/joltrin/v5/verify"
)

// steps is a four-step "pull the plug" runbook. Stopping the primary needs the
// standby verified, traffic moved to it, and the error rate confirmed there.
var steps = []verify.Step{
	{ID: "verify_standby", Establishes: []verify.State{"standby_verified"}},
	{ID: "shift_traffic", Requires: []verify.State{"standby_verified"}, Establishes: []verify.State{"traffic_on_standby"}},
	{ID: "confirm_error_rate", Requires: []verify.State{"traffic_on_standby"}, Establishes: []verify.State{"failover_verified"}},
	{ID: "stop_primary", Requires: []verify.State{"failover_verified"}, Establishes: []verify.State{"primary_stopped"}},
}

func newWorkflow() (*verify.Workflow, error) {
	return verify.NewWorkflow(steps,
		[]verify.SafetyRule{
			{Name: "no-stop-before-standby-verified", Forbidden: "primary_stopped", Requires: "standby_verified"},
			{Name: "no-stop-before-traffic-confirmed", Forbidden: "primary_stopped", Requires: "failover_verified"},
		},
		[]verify.ReachabilityRule{{Name: "failover-always-completes", Target: "primary_stopped"}})
}

// run sends seq through the barrier in a fresh trace and returns which steps
// were blocked and which were committed.
func run(wf *verify.Workflow, seq []verify.StepID) (blocked, committed []verify.StepID) {
	tr := verify.NewTrace()
	for _, id := range seq {
		if err := wf.CheckAndCommit(tr, id); err != nil {
			blocked = append(blocked, id)
			continue
		}
		committed = append(committed, id)
	}
	return blocked, committed
}

func join(seq []verify.StepID) string {
	parts := make([]string, len(seq))
	for i, id := range seq {
		parts[i] = string(id)
	}
	return strings.Join(parts, " -> ")
}

// Main prints what the barrier does with a careless agent and with a careful one.
func Main() {
	wf, err := newWorkflow()
	if err != nil {
		panic(err)
	}
	if err := wf.VerifyReachability(); err != nil {
		panic(err)
	}

	careless := []verify.StepID{"stop_primary", "verify_standby", "shift_traffic", "confirm_error_rate", "stop_primary"}
	blocked, committed := run(wf, careless)
	fmt.Printf("careless agent: %s\n  blocked: %s\n  committed: %s\n", join(careless), join(blocked), join(committed))

	careful := []verify.StepID{"verify_standby", "shift_traffic", "confirm_error_rate", "stop_primary"}
	blocked, committed = run(wf, careful)
	fmt.Printf("careful agent: %s\n  blocked: %d\n  committed: %s\n", join(careful), len(blocked), join(committed))
}
