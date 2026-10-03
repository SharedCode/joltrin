// Package main demonstrates ai/replay: recording verification decisions into
// an ai/ledger run, then replaying that run's history against a stricter
// candidate workflow to catch a safety regression before it ships.
//
// Run with zero external infrastructure:
//
//	go run ./examples/replay
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/sharedcode/joltrin/ai/ledger"
	"github.com/sharedcode/joltrin/ai/replay"
	"github.com/sharedcode/joltrin/v5/fs"
	"github.com/sharedcode/joltrin/v5/verify"
)

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

// releaseWorkflow builds a small release pipeline. safety is the only knob
// that differs between the "original" workflow a run actually executed
// under and the "candidate" workflow a proposed policy change would impose.
func releaseWorkflow(safety []verify.SafetyRule) *verify.Workflow {
	wf, err := verify.NewWorkflow(
		[]verify.Step{
			{ID: "take_backup", Establishes: []verify.State{"backup_taken"}},
			{ID: "validate_backup", Requires: []verify.State{"backup_taken"}, Establishes: []verify.State{"backup_validated"}},
			{ID: "drop_prod_db", Establishes: []verify.State{"prod_db_dropped"}},
		},
		safety,
		nil,
	)
	must(err)
	return wf
}

func main() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "joltrin-replay-demo-*")
	must(err)
	defer os.RemoveAll(dir)

	l := ledger.New(fs.NewBlobStore(dir, nil, nil))
	const runID = "release-run-1"

	// The run actually executed under a permissive workflow: nothing
	// stopped it from dropping prod straight after taking a backup, no
	// validation in between.
	permissive := releaseWorkflow(nil)
	_, err = l.CreateRun(ctx, runID, nil)
	must(err)
	trace := verify.NewTrace()
	must(replay.RecordDecision(ctx, l, runID, permissive, trace, "take_backup"))
	must(replay.RecordDecision(ctx, l, runID, permissive, trace, "drop_prod_db"))
	fmt.Println("recorded a run that dropped prod without ever validating the backup")

	// A team proposes a stricter policy: prod can never be dropped unless a
	// backup was already validated. Before rolling that policy out, replay
	// real recorded history against it to see what it would have caught.
	proposed := releaseWorkflow([]verify.SafetyRule{
		{Name: "no-drop-without-validated-backup", Forbidden: "prod_db_dropped", Requires: "backup_validated"},
	})

	report, err := replay.NewHarness(l).ReplayAgainst(ctx, runID, proposed)
	must(err)

	fmt.Printf("replayed %d recorded decision(s) against the proposed policy\n", report.StepsReplayed)
	if report.Clean() {
		fmt.Println("no divergence: the proposed policy agrees with everything this run did")
		return
	}
	for _, d := range report.Divergences {
		fmt.Printf("divergence at seq %d: step %q was originally allowed=%v, the proposed policy would set allowed=%v (%s)\n",
			d.Seq, d.Step, d.OriginallyAllowed, d.CandidateAllowed, d.Kind())
	}
}
