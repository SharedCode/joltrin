package replay

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sharedcode/joltrin/ai/ledger"
	"github.com/sharedcode/joltrin/v5/fs"
	"github.com/sharedcode/joltrin/v5/verify"
)

func newTestLedger(t *testing.T) *ledger.Ledger {
	t.Helper()
	return ledger.New(fs.NewBlobStore(t.TempDir(), nil, nil))
}

// releaseWorkflow mirrors a small, realistic release pipeline: a backup must
// be validated before prod can be dropped, matching the scenario verify's
// own tests use.
func releaseWorkflow(t *testing.T, safety []verify.SafetyRule) *verify.Workflow {
	t.Helper()
	wf, err := verify.NewWorkflow(
		[]verify.Step{
			{ID: "take_backup", Establishes: []verify.State{"backup_taken"}},
			{ID: "validate_backup", Requires: []verify.State{"backup_taken"}, Establishes: []verify.State{"backup_validated"}},
			// drop_prod_db has no precondition of its own: whether it is
			// allowed is controlled entirely by the safety rules passed in,
			// which is exactly the axis these tests vary between the
			// original and candidate workflow.
			{ID: "drop_prod_db", Establishes: []verify.State{"prod_db_dropped"}},
		},
		safety,
		nil,
	)
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	return wf
}

func Test_RecordDecision_AllowedStepIsAuditable(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	wf := releaseWorkflow(t, nil)
	trace := verify.NewTrace()

	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := RecordDecision(ctx, l, "run-1", wf, trace, "take_backup"); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}

	events, err := l.Replay(ctx, "run-1")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	last := events[len(events)-1]
	if last.Type != ledger.EventVerificationDecision {
		t.Fatalf("last event type = %s, want %s", last.Type, ledger.EventVerificationDecision)
	}

	var decoded DecisionPayload
	if err := json.Unmarshal(last.Payload, &decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !decoded.Allowed || decoded.Step != "take_backup" {
		t.Fatalf("decoded = %+v, want allowed take_backup", decoded)
	}
}

