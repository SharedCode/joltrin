package verify

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// This test compares the real barrier with a small independent model, over many
// random runbooks and random call sequences. The model is written from the
// documented rules only:
//
//   - a step is allowed when every state it requires already holds, and, for
//     each state it would establish, every safety rule that forbids that state
//     has its required state already holding;
//   - an allowed step is committed: it is appended to the trace and its states
//     start to hold. A blocked step changes nothing;
//   - an idempotency key names one call: the same key and step replays the
//     first answer without committing again, and the same key with another step
//     is refused and changes nothing.

type modelKey struct {
	step    StepID
	allowed bool
}

type barrierModel struct {
	holds    map[State]bool
	executed []StepID
	keys     map[string]modelKey
}

func newBarrierModel() *barrierModel {
	return &barrierModel{holds: map[State]bool{}, keys: map[string]modelKey{}}
}

func (m *barrierModel) allowed(w *Workflow, id StepID) bool {
	step, ok := w.Steps[id]
	if !ok {
		return false
	}
	for _, r := range step.Requires {
		if !m.holds[r] {
			return false
		}
	}
	for _, est := range step.Establishes {
		for _, rule := range w.Safety {
			if rule.Forbidden == est && !m.holds[rule.Requires] {
				return false
			}
		}
	}
	return true
}

func (m *barrierModel) commit(w *Workflow, id StepID) {
	m.executed = append(m.executed, id)
	for _, est := range w.Steps[id].Establishes {
		m.holds[est] = true
	}
}

func randomWorkflow(r *rand.Rand) (*Workflow, []StepID) {
	states := []State{"s0", "s1", "s2", "s3", "s4", "s5"}
	pick := func(n int) []State {
		var out []State
		for i := 0; i < n; i++ {
			out = append(out, states[r.Intn(len(states))])
		}
		return out
	}
	n := 3 + r.Intn(5)
	steps := make([]Step, n)
	ids := make([]StepID, n)
	for i := range steps {
		ids[i] = StepID(fmt.Sprintf("step%d", i))
		steps[i] = Step{ID: ids[i], Requires: pick(r.Intn(3)), Establishes: pick(1 + r.Intn(2))}
	}
	var rules []SafetyRule
	for i, k := 0, r.Intn(3); i < k; i++ {
		f := states[r.Intn(len(states))]
		q := states[r.Intn(len(states))]
		rules = append(rules, SafetyRule{Name: fmt.Sprintf("rule%d", i), Forbidden: f, Requires: q})
	}
	wf, err := NewWorkflow(steps, rules, nil)
	if err != nil {
		panic(err)
	}
	return wf, ids
}

func holdsOf(t *Trace) map[State]bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[State]bool, len(t.Holds))
	for k, v := range t.Holds {
		if v {
			out[k] = true
		}
	}
	return out
}

func sameStates(a, b map[State]bool) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func TestProperty_BarrierMatchesModel(t *testing.T) {
	keyNames := []string{"k1", "k2", "k3"}
	for seed := int64(0); seed < 3000; seed++ {
		r := rand.New(rand.NewSource(seed))
		wf, ids := randomWorkflow(r)
		trace := NewTrace()
		m := newBarrierModel()

		for op := 0; op < 40; op++ {
			step := ids[r.Intn(len(ids))]
			if r.Intn(12) == 0 {
				step = "no_such_step"
			}
			desc := fmt.Sprintf("seed %d op %d step %s", seed, op, step)

			switch r.Intn(3) {
			case 0: // CheckSafety: never changes anything
				err := wf.CheckSafety(trace, step)
				if want := m.allowed(wf, step); (err == nil) != want {
					t.Fatalf("%s: CheckSafety err=%v, model allowed=%v", desc, err, want)
				}
			case 1: // CheckAndCommit
				err := wf.CheckAndCommit(trace, step)
				want := m.allowed(wf, step)
				if (err == nil) != want {
					t.Fatalf("%s: CheckAndCommit err=%v, model allowed=%v", desc, err, want)
				}
				if want {
					m.commit(wf, step)
				}
				if err != nil && step != "no_such_step" && !IsViolation(err) {
					t.Fatalf("%s: a blocked known step should be a Violation, got %T %v", desc, err, err)
				}
			default: // CheckAndCommitIdempotent
				key := keyNames[r.Intn(len(keyNames))]
				replayed, err := wf.CheckAndCommitIdempotent(trace, step, key)
				if prev, seen := m.keys[key]; seen {
					if prev.step != step {
						if replayed || !IsKeyReused(err) {
							t.Fatalf("%s key %s: reuse for another step should be refused, got replayed=%v err=%v", desc, key, replayed, err)
						}
					} else {
						if !replayed || (err == nil) != prev.allowed {
							t.Fatalf("%s key %s: same step should replay the first answer (allowed=%v), got replayed=%v err=%v", desc, key, prev.allowed, replayed, err)
						}
					}
				} else {
					want := m.allowed(wf, step)
					if replayed || (err == nil) != want {
						t.Fatalf("%s key %s: first use err=%v replayed=%v, model allowed=%v", desc, key, err, replayed, want)
					}
					if want {
						m.commit(wf, step)
					}
					m.keys[key] = modelKey{step: step, allowed: want}
				}
			}

			if got := trace.ExecutedSteps(); !(len(got) == 0 && len(m.executed) == 0) && !reflect.DeepEqual(got, m.executed) {
				t.Fatalf("%s: executed %v, model %v", desc, got, m.executed)
			}
			if got := holdsOf(trace); !sameStates(got, m.holds) {
				t.Fatalf("%s: holds %v, model %v", desc, sortedStates(got), sortedStates(m.holds))
			}
		}
	}
}

func sortedStates(m map[State]bool) []string {
	var out []string
	for k := range m {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}

// A Violation must name the first thing that is missing, and that thing must
// really be missing, so an agent that acts on it is acting on the truth.
func TestProperty_ViolationNamesARealMissingState(t *testing.T) {
	for seed := int64(0); seed < 3000; seed++ {
		r := rand.New(rand.NewSource(seed))
		wf, ids := randomWorkflow(r)
		trace := NewTrace()
		m := newBarrierModel()
		for op := 0; op < 30; op++ {
			step := ids[r.Intn(len(ids))]
			err := wf.CheckAndCommit(trace, step)
			if err == nil {
				m.commit(wf, step)
				continue
			}
			var v *Violation
			if !asViolation(err, &v) {
				t.Fatalf("seed %d: want a Violation, got %v", seed, err)
			}
			if v.MissingState == "" {
				t.Fatalf("seed %d: Violation has no MissingState: %+v", seed, v)
			}
			if m.holds[v.MissingState] {
				t.Fatalf("seed %d: Violation says %q is missing but it holds: %+v", seed, v.MissingState, v)
			}
			if v.Rule == "precondition" {
				found := false
				for _, req := range wf.Steps[step].Requires {
					if req == v.MissingState {
						found = true
					}
				}
				if !found {
					t.Fatalf("seed %d: precondition block names %q, which step %s does not require", seed, v.MissingState, step)
				}
			}
		}
	}
}

func asViolation(err error, v **Violation) bool {
	e, ok := err.(*Violation)
	if ok {
		*v = e
	}
	return ok
}
