// Package ledger provides a durable, auditable execution history for AI agent
// runs, built directly on Joltrin's existing sop.BlobStore abstraction (the
// same interface backing the fs, Redis, and Cassandra storage adapters). It
// does not introduce a parallel storage system: every event, checkpoint, and
// run manifest is a blob written through the caller-supplied sop.BlobStore.
//
// A run's events are immutable once written, keyed deterministically from
// (runID, sequence number), so replay never needs a separate index: the
// caller reads sequence 1, 2, 3, ... until it runs out. A small manifest blob
// per run tracks status, the last sequence number, and which sequences are
// checkpoints, so status lookups and "give me the latest checkpoint" queries
// don't require scanning the whole run.
//
// What this is: an append-only, crash-recoverable event log scoped to one
// agent run at a time, plus a resume path that finds the latest verifiably
// intact checkpoint and reports what happened after it.
//
// What this is not: a distributed consensus log or a multi-writer database.
// A single run is assumed to have a single active writer (the agent process
// executing it, or its successor after a restart); concurrent Append calls
// from multiple goroutines against the *same* Ledger value are serialized
// safely, but two separate Ledger instances (e.g. two processes) writing to
// the same run at the same time can race on the manifest. That matches the
// intended usage: one agent owns a run until it hands off to a successor.
package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	sop "github.com/sharedcode/joltrin/v5"
)

// EventType identifies the kind of event recorded in a run's ledger.
type EventType string

// The set of event types a run's ledger can contain. Callers may also record
// tool-specific events under EventStateTransition or EventFailure by putting
// the detail in the event payload; the type set intentionally stays small so
// replay logic doesn't need to special-case an open-ended list.
const (
	EventRunCreated           EventType = "run_created"
	EventRunCompleted         EventType = "run_completed"
	EventRunFailed            EventType = "run_failed"
	EventToolCallStarted      EventType = "tool_call_started"
	EventToolCallResult       EventType = "tool_call_result"
	EventStateTransition      EventType = "state_transition"
	EventVerificationDecision EventType = "verification_decision"
	EventCheckpoint           EventType = "checkpoint"
	EventRecoveryStarted      EventType = "recovery_started"
	EventRecoveryCompleted    EventType = "recovery_completed"
	EventFailure              EventType = "failure"
)

// RunStatus is the lifecycle state of an agent run as recorded in its manifest.
type RunStatus string

const (
	StatusRunning   RunStatus = "running"
	StatusCompleted RunStatus = "completed"
	StatusFailed    RunStatus = "failed"
)

// Sentinel errors. Callers should use errors.Is against these rather than
// comparing strings; several are wrapped with run/sequence context via %w.
var (
	ErrInvalidRunID     = errors.New("ledger: run id must not be empty")
	ErrRunNotFound      = errors.New("ledger: run not found")
	ErrRunAlreadyExists = errors.New("ledger: run already exists")
	ErrRunClosed        = errors.New("ledger: run already completed or failed")
	ErrDuplicateEvent   = errors.New("ledger: duplicate event with a different payload than the one already recorded")
	ErrCorruptedEvent   = errors.New("ledger: event failed checksum or decode validation")
	ErrNoCheckpoint     = errors.New("ledger: no valid checkpoint recorded for this run")
	ErrEventNotFound    = errors.New("ledger: event not found")
)

// Event is one immutable, append-only entry in a run's ledger.
type Event struct {
	RunID     string    `json:"run_id"`
	Seq       uint64    `json:"seq"`
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Payload   []byte    `json:"payload,omitempty"`
	Checksum  string    `json:"checksum"`
}

