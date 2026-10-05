package blocklog

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5/verify"
)

const (
	wf  = "db-maintenance"
	ver = "v1"
)

func TestStatsCountsRunsOnceEach(t *testing.T) {
	l := New()
	for i := 0; i < 3; i++ {
		l.RecordRun(wf, ver, "run-1")
	}
	l.RecordRun(wf, ver, "run-2")
	l.RecordRun("other", ver, "run-3")
	l.RecordRun(wf, "v2", "run-4")

	if got := l.Stats(wf, ver).Runs; got != 2 {
		t.Errorf("Runs = %d, want 2: a run counts once, and other workflows and versions do not count", got)
	}
}

func TestStatsRateAndRecoveryPerRule(t *testing.T) {
	l := New()
	for _, run := range []string{"r1", "r2", "r3", "r4"} {
		l.RecordRun(wf, ver, run)
	}
	// r1 and r2 are blocked by the precondition. r1 then gets past it.
	l.Record(entry("r1", "drop_prod_db"))
	l.Record(entry("r2", "drop_prod_db"))
	if n, _ := l.RecordRecovery(wf, ver, "r1", "drop_prod_db"); n != 1 {
		t.Fatalf("recovery of a blocked step recorded %d, want 1", n)
	}
	// r3 is blocked by a named rule and never recovers. r4 is never blocked.
	e := entry("r3", "drop_prod_db")
	e.BlockedBy = "no-drop-without-validated-backup"
	l.Record(e)

	st := l.Stats(wf, ver)
	if st.Runs != 4 || len(st.Rules) != 2 {
		t.Fatalf("want 4 runs and 2 rules, got %+v", st)
	}
	pre, rule := st.Rules[0], st.Rules[1]
	if pre.BlockedBy != "precondition" || pre.BlockedRuns != 2 || pre.RecoveredRuns != 1 {
		t.Errorf("precondition: %+v, want 2 blocked runs and 1 recovered", pre)
	}
	if rule.BlockedBy != "no-drop-without-validated-backup" || rule.BlockedRuns != 1 || rule.RecoveredRuns != 0 {
		t.Errorf("named rule: %+v, want 1 blocked run and 0 recovered", rule)
	}
}

func TestRecoveryNeedsEveryBlockedStepOfTheRule(t *testing.T) {
	l := New()
	l.RecordRun(wf, ver, "r1")
	l.Record(entry("r1", "drop_prod_db"))
	l.Record(entry("r1", "restore_from_backup"))
	l.RecordRecovery(wf, ver, "r1", "drop_prod_db")

	if r := l.Stats(wf, ver).Rules[0]; r.BlockedRuns != 1 || r.RecoveredRuns != 0 {
		t.Errorf("one of two blocked steps recovered, the run has not recovered: %+v", r)
	}
	l.RecordRecovery(wf, ver, "r1", "restore_from_backup")
	if r := l.Stats(wf, ver).Rules[0]; r.RecoveredRuns != 1 {
		t.Errorf("both blocked steps recovered, want the run recovered: %+v", r)
	}
}

func TestRecoveryOfAStepThatWasNeverBlockedRecordsNothing(t *testing.T) {
	l := New()
	l.RecordRun(wf, ver, "r1")
	l.Record(entry("r1", "drop_prod_db"))
	if n, _ := l.RecordRecovery(wf, ver, "r1", "take_backup"); n != 0 {
		t.Errorf("recorded %d recoveries for a step that was not blocked", n)
	}
	if n, _ := l.RecordRecovery(wf, ver, "r2", "drop_prod_db"); n != 0 {
		t.Errorf("recorded %d recoveries for another run's step", n)
	}
	l.RecordRecovery(wf, ver, "r1", "drop_prod_db")
	if n, _ := l.RecordRecovery(wf, ver, "r1", "drop_prod_db"); n != 0 {
		t.Errorf("a recovery counts once, recorded %d more", n)
	}
}