func Test_RecordDecision_BlockedStepRecordsViolationDetail(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	safety := []verify.SafetyRule{{Name: "no-drop-without-backup", Forbidden: "prod_db_dropped", Requires: "backup_validated"}}
	wf := releaseWorkflow(t, safety)
	trace := verify.NewTrace()

	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	err := RecordDecision(ctx, l, "run-1", wf, trace, "drop_prod_db")
	if !verify.IsViolation(err) {
		t.Fatalf("RecordDecision err = %v, want a *verify.Violation", err)
	}

	events, rerr := l.Replay(ctx, "run-1")
	if rerr != nil {
		t.Fatalf("Replay: %v", rerr)
	}
	var decoded DecisionPayload
	if err := json.Unmarshal(events[len(events)-1].Payload, &decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if decoded.Allowed {
		t.Fatalf("decoded.Allowed = true, want false")
	}
	if decoded.Rule != "no-drop-without-backup" || decoded.MissingState != "backup_validated" {
		t.Fatalf("decoded = %+v, want rule/missing state populated", decoded)
	}
}

func Test_RecordDecision_UnknownRunSurfacesLedgerError(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	wf := releaseWorkflow(t, nil)
	trace := verify.NewTrace()

	err := RecordDecision(ctx, l, "nope", wf, trace, "take_backup")
	if !errors.Is(err, ledger.ErrRunNotFound) {
		t.Fatalf("err = %v, want wrapped ErrRunNotFound", err)
	}
}

// recordFullRelease runs the three-step release workflow to completion
// against a fresh run in l, recording every decision, and returns the runID.
func recordFullRelease(t *testing.T, ctx context.Context, l *ledger.Ledger, runID string, wf *verify.Workflow) {
	t.Helper()
	if _, err := l.CreateRun(ctx, runID, nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	trace := verify.NewTrace()
	for _, step := range []verify.StepID{"take_backup", "validate_backup", "drop_prod_db"} {
		if err := RecordDecision(ctx, l, runID, wf, trace, step); err != nil {
			t.Fatalf("RecordDecision %s: %v", step, err)
		}
	}
}

func Test_Harness_CleanReplayAgainstSameWorkflow(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	safety := []verify.SafetyRule{{Name: "no-drop-without-backup", Forbidden: "prod_db_dropped", Requires: "backup_validated"}}
	wf := releaseWorkflow(t, safety)
	recordFullRelease(t, ctx, l, "run-1", wf)

	h := NewHarness(l)
	report, err := h.ReplayAgainst(ctx, "run-1", releaseWorkflow(t, safety))
	if err != nil {
		t.Fatalf("ReplayAgainst: %v", err)
	}
	if !report.Clean() {
		t.Fatalf("report not clean: %+v", report.Divergences)
	}
	if report.StepsReplayed != 3 {
		t.Fatalf("StepsReplayed = %d, want 3", report.StepsReplayed)
	}
}

func Test_Harness_DetectsNewlyBlockedRegression(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	// The original workflow has no safety rules, so this run took a
	// shortcut: it dropped prod straight after taking a backup, with no
	// validation step at all, and that was allowed at the time.
	original := releaseWorkflow(t, nil)
	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	trace := verify.NewTrace()
	if err := RecordDecision(ctx, l, "run-1", original, trace, "take_backup"); err != nil {
		t.Fatalf("RecordDecision take_backup: %v", err)
	}
	if err := RecordDecision(ctx, l, "run-1", original, trace, "drop_prod_db"); err != nil {
		t.Fatalf("RecordDecision drop_prod_db: %v", err)
	}

	// Candidate tightens the rules after the fact: dropping prod without a
	// validated backup, which this run actually did, is now forbidden.
	candidate := releaseWorkflow(t, []verify.SafetyRule{
		{Name: "no-drop-without-backup", Forbidden: "prod_db_dropped", Requires: "backup_validated"},
	})

	h := NewHarness(l)
	report, err := h.ReplayAgainst(ctx, "run-1", candidate)
	if err != nil {
		t.Fatalf("ReplayAgainst: %v", err)
	}
	if len(report.Divergences) != 1 {
		t.Fatalf("Divergences = %+v, want exactly 1", report.Divergences)
	}
	d := report.Divergences[0]
	if d.Kind() != "newly_blocked" || d.Step != "drop_prod_db" || !d.OriginallyAllowed || d.CandidateAllowed {
		t.Fatalf("divergence = %+v, want newly_blocked drop_prod_db", d)
	}
}

func Test_Harness_DetectsNewlyAllowedSafetyLoosening(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	strict := releaseWorkflow(t, []verify.SafetyRule{
		{Name: "no-drop-without-backup", Forbidden: "prod_db_dropped", Requires: "backup_validated"},
	})

	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	trace := verify.NewTrace()
	if err := RecordDecision(ctx, l, "run-1", strict, trace, "take_backup"); err != nil {
		t.Fatalf("RecordDecision take_backup: %v", err)
	}
	// Blocked: backup was never validated. This step is recorded as
	// allowed=false and never actually executed.
	if err := RecordDecision(ctx, l, "run-1", strict, trace, "drop_prod_db"); !verify.IsViolation(err) {
		t.Fatalf("expected drop_prod_db to be blocked, got %v", err)
	}

	// Candidate removes the safety rule entirely.
	loose := releaseWorkflow(t, nil)

	h := NewHarness(l)
	report, err := h.ReplayAgainst(ctx, "run-1", loose)
	if err != nil {
		t.Fatalf("ReplayAgainst: %v", err)
	}
	if len(report.Divergences) != 1 {
		t.Fatalf("Divergences = %+v, want exactly 1", report.Divergences)
	}
	d := report.Divergences[0]
	if d.Kind() != "newly_allowed" || d.Step != "drop_prod_db" || d.OriginallyAllowed || !d.CandidateAllowed {
		t.Fatalf("divergence = %+v, want newly_allowed drop_prod_db", d)
	}
}

func Test_Harness_SkipsMalformedDecisionPayload(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	wf := releaseWorkflow(t, nil)

	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	// Not a DecisionPayload at all, some other verification_decision writer.
	if _, err := l.Append(ctx, "run-1", ledger.EventVerificationDecision, []byte("not json")); err != nil {
		t.Fatalf("Append malformed event: %v", err)
	}
	trace := verify.NewTrace()
	if err := RecordDecision(ctx, l, "run-1", wf, trace, "take_backup"); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}

	h := NewHarness(l)
	report, err := h.ReplayAgainst(ctx, "run-1", releaseWorkflow(t, nil))
	if err != nil {
		t.Fatalf("ReplayAgainst: %v", err)
	}
	if report.SkippedEvents != 1 {
		t.Fatalf("SkippedEvents = %d, want 1", report.SkippedEvents)
	}
	if report.StepsReplayed != 1 {
		t.Fatalf("StepsReplayed = %d, want 1", report.StepsReplayed)
	}
}

func Test_Harness_InvalidInputs(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	wf := releaseWorkflow(t, nil)
	h := NewHarness(l)

	if _, err := h.ReplayAgainst(ctx, "", wf); !errors.Is(err, ledger.ErrInvalidRunID) {
		t.Fatalf("empty run id: err = %v, want ErrInvalidRunID", err)
	}
	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := h.ReplayAgainst(ctx, "run-1", nil); err == nil {
		t.Fatalf("nil candidate: want error")
	}
	if _, err := h.ReplayAgainst(ctx, "unknown-run", wf); !errors.Is(err, ledger.ErrRunNotFound) {
		t.Fatalf("unknown run: err = %v, want ErrRunNotFound", err)
	}
}
