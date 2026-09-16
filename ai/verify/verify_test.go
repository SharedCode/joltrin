package verify

import (
	"errors"
	"strings"
	"testing"
)

// dbMaintenanceWorkflow builds the exact scenario from the design brief:
// production database drop must never happen unless backup was already
// validated, and a rollback path must always be reachable from wherever
// execution currently stands, including after a partial failure.
func dbMaintenanceWorkflow(t *testing.T) *Workflow {
	t.Helper()
	steps := []Step{
		{ID: "take_backup", Establishes: []State{"backup_taken"}},
		{ID: "validate_backup", Requires: []State{"backup_taken"}, Establishes: []State{"backup_validated"}},
		{ID: "drop_prod_db", Requires: []State{"backup_validated"}, Establishes: []State{"prod_db_dropped"}},
		{ID: "restore_from_backup", Requires: []State{"backup_validated"}, Establishes: []State{"rollback_complete"}},
		// Rollback must still be reachable even after prod was actually
		// dropped, not just from the "about to drop" state, that's the
		// entire point of having a validated backup. Without this step,
		// prod_db_dropped is a dead end and VerifyReachability correctly
		// flags it (see Test_VerifyReachability_CatchesADeadEnd for that
		// exact failure mode on a workflow that omits this step).
		{ID: "restore_from_backup_post_drop", Requires: []State{"prod_db_dropped"}, Establishes: []State{"rollback_complete"}},
		{ID: "partial_failure", Requires: []State{"backup_taken"}, Establishes: []State{"infra_degraded"}},
		{ID: "recover_degraded_infra", Requires: []State{"infra_degraded"}, Establishes: []State{"backup_validated"}},
	}
	safety := []SafetyRule{
		{Name: "no-drop-without-validated-backup", Forbidden: "prod_db_dropped", Requires: "backup_validated"},
	}
	reachability := []ReachabilityRule{
		{Name: "rollback-always-reachable", Target: "rollback_complete"},
	}
	wf, err := NewWorkflow(steps, safety, reachability)
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	return wf
}

func Test_CheckSafety_BlocksDropWithoutValidatedBackup(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	trace := NewTrace()

	// No steps executed yet: dropping prod is not yet safe.
	err := wf.CheckSafety(trace, "drop_prod_db")
	if err == nil {
		t.Fatal("expected drop_prod_db to be blocked with no prior trace")
	}
	if !strings.Contains(err.Error(), "requires state") {
		t.Fatalf("expected a precondition error, got: %v", err)
	}
}

func Test_CheckSafety_AllowsDropAfterValidatedBackup(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	trace := NewTrace()

	for _, step := range []StepID{"take_backup", "validate_backup"} {
		if err := wf.CheckSafety(trace, step); err != nil {
			t.Fatalf("step %q unexpectedly blocked: %v", step, err)
		}
		if err := wf.Commit(trace, step); err != nil {
			t.Fatalf("Commit(%q): %v", step, err)
		}
	}

	if err := wf.CheckSafety(trace, "drop_prod_db"); err != nil {
		t.Fatalf("expected drop_prod_db to be allowed after a validated backup, got: %v", err)
	}
}

// Test_CheckSafety_CatchesAgentTryingToSkipValidation is the exact failure
// mode the design brief names: an autonomous agent attempting to execute a
// destructive step out of order. This proves the barrier certificate
// actually blocks it rather than trusting the agent's own claimed reasoning.
func Test_CheckSafety_CatchesAgentTryingToSkipValidation(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	trace := NewTrace()

	// Agent takes a backup but never validates it, then tries to drop prod
	// anyway (e.g. because it hallucinated that validation happened).
	if err := wf.CheckSafety(trace, "take_backup"); err != nil {
		t.Fatalf("take_backup unexpectedly blocked: %v", err)
	}
	if err := wf.Commit(trace, "take_backup"); err != nil {
		t.Fatalf("Commit(take_backup): %v", err)
	}

	err := wf.CheckSafety(trace, "drop_prod_db")
	if err == nil {
		t.Fatal("expected drop_prod_db to be blocked: backup was taken but never validated")
	}
}

