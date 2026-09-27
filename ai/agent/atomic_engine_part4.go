package agent

import (
	"context"
	"encoding/json"
	"fmt"
	log "log/slog"
	"regexp"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
)

func queryFieldPattern(field string) string {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(field)), func(r rune) bool {
		switch r {
		case '.', '_', ' ':
			return true
		default:
			return false
		}
	})
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, `[\s_\.]+`)
}

func inferQuotedStringPredicateFromQueryPattern(query string, fieldPattern string) (map[string]any, bool) {
	if fieldPattern == "" {
		return nil, false
	}
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)` + fieldPattern + `\s*(?:is|=|==|equals?)?\s*['"]([^'"]+)['"]`),
	}
	for _, pattern := range patterns {
		if matches := pattern.FindStringSubmatch(query); len(matches) == 2 {
			value := strings.TrimSpace(matches[1])
			if value != "" {
				return map[string]any{"$eq": value}, true
			}
		}
	}
	return nil, false
}

func inferNumericPredicateFromQueryPattern(query string, fieldPattern string) (map[string]any, bool) {
	if fieldPattern == "" {
		return nil, false
	}
	patterns := []struct {
		re *regexp.Regexp
		op string
	}{
		{re: regexp.MustCompile(`(?i)` + fieldPattern + `\s*(>=|<=|>|<|=|==)\s*(-?\d+(?:\.\d+)?)`), op: ""},
		{re: regexp.MustCompile(`(?i)` + fieldPattern + `\s*(?:is\s+)?greater\s+than\s+(-?\d+(?:\.\d+)?)`), op: "$gt"},
		{re: regexp.MustCompile(`(?i)` + fieldPattern + `\s*(?:is\s+)?less\s+than\s+(-?\d+(?:\.\d+)?)`), op: "$lt"},
	}
	for _, pattern := range patterns {
		matches := pattern.re.FindStringSubmatch(query)
		if len(matches) == 0 {
			continue
		}
		op := pattern.op
		valueIndex := 1
		if op == "" {
			op = comparisonOperatorToAST(matches[1])
			valueIndex = 2
		}
		value := parseCompatibilityLiteral(matches[valueIndex])
		return map[string]any{op: value}, true
	}
	return nil, false
}

func isAliasPlaceholderField(field string) bool {
	field = strings.ToLower(strings.TrimSpace(field))
	if field == "" || strings.Contains(field, ".") {
		return false
	}
	switch field {
	case "users", "orders", "users_orders", "products", "customers", "items", "payments", "invoices", "details":
		return true
	default:
		return strings.HasPrefix(field, "store_") || strings.HasPrefix(field, "s_")
	}
}

func preserveLastResultOnNil(op string) bool {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "commit_tx", "rollback_tx":
		return true
	default:
		return false
	}
}

func isInternalScriptHandle(v any) bool {
	if v == nil {
		return false
	}
	switch v.(type) {
	case Database, sop.Transaction, jsondb.StoreAccessor:
		return true
	default:
		return false
	}
}

// resolveVarName strips the optional '@' or '$' prefix from a variable name.
func (e *ScriptEngine) resolveVarName(name string) string {
	name = strings.TrimPrefix(name, "@")
	return strings.TrimPrefix(name, "$")
}

// mapKeys returns the keys of a map[string]any for debugging
func (e *ScriptEngine) mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// txMapKeys returns the keys of a map[string]sop.Transaction for debugging
func (e *ScriptEngine) txMapKeys(m map[string]sop.Transaction) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func normalizeOpenDBName(args map[string]any) string {
	for _, key := range []string{"name", "database", "db", "db_name", "database_name", "current_db", "currentDatabase"} {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "current"
}

func (e *ScriptEngine) OpenDB(args map[string]any) (Database, error) {
	name := normalizeOpenDBName(args)
	if e.ResolveDatabase == nil {
		return nil, fmt.Errorf("database resolver not configured")
	}
	return e.ResolveDatabase(name)
}

func (e *ScriptEngine) BeginTx(ctx context.Context, args map[string]any) (sop.Transaction, error) {
	dbName, _ := args["database"].(string)
	dbName = e.resolveVarName(dbName)
	modeStr, _ := args["mode"].(string)

	var db Database
	if dbObj, ok := e.getDatabase(dbName); ok {
		db = dbObj
	} else {

		if e.ResolveDatabase == nil {
			return nil, fmt.Errorf("database resolver not configured")
		}
		var err error
		db, err = e.ResolveDatabase(dbName)
		if err != nil {
			return nil, fmt.Errorf("database '%s' not found in context or registry", dbName)
		}
	}

	mode := sop.ForReading
	if modeStr == "write" {
		mode = sop.ForWriting
	}

	tx, err := db.BeginTransaction(ctx, mode)
	if err == nil {
		log.Info("BeginTx: Transaction started", "database", dbName, "mode", modeStr, "tx_id", tx.GetID())
		if e.Context.TxToDB == nil {
			e.Context.TxToDB = make(map[sop.Transaction]Database)
		}
		e.Context.TxToDB[tx] = db
	} else {
		log.Error("BeginTx: Failed to start transaction", "database", dbName, "mode", modeStr, "error", err)
	}
	return tx, err
}

func (e *ScriptEngine) CommitTx(ctx context.Context, args map[string]any) (err error) {
	txName, _ := args["transaction"].(string)
	txName = e.resolveVarName(txName)

	// DEBUG: Log all transaction "bags" to diagnose storage confusion
	log.Info("CommitTx: Looking for transaction", "txName", txName)
	log.Info("CommitTx: Context.Transactions keys", "keys", e.txMapKeys(e.Context.Transactions))
	log.Info("CommitTx: Context.Variables keys", "keys", e.mapKeys(e.Context.Variables))
	log.Info("CommitTx: Context.TxToDB count", "count", len(e.Context.TxToDB))

	// Check if transaction is in Variables but not in Transactions
	if varTx, inVars := e.Context.Variables[txName]; inVars {
		if _, isTx := varTx.(sop.Transaction); isTx {
			log.Info("CommitTx: Found transaction in Variables but checking Transactions", "in_transactions", e.Context.Transactions[txName] != nil)
		}
	}

	tx, ok := e.Context.Transactions[txName]
	if !ok {
		return fmt.Errorf("transaction '%s' not found", txName)
	}

	commitCtx := ctx
	if maxDuration := tx.CommitMaxDuration(); maxDuration > 0 {
		var cancel context.CancelFunc
		commitCtx, cancel = context.WithTimeout(ctx, maxDuration)
		defer cancel()
	}

	// Stream and drain all cursors BEFORE committing the transaction.
	// This ensures cursors are fully read while transaction is still open,
	// and data is streamed to client without loading everything into memory.
	if err = e.streamAndDrainCursorsBeforeCommit(commitCtx); err != nil {
		return err
	}

	// Set output variable if not already set
	if _, ok := e.Context.Variables["output"]; !ok {
		if e.Context.LastUpdatedVar != "" {
			if materialized, ok := e.Context.Variables[e.Context.LastUpdatedVar]; ok && !isInternalScriptHandle(materialized) {
				e.Context.Variables["output"] = materialized
			}
		}
		if _, ok := e.Context.Variables["output"]; !ok && e.LastResult != nil && !isInternalScriptHandle(e.LastResult) {
			e.Context.Variables["output"] = e.LastResult
		}
	}

	// Now commit the transaction (all cursors are already drained)
	log.Info("CommitTx: Committing transaction", "transaction", txName, "tx_id", tx.GetID())
	err = tx.Commit(commitCtx)
	if err == nil {
		log.Info("CommitTx: Transaction committed successfully", "transaction", txName, "tx_id", tx.GetID())
	} else {
		log.Error("CommitTx: Failed to commit transaction", "transaction", txName, "tx_id", tx.GetID(), "error", err)
	}
	return err
}

// streamAndDrainCursorsBeforeCommit finds all cursors in the context and streams them
// to the client in a memory-efficient way before the transaction is committed.
// This prevents cursors from becoming invalid after commit while avoiding loading
// large result sets into memory.
func (e *ScriptEngine) streamAndDrainCursorsBeforeCommit(ctx context.Context) error {
	log.Info("streamAndDrainCursorsBeforeCommit: Starting", "last_updated_var", e.Context.LastUpdatedVar)

	// Get the JSON streamer if available
	var streamer *JSONStreamer
	if s, ok := ctx.Value(CtxKeyJSONStreamer).(*JSONStreamer); ok {
		streamer = s
		// Disable per-item flushing during bulk streaming for performance.
		// The streamer will flush at the end when Close() is called.
		streamer.SetFlush(false)
		log.Info("streamAndDrainCursorsBeforeCommit: Streamer available, flush disabled for bulk ops")
	} else {
		log.Info("streamAndDrainCursorsBeforeCommit: No streamer in context, will materialize to arrays")
	}

	// streamAndDrain streams a cursor's data to the client (if streamer present)
	// or materializes to array (if no streamer), then drains it completely.
	// Returns the materialized array (or nil if streamed) and row count.
	streamAndDrain := func(name string, cursor ScriptCursor) ([]any, int, error) {
		log.Info("streamAndDrainCursorsBeforeCommit: Starting cursor drain", "var_name", name, "has_streamer", streamer != nil)

		// Set up streaming if streamer is available
		var resultStreamer interface {
			WriteItem(any)
			Close()
			SetMetadata(map[string]any)
		}

		log.Info("streamAndDrain: About to check streamer", "var_name", name, "streamer_nil", streamer == nil)

		if streamer != nil {
			streamer.SetSuppressStepStart(true)
			resultStreamer = streamer.StartStreamingStep("commit_drain", name, "", 0)
			log.Info("streamAndDrain: Created result streamer", "var_name", name)

			// Get ordered fields for metadata if available
			if provider, ok := cursor.(OrderedFieldsProvider); ok {
				if orderedFields := provider.GetOrderedFields(); len(orderedFields) > 0 {
					resultStreamer.SetMetadata(map[string]any{"columns": orderedFields})
					log.Info("streamAndDrain: Set metadata columns", "var_name", name, "column_count", len(orderedFields))
				}
			}
		} else {
			log.Info("streamAndDrain: No streamer, will accumulate to array", "var_name", name)
		}

		// If no streamer, accumulate results for backward compatibility
		var results []any
		if streamer == nil {
			results = make([]any, 0)
			log.Info("streamAndDrain: Initialized results array", "var_name", name)
		}

		log.Info("streamAndDrain: Starting iteration loop", "var_name", name)

		// Process each item: stream or accumulate
		count := 0
		for {
			// Check context cancellation before each iteration
			select {
			case <-ctx.Done():
				return results, count, fmt.Errorf("context cancelled while draining cursor '%s': %w", name, ctx.Err())
			default:
			}

			log.Info("streamAndDrain: Calling cursor.Next", "var_name", name, "iteration", count)
			itemObj, ok, err := cursor.Next(ctx)
			log.Info("streamAndDrain: cursor.Next returned", "var_name", name, "iteration", count, "ok", ok, "err", err)
			if err != nil {
				return results, count, fmt.Errorf("failed to drain cursor '%s' before commit: %v", name, err)
			}
			if !ok {
				break
			}

			// Stream to client if streamer is available
			if resultStreamer != nil {
				resultStreamer.WriteItem(itemObj)
			} else {
				// Accumulate in array if no streamer
				results = append(results, itemObj)
			}

			count++

			// Log progress every 10,000 rows to detect potential infinite loops
			if count%10000 == 0 {
				log.Info("streamAndDrainCursorsBeforeCommit: Progress update", "var_name", name, "rows_processed", count)
			}
		}

		log.Info("streamAndDrainCursorsBeforeCommit: Cursor drained", "var_name", name, "row_count", count, "streamed", streamer != nil)

		// Close the streamer
		if resultStreamer != nil {
			log.Info("streamAndDrain: Closing result streamer", "var_name", name, "row_count", count)
			resultStreamer.Close()
			log.Info("streamAndDrain: Result streamer closed", "var_name", name)
		}

		// Close the cursor
		cursor.Close()

		return results, count, nil
	}

	// drainVariable checks if a variable is a cursor and drains it
	drainVariable := func(name string) (bool, error) {
		if name == "" {
			return false, nil
		}
		val, ok := e.Context.Variables[name]
		if !ok {
			return false, nil
		}
		cursor, ok := val.(ScriptCursor)
		if !ok {
			return false, nil
		}

		log.Info("streamAndDrainCursorsBeforeCommit: Found cursor in variable", "var_name", name)
		results, count, err := streamAndDrain(name, cursor)
		if err != nil {
			return true, err
		}

		// If streamed (results is nil), replace with summary
		// If not streamed (results is array), replace with array
		if results == nil {
			e.Context.Variables[name] = map[string]any{
				"streamed": true,
				"rows":     count,
			}
			log.Info("streamAndDrainCursorsBeforeCommit: Variable cursor streamed", "var_name", name, "rows", count)
		} else {
			e.Context.Variables[name] = results
			log.Info("streamAndDrainCursorsBeforeCommit: Variable cursor materialized to array", "var_name", name, "rows", count)
		}

		return true, nil
	}

	// drainDirect checks if a direct value is a cursor and drains it
	drainDirect := func(current any, set func(any), label string) (bool, error) {
		cursor, ok := current.(ScriptCursor)
		if !ok {
			return false, nil
		}

		log.Info("streamAndDrainCursorsBeforeCommit: Found cursor in direct value", "label", label)
		results, count, err := streamAndDrain(label, cursor)
		if err != nil {
			return true, err
		}

		// If streamed (results is nil), replace with summary
		// If not streamed (results is array), replace with array
		if results == nil {
			set(map[string]any{
				"streamed": true,
				"rows":     count,
			})
			log.Info("streamAndDrainCursorsBeforeCommit: Direct cursor streamed", "label", label, "rows", count)
		} else {
			set(results)
			log.Info("streamAndDrainCursorsBeforeCommit: Direct cursor materialized to array", "label", label, "rows", count)
		}

		return true, nil
	}

	// Try to drain cursors in priority order
	for _, name := range []string{"output", "final_result", "result", e.Context.LastUpdatedVar} {
		log.Info("streamAndDrainCursorsBeforeCommit: Checking variable", "var_name", name)
		if handled, err := drainVariable(name); handled || err != nil {
			return err
		}
	}

	// Check ReturnValue
	log.Info("streamAndDrainCursorsBeforeCommit: Checking ReturnValue")
	if handled, err := drainDirect(e.ReturnValue, func(v any) { e.ReturnValue = v }, "return_value"); handled || err != nil {
		return err
	}

	// Check LastResult
	log.Info("streamAndDrainCursorsBeforeCommit: Checking LastResult")
	if handled, err := drainDirect(e.LastResult, func(v any) { e.LastResult = v }, "last_result"); handled || err != nil {
		return err
	}

	log.Info("streamAndDrainCursorsBeforeCommit: Completed successfully")
	return nil
}

func (e *ScriptEngine) RollbackTx(ctx context.Context, args map[string]any) error {
	txName, _ := args["transaction"].(string)
	txName = e.resolveVarName(txName)

	// DEBUG: Log all transaction "bags" to diagnose storage confusion
	log.Info("RollbackTx: Looking for transaction", "txName", txName)
	log.Info("RollbackTx: Context.Transactions keys", "keys", e.txMapKeys(e.Context.Transactions))
	log.Info("RollbackTx: Context.Variables keys", "keys", e.mapKeys(e.Context.Variables))

	tx, ok := e.Context.Transactions[txName]
	if !ok {
		return fmt.Errorf("transaction '%s' not found", txName)
	}

	log.Info("RollbackTx: Rolling back transaction", "transaction", txName, "tx_id", tx.GetID())
	err := tx.Rollback(ctx)
	if err == nil {
		log.Info("RollbackTx: Transaction rolled back successfully", "transaction", txName, "tx_id", tx.GetID())
	} else {
		log.Error("RollbackTx: Failed to rollback transaction", "transaction", txName, "tx_id", tx.GetID(), "error", err)
	}
	return err
}

func (e *ScriptEngine) OpenStore(ctx context.Context, args map[string]any) (jsondb.StoreAccessor, error) {
	txName, _ := args["transaction"].(string)
	txName = e.resolveVarName(txName)
	storeName, _ := args["name"].(string)
	if storeName == "" {
		storeName, _ = args["store"].(string)
	}
	if storeName == "" {
		storeName, _ = args["store_name"].(string)
	}

	var tx sop.Transaction
	var ok bool

	if txName == "" {

		if len(e.Context.Transactions) == 1 {
			for _, t := range e.Context.Transactions {
				tx = t
				ok = true
				break
			}
		} else if len(e.Context.Transactions) > 1 {
			return nil, fmt.Errorf("transaction name required (multiple active transactions)")
		} else {
			return nil, fmt.Errorf("no active transaction found")
		}
	} else {
		tx, ok = e.Context.Transactions[txName]
	}

	if !ok {
		return nil, fmt.Errorf("transaction '%s' not found", txName)
	}

	// Resolve Database
	var db Database
	dbName, _ := args["database"].(string)
	dbName = e.resolveVarName(dbName)

	if dbName != "" {
		// Explicit database argument
		var found bool
		db, found = e.getDatabase(dbName)
		if !found {
			if e.ResolveDatabase != nil {
				var err error
				db, err = e.ResolveDatabase(dbName)
				if err != nil {
					return nil, fmt.Errorf("database '%s' not found", dbName)
				}
			} else {
				return nil, fmt.Errorf("database '%s' not found", dbName)
			}
		}
	} else {

		if associatedDB, found := e.Context.TxToDB[tx]; found {
			db = associatedDB
		} else if len(e.Context.Databases) == 1 {

			for _, d := range e.Context.Databases {
				db = d
				break
			}
		} else if len(e.Context.Databases) > 1 {
			return nil, fmt.Errorf("database argument required (multiple open databases)")
		} else {
			return nil, fmt.Errorf("database argument required")
		}
	}

	create, _ := args["create"].(bool)

	if e.StoreOpener != nil {

		return e.StoreOpener(ctx, db.Config(), storeName, tx)
	}

	if create {

		return jsondb.CreateObjectStore(ctx, db.Config(), storeName, tx)
	}
	return jsondb.OpenStore(ctx, db.Config(), storeName, tx)
}

func (e *ScriptEngine) Scan(ctx context.Context, args map[string]any, input any) (any, error) {

	storeVarName, _ := args["store"].(string)
	storeVarName = e.resolveVarName(storeVarName)

	var store jsondb.StoreAccessor
	var ok bool

	if inputStore, isStore := input.(jsondb.StoreAccessor); isStore && storeVarName == "" {
		store = inputStore
		ok = true
	} else if inputStr, isStr := input.(string); isStr && storeVarName == "" {
		storeVarName = inputStr
		store, ok = e.getStore(storeVarName)
	} else {
		store, ok = e.getStore(storeVarName)
	}

	if !ok {
		return nil, fmt.Errorf("store variable '%s' not found", storeVarName)
	}

	storeName := storeVarName
	info := store.GetStoreInfo()
	if info.Name != "" {
		storeName = info.Name
	}

	limit, _ := args["limit"].(float64)
	if limit <= 0 {
		limit = 1000
	}
	direction, _ := args["direction"].(string)
	dirLower := strings.ToLower(direction)
	isDesc := dirLower == "desc" || dirLower == "descending"
	startKey := args["start_key"]
	prefix := args["prefix"]

	// Accept both "condition" (used by LLM) and "filter" (legacy parameter name)
	filter := args["filter"]
	if condition := args["condition"]; condition != nil {
		filter = condition
	}

	// Validate filter condition before execution
	if filter != nil {
		log.Info("Scan: Validating filter condition", "condition", filter)
		if err := e.validateScanFilterCondition(filter); err != nil {
			log.Error("Scan: Validation failed", "error", err, "condition", filter)
			return nil, fmt.Errorf("scan filter validation failed: %w", err)
		}
		log.Info("Scan: Validation passed", "condition", filter)
	}

	stream, _ := args["stream"].(bool)

	var okIter bool
	var err error
	var indexSpec *jsondb.IndexSpecification
	info = store.GetStoreInfo()
	if info.MapKeyIndexSpecification != "" {
		var spec jsondb.IndexSpecification
		if err := json.Unmarshal([]byte(info.MapKeyIndexSpecification), &spec); err == nil {
			indexSpec = &spec
		}
	}

	if startKey != nil {
		if isDesc {
			okIter, err = store.FindInDescendingOrder(ctx, startKey)
		} else {
			okIter, err = store.FindOne(ctx, startKey, true)
		}
	} else if prefix != nil {
		if isDesc {
			okIter, err = store.FindInDescendingOrder(ctx, prefix)
		} else {
			okIter, err = store.FindOne(ctx, prefix, true)
		}
	} else {
		if isDesc {
			okIter, err = store.Last(ctx)
		} else {
			okIter, err = store.First(ctx)
		}
	}

	if err != nil {
		return nil, err
	}

	if !okIter {
		if stream {

			return &StoreCursor{
				store:     store,
				storeName: storeName,
				indexSpec: indexSpec,
				ctx:       ctx,
				limit:     int(limit),
				started:   true,
			}, nil
		}
		return []map[string]any{}, nil
	}

	var filterMap map[string]any
	if filter != nil {
		if m, ok := filter.(map[string]any); ok {
			filterMap = m
		}
	}

	if stream {
		return &StoreCursor{
			store:     store,
			storeName: storeName,
			indexSpec: indexSpec,
			ctx:       ctx,
			limit:     int(limit),
			filter:    filterMap,
			engine:    e,
			isDesc:    isDesc,
			prefix:    prefix,
			started:   false,
		}, nil
	}

	results := make([]map[string]any, 0)
	count := 0
	for okIter && count < int(limit) {
		k := store.GetCurrentKey()
		// Use GetCurrentValueNoLock to avoid tracking items during scan
		v, _ := store.GetCurrentValueNoLock(ctx)

		if prefix != nil {
			if kStr, isStr := k.(string); isStr {
				pStr := fmt.Sprintf("%v", prefix)
				if !strings.HasPrefix(kStr, pStr) {
					break
				}
			}
		}

		itemAny := renderItem(k, v, nil)

		item, _ := itemAny.(map[string]any)

		if filter != nil {

			match, err := e.evaluateCondition(item, filter.(map[string]any))
			if err != nil {

				return nil, fmt.Errorf("filter evaluation failed: %v", err)
			}
			if !match {

				if isDesc {
					okIter, _ = store.Previous(ctx)
				} else {
					okIter, _ = store.Next(ctx)
				}
				continue
			}
		}

		// Only register read lock for items we're actually emitting
		if err := store.RLockCurrentItem(ctx); err != nil {
			return nil, fmt.Errorf("failed to lock emitted item: %w", err)
		}

		if storeName != "" {
			prefixed := make(map[string]any, len(item))
			for k, val := range item {
				prefixed[storeName+"."+k] = val
			}
			item = prefixed
		}

		results = append(results, item)
		count++

		if isDesc {
			okIter, _ = store.Previous(ctx)
		} else {
			okIter, _ = store.Next(ctx)
		}
	}

	return results, nil
}

func TranslateASTToCEL(ast any) (string, bool) {
	switch v := ast.(type) {
	case string:
		return fmt.Sprintf("%q", v), true
	case float64, int, bool:
		return fmt.Sprintf("%v", v), true
	case map[string]any:
		if varName, ok := v["var"].(string); ok {
			if !strings.Contains(varName, ".") {
				return "value." + varName, true
			}
			return varName, true
		}
		for op, argsRAW := range v {
			if args, ok := argsRAW.([]any); ok {
				if len(args) == 2 {
					switch op {
					case "==", "!=", ">", ">=", "<", "<=":
						left, leftOk := TranslateASTToCEL(args[0])
						right, rightOk := TranslateASTToCEL(args[1])
						if leftOk && rightOk {
							return fmt.Sprintf("(%s %s %s)", left, op, right), true
						}
					}
				}
				if op == "and" || op == "or" || op == "&&" || op == "||" {
					opStr := op
					if op == "and" {
						opStr = "&&"
					}
					if op == "or" {
						opStr = "||"
					}
					var parts []string
					for _, arg := range args {
						part, ok := TranslateASTToCEL(arg)
						if ok {
							parts = append(parts, part)
						}
					}
					if len(parts) > 0 {
						return fmt.Sprintf("(%s)", strings.Join(parts, " "+opStr+" ")), true
					}
				}
			}
		}
	}
	return "", false
}

func (e *ScriptEngine) evaluateCondition(item any, condition any) (bool, error) {

	// Automatically translate map-based proprietary AST to CEL string expression natively.
	if _, ok := condition.(map[string]any); ok {
		if celStr, ok := TranslateASTToCEL(condition); ok {
			condition = celStr
		}
	}

	if expr, ok := condition.(string); ok {
		// Extract variables dynamically to satisfy CEL strict compilation
		identRegex := regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*`)
		matches := identRegex.FindAllString(expr, -1)

		var celVars []cel.EnvOption
		declared := map[string]bool{
			"key": true, "value": true, "item": true, "true": true, "false": true, "null": true,
		}
		celVars = append(celVars, cel.Variable("key", cel.AnyType))
		celVars = append(celVars, cel.Variable("value", cel.AnyType))
		celVars = append(celVars, cel.Variable("item", cel.AnyType))

		for _, m := range matches {
			if !declared[m] {
				declared[m] = true
				celVars = append(celVars, cel.Variable(m, cel.AnyType))
			}
		}

		env, err := cel.NewEnv(celVars...)
		if err != nil {
			return false, err
		}
		ast, issues := env.Compile(expr)
		if issues != nil && issues.Err() != nil {
			return false, issues.Err()
		}
		prg, err := env.Program(ast)
		if err != nil {
			return false, err
		}

		itemMap, isMap := item.(map[string]any)
		if !isMap {
			if om, ok := item.(*OrderedMap); ok && om != nil {
				itemMap = om.m
			} else if om, ok := item.(OrderedMap); ok {
				itemMap = om.m
			} else {
				return false, fmt.Errorf("item is not a map, got %T", item)
			}
		}

		// Build a robust context map that supports "value.x", "orders.x", or just "x"
		ctxMap := make(map[string]any)
		unprefixed := make(map[string]any)

		for k, v := range itemMap {
			ctxMap[k] = v // literal prefix map

			if idx := strings.Index(k, "."); idx != -1 {
				base := k[idx+1:]
				unprefixed[base] = v

				prefix := k[:idx]
				if _, ok := unprefixed[prefix]; !ok {
					unprefixed[prefix] = make(map[string]any)
				}
				unprefixed[prefix].(map[string]any)[base] = v
			} else {
				unprefixed[k] = v
			}
		}

		for k, v := range unprefixed {
			ctxMap[k] = v
		}
		ctxMap["item"] = itemMap
		ctxMap["value"] = unprefixed
		ctxMap["key"] = unprefixed["key"]

		out, _, err := prg.Eval(ctxMap)
		if err != nil {
			return false, err
		}
		if b, ok := out.Value().(bool); ok {
			return b, nil
		}
		return false, fmt.Errorf("expression did not return a boolean")
	}

	if matchMap, ok := condition.(map[string]any); ok {

		if itemMap, ok := item.(map[string]any); ok {
			for k := range matchMap {

				if _, found := resolveKey(itemMap, k); !found {

					if suggested := findSimilarKey(k, itemMap); suggested != "" {
						return false, fmt.Errorf("field '%s' not found in item. Did you mean '%s'?", k, suggested)
					}

					return false, fmt.Errorf("field '%s' not found in item. Available fields: %v", k, getKeys(itemMap))
				}
			}
		}

		return matchesMap(item, matchMap), nil
	}

	return false, fmt.Errorf("unsupported filter condition type")
}
