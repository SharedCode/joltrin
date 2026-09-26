package ledger

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	sop "github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/fs"
)

// newTestLedger returns a Ledger backed by a real filesystem blob store
// rooted at a fresh temp directory, matching how the package is meant to be
// used in production (through sop.BlobStore, not a hand-rolled test double).
func newTestLedger(t *testing.T) *Ledger {
	t.Helper()
	store := fs.NewBlobStore(t.TempDir(), nil, nil)
	return New(store)
}

func Test_CreateRun_And_GetRun(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)

	run, err := l.CreateRun(ctx, "run-1", []byte(`{"agent":"planner"}`))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if run.Status != StatusRunning {
		t.Fatalf("status = %s, want %s", run.Status, StatusRunning)
	}
	if run.LastSeq != 1 {
		t.Fatalf("LastSeq = %d, want 1 (RunCreated event)", run.LastSeq)
	}

	got, err := l.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.ID != "run-1" || got.Status != StatusRunning {
		t.Fatalf("GetRun mismatch: %+v", got)
	}
}

func Test_CreateRun_InvalidAndDuplicate(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)

	if _, err := l.CreateRun(ctx, "", nil); !errors.Is(err, ErrInvalidRunID) {
		t.Fatalf("empty run id: err = %v, want ErrInvalidRunID", err)
	}

	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := l.CreateRun(ctx, "run-1", nil); !errors.Is(err, ErrRunAlreadyExists) {
		t.Fatalf("second CreateRun: err = %v, want ErrRunAlreadyExists", err)
	}
}

func Test_GetRun_Unknown(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	if _, err := l.GetRun(ctx, "nope"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("err = %v, want ErrRunNotFound", err)
	}
}

func Test_Append_OrderingAndSequence(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)

	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	types := []EventType{EventToolCallStarted, EventToolCallResult, EventStateTransition, EventVerificationDecision}
	for i, et := range types {
		ev, err := l.Append(ctx, "run-1", et, []byte(fmt.Sprintf("payload-%d", i)))
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		wantSeq := uint64(i + 2) // seq 1 is RunCreated
		if ev.Seq != wantSeq {
			t.Fatalf("event %d: seq = %d, want %d", i, ev.Seq, wantSeq)
		}
	}

	events, err := l.Replay(ctx, "run-1")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("len(events) = %d, want 5", len(events))
	}
	for i, ev := range events {
		if ev.Seq != uint64(i+1) {
			t.Fatalf("events[%d].Seq = %d, want %d", i, ev.Seq, i+1)
		}
	}
	if events[0].Type != EventRunCreated {
		t.Fatalf("events[0].Type = %s, want %s", events[0].Type, EventRunCreated)
	}
}

func Test_Append_ClosedRunRejected(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)

	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := l.CompleteRun(ctx, "run-1", true, nil); err != nil {
		t.Fatalf("CompleteRun: %v", err)
	}
	if _, err := l.Append(ctx, "run-1", EventToolCallStarted, nil); !errors.Is(err, ErrRunClosed) {
		t.Fatalf("Append after completion: err = %v, want ErrRunClosed", err)
	}
	if err := l.CompleteRun(ctx, "run-1", true, nil); !errors.Is(err, ErrRunClosed) {
		t.Fatalf("double CompleteRun: err = %v, want ErrRunClosed", err)
	}
}

func Test_CompleteRun_FailurePath(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)

	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := l.Append(ctx, "run-1", EventFailure, []byte("boom")); err != nil {
		t.Fatalf("Append failure event: %v", err)
	}
	if err := l.CompleteRun(ctx, "run-1", false, []byte("terminal failure")); err != nil {
		t.Fatalf("CompleteRun: %v", err)
	}

	run, err := l.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.Status != StatusFailed {
		t.Fatalf("status = %s, want %s", run.Status, StatusFailed)
	}
	if run.CompletedAt == nil {
		t.Fatalf("CompletedAt not set")
	}

	events, err := l.Replay(ctx, "run-1")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	last := events[len(events)-1]
	if last.Type != EventRunFailed {
		t.Fatalf("last event type = %s, want %s", last.Type, EventRunFailed)
	}
}

