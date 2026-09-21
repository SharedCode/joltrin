// Package main is examples/verify_barrier plus one opt-in addition:
// adapters/nats.VerifyBridge, publishing every barrier decision to NATS so
// an external subscriber (this program's own second goroutine, standing in
// for a dashboard or another service) can observe them in real time. The
// barrier itself behaves identically to examples/verify_barrier; nothing
// about ai/verify changes, and joltrin's embedded core never depends on
// NATS, only this example (and any caller who opts in the same way) does.
//
// Needs a NATS server reachable at nats://127.0.0.1:4222 (the default of
// `nats-server` with no flags, or `docker run -p 4222:4222 nats`). Run with:
//
//	go run ./examples/verify_barrier_nats
package main

import (
	"fmt"
	"time"

	natsgo "github.com/nats-io/nats.go"

	natsbridge "github.com/sharedcode/joltrin/adapters/nats"
	"github.com/sharedcode/joltrin/ai/verify"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	nc, err := natsgo.Connect(natsgo.DefaultURL)
	if err != nil {
		fmt.Printf("could not connect to NATS at %s: %v\n", natsgo.DefaultURL, err)
		fmt.Println("start one with `nats-server` or `docker run -p 4222:4222 nats` and try again")
		return
	}
	defer nc.Close()

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
	must(wf.VerifyReachability())

	bridge := natsbridge.NewVerifyBridge(wf, nc, "prod-db-rollout")
	bridge.OnPublishError(func(err error) { fmt.Printf("  ! nats publish error: %v\n", err) })

	// Stand-in for the external subscriber this bridge exists for: any
	// service that already runs NATS, subscribing to the same subject a
	// completely separate process could subscribe to.
	sub, err := natsbridge.Subscribe(nc, bridge.Subject(), func(ev natsbridge.BarrierDecision) {
		if ev.Allowed {
			fmt.Printf("  [subscriber] %s: ALLOWED\n", ev.Step)
		} else {
			fmt.Printf("  [subscriber] %s: BLOCKED (%s, missing %q)\n", ev.Step, ev.Rule, ev.MissingState)
		}
	})
	must(err)
	defer sub.Unsubscribe()

	trace := verify.NewTrace()
	pause := 300 * time.Millisecond

	fmt.Printf("subscribed an observer to %q\n\n", bridge.Subject())

	fmt.Println("agent: \"backup looks fine, dropping prod now\"")
	fmt.Print("→ execute_step(drop_prod_db) ... ")
	if err := bridge.CheckAndCommit(trace, "drop_prod_db"); err != nil {
		fmt.Println("BLOCKED")
		fmt.Printf("  barrier certificate: %v\n", err)
	}
	time.Sleep(pause)

	fmt.Println("\nagent: okay, taking a real backup first")
	fmt.Print("→ execute_step(take_backup) ... ")
	must(bridge.CheckAndCommit(trace, "take_backup"))
	fmt.Println("committed")
	time.Sleep(pause)

	fmt.Print("→ execute_step(validate_backup) ... ")
	must(bridge.CheckAndCommit(trace, "validate_backup"))
	fmt.Println("committed")
	time.Sleep(pause)

	fmt.Println("\nagent: backup is now validated, retrying the drop")
	fmt.Print("→ execute_step(drop_prod_db) ... ")
	must(bridge.CheckAndCommit(trace, "drop_prod_db"))
	fmt.Println("ALLOWED, committed")
	time.Sleep(pause)

	fmt.Printf("\ntrace: %v\n", trace.ExecutedSteps())
}
