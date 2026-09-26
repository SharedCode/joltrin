// Package main demonstrates ai/ledger's crash-recovery story end to end:
// one process runs an agent partway, checkpoints, and is interrupted before
// completing; a successor process opens the same durable storage, resumes
// from the latest checkpoint, finishes the run, and replays the full history.
//
// Run with zero external infrastructure:
//
//	go run ./examples/ledger
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/sharedcode/joltrin/ai/ledger"
	"github.com/sharedcode/joltrin/fs"
)

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	ctx := context.Background()

	dir, err := os.MkdirTemp("", "joltrin-ledger-demo-*")
	must(err)
	defer os.RemoveAll(dir)

	const runID = "demo-run-1"

	fmt.Println("=== process A: runs the agent partway, then is interrupted ===")
	runFirstAttempt(ctx, dir, runID)

	fmt.Println()
	fmt.Println("=== process B: a successor opens the same storage and resumes ===")
	runSuccessor(ctx, dir, runID)

	fmt.Println()
	fmt.Println("=== deterministic replay of the completed run ===")
	replayRun(ctx, dir, runID)
}

// runFirstAttempt plays the part of the agent process that starts the run,
// does some work, checkpoints once, starts one more step, and then "crashes"
// (the function just returns without ever calling CompleteRun).
func runFirstAttempt(ctx context.Context, dir, runID string) {
	l := ledger.New(fs.NewBlobStore(dir, nil, nil))

	_, err := l.CreateRun(ctx, runID, []byte(`{"agent":"release-notes-drafter"}`))
	must(err)
	fmt.Printf("created run %q\n", runID)

	_, err = l.Append(ctx, runID, ledger.EventToolCallStarted, []byte(`{"tool":"fetch_commits","range":"v1.2.0..HEAD"}`))
	must(err)
	res, err := l.Append(ctx, runID, ledger.EventToolCallResult, []byte(`{"tool":"fetch_commits","commit_count":42}`))
	must(err)
	fmt.Printf("recorded tool call and result up to seq %d\n", res.Seq)

	cp, err := l.Checkpoint(ctx, runID, []byte(`{"stage":"commits_fetched","commit_count":42}`))
	must(err)
	fmt.Printf("checkpointed at seq %d\n", cp.Seq)

	// One more step starts, but the process is interrupted before it
	// completes or the run is marked done, no CompleteRun call below.
	_, err = l.Append(ctx, runID, ledger.EventToolCallStarted, []byte(`{"tool":"summarize_commits"}`))
	must(err)
	fmt.Println("started summarize_commits, then the process is interrupted here")
}

// runSuccessor plays the part of a new process picking up the same run after
// the interruption above: it does not know what process A was doing beyond
// what the ledger recorded.
func runSuccessor(ctx context.Context, dir, runID string) {
	l := ledger.New(fs.NewBlobStore(dir, nil, nil))

	result, err := l.Resume(ctx, runID)
	must(err)

	if result.Checkpoint != nil {
		fmt.Printf("recovered checkpoint at seq %d: %s\n", result.Checkpoint.Seq, result.Checkpoint.State)
	}
	fmt.Printf("%d event(s) recorded after the checkpoint were not yet reflected in it:\n", len(result.TailEvents))
	for _, ev := range result.TailEvents {
		fmt.Printf("  seq %d: %s %s\n", ev.Seq, ev.Type, ev.Payload)
	}

	// The successor finishes the interrupted step and completes the run.
	_, err = l.Append(ctx, runID, ledger.EventToolCallResult, []byte(`{"tool":"summarize_commits","summary":"42 commits since v1.2.0"}`))
	must(err)
	must(l.CompleteRun(ctx, runID, true, []byte(`{"result":"release notes drafted"}`)))
	fmt.Println("run completed by the successor process")
}

// replayRun shows deterministic inspection of the finished run: every event,
// in order, exactly as it was recorded across both processes.
func replayRun(ctx context.Context, dir, runID string) {
	l := ledger.New(fs.NewBlobStore(dir, nil, nil))

	events, err := l.Replay(ctx, runID)
	must(err)
	for _, ev := range events {
		fmt.Printf("seq %d  %-22s %s\n", ev.Seq, ev.Type, ev.Payload)
	}

	run, err := l.GetRun(ctx, runID)
	must(err)
	fmt.Printf("final status: %s\n", run.Status)
}
