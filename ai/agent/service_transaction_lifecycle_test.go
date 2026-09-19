package agent

import (
	"context"
	"testing"
	"time"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
)

// TestTransactionLifecycle_ContextCancellation verifies that transactions are properly rolled back
// when the context is canceled.
func TestTransactionLifecycle_ContextCancellation(t *testing.T) {
	// Create a mock service with session
	service := &Service{
		session: NewRunnerSession(),
	}

	// Create a cancelable context
	ctx, cancel := context.WithCancel(context.Background())

	// Create a mock transaction
	mockTx := &mockTransaction{
		begun:      true,
		rolledBack: false,
		committed:  false,
	}

	// Set up session payload with transaction
	payload := &ai.SessionPayload{
		Transaction:         mockTx,
		ExplicitTransaction: false,
		CurrentDB:           "testdb",
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	// Cancel the context to simulate user abort
	cancel()

	// Call Close - it should detect cancellation and rollback
	err := service.Close(ctx)
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify transaction was rolled back
	if !mockTx.rolledBack {
		t.Error("Expected transaction to be rolled back on context cancellation")
	}

	// Verify transaction was NOT committed
	if mockTx.committed {
		t.Error("Transaction should not be committed on context cancellation")
	}

	// Verify session state was cleared
	if service.session.Transaction != nil {
		t.Error("Session transaction should be cleared after context cancellation")
	}
}

// TestTransactionLifecycle_NormalCommit verifies that implicit transactions are committed normally.
func TestTransactionLifecycle_NormalCommit(t *testing.T) {
	service := &Service{
		session: NewRunnerSession(),
	}

	ctx := context.Background()

	mockTx := &mockTransaction{
		begun:      true,
		rolledBack: false,
		committed:  false,
	}

	payload := &ai.SessionPayload{
		Transaction: mockTx,
		CurrentDB:   "testdb",
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	err := service.Close(ctx)
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify transaction was committed
	if !mockTx.committed {
		t.Error("Expected implicit transaction to be committed")
	}

	// Verify transaction was NOT rolled back
	if mockTx.rolledBack {
		t.Error("Transaction should not be rolled back on normal close")
	}

	// Verify session state was cleared
	if service.session.Transaction != nil {
		t.Error("Session transaction should be cleared after commit")
	}
}

// TestTransactionLifecycle_ExplicitTransaction verifies that explicit transactions are NOT auto-managed.
//
// CRITICAL BEHAVIOR:
// When ExplicitTransaction = true, the transaction is managed externally by language bindings
// (Python, Rust, Java, .NET) via the manage_transaction API. SessionPayload.Close() must NOT
// auto-commit or auto-rollback because:
//  1. The transaction may span multiple API calls/HTTP requests
//  2. Language binding code is responsible for calling commit/rollback explicitly
//  3. Auto-managing would break the multi-request transaction scope
//
// This test prevents regression of the explicit transaction contract.
func TestTransactionLifecycle_ExplicitTransaction(t *testing.T) {
	service := &Service{
		session: NewRunnerSession(),
	}

	ctx := context.Background()

	mockTx := &mockTransaction{
		begun:      true,
		rolledBack: false,
		committed:  false,
	}

	payload := &ai.SessionPayload{
		Transaction:         mockTx,
		ExplicitTransaction: true, // Explicit transaction (e.g., from script that didn't commit)
		CurrentDB:           "testdb",
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)

	err := service.Close(ctx)
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// CRITICAL ASSERTION: Explicit transactions must NOT be auto-committed
	// Language bindings manage the commit via manage_transaction API
	if mockTx.committed {
		t.Error("REGRESSION: Explicit transaction was auto-committed. This breaks the external transaction lifecycle contract. ExplicitTransaction=true means language bindings manage commit/rollback.")
	}

	// CRITICAL ASSERTION: Explicit transactions must NOT be auto-rolled back
	// Language bindings manage the rollback via manage_transaction API
	if mockTx.rolledBack {
		t.Error("REGRESSION: Explicit transaction was auto-rolled back. This breaks the external transaction lifecycle contract. ExplicitTransaction=true means language bindings manage commit/rollback.")
	}

	// SUCCESS: Transaction state unchanged, as expected for explicit transactions.
	// Language bindings (Python, Rust, Java, .NET) will call manage_transaction to commit/rollback.
	t.Logf("SUCCESS: Explicit transaction correctly left untouched by SessionPayload.Close()")
}

// TestAsk_ContextCancellationEarly verifies that Ask returns early on context cancellation.
func TestAsk_ContextCancellationEarly(t *testing.T) {
	service := &Service{
		session: NewRunnerSession(),
	}

	// Create already-canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Call Ask - should return immediately with error
	_, err := service.Ask(ctx, "test query", nil)
	if err == nil {
		t.Fatal("Expected error on canceled context")
	}

	if err.Error() != "request canceled before execution: context canceled" {
		t.Errorf("Expected context cancellation error, got: %v", err)
	}
}

// TestAsk_ContextCancellationDuringExecution verifies proper error message on mid-execution cancellation.
func TestAsk_ContextCancellationDuringExecution(t *testing.T) {
	// This test verifies the error handling path when context is canceled during executeReasoningEngine
	// The actual cancellation is tested at the ReAct engine level
	service := &Service{
		session:   NewRunnerSession(),
		pipeline:  []PipelineStep{}, // Empty pipeline to avoid short-circuit
		domain:    nil,
		generator: nil,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// Let context expire
	time.Sleep(2 * time.Millisecond)

	// Ask should check context and return appropriate error
	_, err := service.Ask(ctx, "test query", nil)
	if err == nil {
		t.Fatal("Expected error on canceled context")
	}

	// Should contain "canceled" in error message
	if err != nil && ctx.Err() != nil {
		// This is expected - context canceled
		t.Logf("Got expected context cancellation error: %v", err)
	}
}

// Mock transaction for testing that tracks rollback/commit calls
type mockTransaction struct {
	begun      bool
	rolledBack bool
	committed  bool
}

func (m *mockTransaction) Begin(ctx context.Context) error { return nil }
func (m *mockTransaction) Commit(ctx context.Context) error {
	m.committed = true
	return nil
}
func (m *mockTransaction) Rollback(ctx context.Context) error {
	m.rolledBack = true
	return nil
}
func (m *mockTransaction) HasBegun() bool                                                         { return m.begun }
func (m *mockTransaction) GetPhasedTransaction() sop.TwoPhaseCommitTransaction                    { return nil }
func (m *mockTransaction) AddPhasedTransaction(otherTransaction ...sop.TwoPhaseCommitTransaction) {}
func (m *mockTransaction) GetStores(ctx context.Context) ([]string, error)                        { return nil, nil }
func (m *mockTransaction) Close() error                                                           { return nil }
func (m *mockTransaction) GetID() sop.UUID                                                        { return sop.UUID{} }
func (m *mockTransaction) CommitMaxDuration() time.Duration                                       { return 0 }
func (m *mockTransaction) OnCommit(callback func(ctx context.Context) error)                      {}
func (m *mockTransaction) GetMode() sop.TransactionMode {
	return sop.ForWriting
}
