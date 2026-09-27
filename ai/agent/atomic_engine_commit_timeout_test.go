package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

type blockingCommitTx struct{}

func (m *blockingCommitTx) Begin(ctx context.Context) error                                        { return nil }
func (m *blockingCommitTx) Commit(ctx context.Context) error                                       { <-ctx.Done(); return ctx.Err() }
func (m *blockingCommitTx) Rollback(ctx context.Context) error                                     { return nil }
func (m *blockingCommitTx) HasBegun() bool                                                         { return true }
func (m *blockingCommitTx) GetPhasedTransaction() sop.TwoPhaseCommitTransaction                    { return nil }
func (m *blockingCommitTx) AddPhasedTransaction(otherTransaction ...sop.TwoPhaseCommitTransaction) {}
func (m *blockingCommitTx) GetStores(ctx context.Context) ([]string, error)                        { return nil, nil }
func (m *blockingCommitTx) Close() error                                                           { return nil }
func (m *blockingCommitTx) GetID() sop.UUID                                                        { return sop.UUID{} }
func (m *blockingCommitTx) CommitMaxDuration() time.Duration                                       { return 25 * time.Millisecond }
func (m *blockingCommitTx) OnCommit(callback func(ctx context.Context) error)                      {}

func TestCommitTx_HonorsCommitTimeout(t *testing.T) {
	engine := &ScriptEngine{Context: NewScriptContext()}
	engine.Context.Transactions["tx"] = &blockingCommitTx{}

	done := make(chan error, 1)
	go func() {
		done <- engine.CommitTx(context.Background(), map[string]any{"transaction": "tx"})
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected deadline exceeded from commit timeout, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("CommitTx hung instead of honoring the transaction timeout")
	}
}