// Run is the manifest record tracking one agent run's lifecycle.
type Run struct {
	ID             string     `json:"id"`
	Status         RunStatus  `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	LastSeq        uint64     `json:"last_seq"`
	CheckpointSeqs []uint64   `json:"checkpoint_seqs,omitempty"`
	Metadata       []byte     `json:"metadata,omitempty"`
}

// clone returns a deep-enough copy so callers can't mutate Ledger-internal state.
func (r *Run) clone() *Run {
	c := *r
	if r.CompletedAt != nil {
		ts := *r.CompletedAt
		c.CompletedAt = &ts
	}
	c.CheckpointSeqs = append([]uint64(nil), r.CheckpointSeqs...)
	c.Metadata = append([]byte(nil), r.Metadata...)
	return &c
}

// Checkpoint is a point-in-time snapshot of agent state recorded mid-run so
// execution can resume without replaying from the beginning.
type Checkpoint struct {
	RunID     string
	Seq       uint64
	Timestamp time.Time
	State     []byte
}

// ResumeResult is what a successor process needs to continue an interrupted
// run: the latest intact checkpoint (if any) and the events recorded after
// it, in order.
type ResumeResult struct {
	Run        *Run
	Checkpoint *Checkpoint // nil if the run never checkpointed
	TailEvents []Event     // events after Checkpoint.Seq (or from seq 1 if none)
}

const (
	eventsTable = "ledger_events"
	runsTable   = "ledger_runs"
)

// ledgerNamespace roots the deterministic UUIDs the ledger derives for run
// manifests and events, so blob keys never need a separate lookup index.
var ledgerNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("github.com/sharedcode/joltrin/ai/ledger"))

func runKey(runID string) sop.UUID {
	return sop.UUID(uuid.NewSHA1(ledgerNamespace, []byte("run:"+runID)))
}

func eventKey(runID string, seq uint64) sop.UUID {
	return sop.UUID(uuid.NewSHA1(ledgerNamespace, []byte(fmt.Sprintf("run:%s:event:%d", runID, seq))))
}

func computeChecksum(runID string, seq uint64, evType EventType, payload []byte) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%d\x00%s\x00", runID, seq, evType)
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// Ledger records and retrieves agent run history through a sop.BlobStore.
// A single Ledger value is safe for concurrent use.
type Ledger struct {
	store sop.BlobStore

	mu       sync.Mutex
	runLocks map[string]*sync.Mutex
}

// New constructs a Ledger backed by the given blob store. Any sop.BlobStore
// implementation works (fs, Redis, Cassandra, or a test double); the ledger
// does not care which backend persists the blobs.
func New(store sop.BlobStore) *Ledger {
	return &Ledger{store: store, runLocks: make(map[string]*sync.Mutex)}
}

func (l *Ledger) lockFor(runID string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	m, ok := l.runLocks[runID]
	if !ok {
		m = &sync.Mutex{}
		l.runLocks[runID] = m
	}
	return m
}

// backendNotFound reports whether err is the "blob does not exist" case from
// the underlying sop.BlobStore. Every in-tree backend (fs, Redis, Cassandra)
// surfaces a missing key as (a wrapped) os.ErrNotExist, which is also the
// error retryIO passes through unwrapped since it is explicitly classified
// as non-retryable and non-failover-qualified in this repo's fs adapter.
func backendNotFound(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

func (l *Ledger) readRun(ctx context.Context, runID string) (*Run, error) {
	raw, err := l.store.GetOne(ctx, runsTable, runKey(runID))
	if err != nil {
		if backendNotFound(err) {
			return nil, ErrRunNotFound
		}
		return nil, fmt.Errorf("ledger: reading run %s: %w", runID, err)
	}
	var run Run
	if err := json.Unmarshal(raw, &run); err != nil {
		return nil, fmt.Errorf("%w: run %s manifest: %v", ErrCorruptedEvent, runID, err)
	}
	return &run, nil
}

func (l *Ledger) writeRun(ctx context.Context, run *Run) error {
	raw, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("ledger: encoding run %s: %w", run.ID, err)
	}
	return l.store.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{
		BlobTable: runsTable,
		Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{
			Key:   runKey(run.ID),
			Value: raw,
		}},
	}})
}

func (l *Ledger) readEvent(ctx context.Context, runID string, seq uint64) (Event, error) {
	raw, err := l.store.GetOne(ctx, eventsTable, eventKey(runID, seq))
	if err != nil {
		if backendNotFound(err) {
			return Event{}, ErrEventNotFound
		}
		return Event{}, fmt.Errorf("ledger: reading event run=%s seq=%d: %w", runID, seq, err)
	}
	var ev Event
	if err := json.Unmarshal(raw, &ev); err != nil {
		return Event{}, fmt.Errorf("%w: run %s seq %d: decode: %v", ErrCorruptedEvent, runID, seq, err)
	}
	want := computeChecksum(ev.RunID, ev.Seq, ev.Type, ev.Payload)
	if ev.RunID != runID || ev.Seq != seq || want != ev.Checksum {
		return Event{}, fmt.Errorf("%w: run %s seq %d", ErrCorruptedEvent, runID, seq)
	}
	return ev, nil
}

func (l *Ledger) writeEvent(ctx context.Context, ev Event) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("ledger: encoding event run=%s seq=%d: %w", ev.RunID, ev.Seq, err)
	}
	return l.store.Add(ctx, []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]{{
		BlobTable: eventsTable,
		Blobs: []sop.KeyValuePair[sop.UUID, []byte]{{
			Key:   eventKey(ev.RunID, ev.Seq),
			Value: raw,
		}},
	}})
}

// CreateRun starts a new agent run, recording its RunCreated event as
// sequence 1. It is idempotent: calling it again for the same runID after a
// crash that left an orphaned event but no manifest recovers the original
// event instead of failing, and calling it again once the manifest exists
// returns ErrRunAlreadyExists.
func (l *Ledger) CreateRun(ctx context.Context, runID string, metadata []byte) (*Run, error) {
	if runID == "" {
		return nil, ErrInvalidRunID
	}
	mu := l.lockFor(runID)
	mu.Lock()
	defer mu.Unlock()

	if _, err := l.readRun(ctx, runID); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrRunAlreadyExists, runID)
	} else if !errors.Is(err, ErrRunNotFound) {
		return nil, err
	}

	run := &Run{ID: runID, Status: StatusRunning, CreatedAt: time.Now().UTC(), Metadata: metadata}
	ev, err := l.appendLocked(ctx, run, EventRunCreated, metadata)
	if err != nil {
		return nil, err
	}
	run.CreatedAt = ev.Timestamp
	return run.clone(), nil
}

// appendLocked writes (or, for a retried write, idempotently reuses) the
// event at run.LastSeq+1 and advances the manifest. Caller must hold the
// run's lock and persist nothing about run before calling; appendLocked
// writes the manifest itself.
func (l *Ledger) appendLocked(ctx context.Context, run *Run, evType EventType, payload []byte) (Event, error) {
	seq := run.LastSeq + 1
	checksum := computeChecksum(run.ID, seq, evType, payload)

	existing, err := l.readEvent(ctx, run.ID, seq)
	var ev Event
	switch {
	case err == nil:
		if existing.Checksum != checksum {
			return Event{}, fmt.Errorf("%w: run %s seq %d", ErrDuplicateEvent, run.ID, seq)
		}
		ev = existing
	case errors.Is(err, ErrEventNotFound):
		ev = Event{
			RunID:     run.ID,
			Seq:       seq,
			Type:      evType,
			Timestamp: time.Now().UTC(),
			Payload:   payload,
			Checksum:  checksum,
		}
		if werr := l.writeEvent(ctx, ev); werr != nil {
			return Event{}, werr
		}
	default:
		return Event{}, err
	}

	run.LastSeq = seq
	if evType == EventCheckpoint {
		run.CheckpointSeqs = append(run.CheckpointSeqs, seq)
	}
	if err := l.writeRun(ctx, run); err != nil {
		return Event{}, err
	}
	return ev, nil
}

// Append records a new event for an existing, open (not yet completed) run
// and returns the persisted event, including its assigned sequence number.
func (l *Ledger) Append(ctx context.Context, runID string, evType EventType, payload []byte) (Event, error) {
	if runID == "" {
		return Event{}, ErrInvalidRunID
	}
	mu := l.lockFor(runID)
	mu.Lock()
	defer mu.Unlock()

	run, err := l.readRun(ctx, runID)
	if err != nil {
		return Event{}, err
	}
	if run.Status != StatusRunning {
		return Event{}, fmt.Errorf("%w: run %s is %s", ErrRunClosed, runID, run.Status)
	}
	return l.appendLocked(ctx, run, evType, payload)
}

// Checkpoint records a snapshot of agent state and marks it as a checkpoint,
// so a later Resume or LatestCheckpoint call can find it directly.
func (l *Ledger) Checkpoint(ctx context.Context, runID string, state []byte) (Checkpoint, error) {
	ev, err := l.Append(ctx, runID, EventCheckpoint, state)
	if err != nil {
		return Checkpoint{}, err
	}
	return Checkpoint{RunID: ev.RunID, Seq: ev.Seq, Timestamp: ev.Timestamp, State: ev.Payload}, nil
}

// CompleteRun closes a run as completed (success=true) or failed
// (success=false), recording the corresponding terminal event.
func (l *Ledger) CompleteRun(ctx context.Context, runID string, success bool, payload []byte) error {
	if runID == "" {
		return ErrInvalidRunID
	}
	mu := l.lockFor(runID)
	mu.Lock()
	defer mu.Unlock()

	run, err := l.readRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status != StatusRunning {
		return fmt.Errorf("%w: run %s is already %s", ErrRunClosed, runID, run.Status)
	}

	evType := EventRunCompleted
	status := StatusCompleted
	if !success {
		evType = EventRunFailed
		status = StatusFailed
	}

	ev, err := l.appendLocked(ctx, run, evType, payload)
	if err != nil {
		return err
	}
	run.Status = status
	completedAt := ev.Timestamp
	run.CompletedAt = &completedAt
	return l.writeRun(ctx, run)
}

// DeleteRun permanently removes a run's manifest and every event blob it
// recorded. It is meant for retention cleanup of runs whose history no
// longer needs to be kept, not for correcting a bad run mid-execution.
// Deleting an unknown run is a no-op, not an error, so cleanup jobs can
// delete idempotently without checking existence first.
func (l *Ledger) DeleteRun(ctx context.Context, runID string) error {
	if runID == "" {
		return ErrInvalidRunID
	}
	mu := l.lockFor(runID)
	mu.Lock()
	defer mu.Unlock()

	run, err := l.readRun(ctx, runID)
	if err != nil {
		if errors.Is(err, ErrRunNotFound) {
			return nil
		}
		return err
	}

	if run.LastSeq > 0 {
		ids := make([]sop.UUID, 0, run.LastSeq)
		for seq := uint64(1); seq <= run.LastSeq; seq++ {
			ids = append(ids, eventKey(runID, seq))
		}
		if err := l.store.Remove(ctx, []sop.BlobsPayload[sop.UUID]{{BlobTable: eventsTable, Blobs: ids}}); err != nil {
			return fmt.Errorf("ledger: deleting events for run %s: %w", runID, err)
		}
	}

	if err := l.store.Remove(ctx, []sop.BlobsPayload[sop.UUID]{{BlobTable: runsTable, Blobs: []sop.UUID{runKey(runID)}}}); err != nil {
		return fmt.Errorf("ledger: deleting manifest for run %s: %w", runID, err)
	}
	return nil
}

// GetRun returns the current manifest for a run.
func (l *Ledger) GetRun(ctx context.Context, runID string) (*Run, error) {
	if runID == "" {
		return nil, ErrInvalidRunID
	}
	run, err := l.readRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	return run.clone(), nil
}

// Replay returns every event of a run, in sequence order, for deterministic
// inspection of a completed (or in-progress) run. It stops and returns
// ErrEventNotFound wrapped with sequence context if it encounters a gap
// before reaching run.LastSeq, which indicates an incomplete or corrupted
// write rather than a clean end of history.
func (l *Ledger) Replay(ctx context.Context, runID string) ([]Event, error) {
	run, err := l.readRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	events := make([]Event, 0, run.LastSeq)
	for seq := uint64(1); seq <= run.LastSeq; seq++ {
		ev, err := l.readEvent(ctx, runID, seq)
		if err != nil {
			return events, fmt.Errorf("ledger: replay run %s stopped at seq %d of %d: %w", runID, seq, run.LastSeq, err)
		}
		events = append(events, ev)
	}
	return events, nil
}

// LatestCheckpoint returns the newest checkpoint that passes validation,
// walking backward through the run's recorded checkpoint sequence numbers so
// a corrupted checkpoint doesn't block recovery: it is skipped in favor of
// the next older one. Returns ErrNoCheckpoint if none validate (including if
// the run never checkpointed at all).
func (l *Ledger) LatestCheckpoint(ctx context.Context, runID string) (Checkpoint, error) {
	run, err := l.readRun(ctx, runID)
	if err != nil {
		return Checkpoint{}, err
	}
	for i := len(run.CheckpointSeqs) - 1; i >= 0; i-- {
		seq := run.CheckpointSeqs[i]
		ev, err := l.readEvent(ctx, runID, seq)
		if err != nil {
			continue // corrupted or missing: fall back to the next older checkpoint
		}
		return Checkpoint{RunID: ev.RunID, Seq: ev.Seq, Timestamp: ev.Timestamp, State: ev.Payload}, nil
	}
	return Checkpoint{}, fmt.Errorf("%w: run %s", ErrNoCheckpoint, runID)
}

// Resume prepares a successor process to continue an interrupted run: it
// finds the latest valid checkpoint (if any), reads every event recorded
// after it, and records a RecoveryStarted event marking that recovery was
// invoked. It only makes sense for a run still in StatusRunning; resuming a
// run that already reached a terminal status returns ErrRunClosed (use
// Replay to inspect a finished run instead).
func (l *Ledger) Resume(ctx context.Context, runID string) (*ResumeResult, error) {
	if runID == "" {
		return nil, ErrInvalidRunID
	}
	mu := l.lockFor(runID)
	mu.Lock()
	defer mu.Unlock()

	run, err := l.readRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.Status != StatusRunning {
		return nil, fmt.Errorf("%w: run %s is %s, nothing to resume", ErrRunClosed, runID, run.Status)
	}

	var (
		checkpoint *Checkpoint
		fromSeq    uint64 = 1
	)
	for i := len(run.CheckpointSeqs) - 1; i >= 0; i-- {
		seq := run.CheckpointSeqs[i]
		ev, err := l.readEvent(ctx, runID, seq)
		if err != nil {
			continue
		}
		checkpoint = &Checkpoint{RunID: ev.RunID, Seq: ev.Seq, Timestamp: ev.Timestamp, State: ev.Payload}
		fromSeq = seq + 1
		break
	}

	tail := make([]Event, 0)
	for seq := fromSeq; seq <= run.LastSeq; seq++ {
		ev, err := l.readEvent(ctx, runID, seq)
		if err != nil {
			return nil, fmt.Errorf("ledger: resume run %s stopped at seq %d of %d: %w", runID, seq, run.LastSeq, err)
		}
		tail = append(tail, ev)
	}

	recoveryPayload, _ := json.Marshal(struct {
		ResumedAtSeq  uint64 `json:"resumed_at_seq"`
		TailEventsLen int    `json:"tail_events_len"`
	}{fromSeq - 1, len(tail)})
	if _, err := l.appendLocked(ctx, run, EventRecoveryStarted, recoveryPayload); err != nil {
		return nil, err
	}

	return &ResumeResult{Run: run.clone(), Checkpoint: checkpoint, TailEvents: tail}, nil
}
