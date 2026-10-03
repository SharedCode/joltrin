package verify

import "fmt"

// VerifyReachability statically checks every ReachabilityRule against the
// workflow graph: for each rule, is its Target state reachable from every
// other state that appears anywhere in the graph (as a precondition or
// postcondition)? This is a static property of the Workflow's structure, it
// does not depend on any particular Trace.
//
// Implementation: build a directed graph where an edge State A -> State B
// exists if some step requiring A (among possibly other states) establishes
// B. Then, from Target, walk the graph backwards (reverse BFS) to find every
// state that can reach Target. If any state in the workflow is not in that
// set, the rule is violated, name it explicitly in the returned error so a
// human (or an agent asking why deployment is blocked) can see exactly which
// state has no path forward.
//
// Known limitation, found while writing this package's own tests: this
// checks reachability per individual State, not over the full powerset of
// states that might simultaneously hold at a given point in a real trace
// (Trace.Holds is monotonic; earlier states don't get unestablished). A
// state that never appears as any step's precondition is a dead end under
// this check even if, in a real trace, it always co-occurs with some other
// state that does have a path forward. The practical consequence: model
// every state that must remain "exitable" with at least one step that can
// fire once it holds, don't rely on some other concurrently-true state to
// carry the path forward for it. This repo's own test fixture
// (dbMaintenanceWorkflow in verify_test.go) hit exactly this: dropping prod
// needed its own explicit restore_from_backup_post_drop step, reusing the
// pre-drop restore step wasn't enough for this check to see the path. Full
// conjunctive-state (powerset) reachability would close this gap but at the
// cost of real state-space explosion risk for larger workflows, an
// intentional tradeoff, not an oversight.
func (w *Workflow) VerifyReachability() error {
	allStates := w.allStates()

	for _, rule := range w.Reachability {
		if _, ok := allStates[rule.Target]; !ok {
			return fmt.Errorf("verify: reachability rule %q targets state %q, which no step establishes", rule.Name, rule.Target)
		}

		canReachTarget := w.statesReaching(rule.Target)

		var stuck []State
		for s := range allStates {
			if s == rule.Target {
				continue
			}
			if !canReachTarget[s] {
				stuck = append(stuck, s)
			}
		}
		if len(stuck) > 0 {
			return fmt.Errorf("verify: reachability rule %q violated: %d state(s) have no path to %q: %v", rule.Name, len(stuck), rule.Target, stuck)
		}
	}
	return nil
}

// allStates collects every State mentioned anywhere in the workflow, either
// as a precondition or a postcondition of any step.
func (w *Workflow) allStates() map[State]bool {
	states := make(map[State]bool)
	for _, step := range w.Steps {
		for _, s := range step.Requires {
			states[s] = true
		}
		for _, s := range step.Establishes {
			states[s] = true
		}
	}
	return states
}

// statesReaching returns the set of every State from which `target` is
// reachable via some sequence of steps: a state S reaches target if S is
// target itself, or if some step whose preconditions include S establishes
// a state that (transitively) reaches target. Computed as a reverse BFS
// over the step graph starting from target.
func (w *Workflow) statesReaching(target State) map[State]bool {
	reaches := map[State]bool{target: true}

	// Repeatedly scan steps until a full pass adds nothing new. The
	// workflow graphs this package targets (finite runbooks) are small
	// enough that this fixed-point iteration, rather than a proper
	// adjacency-list BFS queue, is both correct and simple to verify by
	// reading it.
	for changed := true; changed; {
		changed = false
		for _, step := range w.Steps {
			if !stepEstablishesAny(step, reaches) {
				continue
			}
			for _, req := range step.Requires {
				if !reaches[req] {
					reaches[req] = true
					changed = true
				}
			}
		}
	}
	return reaches
}

// stepEstablishesAny reports whether step establishes at least one state
// already known to reach the target.
func stepEstablishesAny(step Step, reaches map[State]bool) bool {
	for _, est := range step.Establishes {
		if reaches[est] {
			return true
		}
	}
	return false
}