func Test_VerifyReachability_PassesOnWellFormedWorkflow(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	if err := wf.VerifyReachability(); err != nil {
		t.Fatalf("expected rollback_complete to be reachable from every state, got: %v", err)
	}
}

// Test_VerifyReachability_CatchesADeadEnd removes the recovery path from a
// degraded-infra state, so infra_degraded can no longer reach
// rollback_complete, and confirms VerifyReachability actually catches it
// instead of passing silently.
func Test_VerifyReachability_CatchesADeadEnd(t *testing.T) {
	steps := []Step{
		{ID: "take_backup", Establishes: []State{"backup_taken"}},
		{ID: "validate_backup", Requires: []State{"backup_taken"}, Establishes: []State{"backup_validated"}},
		{ID: "restore_from_backup", Requires: []State{"backup_validated"}, Establishes: []State{"rollback_complete"}},
		{ID: "partial_failure", Requires: []State{"backup_taken"}, Establishes: []State{"infra_degraded"}},
		// No step establishes a path out of infra_degraded in this version.
	}
	wf, err := NewWorkflow(steps, nil, []ReachabilityRule{
		{Name: "rollback-always-reachable", Target: "rollback_complete"},
	})
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}

	err = wf.VerifyReachability()
	if err == nil {
		t.Fatal("expected VerifyReachability to catch infra_degraded as a dead end")
	}
	if !strings.Contains(err.Error(), "infra_degraded") {
		t.Fatalf("expected the error to name the stuck state infra_degraded, got: %v", err)
	}
}

func Test_CheckSafety_UnknownStep(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	trace := NewTrace()
	if err := wf.CheckSafety(trace, "nonexistent_step"); err == nil {
		t.Fatal("expected an error for an unknown step ID")
	}
}

func Test_NewWorkflow_RejectsDuplicateStepIDs(t *testing.T) {
	_, err := NewWorkflow([]Step{
		{ID: "a"},
		{ID: "a"},
	}, nil, nil)
	if err == nil {
		t.Fatal("expected an error for duplicate step IDs")
	}
}

func Test_Violation_MissingState_SetOnPreconditionBlock(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	trace := NewTrace()

	err := wf.CheckSafety(trace, "validate_backup")
	var v *Violation
	if !errors.As(err, &v) {
		t.Fatalf("expected a *Violation, got: %v", err)
	}
	if v.MissingState != "backup_taken" {
		t.Fatalf("expected MissingState %q, got %q", "backup_taken", v.MissingState)
	}
}

func Test_Violation_MissingState_SetOnSafetyRuleBlock(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	trace := NewTrace()
	if err := wf.CheckSafety(trace, "take_backup"); err != nil {
		t.Fatalf("take_backup unexpectedly blocked: %v", err)
	}
	if err := wf.Commit(trace, "take_backup"); err != nil {
		t.Fatalf("Commit(take_backup): %v", err)
	}

	err := wf.CheckSafety(trace, "drop_prod_db")
	var v *Violation
	if !errors.As(err, &v) {
		t.Fatalf("expected a *Violation, got: %v", err)
	}
	if v.MissingState != "backup_validated" {
		t.Fatalf("expected MissingState %q, got %q", "backup_validated", v.MissingState)
	}
}

func Test_StepsThatEstablish_FindsAllMatchingSteps(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)

	// Two steps establish rollback_complete: the pre-drop and post-drop
	// restore paths. Order must be deterministic since map iteration isn't.
	got := wf.StepsThatEstablish("rollback_complete")
	want := []StepID{"restore_from_backup", "restore_from_backup_post_drop"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

func Test_StepsThatEstablish_EmptyForUnestablishedState(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	if got := wf.StepsThatEstablish("no_such_state"); len(got) != 0 {
		t.Fatalf("expected no steps, got %v", got)
	}
}
