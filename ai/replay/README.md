# ai/replay

Closes the loop between [`ai/ledger`](../ledger) and [`ai/verify`](../verify):
records verification-barrier decisions into a run's ledger, then replays a
run's recorded decision history against a candidate `verify.Workflow` to
catch behavioral drift before a workflow definition change ships.

The underlying idea, run a proposed policy change against a real recorded
trace before trusting it with live traffic, shows up under different names
across the industry: replay testing in durable-execution systems, shadow
evaluation for a guardrail or policy change, WAF rule shadow mode. Here,
"real recorded trace" is an `ai/ledger` run's `verification_decision` events,
and "proposed policy" is a candidate `verify.Workflow`.

This is not a live shadow-traffic mirror. It replays already-recorded,
completed history entirely offline; no tool call ever executes again, which
is what makes it safe to run against arbitrary past runs on demand.

## Recording decisions

`RecordDecision` wraps `verify.Workflow.CheckAndCommit` and appends a
`verification_decision` event with the outcome, allowed or blocked, to the
run's ledger:

```go
err := replay.RecordDecision(ctx, ledgerInstance, runID, workflow, trace, step)
```

It returns the exact same error `CheckAndCommit` would, so a caller that
already gates execution on the barrier can adopt this unchanged and get an
auditable, replayable record for free.

## Replaying against a candidate workflow

```go
report, err := replay.NewHarness(ledgerInstance).ReplayAgainst(ctx, runID, candidateWorkflow)
if !report.Clean() {
    for _, d := range report.Divergences {
        // d.Kind() is "newly_blocked" (candidate is stricter than what
        // actually ran, a possible regression) or "newly_allowed" (candidate
        // is looser than what actually ran, a safety loosening).
    }
}
```

The harness reconstructs the run's actual execution trace step by step: a
step the run executed is re-checked against the candidate and then force-
committed regardless of the candidate's answer, because it is historical
fact that it happened and later steps must be judged against that same
history. A step the run never executed (it was blocked at the time) is only
checked, never committed, since it never affected real state.

`Report.SkippedEvents` counts `verification_decision` events that did not
decode as a `DecisionPayload` (recorded by something other than
`RecordDecision`); replay continues past them. `Report.Incomplete` is set
when the underlying ledger run itself reported a gap or corruption, per
`ledger.Replay`.

See `examples/replay` for a runnable demonstration: a run that dropped a
database without validating a backup, replayed against a policy that would
have forbidden exactly that.
