// Package main demonstrates ai/verify's barrier certificate blocking an
// out-of-order operational step in real time, the exact scenario described
// in docs/MCP_A2A_AND_VERIFICATION_ENGINE.md: an agent must not be allowed
// to drop the production database before a backup has been taken and
// validated, no matter what it claims about its own prior actions.
//
// Run with zero external infrastructure:
//
//	go run ./examples/verify_barrier
package main

import (
	"fmt"
	"time"

	"github.com/sharedcode/joltrin/v5/verify"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	wf, err := verify.NewWorkflow(
		[]verify.Step{
			{ID: "take_backup", Establishes: []verify.State{"backup_taken"}},
			{ID: "validate_backup", Requires: []verify.State{"backup_taken"}, Establishes: []verify.State{"backup_validated"}},
			{ID: "drop_prod_db", Requires: []verify.State{"backup_validated"}, Establishes: []verify.State{"prod_db_dropped"}},
			{ID: "restore_from_backup", Requires: []verify.State{"backup_validated"}, Establishes: []verify.State{"rollback_complete"}},
			{ID: "restore_from_backup_post_drop", Requires: []verify.State{"prod_db_dropped"}, Establishes: []verify.State{"rollback_complete"}},
		},
		[]verify.SafetyRule{
			{Name: "no-drop-without-validated-backup", Forbidden: "prod_db_dropped", Requires: "backup_validated"},
		},
		[]verify.ReachabilityRule{
			{Name: "rollback-always-reachable", Target: "rollback_complete"},
		},
	)
	must(err)

	// Registration-time check: refuse to even serve a runbook whose
	// rollback path can dead-end.
	must(wf.VerifyReachability())
	fmt.Println("✓ workflow registered: rollback_complete is reachable from every state")
	fmt.Println()

	trace := verify.NewTrace()
	pause := 700 * time.Millisecond

	fmt.Println("agent: \"backup looks fine, dropping prod now\"")
	time.Sleep(pause)
	fmt.Print("→ execute_step(drop_prod_db) ... ")
	if err := wf.CheckSafety(trace, "drop_prod_db"); err != nil {
		fmt.Println("BLOCKED")
		fmt.Printf("  barrier certificate: %v\n\n", err)
	} else {
		fmt.Println("allowed (unexpected)")
	}
	time.Sleep(pause)

	fmt.Println("agent: okay, taking a real backup first")
	time.Sleep(pause)
	fmt.Print("→ execute_step(take_backup) ... ")
	must(wf.CheckSafety(trace, "take_backup"))
	must(wf.Commit(trace, "take_backup"))
	fmt.Println("committed")
	time.Sleep(pause)

	fmt.Print("→ execute_step(validate_backup) ... ")
	must(wf.CheckSafety(trace, "validate_backup"))
	must(wf.Commit(trace, "validate_backup"))
	fmt.Println("committed")
	time.Sleep(pause)

	fmt.Println()
	fmt.Println("agent: backup is now validated, retrying the drop")
	time.Sleep(pause)
	fmt.Print("→ execute_step(drop_prod_db) ... ")
	if err := wf.CheckSafety(trace, "drop_prod_db"); err != nil {
		fmt.Printf("BLOCKED (unexpected): %v\n", err)
		return
	}
	must(wf.Commit(trace, "drop_prod_db"))
	fmt.Println("ALLOWED, committed")
	fmt.Println()
	fmt.Printf("trace: %v\n", trace.Executed)
}
