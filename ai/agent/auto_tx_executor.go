package agent

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/v5"
)

type autoTxExecutor struct {
	original ai.ToolExecutor
	s        *Service
	db       *database.Database
}

// ListTools delegates to the original.
func (e *autoTxExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	if e.original == nil {
		return nil, nil
	}
	return e.original.ListTools(ctx)
}

// Execute wraps execution with auto-transaction management.
func (e *autoTxExecutor) Execute(ctx context.Context, name string, args map[string]any) (string, error) {
	if e.original == nil {
		return "", fmt.Errorf("no tool executor available")
	}

	if e.db != nil {
		if args == nil {
			args = make(map[string]any)
		}
		args["_db_instance"] = e.db
	}

	e.s.session.LastStep = &ai.ScriptStep{
		Type:    "command",
		Command: name,
		Args:    args,
	}

	var tx sop.Transaction
	if e.db != nil {
		if p := ai.GetSessionPayload(ctx); p != nil && p.Transaction == nil {
			var err error
			tx, err = e.db.BeginTransaction(ctx, sop.ForWriting)
			if err != nil {
				return "", fmt.Errorf("failed to begin auto-transaction: %w", err)
			}
			p.Transaction = tx
		}
	}

	result, err := e.original.Execute(ctx, name, args)

	if tx != nil {
		if err != nil {
			tx.Rollback(ctx)
		} else {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return "", fmt.Errorf("tool execution succeeded but transaction commit failed: %w", commitErr)
			}
		}
		if p := ai.GetSessionPayload(ctx); p != nil {
			p.Transaction = nil
		}
		e.s.session.Transaction = nil
	} else if e.s.session.Transaction != nil {
		if err != nil {
			e.s.session.Transaction.Rollback(ctx)
		} else {
			if commitErr := e.s.session.Transaction.Commit(ctx); commitErr != nil {
				e.s.session.Transaction = nil
				e.s.session.Variables = nil
				return "", fmt.Errorf("tool execution succeeded but session transaction commit failed: %w", commitErr)
			}
		}
		e.s.session.Transaction = nil
		e.s.session.Variables = nil
	}

	return result, err
}
