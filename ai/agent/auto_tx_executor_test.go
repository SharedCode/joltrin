package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
)

type autoTxExecutorTestExecutor struct {
	result string
	err    error
}

func (e autoTxExecutorTestExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return nil, nil
}

func (e autoTxExecutorTestExecutor) Execute(ctx context.Context, name string, args map[string]any) (string, error) {
	return e.result, e.err
}

type autoTxExecutorTestTx struct {
	commitErr  error
	committed  bool
	rolledBack bool
}

func (t *autoTxExecutorTestTx) Begin(ctx context.Context) error { return nil }
func (t *autoTxExecutorTestTx) Commit(ctx context.Context) error {
	t.committed = true
	return t.commitErr
}
func (t *autoTxExecutorTestTx) Rollback(ctx context.Context) error {
	t.rolledBack = true
	return nil
}
func (t *autoTxExecutorTestTx) HasBegun() bool                                      { return true }
func (t *autoTxExecutorTestTx) GetPhasedTransaction() sop.TwoPhaseCommitTransaction { return nil }
func (t *autoTxExecutorTestTx) AddPhasedTransaction(otherTransaction ...sop.TwoPhaseCommitTransaction) {
}
func (t *autoTxExecutorTestTx) GetStores(ctx context.Context) ([]string, error)   { return nil, nil }
func (t *autoTxExecutorTestTx) Close() error                                      { return nil }
func (t *autoTxExecutorTestTx) GetID() sop.UUID                                   { return sop.NewUUID() }
func (t *autoTxExecutorTestTx) CommitMaxDuration() time.Duration                  { return 0 }
func (t *autoTxExecutorTestTx) OnCommit(callback func(ctx context.Context) error) {}

func TestAutoTxExecutor_PropagatesSessionCommitFailure(t *testing.T) {
	tx := &autoTxExecutorTestTx{commitErr: fmt.Errorf("redis unavailable")}
	svc := &Service{session: &RunnerSession{Transaction: tx, Variables: map[string]any{"x": 1}}}
	executor := &autoTxExecutor{
		original: autoTxExecutorTestExecutor{result: "ok", err: nil},
		s:        svc,
	}

	result, err := executor.Execute(context.Background(), "tool", map[string]any{"a": 1})
	if err == nil {
		t.Fatal("expected commit failure to be returned")
	}
	if result != "" {
		t.Fatalf("expected empty result on commit failure, got %q", result)
	}
	if err.Error() == "" || !containsAll(err.Error(), []string{"session transaction commit failed", "redis unavailable"}) {
		t.Fatalf("unexpected error: %v", err)
	}
	if !tx.committed {
		t.Fatal("expected session transaction commit to be attempted")
	}
	if tx.rolledBack {
		t.Fatal("did not expect rollback on successful tool execution path")
	}
	if svc.session.Transaction != nil {
		t.Fatal("expected session transaction to be cleared after commit failure")
	}
	if svc.session.Variables != nil {
		t.Fatal("expected session variables to be cleared after commit failure")
	}
}

func containsAll(s string, parts []string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
