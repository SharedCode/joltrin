package verify_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sharedcode/joltrin/v5/verify"
)

// These tests use only the exported API, the way an application that imports
// this package would. They pin the feedback a blocked step gives back: which
// rule tripped, the state that is missing, and the steps that would establish
// it, so a caller can act on a block without parsing the message.

func consumerWorkflow(t *testing.T) *verify.Workflow {
	t.Helper()
	wf, err := verify.NewWorkflow(
		[]verify.Step{
			{ID: "take_backup", Establishes: []verify.State{"backup_taken"}},
			{ID: "validate_backup", Requires: []verify.State{"backup_taken"}, Establishes: []verify.State{"backup_validated"}},
			{ID: "drop_prod_db", Requires: []verify.State{"backup_validated"}, Establishes: []verify.State{"prod_db_dropped"}},
		},
		[]verify.SafetyRule{{Name: "no-drop-without-validated-backup", Forbidden: "prod_db_dropped", Requires: "backup_validated"}},
		nil,
	)
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	return wf
}

func TestConsumer_BlockedStepReportsRuleMissingStateAndFix(t *testing.T) {
	wf := consumerWorkflow(t)
	trace := verify.NewTrace()

	err := wf.CheckSafety(trace, "drop_prod_db")
	if !verify.IsViolation(err) {
		t.Fatalf("expected a barrier Violation, got %v", err)
	}
	var v *verify.Violation
	if !errors.As(err, &v) {
		t.Fatalf("errors.As to *verify.Violation failed for %v", err)
	}
	if v.Rule != "precondition" {
		t.Errorf("Rule = %q, want %q", v.Rule, "precondition")
	}
	if v.MissingState != "backup_validated" {
		t.Errorf("MissingState = %q, want %q", v.MissingState, "backup_validated")
	}
	if got, want := wf.StepsThatEstablish(v.MissingState), []verify.StepID{"validate_backup"}; !reflect.DeepEqual(got, want) {
		t.Errorf("StepsThatEstablish(%q) = %v, want %v", v.MissingState, got, want)
	}
}

// A caller that only follows the fix each block points at, and never reads the
// workflow, reaches the blocked step without ever repeating the same blocked
// call twice in a row.
func TestConsumer_FollowingTheFixUnblocksTheStep(t *testing.T) {
	wf := consumerWorkflow(t)
	trace := verify.NewTrace()

	target := verify.StepID("drop_prod_db")
	next := target
	var blocked []verify.StepID
	for i := 0; i < 10; i++ {
		err := wf.CheckAndCommit(trace, next)
		if err == nil {
			if next == target {
				break
			}
			next = target // fix applied, go back to the step that was blocked
			continue
		}
		var v *verify.Violation
		if !errors.As(err, &v) {
			t.Fatalf("step %q: unexpected non-violation error: %v", next, err)
		}
		blocked = append(blocked, next)
		fixes := wf.StepsThatEstablish(v.MissingState)
		if len(fixes) == 0 {
			t.Fatalf("step %q blocked on %q, but no step establishes it", next, v.MissingState)
		}
		next = fixes[0]
	}

	want := []verify.StepID{"take_backup", "validate_backup", "drop_prod_db"}
	if got := trace.ExecutedSteps(); !reflect.DeepEqual(got, want) {
		t.Fatalf("trace = %v, want %v", got, want)
	}
	for i := 1; i < len(blocked); i++ {
		if blocked[i] == blocked[i-1] {
			t.Errorf("step %q was blocked twice in a row: the fix it was given did not make progress (blocked calls: %v)", blocked[i], blocked)
		}
	}
	if len(blocked) != 3 {
		t.Errorf("blocked calls = %v, want 3 (drop_prod_db, validate_backup, drop_prod_db)", blocked)
	}
}

func TestConsumer_RuleViolationNamesTheRule(t *testing.T) {
	wf, err := verify.NewWorkflow(
		[]verify.Step{
			{ID: "get_evidence", Establishes: []verify.State{"evidence_confirmed"}},
			{ID: "rollback", Establishes: []verify.State{"rolled_back"}},
		},
		[]verify.SafetyRule{{Name: "no-rollback-without-evidence", Forbidden: "rolled_back", Requires: "evidence_confirmed"}},
		nil,
	)
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	var v *verify.Violation
	if err := wf.CheckSafety(verify.NewTrace(), "rollback"); !errors.As(err, &v) {
		t.Fatalf("expected a Violation, got %v", err)
	}
	if v.Rule != "no-rollback-without-evidence" || v.MissingState != "evidence_confirmed" {
		t.Errorf("got rule %q missing %q, want the named rule and evidence_confirmed", v.Rule, v.MissingState)
	}
	if got := wf.StepsThatEstablish(v.MissingState); !reflect.DeepEqual(got, []verify.StepID{"get_evidence"}) {
		t.Errorf("fix steps = %v, want [get_evidence]", got)
	}
}
