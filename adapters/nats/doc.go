// Package nats is an optional, opt-in bridge that publishes ai/verify
// barrier decisions to a NATS subject so an external system (a dashboard,
// a SIEM, another service in a team's stack that already runs NATS) can
// observe them without joltrin's embedded core ever depending on NATS or
// requiring it to function.
//
// This package changes nothing about ai/verify. VerifyBridge wraps a
// *verify.Workflow and calls straight through to the same-named method on
// it; the barrier's decision (allow or block) is exactly what
// ai/verify.Workflow would have returned on its own. Publishing to NATS is
// a side effect on the way out, not a precondition for the barrier to
// work: a caller that never imports this package, or that imports it but
// never wires it in, sees no behavior change and no new dependency.
//
// Nothing here touches storage. VerifyBridge only ever calls ai/verify
// methods (CheckSafety, CheckAndCommit, CheckAndCommitIdempotent), which
// hold an in-memory *verify.Trace lock and do not read or write the
// B-Tree. There is no code path from this package into btree, inmemory,
// fs, or any transaction path.
//
// Typical use: a server that already owns a *verify.Workflow and a
// *nats.Conn (tools/mcpserver and tools/a2aagent are two examples in this
// repo, see their use of ai/verify directly) wraps the workflow once at
// startup:
//
//	nc, _ := natsgo.Connect(natsgo.DefaultURL)
//	bridge := nats.NewVerifyBridge(wf, nc, "prod-db-rollout")
//	// use bridge.CheckAndCommit in place of wf.CheckAndCommit
//
// and every barrier decision made through bridge is also published as a
// BarrierDecision event to bridge's subject. See examples/verify_barrier_nats
// for a full runnable demonstration, including a subscriber.
package nats
