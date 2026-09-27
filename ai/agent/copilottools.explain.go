package agent

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/v5"
)

// toolExplainJoin predicts the execution strategy for a join.
func (a *CopilotAgent) toolExplainJoin(ctx context.Context, args map[string]any) (string, error) {
	p := ai.GetSessionPayload(ctx)

	// Resolve Database
	var db *database.Database
	dbName, _ := args["database"].(string)
	if dbName == "" && p != nil {
		dbName = p.CurrentDB
	}
	if dbName != "" {
		if dbName == "system" && a.systemDB != nil {
			db = a.systemDB
		} else if opts, ok := a.databases[dbName]; ok {
			db = database.NewDatabase(opts)
		}
	}
	if db == nil {
		return "", fmt.Errorf("database not found or not selected")
	}

	// Resolve Right Store
	storeName, _ := args["right_store"].(string)
	if storeName == "" {
		storeName, _ = args["store"].(string)
	}
	if storeName == "" {
		return "", fmt.Errorf("right_store (or store) argument is required")
	}

	// Resolve Transaction
	tx, autoCommit, err := a.resolveTransaction(ctx, db, dbName, sop.ForReading)
	if err != nil {
		return "", err
	}
	if autoCommit {
		defer tx.Rollback(ctx)
	}

	// Open Store
	store, _, _, err := a.openGenericStore(ctx, db.Options(), storeName, tx)
	if err != nil {
		return "", fmt.Errorf("failed to open store '%s': %w", storeName, err)
	}

	// Parse ON Clause
	// Expecting on: {"left_field": "right_field"}
	var rightFields []string
	if on, ok := args["on"].(map[string]any); ok {
		for _, v := range on {
			rightFields = append(rightFields, fmt.Sprintf("%v", v))
		}
	} else if _, ok := args["left_join_fields"]; ok {
		// Fallback to simpler list format if passed like 'join' tool
		// If explicit fields are passed, we assume right_join_fields are what we care about
		if rf, ok := args["right_join_fields"].([]any); ok {
			for _, v := range rf {
				rightFields = append(rightFields, fmt.Sprintf("%v", v))
			}
		} else if rf, ok := args["right_join_fields"].([]string); ok {
			rightFields = rf
		}
	}

	if len(rightFields) == 0 {
		return "Strategy: Unknown. Reason: No 'on' clause or 'right_join_fields' provided.", nil
	}

	// Analyze
	_, explanation := AnalyzeJoinPlan(store.GetStoreInfo(), rightFields)

	return fmt.Sprintf("Query Plan for joining store '%s':\n%s", storeName, explanation), nil
}
