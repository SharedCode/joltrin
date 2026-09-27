package common

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// Covers acquireLocks branch where a different owner holds a lock, returning a failover error.
func Test_TransactionLogger_AcquireLocks_PartialLockByOther_ReturnsFailoverError(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(mocks.NewMockTransactionLog(), true)
	l2 := mocks.NewMockClient()
	tx := &Transaction{l2Cache: l2}
	// Build two logical IDs and seed one lock as owned by another transaction.
	id1, id2 := sop.NewUUID(), sop.NewUUID()
	owner := sop.NewUUID()
	// Seed the mock cache so Lock() sees a conflicting owner on id1.
	key := l2.FormatLockKey(id1.String())
	_ = l2.Set(ctx, key, owner.String(), time.Minute)

	// Compose registry payload with these logical IDs.
	uhAndrh := []sop.RegistryPayload[sop.Handle]{{IDs: []sop.Handle{sop.NewHandle(id1), sop.NewHandle(id2)}}}
	tid := sop.NewUUID()
	_, err := tl.acquireLocks(ctx, tx, tid, uhAndrh)
	if err == nil {
		t.Fatalf("expected error when another owner holds a lock")
	}
	if se, ok := err.(sop.Error); !ok || se.Code != sop.RestoreRegistryFileSectorFailure {
		t.Fatalf("expected RestoreRegistryFileSectorFailure, got %v", err)
	}
}

// tlErr simulates a TransactionLog that returns errors from GetOne/GetOneOfHour.
type tlErr struct {
	errOne  error
	errHour error
}

func (t tlErr) PriorityLog() sop.TransactionPriorityLog {
	return mocks.NewMockTransactionLog().PriorityLog()
}
func (t tlErr) Add(ctx context.Context, tid sop.UUID, commitFunction int, payload []byte) error {
	return nil
}
func (t tlErr) Remove(ctx context.Context, tid sop.UUID) error { return nil }
func (t tlErr) GetOne(ctx context.Context) (sop.UUID, string, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, "", nil, t.errOne
}
func (t tlErr) GetOneOfHour(ctx context.Context, hour string) (sop.UUID, []sop.KeyValuePair[int, []byte], error) {
	return sop.NilUUID, nil, t.errHour
}
func (t tlErr) NewUUID() sop.UUID { return sop.NewUUID() }

func Test_ProcessExpiredTransactionLogs_GetOne_Error(t *testing.T) {
	ctx := context.Background()
	tl := newTransactionLogger(tlErr{errOne: errors.New("boom")}, true)
	tx := &Transaction{}
	if err := tl.processExpiredTransactionLogs(ctx, tx); err == nil {
		t.Fatalf("expected error from GetOne")
	}
}

func Test_ProcessExpiredTransactionLogs_GetOneOfHour_Error(t *testing.T) {
	ctx := context.Background()
	prev := hourBeingProcessed
	hourBeingProcessed = time.Now().UTC().Format("2006010215")
	defer func() { hourBeingProcessed = prev }()
	tl := newTransactionLogger(tlErr{errHour: errors.New("x")}, true)
	tx := &Transaction{}
	if err := tl.processExpiredTransactionLogs(ctx, tx); err == nil {
		t.Fatalf("expected error from GetOneOfHour")
	}
}

func Test_HandleRegistrySectorLockTimeout_UserDataTypeMismatch(t *testing.T) {
	ctx := context.Background()
	// Transaction with mock cache so CreateLockKeys works.
	tx := &Transaction{l2Cache: mocks.NewMockClient()}
	// Error with non-*LockKey UserData to hit early return path.
	orig := sop.Error{Code: sop.RestoreRegistryFileSectorFailure, Err: errors.New("reg err"), UserData: sop.NewUUID()}
	if err := tx.handleRegistrySectorLockTimeout(ctx, orig); err == nil {
		t.Fatalf("expected error returned as-is")
	}
}

func Test_HandleRegistrySectorLockTimeout_SuccessPath_Alt(t *testing.T) {
	ctx := context.Background()
	tx := &Transaction{l2Cache: mocks.NewMockClient(), logger: newTransactionLogger(mocks.NewMockTransactionLog(), true)}
	lk := &sop.LockKey{Key: tx.l2Cache.FormatLockKey("abc"), LockID: sop.NewUUID()}
	// Provide sop.Error with proper type in UserData and correct code.
	se := sop.Error{Code: sop.RestoreRegistryFileSectorFailure, Err: errors.New("x"), UserData: lk}
	if err := tx.handleRegistrySectorLockTimeout(ctx, se); err != nil {
		t.Fatalf("expected nil error on success path, got %v", err)
	}
	if !lk.IsLockOwner {
		t.Fatalf("expected IsLockOwner=true on provided lock key")
	}
}
