package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
)

func (a *CopilotAgent) toolFetch(ctx context.Context, args map[string]any) (string, error) {
	p := ai.GetSessionPayload(ctx)
	if p == nil {
		return "", fmt.Errorf("no session payload found")
	}

	// Resolve Database
	var db *database.Database
	dbName, _ := args["database"].(string)
	if dbName == "" {
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
		return "", fmt.Errorf("database not found or not selected. Requested: '%s'", dbName)
	}

	storeName, _ := args["store"].(string)
	if storeName == "" {
		return "", fmt.Errorf("store name is required")
	}

	// Transaction logic
	var tx sop.Transaction
	var autoCommit bool

	if p.Transaction != nil {
		if t, ok := p.Transaction.(sop.Transaction); ok {
			tx = t
		}
	}
	if tx == nil {
		var err error
		tx, err = db.BeginTransaction(ctx, sop.ForReading)
		if err != nil {
			return "", fmt.Errorf("failed to begin transaction: %w", err)
		}
		autoCommit = true
	}

	// Ensure cleanup if auto-managed
	defer func() {
		if autoCommit && tx.HasBegun() {
			tx.Rollback(ctx)
		}
	}()

	store, err := jsondb.OpenStore(ctx, db.Options(), storeName, tx)
	if err != nil {
		return "", fmt.Errorf("failed to open store '%s': %w", storeName, err)
	}

	// Parse Arguments
	var limit int = 10
	if args["limit"] != nil {
		limit = int(coerceToFloat(args["limit"]))
	}
	if limit <= 0 {
		limit = 10
	}

	prefix, _ := args["prefix"].(string)

	// Single Key Lookup
	if keyArg, ok := args["key"]; ok && keyArg != nil {
		// Try to use the key as is
		ok, err := store.FindOne(ctx, keyArg, false)
		if err != nil {
			return "", fmt.Errorf("error searching for key: %w", err)
		}
		if !ok {
			return wrapNativeTerminalToolResult(ctx,
				fmt.Sprintf("Key not found: %v", keyArg),
				"terminal_error",
				"Stop retrying this exact key lookup until the key, store, or database selection changes.",
			), nil
		}
		k := store.GetCurrentKey()
		v, _ := store.GetCurrentValue(ctx)

		res := []map[string]any{
			{"key": k, "value": v},
		}
		b, _ := json.MarshalIndent(res, "", "  ")
		return string(b), nil
	}

	// Range/Scan
	var ok bool
	if prefix != "" {
		// Use the prefix as a starting key? In jsondb, keys are specific types.
		// If the store Key type is string, this works. If not, it might fail or strictly match.
		// For diagnostics, we assume string keys often, or we try.
		ok, err = store.FindOne(ctx, prefix, false)
	} else {
		ok, err = store.First(ctx)
	}

	if err != nil {
		return "", fmt.Errorf("error navigating store: %w", err)
	}
	if !ok {
		return "No data found.", nil
	}

	var results []map[string]any
	count := 0

	filter, _ := args["filter"].(map[string]any)

	for count < limit {
		k := store.GetCurrentKey()

		// If prefix mode, check prefix
		if prefix != "" {
			if kStr, ok := k.(string); ok {
				if !strings.HasPrefix(kStr, prefix) {
					break
				}
			}
		}

		v, err := store.GetCurrentValue(ctx)
		if err != nil {
			// In case of error reading value, we skip or report?
			// Let's report as error item
			results = append(results, map[string]any{"error": fmt.Sprintf("Error retrieving value: %v", err)})
			break
		}

		// Apply filter ONLY on Key (as requested by user guidelines for diagnostics)
		// We do NOT scan/filter the Value blob to avoid inefficient full scans on unindexed data.
		if len(filter) > 0 {
			match, _ := matchesKey(k, filter)
			if !match {
				goto next
			}
		}

		{
			// User requested Fetch behavior consistent with Join (flattened fields)
			// This helps tools that assume record-based schemas.
			results = append(results, flattenItem(k, v))
			count++
		}

	next:
		if ok, err := store.Next(ctx); err != nil || !ok {
			break
		}
	}

	if autoCommit {
		tx.Commit(ctx)
	}

	if count == 0 {
		return "No items matched criteria.", nil
	}

	bytes, _ := json.MarshalIndent(results, "", "  ")
	return string(bytes), nil
}
