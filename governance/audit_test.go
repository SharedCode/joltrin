package governance

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type dummySigner struct{}

func (d *dummySigner) Sign(digest []byte) ([]byte, error) {
	return append([]byte("SIG:"), digest...), nil
}

func (d *dummySigner) Algorithm() string {
	return "DUMMY-HMAC"
}

func TestMemoryAuditRecorder_HashChainAndIntegrity(t *testing.T) {
	ctx := context.Background()
	signer := &dummySigner{}
	rec := NewMemoryAuditRecorder(signer)

	ev1 := &AuditEvent{
		TenantID:    "tenant-1",
		WorkspaceID: "ws-1",
		ActorID:     "agent-alpha",
		Action:      "drop_db",
		Resource:    "prod_db",
		Decision:    DecisionViolation,
		Reason:      "backup not validated",
	}

	if err := rec.Record(ctx, ev1); err != nil {
		t.Fatalf("failed to record ev1: %v", err)
	}

	ev2 := &AuditEvent{
		TenantID:    "tenant-1",
		WorkspaceID: "ws-1",
		ActorID:     "agent-alpha",
		Action:      "create_backup",
		Resource:    "prod_db",
		Decision:    DecisionAllow,
		Reason:      "valid operation",
	}

	if err := rec.Record(ctx, ev2); err != nil {
		t.Fatalf("failed to record ev2: %v", err)
	}

	if rec.Count() != 2 {
		t.Fatalf("expected 2 events, got %d", rec.Count())
	}

	// Verify cryptographic integrity
	if err := rec.VerifyIntegrity(); err != nil {
		t.Fatalf("integrity verification failed unexpectedly: %v", err)
	}

	// Verify signature exists
	events := rec.Query(AuditFilter{})
	if len(events) != 2 {
		t.Fatalf("expected 2 queried events, got %d", len(events))
	}
	if !strings.HasPrefix(string(events[0].Signature), "SIG:") {
		t.Errorf("expected signature on event, got %s", string(events[0].Signature))
	}

	// Tamper simulation: directly alter ev1 in recorder memory
	rec.mu.Lock()
	rec.events[0].Decision = DecisionAllow
	rec.mu.Unlock()

	// Integrity verification MUST fail now
	err := rec.VerifyIntegrity()
	if err == nil {
		t.Fatalf("expected integrity verification to fail on tampered event, but got nil")
	}
	if !errors.Is(err, ErrTamperedAuditChain) {
		t.Errorf("expected ErrTamperedAuditChain, got %v", err)
	}
}

func TestMemoryAuditRecorder_QueryAndExport(t *testing.T) {
	ctx := context.Background()
	rec := NewMemoryAuditRecorder(nil)

	now := time.Now().UTC()

	_ = rec.Record(ctx, &AuditEvent{
		TenantID:  "tenant-A",
		ActorID:   "user-1",
		Action:    "login",
		Decision:  DecisionAllow,
		Timestamp: now,
	})
	_ = rec.Record(ctx, &AuditEvent{
		TenantID:  "tenant-B",
		ActorID:   "user-2",
		Action:    "modify_rule",
		Decision:  DecisionDeny,
		Timestamp: now.Add(time.Second),
	})

	// Query by tenant
	res := rec.Query(AuditFilter{TenantID: "tenant-A"})
	if len(res) != 1 || res[0].ActorID != "user-1" {
		t.Fatalf("expected 1 result for tenant-A, got %d", len(res))
	}

	// Export JSON
	var buf bytes.Buffer
	if err := rec.ExportJSON(&buf); err != nil {
		t.Fatalf("failed to export JSON: %v", err)
	}
	if !strings.Contains(buf.String(), "modify_rule") {
		t.Errorf("expected exported JSON to contain 'modify_rule'")
	}
}
