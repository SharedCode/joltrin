package common

import (
	"context"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// MockPriorityLog captures calls to ProcessNewer for verification.
type MockPriorityLog struct {
	processNewerCalled bool
}

func (m *MockPriorityLog) IsEnabled() bool                                             { return true }
func (m *MockPriorityLog) Add(ctx context.Context, tid sop.UUID, payload []byte) error { return nil }
func (m *MockPriorityLog) Remove(ctx context.Context, tid sop.UUID) error              { return nil }
func (m *MockPriorityLog) Get(ctx context.Context, tid sop.UUID) ([]sop.RegistryPayload[sop.Handle], error) {
	return nil, nil
}
func (m *MockPriorityLog) GetBatch(ctx context.Context, batchSize int) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	return nil, nil
}
func (m *MockPriorityLog) ProcessNewer(ctx context.Context, processor func(tid sop.UUID, payload []sop.RegistryPayload[sop.Handle]) error) error {
	m.processNewerCalled = true
	return nil
}
func (m *MockPriorityLog) LogCommitChanges(ctx context.Context, stores []sop.StoreInfo, newRootNodesHandles, addedNodesHandles, updatedNodesHandles, removedNodesHandles []sop.RegistryPayload[sop.Handle]) error {
	return nil
}
func (m *MockPriorityLog) GetAll(ctx context.Context) ([]sop.KeyValuePair[sop.UUID, []sop.RegistryPayload[sop.Handle]], error) {
	return nil, nil
}

// MockTransactionLogWithPriority wraps the standard mock to return our custom PriorityLog.
type MockTransactionLogWithPriority struct {
	sop.TransactionLog
	priorityLog *MockPriorityLog
}

func (m *MockTransactionLogWithPriority) PriorityLog() sop.TransactionPriorityLog {
	return m.priorityLog
}

func Test_RedisRestartDetector_ResurrectsLocks(t *testing.T) {
	// 1. Setup Mocks
	mockL2 := mocks.NewMockClient()
	mockPL := &MockPriorityLog{}
	mockTL := &MockTransactionLogWithPriority{
		TransactionLog: mocks.NewMockTransactionLog(),
		priorityLog:    mockPL,
	}
	mockRegistry := mocks.NewMockRegistry(false)
	mockStoreRepo := mocks.NewMockStoreRepository()
	mockBlobStore := mocks.NewMockBlobStore()

	// 2. Create Transaction
	// We use a long duration to avoid timeouts during test execution.
	trans, err := NewTwoPhaseCommitTransaction(sop.ForWriting, 1*time.Hour, mockBlobStore, mockStoreRepo, mockRegistry, mockL2, mockTL)
	if err != nil {
		t.Fatalf("Failed to create transaction: %v", err)
	}

	// 3. Scenario: Restart Detected (Token Missing)
	// Ensure "notrestarted" key is NOT in L2Cache (default state of mock).
	ctx := context.Background()

	// Run the detector
	trans.processNewerPriorityLogsLocksResurrection(ctx)

	// Verify ProcessNewer was called
	if !mockPL.processNewerCalled {
		t.Errorf("Expected ProcessNewer to be called when 'notrestarted' token is missing, but it wasn't.")
	}

	// Verify the token was set (we acquired the lock)
	// The key is "notrestarted". The mock stores it in stringStore.
	// We can't easily access stringStore of the mock since it's private in mocks package.
	// But we can check if it's locked now.
	isLocked, err := mockL2.IsLockedByOthers(ctx, []string{mockL2.FormatLockKey("notrestarted")})
	if err != nil {
		t.Fatalf("IsLockedByOthers failed: %v", err)
	}
	// Since we (the transaction's L2 client) acquired it, IsLockedByOthers might return false if it checks against "others".
	// But mockRedis.IsLockedByOthers just checks existence.
	if !isLocked {
		t.Errorf("Expected 'notrestarted' token to be present (locked) after resurrection check, but it wasn't.")
	}
}

func Test_RedisRestartDetector_NoResurrectionIfTokenExists(t *testing.T) {
	// 1. Setup Mocks
	mockL2 := mocks.NewMockClient()
	mockPL := &MockPriorityLog{}
	mockTL := &MockTransactionLogWithPriority{
		TransactionLog: mocks.NewMockTransactionLog(),
		priorityLog:    mockPL,
	}
	mockRegistry := mocks.NewMockRegistry(false)
	mockStoreRepo := mocks.NewMockStoreRepository()
	mockBlobStore := mocks.NewMockBlobStore()

	trans, err := NewTwoPhaseCommitTransaction(sop.ForWriting, 1*time.Hour, mockBlobStore, mockStoreRepo, mockRegistry, mockL2, mockTL)
	if err != nil {
		t.Fatalf("Failed to create transaction: %v", err)
	}

	// 2. Scenario: No Restart (Token Exists)
	ctx := context.Background()

	// Manually set the token in L2Cache to simulate it being there.
	// We use Set to put it in the stringStore of the mock.
	mockL2.Set(ctx, mockL2.FormatLockKey("notrestarted"), "some-lock-id", 1*time.Hour)

	// Run the detector
	trans.processNewerPriorityLogsLocksResurrection(ctx)

	// Verify ProcessNewer was NOT called
	if mockPL.processNewerCalled {
		t.Errorf("Expected ProcessNewer NOT to be called when 'notrestarted' token exists, but it was.")
	}
}
