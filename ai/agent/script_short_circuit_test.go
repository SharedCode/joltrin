package agent

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
)

type txFailureToolExecutor struct {
	failCommand string
	failErr     error
	calls       []string
}

func (m *txFailureToolExecutor) Execute(ctx context.Context, toolName string, args map[string]any) (string, error) {
	m.calls = append(m.calls, toolName)
	if toolName == m.failCommand {
		return "", m.failErr
	}
	return "ok", nil
}

func (m *txFailureToolExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	return nil, nil
}

func TestRunSteps_ManageTransactionErrorShortCircuitsDespiteContinueOnError(t *testing.T) {
	svc := NewService(&MockDomain{}, nil, nil, nil, nil, nil, false)
	executor := &txFailureToolExecutor{
		failCommand: "manage_transaction",
		failErr:     errors.New("failed to begin transaction: redis unavailable"),
	}

	ctx := context.WithValue(context.Background(), ai.CtxKeyExecutor, executor)
	steps := []ai.ScriptStep{
		{
			Type:            "command",
			Command:         "manage_transaction",
			Args:            map[string]any{"action": "begin"},
			ContinueOnError: true,
		},
		{
			Type:    "command",
			Command: "select",
			Args:    map[string]any{"store": "employees"},
		},
	}

	var sb strings.Builder
	err := svc.runSteps(ctx, steps, make(map[string]any), nil, &sb, nil, nil)
	if err == nil {
		t.Fatal("expected transaction begin failure to abort script")
	}
	if !strings.Contains(err.Error(), "failed to begin transaction") {
		t.Fatalf("expected begin failure, got: %v", err)
	}
	if len(executor.calls) != 1 {
		t.Fatalf("expected script to stop after first failing step, calls: %v", executor.calls)
	}
}

func TestRunSteps_TransactionalToolErrorShortCircuitsDespiteContinueOnError(t *testing.T) {
	svc := NewService(&MockDomain{}, nil, nil, nil, nil, nil, false)
	executor := &txFailureToolExecutor{
		failCommand: "add",
		failErr:     errors.New("failed to commit transaction: redis unavailable"),
	}

	ctx := context.WithValue(context.Background(), ai.CtxKeyExecutor, executor)
	steps := []ai.ScriptStep{
		{
			Type:            "command",
			Command:         "add",
			Args:            map[string]any{"store": "employees", "key": "emp1", "value": "John Doe"},
			ContinueOnError: true,
		},
		{
			Type:    "command",
			Command: "select",
			Args:    map[string]any{"store": "employees"},
		},
	}

	var sb strings.Builder
	err := svc.runSteps(ctx, steps, make(map[string]any), nil, &sb, nil, nil)
	if err == nil {
		t.Fatal("expected transactional tool failure to abort script")
	}
	if !strings.Contains(err.Error(), "failed to commit transaction") {
		t.Fatalf("expected commit failure, got: %v", err)
	}
	if len(executor.calls) != 1 {
		t.Fatalf("expected script to stop after first failing step, calls: %v", executor.calls)
	}
}

func TestShouldShortCircuitScriptOnError_DetectsAtomicTransactionFailures(t *testing.T) {
	err := errors.New("step failed: operation 'commit_tx' failed: redis unavailable")
	if !shouldShortCircuitScriptOnError("execute_script", nil, err) {
		t.Fatal("expected atomic transaction failure to force short-circuit")
	}
}

type failingDatabase struct {
	err error
}

func (d *failingDatabase) BeginTransaction(context.Context, sop.TransactionMode, ...time.Duration) (sop.Transaction, error) {
	return nil, d.err
}

func (d *failingDatabase) Config() sop.DatabaseOptions {
	return sop.DatabaseOptions{}
}

func TestCompileScript_DoesNotWriteErrorEventOnTransactionFailureDuringInternalCompile(t *testing.T) {
	var buf bytes.Buffer
	streamer := NewJSONStreamer(&buf)
	streamer.SetSuppressStepStart(true)
	ctx := context.WithValue(context.Background(), CtxKeyJSONStreamer, streamer)

	engine := NewScriptEngine(NewScriptContext(), func(name string) (Database, error) {
		return &failingDatabase{err: errors.New("redis unavailable")}, nil
	})

	compiled, err := engine.Compile([]ScriptInstruction{{
		Op:   "begin_tx",
		Args: map[string]any{"database": "system", "mode": "write"},
	}})
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	err = compiled(ctx, engine)
	if err == nil {
		t.Fatal("expected transaction failure")
	}
	if !strings.Contains(err.Error(), "redis unavailable") {
		t.Fatalf("expected transaction failure details, got: %v", err)
	}

	output := buf.String()
	if output != "" {
		t.Fatalf("expected compile-path execution to stay silent, got: %s", output)
	}
}
