package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Decision defines the outcome of an evaluated policy or action.
type Decision string

const (
	DecisionAllow       Decision = "allow"
	DecisionDeny        Decision = "deny"
	DecisionViolation   Decision = "violation"
	DecisionQuarantined Decision = "quarantined"
)

var (
	// ErrTamperedAuditChain indicates cryptographic hash validation failed.
	ErrTamperedAuditChain = errors.New("governance: audit chain integrity verification failed (tamper detected)")
	// ErrAuditSinkClosed is returned when attempting to write to a closed sink.
	ErrAuditSinkClosed = errors.New("governance: audit sink is closed")
)

// AuditEvent represents a single immutable, tamper-evident security or policy event.
type AuditEvent struct {
	ID          string            `json:"id"`
	Timestamp   time.Time         `json:"timestamp"`
	TenantID    string            `json:"tenant_id,omitempty"`
	WorkspaceID string            `json:"workspace_id,omitempty"`
	ActorID     string            `json:"actor_id"`
	Action      string            `json:"action"`
	Resource    string            `json:"resource"`
	Decision    Decision          `json:"decision"`
	Reason      string            `json:"reason,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	PrevHash    string            `json:"prev_hash,omitempty"`
	Hash        string            `json:"hash"`
	Signature   []byte            `json:"signature,omitempty"`
}

// ComputeHash computes a canonical SHA-256 digest over the event and its predecessor hash.
func (e *AuditEvent) ComputeHash(prevHash string) string {
	h := sha256.New()
	canonical := fmt.Sprintf("%s|%d|%s|%s|%s|%s|%s|%s|%s",
		prevHash,
		e.Timestamp.UnixNano(),
		e.TenantID,
		e.WorkspaceID,
		e.ActorID,
		e.Action,
		e.Resource,
		e.Decision,
		e.Reason,
	)
	h.Write([]byte(canonical))
	return hex.EncodeToString(h.Sum(nil))
}

// AuditFilter enables querying historical audit records.
type AuditFilter struct {
	TenantID    string
	WorkspaceID string
	ActorID     string
	Decision    Decision
	Since       time.Time
}

// AuditSink is the ingestion interface for audit events.
type AuditSink interface {
	Record(ctx context.Context, ev *AuditEvent) error
	Flush(ctx context.Context) error
	Close() error
}

// AuditSigner provides cryptographic attestation of audit events.
type AuditSigner interface {
	Sign(digest []byte) ([]byte, error)
	Algorithm() string
}

// AuditStreamer extends AuditSink to support real-time SIEM / Kafka streaming.
type AuditStreamer interface {
	AuditSink
	Stream(ctx context.Context, filter AuditFilter) (<-chan *AuditEvent, error)
}

// MemoryAuditRecorder is a thread-safe, in-memory implementation of AuditSink
// with tamper-evident SHA-256 hash chaining and integrity verification.
type MemoryAuditRecorder struct {
	mu       sync.RWMutex
	closed   bool
	events   []*AuditEvent
	lastHash string
	signer   AuditSigner
}

// NewMemoryAuditRecorder constructs a new audit recorder.
func NewMemoryAuditRecorder(signer AuditSigner) *MemoryAuditRecorder {
	return &MemoryAuditRecorder{
		events: make([]*AuditEvent, 0),
		signer: signer,
	}
}

// Record appends an audit event to the tamper-evident chain.
func (r *MemoryAuditRecorder) Record(ctx context.Context, ev *AuditEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return ErrAuditSinkClosed
	}

	if ev.ID == "" {
		ev.ID = uuid.NewString()
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}

	ev.PrevHash = r.lastHash
	ev.Hash = ev.ComputeHash(r.lastHash)

	if r.signer != nil {
		sig, err := r.signer.Sign([]byte(ev.Hash))
		if err != nil {
			return fmt.Errorf("governance: failed to sign audit event: %w", err)
		}
		ev.Signature = sig
	}

	r.lastHash = ev.Hash

	// Store defensive copy
	evCopy := *ev
	if ev.Metadata != nil {
		evCopy.Metadata = make(map[string]string, len(ev.Metadata))
		for k, v := range ev.Metadata {
			evCopy.Metadata[k] = v
		}
	}
	r.events = append(r.events, &evCopy)
	return nil
}

// Flush ensures all buffered events are committed (no-op for in-memory).
func (r *MemoryAuditRecorder) Flush(ctx context.Context) error {
	return nil
}

// Close closes the recorder.
func (r *MemoryAuditRecorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

// VerifyIntegrity traverses the recorded hash chain and verifies that no event has been modified.
func (r *MemoryAuditRecorder) VerifyIntegrity() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	prev := ""
	for idx, ev := range r.events {
		if ev.PrevHash != prev {
			return fmt.Errorf("%w: event index %d (ID %s) broken link: expected prev %q got %q",
				ErrTamperedAuditChain, idx, ev.ID, prev, ev.PrevHash)
		}
		expectedHash := ev.ComputeHash(prev)
		if ev.Hash != expectedHash {
			return fmt.Errorf("%w: event index %d (ID %s) hash mismatch: computed %q stored %q",
				ErrTamperedAuditChain, idx, ev.ID, expectedHash, ev.Hash)
		}
		prev = ev.Hash
	}
	return nil
}

// Query returns events matching the filter.
func (r *MemoryAuditRecorder) Query(filter AuditFilter) []*AuditEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make([]*AuditEvent, 0)
	for _, ev := range r.events {
		if filter.TenantID != "" && ev.TenantID != filter.TenantID {
			continue
		}
		if filter.WorkspaceID != "" && ev.WorkspaceID != filter.WorkspaceID {
			continue
		}
		if filter.ActorID != "" && ev.ActorID != filter.ActorID {
			continue
		}
		if filter.Decision != "" && ev.Decision != filter.Decision {
			continue
		}
		if !filter.Since.IsZero() && ev.Timestamp.Before(filter.Since) {
			continue
		}
		res = append(res, ev)
	}
	return res
}

// ExportJSON streams all recorded audit events as an indented JSON array.
func (r *MemoryAuditRecorder) ExportJSON(w io.Writer) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r.events)
}

// Count returns the number of recorded events.
func (r *MemoryAuditRecorder) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.events)
}