func TestRunAndRecoveryEntriesDoNotChangeSummaries(t *testing.T) {
	l := New()
	l.RecordRun(wf, ver, "r1")
	l.Record(entry("r1", "drop_prod_db"))
	l.RecordRecovery(wf, ver, "r1", "drop_prod_db")
	got := l.Summaries(wf, ver)
	if len(got) != 1 || got[0].Runs != 1 {
		t.Errorf("summaries should still hold only the one block, got %+v", got)
	}
}

func TestStatsSurviveReopenAndOldFilesStillLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocks.jsonl")
	// A line written before entries had a kind is a block.
	old := `{"at":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","workflow":"db-maintenance","version":"v1","trace_id":"r0","step":"drop_prod_db","blocked_by":"precondition","missing_state":"backup_validated"}` + "\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l.RecordRun(wf, ver, "r1")
	l.Record(entry("r1", "drop_prod_db"))
	l.RecordRecovery(wf, ver, "r1", "drop_prod_db")
	l.Close()

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	st := again.Stats(wf, ver)
	if st.Runs != 1 || len(st.Rules) != 1 || st.Rules[0].BlockedRuns != 2 || st.Rules[0].RecoveredRuns != 1 {
		t.Errorf("after reopen: %+v, want 1 run, 2 blocked runs (the old line counts), 1 recovered", st)
	}
}

func TestStatsIgnoreExpiredEntries(t *testing.T) {
	now := time.Now()
	l := New(WithClock(func() time.Time { return now }), WithTTL(time.Hour))
	l.RecordRun(wf, ver, "r1")
	l.Record(entry("r1", "drop_prod_db"))
	now = now.Add(2 * time.Hour)
	if st := l.Stats(wf, ver); st.Runs != 0 || len(st.Rules) != 0 {
		t.Errorf("expired entries must not count: %+v", st)
	}
}

// Summary must agree with the matching row of Summaries, which is what the
// lessons are built from.
func TestSummaryMatchesTheRowInSummaries(t *testing.T) {
	l := New()
	for i := 0; i < 40; i++ {
		e := entry(fmt.Sprintf("run-%d", i), verify.StepID(fmt.Sprintf("step_%d", i%3)))
		e.MissingState = verify.State(fmt.Sprintf("state_%d", i%2))
		if _, err := l.Record(e); err != nil {
			t.Fatal(err)
		}
	}
	rows := l.Summaries("db-maintenance", "v1")
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for _, want := range rows {
		got, ok := l.Summary("db-maintenance", "v1", want.Step, want.BlockedBy, want.MissingState)
		if !ok || got.Runs != want.Runs || !got.LastSeen.Equal(want.LastSeen) || fmt.Sprint(got.EstablishedBy) != fmt.Sprint(want.EstablishedBy) {
			t.Errorf("Summary(%s,%s,%s) = %+v, %v; Summaries has %+v", want.Step, want.BlockedBy, want.MissingState, got, ok, want)
		}
	}
	if _, ok := l.Summary("db-maintenance", "v1", "no_such_step", "precondition", "x"); ok {
		t.Error("a block that was never recorded must not be found")
	}
	if _, ok := l.Summary("db-maintenance", "other-version", rows[0].Step, rows[0].BlockedBy, rows[0].MissingState); ok {
		t.Error("another runbook version must not match")
	}
}

func TestStatsCountsARepeatedBlockOnceAndIgnoresRunsThatNeverBlocked(t *testing.T) {
	l := New()
	l.RecordRun(wf, ver, "r1")
	l.RecordRun(wf, ver, "clean")
	// The same step blocked three times in one run is one blocked step.
	for i := 0; i < 3; i++ {
		l.Record(entry("r1", "drop_prod_db"))
	}
	st := l.Stats(wf, ver)
	if st.Runs != 2 || len(st.Rules) != 1 || st.Rules[0].BlockedRuns != 1 || st.Rules[0].RecoveredRuns != 0 {
		t.Fatalf("want 2 runs and one rule that blocked 1 run: %+v", st)
	}
	l.RecordRecovery(wf, ver, "r1", "drop_prod_db")
	if r := l.Stats(wf, ver).Rules[0]; r.BlockedRuns != 1 || r.RecoveredRuns != 1 {
		t.Errorf("a repeated block recovers with one commit: %+v", r)
	}
}
