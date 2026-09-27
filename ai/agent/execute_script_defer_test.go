package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// --- Mocks ---

type MockTxWithStateDefer struct {
	mock.Mock
	Committed bool
}

func (m *MockTxWithStateDefer) Commit(ctx context.Context) error {
	m.Committed = true
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockTxWithStateDefer) Rollback(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockTxWithStateDefer) Begin(ctx context.Context) error {
	return nil
}

func (m *MockTxWithStateDefer) AddPhasedTransaction(otherTransaction ...sop.TwoPhaseCommitTransaction) {
	// No-op for mock
}

func (m *MockTxWithStateDefer) GetPhasedTransaction() sop.TwoPhaseCommitTransaction {
	return nil
}

func (m *MockTxWithStateDefer) GetStores(ctx context.Context) ([]string, error) {
	return nil, nil
}

func (m *MockTxWithStateDefer) HasBegun() bool {
	return true
}

func (m *MockTxWithStateDefer) OnCommit(callback func(ctx context.Context) error) {
}

func (m *MockTxWithStateDefer) GetID() sop.UUID {
	return sop.NewUUID()
}

func (m *MockTxWithStateDefer) CommitMaxDuration() time.Duration {
	return 0
}

func (m *MockTxWithStateDefer) Close() error {
	return nil
}

// TestDeferCommit verifies that defer transfers to the cursor and executes on close.
func TestDeferCommitWithLazyReturn(t *testing.T) {
	ctx := context.Background()

	// 1. Setup Mock Transaction
	mockTx := new(MockTxWithStateDefer)
	mockTx.On("Commit", mock.Anything).Return(nil)

	// 2. Setup Engine
	engine := NewScriptEngine(NewScriptContext(), func(name string) (Database, error) {
		return nil, fmt.Errorf("db not supported in this test")
	})

	// Pre-seed the transaction so we don't need begin_tx logic complexity
	engine.Context.Transactions["tx1"] = mockTx

	// 3. Define Script
	// Op: defer commit_tx
	// Op: return lazy_cursor (we mock a cursor)

	// We need a dummy lazy cursor
	lazyCursor := &DummyCursor{Items: []any{"A", "B"}}
	engine.Context.Variables["my_cursor"] = lazyCursor

	script := []ScriptInstruction{
		{
			Op: "defer",
			Args: map[string]any{
				"op":          "commit_tx",
				"transaction": "tx1",
			},
		},
		{
			Op:        "assign", // Using assign to return the cursor variable
			Args:      map[string]any{"value": "{{my_cursor}}"},
			ResultVar: "final",
		},
		{
			Op:   "return",
			Args: map[string]any{"value": "final"},
		},
	}

	// 4. Compile and Run
	compiled, err := CompileScript(script)
	assert.NoError(t, err)

	err = compiled(ctx, engine)
	assert.NoError(t, err)

	// 5. Verify Result
	// The return value should be the cursor wrapped in DeferredCleanupCursor
	res := engine.ReturnValue
	assert.NotNil(t, res)

	wrapper, ok := res.(*DeferredCleanupCursor)
	assert.True(t, ok, "Result should be *DeferredCleanupCursor")

	// 6. Verify Transaction NOT committed yet
	assert.False(t, mockTx.Committed, "Transaction should NOT be committed before cursor close")

	// 7. Consume Cursor
	_, _, _ = wrapper.Next(ctx)  // A
	_, _, _ = wrapper.Next(ctx)  // B
	_, ok, _ = wrapper.Next(ctx) // EOF
	assert.False(t, ok)

	// 8. Close Cursor
	err = wrapper.Close()
	assert.NoError(t, err)

	// 9. Verify Transaction IS committed
	assert.True(t, mockTx.Committed, "Transaction SHOULD be committed after cursor close")
}

func TestDeferCommitWithImplicitVariableReturn(t *testing.T) {
	ctx := context.Background()

	mockTx := new(MockTxWithStateDefer)
	mockTx.On("Commit", mock.Anything).Return(nil)

	engine := NewScriptEngine(NewScriptContext(), func(name string) (Database, error) {
		return nil, fmt.Errorf("db not supported in this test")
	})
	engine.Context.Transactions["tx1"] = mockTx

	lazyCursor := &DummyCursor{Items: []any{"A", "B"}}
	engine.Context.Variables["my_cursor"] = lazyCursor

	script := []ScriptInstruction{
		{
			Op:        "assign",
			Args:      map[string]any{"value": "{{my_cursor}}"},
			ResultVar: "filtered_rows",
		},
		{
			Op: "defer",
			Args: map[string]any{
				"op":          "commit_tx",
				"transaction": "tx1",
			},
		},
	}

	compiled, err := CompileScript(script)
	assert.NoError(t, err)

	err = compiled(ctx, engine)
	assert.NoError(t, err)

	wrapped, ok := engine.Context.Variables["filtered_rows"].(*DeferredCleanupCursor)
	assert.True(t, ok, "implicit result variable should receive deferred cleanup wrapper")
	assert.False(t, mockTx.Committed, "transaction should not commit before implicit cursor is drained")

	_, _, _ = wrapped.Next(ctx)
	_, _, _ = wrapped.Next(ctx)
	_, ok, _ = wrapped.Next(ctx)
	assert.False(t, ok)

	err = wrapped.Close()
	assert.NoError(t, err)
	assert.True(t, mockTx.Committed, "transaction should commit after implicit cursor close")
}

func TestDeferredCommitErrorPropagatesWithoutCursor(t *testing.T) {
	ctx := context.Background()

	mockTx := new(MockTxWithStateDefer)
	mockTx.On("Commit", mock.Anything).Return(fmt.Errorf("redis unavailable"))

	engine := NewScriptEngine(NewScriptContext(), func(name string) (Database, error) {
		return nil, fmt.Errorf("db not supported in this test")
	})
	engine.Context.Transactions["tx1"] = mockTx

	script := []ScriptInstruction{
		{
			Op: "defer",
			Args: map[string]any{
				"op":          "commit_tx",
				"transaction": "tx1",
			},
		},
	}

	compiled, err := CompileScript(script)
	assert.NoError(t, err)

	err = compiled(ctx, engine)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "deferred operation failed")
	assert.ErrorContains(t, err, "redis unavailable")
	assert.True(t, mockTx.Committed, "deferred commit should still have been attempted")
}

// DummyCursor for testing
type DummyCursor struct {
	Items []any
	pos   int
}

func (d *DummyCursor) Next(ctx context.Context) (any, bool, error) {
	if d.pos >= len(d.Items) {
		return nil, false, nil
	}
	val := d.Items[d.pos]
	d.pos++
	return val, true, nil
}

func (d *DummyCursor) Close() error {
	return nil
}

// Helper to register mock if needed
// (Simulating the sop.Transaction interface compliance if it has other methods)
// ...
