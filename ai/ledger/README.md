# ai/ledger

Durable, auditable execution history for AI agent runs, built on Joltrin's
existing `sop.BlobStore` abstraction. It records what an agent run did, in
order, so a crashed run can resume from its latest checkpoint and a finished
run can be replayed deterministically for inspection or audit.

This is not a new storage system. Every event, checkpoint, and run manifest
is a blob written through the caller-supplied `sop.BlobStore`, the same
interface already backing the fs, Redis, and Cassandra adapters. Point the
ledger at whichever backend the rest of the application already uses.

## What it records

- **Run lifecycle**: `CreateRun` and `CompleteRun` (success or failure).
- **Append-only events**: tool calls, tool results, state transitions,
  verification decisions, failures, checkpoints, and recovery events, each
  with a sequence number, a type, a timestamp, and an opaque payload.
- **Checkpoints**: a snapshot of agent state, retrievable directly without
  replaying the whole run.

## Basic usage

```go
store := fs.NewBlobStore(dataDir, nil, nil) // or any other sop.BlobStore
l := ledger.New(store)

run, err := l.CreateRun(ctx, runID, metadata)
ev, err := l.Append(ctx, runID, ledger.EventToolCallStarted, payload)
cp, err := l.Checkpoint(ctx, runID, stateSnapshot)
err = l.CompleteRun(ctx, runID, true, resultPayload)
```

## Resuming after an interruption

A run that stops without calling `CompleteRun` (a crash, a killed process, a
deploy) stays in `StatusRunning`. A successor process opens a `Ledger`
against the same backing store and calls `Resume`:

```go
result, err := l.Resume(ctx, runID)
// result.Checkpoint is the latest checkpoint that passed validation, or nil
// result.TailEvents are the events recorded after that checkpoint
```

`Resume` records a `RecoveryStarted` event so the recovery itself shows up in
the run's history. If the newest checkpoint fails validation (corrupted or
missing), `Resume` and `LatestCheckpoint` both fall back to the next older
one automatically.

## Inspecting a finished run

`Replay(ctx, runID)` returns every event in sequence order. If it hits a gap
before the run's recorded last sequence, that is a genuine inconsistency (an
incomplete or corrupted write), not the end of history, and `Replay` returns
the events it did read plus an error identifying where it stopped.

## Scope and assumptions

- One run is assumed to have one active writer at a time (the agent process
  running it, or the successor that resumes it after an interruption).
  Concurrent `Append` calls on the same `Ledger` value are serialized safely;
  two separate `Ledger` instances writing to the *same run* at the same time
  are not a supported configuration.
- Duplicate writes at the same sequence number are handled idempotently when
  the payload matches exactly (the common case after a crash-and-retry), and
  rejected with `ErrDuplicateEvent` when it does not.
- `DeleteRun` removes a run's manifest and all of its events for retention
  cleanup; it is a no-op on an unknown run ID so cleanup jobs can call it
  unconditionally.

See `examples/ledger` for a runnable demonstration of interruption, successor
recovery, and replay using the fs-backed blob store.