func Test_PersistsAcrossLedgerInstances(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	l1 := New(fs.NewBlobStore(dir, nil, nil))
	if _, err := l1.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := l1.Append(ctx, "run-1", EventToolCallStarted, []byte("x")); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// A brand new Ledger value pointed at the same directory (simulating a
	// fresh process) must see the same durable state.
	l2 := New(fs.NewBlobStore(dir, nil, nil))
	run, err := l2.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("GetRun from second instance: %v", err)
	}
	if run.LastSeq != 2 {
		t.Fatalf("LastSeq = %d, want 2", run.LastSeq)
	}
	events, err := l2.Replay(ctx, "run-1")
	if err != nil {
		t.Fatalf("Replay from second instance: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}
}

func Test_Checkpoint_And_LatestCheckpoint(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)

	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := l.Checkpoint(ctx, "run-1", []byte(`{"step":1}`)); err != nil {
		t.Fatalf("Checkpoint 1: %v", err)
	}
	if _, err := l.Append(ctx, "run-1", EventStateTransition, nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
	cp2, err := l.Checkpoint(ctx, "run-1", []byte(`{"step":2}`))
	if err != nil {
		t.Fatalf("Checkpoint 2: %v", err)
	}

	latest, err := l.LatestCheckpoint(ctx, "run-1")
	if err != nil {
		t.Fatalf("LatestCheckpoint: %v", err)
	}
	if latest.Seq != cp2.Seq {
		t.Fatalf("latest.Seq = %d, want %d", latest.Seq, cp2.Seq)
	}
	if string(latest.State) != `{"step":2}` {
		t.Fatalf("latest.State = %s", latest.State)
	}
}

func Test_LatestCheckpoint_NoneRecorded(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := l.LatestCheckpoint(ctx, "run-1"); !errors.Is(err, ErrNoCheckpoint) {
		t.Fatalf("err = %v, want ErrNoCheckpoint", err)
	}
}

func Test_LatestCheckpoint_SkipsCorruptedNewest(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := fs.NewBlobStore(dir, nil, nil)
	l := New(store)

	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	goodCP, err := l.Checkpoint(ctx, "run-1", []byte(`{"step":1}`))
	if err != nil {
		t.Fatalf("Checkpoint 1: %v", err)
	}
	badCP, err := l.Checkpoint(ctx, "run-1", []byte(`{"step":2}`))
	if err != nil {
		t.Fatalf("Checkpoint 2: %v", err)
	}

	corruptEventBlob(ctx, t, store, "run-1", badCP.Seq)

	latest, err := l.LatestCheckpoint(ctx, "run-1")
	if err != nil {
		t.Fatalf("LatestCheckpoint: %v", err)
	}
	if latest.Seq != goodCP.Seq {
		t.Fatalf("latest.Seq = %d, want fallback to older checkpoint %d", latest.Seq, goodCP.Seq)
	}
}

func Test_Replay_StopsAtCorruption(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := fs.NewBlobStore(dir, nil, nil)
	l := New(store)

	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := l.Append(ctx, "run-1", EventToolCallStarted, nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := l.Append(ctx, "run-1", EventToolCallResult, nil); err != nil {
		t.Fatalf("Append: %v", err)
	}

	corruptEventBlob(ctx, t, store, "run-1", 2)

	events, err := l.Replay(ctx, "run-1")
	if err == nil {
		t.Fatalf("Replay succeeded, want corruption error")
	}
	if !errors.Is(err, ErrCorruptedEvent) {
		t.Fatalf("err = %v, want ErrCorruptedEvent", err)
	}
	if len(events) != 1 {
		t.Fatalf("events returned before the gap = %d, want 1", len(events))
	}
}

func Test_Append_DuplicateSeqDifferentPayloadRejected(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)

	run, err := l.CreateRun(ctx, "run-1", nil)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	// Force a manifest that has fallen one behind the actual event log, the
	// state a crash between "write event" and "write manifest" leaves
	// behind, then attempt to append a *different* event at that same slot.
	if _, err := l.Append(ctx, "run-1", EventToolCallStarted, []byte("original")); err != nil {
		t.Fatalf("Append original: %v", err)
	}
	run.LastSeq = 1 // rewind our local copy to simulate the lost manifest update
	if _, err := l.appendLocked(ctx, run, EventToolCallStarted, []byte("different")); !errors.Is(err, ErrDuplicateEvent) {
		t.Fatalf("err = %v, want ErrDuplicateEvent", err)
	}
}

func Test_Append_RetryWithSamePayloadIsIdempotent(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)

	run, err := l.CreateRun(ctx, "run-1", nil)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	first, err := l.Append(ctx, "run-1", EventToolCallStarted, []byte("same"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	run.LastSeq = first.Seq - 1
	second, err := l.appendLocked(ctx, run, EventToolCallStarted, []byte("same"))
	if err != nil {
		t.Fatalf("retried appendLocked with identical payload: %v", err)
	}
	if second.Seq != first.Seq || second.Checksum != first.Checksum {
		t.Fatalf("retried event mismatch: %+v vs %+v", second, first)
	}
}

func Test_Resume_InterruptedRun(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// First "process": run some steps, checkpoint, then more steps, and
	// simulate a crash by simply never calling CompleteRun.
	l1 := New(fs.NewBlobStore(dir, nil, nil))
	if _, err := l1.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := l1.Append(ctx, "run-1", EventToolCallStarted, []byte("step-1")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	cp, err := l1.Checkpoint(ctx, "run-1", []byte(`{"progress":1}`))
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if _, err := l1.Append(ctx, "run-1", EventToolCallStarted, []byte("step-2-in-flight")); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Second "process" (a successor) picks up the same durable state.
	l2 := New(fs.NewBlobStore(dir, nil, nil))
	result, err := l2.Resume(ctx, "run-1")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Checkpoint == nil || result.Checkpoint.Seq != cp.Seq {
		t.Fatalf("Checkpoint = %+v, want seq %d", result.Checkpoint, cp.Seq)
	}
	if len(result.TailEvents) != 1 || result.TailEvents[0].Type != EventToolCallStarted {
		t.Fatalf("TailEvents = %+v, want one tool_call_started event", result.TailEvents)
	}

	// Resume must have recorded a RecoveryStarted event, and the run must
	// still be resumable/completable afterward.
	events, err := l2.Replay(ctx, "run-1")
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if events[len(events)-1].Type != EventRecoveryStarted {
		t.Fatalf("last event = %s, want %s", events[len(events)-1].Type, EventRecoveryStarted)
	}

	if err := l2.CompleteRun(ctx, "run-1", true, nil); err != nil {
		t.Fatalf("CompleteRun after resume: %v", err)
	}
}

func Test_Resume_UnknownOrClosedRun(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)

	if _, err := l.Resume(ctx, "nope"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("err = %v, want ErrRunNotFound", err)
	}

	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := l.CompleteRun(ctx, "run-1", true, nil); err != nil {
		t.Fatalf("CompleteRun: %v", err)
	}
	if _, err := l.Resume(ctx, "run-1"); !errors.Is(err, ErrRunClosed) {
		t.Fatalf("Resume completed run: err = %v, want ErrRunClosed", err)
	}
}

func Test_Resume_NoCheckpointReplaysFromStart(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)

	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := l.Append(ctx, "run-1", EventToolCallStarted, nil); err != nil {
		t.Fatalf("Append: %v", err)
	}

	result, err := l.Resume(ctx, "run-1")
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if result.Checkpoint != nil {
		t.Fatalf("Checkpoint = %+v, want nil", result.Checkpoint)
	}
	// RunCreated + ToolCallStarted, both before any checkpoint.
	if len(result.TailEvents) != 2 {
		t.Fatalf("TailEvents = %+v, want 2", result.TailEvents)
	}
}

func Test_Concurrency_AppendIsSerializedAndGapless(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)

	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	const n = 50
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := l.Append(ctx, "run-1", EventStateTransition, []byte(fmt.Sprintf("%d", i)))
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: Append error: %v", i, err)
		}
	}

	run, err := l.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.LastSeq != uint64(n+1) { // +1 for RunCreated
		t.Fatalf("LastSeq = %d, want %d", run.LastSeq, n+1)
	}

	events, err := l.Replay(ctx, "run-1")
	if err != nil {
		t.Fatalf("Replay: %v (concurrent appends left a gap)", err)
	}
	if len(events) != n+1 {
		t.Fatalf("len(events) = %d, want %d", len(events), n+1)
	}
	for i, ev := range events {
		if ev.Seq != uint64(i+1) {
			t.Fatalf("events[%d].Seq = %d, want %d (gap or duplicate under concurrency)", i, ev.Seq, i+1)
		}
	}
}

func Test_Concurrency_MultipleRunsIndependent(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)

	const nRuns = 10
	var wg sync.WaitGroup
	for r := 0; r < nRuns; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			runID := fmt.Sprintf("run-%d", r)
			if _, err := l.CreateRun(ctx, runID, nil); err != nil {
				t.Errorf("CreateRun %s: %v", runID, err)
				return
			}
			for i := 0; i < 5; i++ {
				if _, err := l.Append(ctx, runID, EventToolCallStarted, nil); err != nil {
					t.Errorf("Append %s: %v", runID, err)
					return
				}
			}
			if err := l.CompleteRun(ctx, runID, true, nil); err != nil {
				t.Errorf("CompleteRun %s: %v", runID, err)
			}
		}(r)
	}
	wg.Wait()

	for r := 0; r < nRuns; r++ {
		runID := fmt.Sprintf("run-%d", r)
		run, err := l.GetRun(ctx, runID)
		if err != nil {
			t.Fatalf("GetRun %s: %v", runID, err)
		}
		if run.Status != StatusCompleted || run.LastSeq != 7 { // created + 5 tool calls + completed
			t.Fatalf("run %s = %+v, want completed with LastSeq 7", runID, run)
		}
	}
}

func Test_DeleteRun_Cleanup(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)

	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if _, err := l.Append(ctx, "run-1", EventToolCallStarted, nil); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := l.Checkpoint(ctx, "run-1", []byte("state")); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	if err := l.DeleteRun(ctx, "run-1"); err != nil {
		t.Fatalf("DeleteRun: %v", err)
	}

	if _, err := l.GetRun(ctx, "run-1"); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("GetRun after delete: err = %v, want ErrRunNotFound", err)
	}
	if _, err := l.readEvent(ctx, "run-1", 1); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("readEvent after delete: err = %v, want ErrEventNotFound", err)
	}

	// A fresh run may reuse the same ID after cleanup.
	if _, err := l.CreateRun(ctx, "run-1", nil); err != nil {
		t.Fatalf("CreateRun after delete: %v", err)
	}
}

func Test_DeleteRun_UnknownIsNoop(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	if err := l.DeleteRun(ctx, "nope"); err != nil {
		t.Fatalf("DeleteRun unknown run: %v", err)
	}
}

// corruptEventBlob overwrites the stored bytes for one event so it fails
// checksum validation on read, simulating on-disk bit rot or a torn write.
func corruptEventBlob(ctx context.Context, t *testing.T, store sop.BlobStore, runID string, seq uint64) {
	t.Helper()
	err := store.Update(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{
		BlobTable: eventsTable,
		Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{
			Key:   eventKey(runID, seq),
			Value: []byte(`{"run_id":"` + runID + `","seq":` + fmt.Sprint(seq) + `,"type":"tool_call_started","payload":"dGFtcGVyZWQ=","checksum":"deadbeef"}`),
		}},
	}})
	if err != nil {
		t.Fatalf("corruptEventBlob: %v", err)
	}
}
