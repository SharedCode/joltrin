package agent

import (
	"context"
	"encoding/json"
	"fmt"
	log "log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/common"
	"github.com/sharedcode/joltrin/jsondb"
)

var inferSpaceNamePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)["']([^"']+)["']\s+space\b`),
	regexp.MustCompile(`(?i)\b(?:to|into|in|within|under|inside|for)\s+(?:my|the)\s+([A-Za-z0-9_.-]+(?:\s+[A-Za-z0-9_.-]+){0,3})\s+space\b`),
	regexp.MustCompile(`(?i)\b(?:my|the)\s+([A-Za-z0-9_.-]+(?:\s+[A-Za-z0-9_.-]+){0,3})\s+space\b`),
}

var inferredSpaceNameStopwords = map[string]struct{}{
	"current":  {},
	"existing": {},
	"new":      {},
	"same":     {},
	"selected": {},
	"target":   {},
	"this":     {},
	"that":     {},
}

func resolveSpaceKBName(args map[string]any, payload *ai.SessionPayload) string {
	for _, key := range []string{"kb_name", "name", "space_name", "space"} {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	if payload == nil {
		return ""
	}
	return inferSpaceKBNameFromQuery(payload.CurrentUserQuery)
}

func inferSpaceKBNameFromQuery(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(query), "current database:") {
		if idx := strings.Index(query, "\n"); idx >= 0 {
			query = strings.TrimSpace(query[idx+1:])
		}
	}

	for _, pattern := range inferSpaceNamePatterns {
		matches := pattern.FindStringSubmatch(query)
		if len(matches) < 2 {
			continue
		}
		candidate := strings.TrimSpace(matches[1])
		candidate = strings.Trim(candidate, " .,:;!?()[]{}")
		if candidate == "" {
			continue
		}
		if _, blocked := inferredSpaceNameStopwords[strings.ToLower(candidate)]; blocked {
			continue
		}
		return candidate
	}
	return ""
}

func (a *CopilotAgent) toolAdd(ctx context.Context, args map[string]any) (string, error) {
	// Stub Mode Check
	if a.Config.StubMode {
		fmt.Printf("DEBUG: toolAdd called in STUB MODE with:\n%+v\n", args)
		return "Add executed successfully (STUBBED).", nil
	}

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

	// Policy Check: Check RESTRICTED access to systemDB via generic tools
	if dbName == "system" {
		return "", fmt.Errorf("direct modification of 'system' database via generic tools (add) is restricted; rely on automated Active Memory context extraction instead")
	}

	if dbName != "" {
		if dbName == "system" && a.systemDB != nil {
			db = a.systemDB
		} else if opts, ok := a.databases[dbName]; ok {
			db = database.NewDatabase(opts)
		}
	}

	storeName, _ := args["store"].(string)
	var key any
	if k, ok := args["key"]; ok {
		key = k
	}
	var value any
	if v, ok := args["value"]; ok {
		value = v
	} else {
		// If "value" is not explicitly provided, try to construct it from other arguments
		valMap := CleanArgs(args, "store", "key", "database", "action")
		if len(valMap) > 0 {
			value = valMap
		}
	}

	if storeName == "" || key == nil || value == nil {
		return "", fmt.Errorf("store, key and value (or fields) are required")
	}

	var tx sop.Transaction
	var localTx bool

	tx, localTx, err := a.resolveTransaction(ctx, db, dbName, sop.ForWriting)

	if err != nil {
		return "", err
	}

	var isPrimitiveKey bool
	if t2, ok := tx.GetPhasedTransaction().(*common.Transaction); ok {
		stores, err := t2.StoreRepository.Get(ctx, storeName)
		if err == nil && len(stores) > 0 {
			isPrimitiveKey = stores[0].IsPrimitiveKey
		}
	}

	// Prepare key based on store type
	if !isPrimitiveKey {
		if keyStr, ok := key.(string); ok {
			var keyMap map[string]any
			if err := json.Unmarshal([]byte(keyStr), &keyMap); err != nil {
				if localTx {
					tx.Rollback(ctx)
				}
				return "", fmt.Errorf("failed to parse complex key JSON: %v", err)
			}
			key = keyMap
		}
		if _, ok := key.(map[string]any); !ok {
			if localTx {
				tx.Rollback(ctx)
			}
			return "", fmt.Errorf("key must be a map or JSON string for complex key store")
		}
	} else {
		if _, ok := key.(string); !ok {
			key = fmt.Sprintf("%v", key)
		}
	}

	// Prepare value (try to unmarshal if string)
	if valStr, ok := value.(string); ok {
		var valMap map[string]any
		if err := json.Unmarshal([]byte(valStr), &valMap); err == nil {
			value = valMap
		} else {
			var valArr []any
			if err := json.Unmarshal([]byte(valStr), &valArr); err == nil {
				value = valArr
			}
		}
	}

	var store jsondb.StoreAccessor

	// Check cache first
	cacheKey := fmt.Sprintf("store_%s", storeName)
	if p.Variables != nil {
		if s, ok := p.Variables[cacheKey].(jsondb.StoreAccessor); ok {
			store = s
		}
	}

	if store == nil {
		store, err = jsondb.OpenStore(ctx, db.Config(), storeName, tx)
		if err != nil {
			if localTx {
				tx.Rollback(ctx)
			}
			return "", fmt.Errorf("failed to open store '%s': %w", storeName, err)
		}
		// Cache it only if we are in a long-running transaction (not local/auto-commit)
		if !localTx {
			if p.Variables == nil {
				p.Variables = make(map[string]any)
			}
			p.Variables[cacheKey] = store
		}
	}

	var ok bool
	ok, err = store.Add(ctx, key, value)
	if err != nil {
		if localTx {
			tx.Rollback(ctx)
		}
		return "", fmt.Errorf("failed to add item: %w", err)
	}
	if !ok {
		if localTx {
			tx.Rollback(ctx)
		}
		return fmt.Sprintf("Item with key '%v' already exists in store '%s'", key, storeName), nil
	}

	if localTx {
		if err := tx.Commit(ctx); err != nil {
			return "", fmt.Errorf("failed to commit add transaction: %w", err)
		}
	}

	return fmt.Sprintf("Item added to store '%s'", storeName), nil
}

func (a *CopilotAgent) toolUpdate(ctx context.Context, args map[string]any) (string, error) {
	// Stub Mode Check
	if a.Config.StubMode {
		fmt.Printf("DEBUG: toolUpdate called in STUB MODE with:\n%+v\n", args)
		return "Update executed successfully (STUBBED).", nil
	}

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

	// Policy Check: Check RESTRICTED access to systemDB via generic tools
	if dbName == "system" {
		return "", fmt.Errorf("direct modification of 'system' database via generic tools (update) is restricted; rely on automated Active Memory context extraction instead")
	}

	if dbName != "" {
		if dbName == "system" && a.systemDB != nil {
			db = a.systemDB
		} else if opts, ok := a.databases[dbName]; ok {
			db = database.NewDatabase(opts)
		}
	}

	storeName, _ := args["store"].(string)
	var key any
	if k, ok := args["key"]; ok {
		key = k
	}
	var value any
	if v, ok := args["value"]; ok {
		value = v
	} else {
		// If "value" is not explicitly provided, try to construct it from other arguments
		valMap := CleanArgs(args, "store", "key", "database", "action")
		if len(valMap) > 0 {
			value = valMap
		}
	}

	if storeName == "" || key == nil || value == nil {
		return "", fmt.Errorf("store, key and value (or fields) are required")
	}

	var tx sop.Transaction
	var localTx bool

	tx, localTx, err := a.resolveTransaction(ctx, db, dbName, sop.ForWriting)

	if err != nil {
		return "", err
	}

	var isPrimitiveKey bool
	if t2, ok := tx.GetPhasedTransaction().(*common.Transaction); ok {
		stores, err := t2.StoreRepository.Get(ctx, storeName)
		if err == nil && len(stores) > 0 {
			isPrimitiveKey = stores[0].IsPrimitiveKey
		}
	}

	// Prepare key based on store type
	if !isPrimitiveKey {
		if keyStr, ok := key.(string); ok {
			var keyMap map[string]any
			if err := json.Unmarshal([]byte(keyStr), &keyMap); err != nil {
				if localTx {
					tx.Rollback(ctx)
				}
				return "", fmt.Errorf("failed to parse complex key JSON: %v", err)
			}
			key = keyMap
		}
		if _, ok := key.(map[string]any); !ok {
			if localTx {
				tx.Rollback(ctx)
			}
			return "", fmt.Errorf("key must be a map or JSON string for complex key store")
		}
	} else {
		if _, ok := key.(string); !ok {
			key = fmt.Sprintf("%v", key)
		}
	}

	// Prepare value (try to unmarshal if string)
	if valStr, ok := value.(string); ok {
		var valMap map[string]any
		if err := json.Unmarshal([]byte(valStr), &valMap); err == nil {
			value = valMap
		} else {
			var valArr []any
			if err := json.Unmarshal([]byte(valStr), &valArr); err == nil {
				value = valArr
			}
		}
	}

	var store jsondb.StoreAccessor

	// Check cache first
	cacheKey := fmt.Sprintf("store_%s", storeName)
	if p.Variables != nil {
		if s, ok := p.Variables[cacheKey].(jsondb.StoreAccessor); ok {
			store = s
		}
	}

	if store == nil {
		store, err = jsondb.OpenStore(ctx, db.Config(), storeName, tx)
		if err != nil {
			if localTx {
				tx.Rollback(ctx)
			}
			return "", fmt.Errorf("failed to open store '%s': %w", storeName, err)
		}
		// Cache it only if we are in a long-running transaction (not local/auto-commit)
		if !localTx {
			if p.Variables == nil {
				p.Variables = make(map[string]any)
			}
			p.Variables[cacheKey] = store
		}
	}

	var ok bool
	ok, err = store.Update(ctx, key, value)
	if err != nil {
		if localTx {
			tx.Rollback(ctx)
		}
		return "", fmt.Errorf("failed to update item: %w", err)
	}
	if !ok {
		if localTx {
			tx.Rollback(ctx)
		}
		return wrapNativeTerminalToolResult(ctx,
			fmt.Sprintf("Item with key '%v' not found in store '%s'", key, storeName),
			"terminal_error",
			"Stop retrying this exact update until the key or target store changes.",
		), nil
	}

	if localTx {
		if err := tx.Commit(ctx); err != nil {
			return "", fmt.Errorf("failed to commit update transaction: %w", err)
		}
	} else {
		// The caller manages the surrounding transaction; no commit here.
		log.Debug("toolUpdate: delegating commit to external transaction", "hasBegun", tx.HasBegun())
	}

	return fmt.Sprintf("Item updated in store '%s'", storeName), nil
}

func (a *CopilotAgent) toolDelete(ctx context.Context, args map[string]any) (string, error) {
	// Stub Mode Check
	if a.Config.StubMode {
		fmt.Printf("DEBUG: toolDelete called in STUB MODE with:\n%+v\n", args)
		return "Delete executed successfully (STUBBED).", nil
	}

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

	// Policy Check: Check RESTRICTED access to systemDB via generic tools
	if dbName == "system" {
		return "", fmt.Errorf("direct modification of 'system' database via generic tools (delete) is restricted; rely on automated Active Memory context extraction instead")
	}

	if dbName != "" {
		if dbName == "system" && a.systemDB != nil {
			db = a.systemDB
		} else if opts, ok := a.databases[dbName]; ok {
			db = database.NewDatabase(opts)
		}
	}

	storeName, _ := args["store"].(string)
	var key any
	if k, ok := args["key"]; ok {
		key = k
	}

	if storeName == "" || key == nil {
		return "", fmt.Errorf("store and key are required")
	}

	var tx sop.Transaction
	var localTx bool

	tx, localTx, err := a.resolveTransaction(ctx, db, dbName, sop.ForWriting)

	if err != nil {
		return "", err
	}

	var isPrimitiveKey bool
	if t2, ok := tx.GetPhasedTransaction().(*common.Transaction); ok {
		stores, err := t2.StoreRepository.Get(ctx, storeName)
		if err == nil && len(stores) > 0 {
			isPrimitiveKey = stores[0].IsPrimitiveKey
		}
	}

	// Prepare key based on store type
	if !isPrimitiveKey {
		if keyStr, ok := key.(string); ok {
			var keyMap map[string]any
			if err := json.Unmarshal([]byte(keyStr), &keyMap); err != nil {
				if localTx {
					tx.Rollback(ctx)
				}
				return "", fmt.Errorf("failed to parse complex key JSON: %v", err)
			}
			key = keyMap
		}
		if _, ok := key.(map[string]any); !ok {
			if localTx {
				tx.Rollback(ctx)
			}
			return "", fmt.Errorf("key must be a map or JSON string for complex key store")
		}
	} else {
		if _, ok := key.(string); !ok {
			key = fmt.Sprintf("%v", key)
		}
	}

	var store jsondb.StoreAccessor

	// Check cache first
	cacheKey := fmt.Sprintf("store_%s", storeName)
	if p.Variables != nil {
		if s, ok := p.Variables[cacheKey].(jsondb.StoreAccessor); ok {
			store = s
		}
	}

	if store == nil {
		store, err = jsondb.OpenStore(ctx, db.Config(), storeName, tx)
		if err != nil {
			if localTx {
				tx.Rollback(ctx)
			}
			return "", fmt.Errorf("failed to open store '%s': %w", storeName, err)
		}
		// Cache it only if we are in a long-running transaction (not local/auto-commit)
		if !localTx {
			if p.Variables == nil {
				p.Variables = make(map[string]any)
			}
			p.Variables[cacheKey] = store
		}
	}

	var found bool
	found, err = store.Remove(ctx, key)
	if err != nil {
		if localTx {
			tx.Rollback(ctx)
		}
		return "", fmt.Errorf("failed to delete item '%v': %w", key, err)
	}

	if !found {
		if localTx {
			tx.Rollback(ctx)
		}
		return wrapNativeTerminalToolResult(ctx,
			fmt.Sprintf("Item '%v' not found in store '%s'", key, storeName),
			"terminal_error",
			"Stop retrying this exact delete until the key or target store changes.",
		), nil
	}

	if localTx {
		if err := tx.Commit(ctx); err != nil {
			return "", fmt.Errorf("failed to commit delete transaction: %w", err)
		}
	}

	return fmt.Sprintf("Item '%v' deleted from store '%s'", key, storeName), nil
}

func (a *CopilotAgent) toolManageTransaction(ctx context.Context, args map[string]any) (string, error) {
	// Stub Mode Check
	if a.Config.StubMode {
		fmt.Printf("DEBUG: toolManageTransaction called in STUB MODE with:\n%+v\n", args)
		return "Transaction managed successfully (STUBBED).", nil
	}

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

	action, _ := args["action"].(string)
	if action == "" {
		return "", fmt.Errorf("action is required")
	}

	switch action {
	case "begin":
		// Check if transaction exists for this database
		if p.Transactions != nil {
			if _, ok := p.Transactions[dbName]; ok {
				return fmt.Sprintf("Transaction already active for database '%s'", dbName), nil
			}
		}
		// Legacy check
		if p.Transaction != nil && (dbName == "" || dbName == p.CurrentDB) {
			p.ExplicitTransaction = true
			return "Transaction already active", nil
		}

		if db == nil {
			return "", fmt.Errorf("no database selected")
		}
		tx, err := db.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			return "", fmt.Errorf("failed to begin transaction: %w", err)
		}

		if p.Transactions == nil {
			p.Transactions = make(map[string]any)
		}
		p.Transactions[dbName] = tx

		// Update legacy field if this is the current DB
		if dbName == p.CurrentDB {
			p.Transaction = tx
		}

		p.ExplicitTransaction = true
		return "Transaction started", nil

	case "commit":
		var tx sop.Transaction
		// Find transaction
		if p.Transactions != nil {
			if tAny, ok := p.Transactions[dbName]; ok {
				tx, _ = tAny.(sop.Transaction)
			}
		}
		if tx == nil && p.Transaction != nil && (dbName == "" || dbName == p.CurrentDB) {
			tx, _ = p.Transaction.(sop.Transaction)
		}

		if tx == nil {
			return fmt.Sprintf("No active transaction to commit for database '%s'", dbName), nil
		}

		commitErr := tx.Commit(ctx)

		// Cleanup
		if p.Transactions != nil {
			delete(p.Transactions, dbName)
		}
		if dbName == p.CurrentDB {
			p.Transaction = nil
		}
		p.ExplicitTransaction = false
		p.Variables = nil // Clear cache

		// // Auto-restart logic (preserve existing behavior)
		// if db != nil {
		// 	newTx, beginErr := db.BeginTransaction(ctx, sop.ForWriting)
		// 	if beginErr != nil {
		// 		if commitErr != nil {
		// 			return "", fmt.Errorf("commit failed: %v. AND failed to auto-start new one: %v", commitErr, beginErr)
		// 		}
		// 		return "", fmt.Errorf("transaction committed, but failed to auto-start new one: %w", beginErr)
		// 	}

		// 	if p.Transactions == nil {
		// 		p.Transactions = make(map[string]any)
		// 	}
		// 	p.Transactions[dbName] = newTx
		// 	if dbName == p.CurrentDB {
		// 		p.Transaction = newTx
		// 	}

		// 	if commitErr != nil {
		// 		return fmt.Sprintf("New transaction started, but previous commit failed: %v", commitErr), commitErr
		// 	}
		// 	return "Transaction committed (and new one started)", nil
		// }

		if commitErr != nil {
			return "", fmt.Errorf("commit failed: %w", commitErr)
		}
		return "Transaction committed", nil

	case "rollback":
		var tx sop.Transaction
		// Find transaction
		if p.Transactions != nil {
			if tAny, ok := p.Transactions[dbName]; ok {
				tx, _ = tAny.(sop.Transaction)
			}
		}
		if tx == nil && p.Transaction != nil && (dbName == "" || dbName == p.CurrentDB) {
			tx, _ = p.Transaction.(sop.Transaction)
		}

		if tx == nil {
			return fmt.Sprintf("No active transaction to rollback for database '%s'", dbName), nil
		}

		if err := tx.Rollback(ctx); err != nil {
			return "", fmt.Errorf("rollback failed: %w", err)
		}

		// Cleanup
		if p.Transactions != nil {
			delete(p.Transactions, dbName)
		}
		if dbName == p.CurrentDB {
			p.Transaction = nil
		}
		p.ExplicitTransaction = false
		p.Variables = nil

		// // Auto-restart logic
		// if db != nil {
		// 	newTx, err := db.BeginTransaction(ctx, sop.ForWriting)
		// 	if err != nil {
		// 		return "", fmt.Errorf("transaction rolled back, but failed to auto-start new one: %w", err)
		// 	}
		// 	if p.Transactions == nil {
		// 		p.Transactions = make(map[string]any)
		// 	}
		// 	p.Transactions[dbName] = newTx
		// 	if dbName == p.CurrentDB {
		// 		p.Transaction = newTx
		// 	}
		// 	return "Transaction rolled back (and new one started)", nil
		// }

		return "Transaction rolled back", nil

	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func (a *CopilotAgent) toolEnrichSpace(ctx context.Context, args map[string]any) (string, error) {
	kbName, _ := args["kb_name"].(string)
	if kbName == "" {
		return "", fmt.Errorf("kb_name is required")
	}

	p := ai.GetSessionPayload(ctx)
	if p == nil {
		return "", fmt.Errorf("no session payload found")
	}

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
		return "", fmt.Errorf("database not found: %s", dbName)
	}

	if a.service != nil {
		err := a.service.enrichSingleKB(ctx, db, kbName)
		if err != nil {
			return "", fmt.Errorf("failed to enrich KB %s: %v", kbName, err)
		}
	}

	return fmt.Sprintf("Enrichment cycle triggered successfully for Knowledge Base '%s'.", kbName), nil
}

func (a *CopilotAgent) toolMintToSpace(ctx context.Context, args map[string]any) (string, error) {
	p := ai.GetSessionPayload(ctx)
	if p == nil {
		return "", fmt.Errorf("session payload is missing")
	}

	kbName := resolveSpaceKBName(args, p)
	if kbName == "" {
		return "", fmt.Errorf("argument 'kb_name' is missing or not a string")
	}

	content, ok := args["content"].(string)
	if !ok || content == "" {
		return "", fmt.Errorf("argument 'content' is missing or not a string")
	}

	category, _ := args["category"].(string)

	var targetDB *database.Database
	if strings.HasPrefix(kbName, "ltm_") {
		if a.systemDB == nil {
			return "", fmt.Errorf("systemDB is not initialized")
		}
		targetDB = a.systemDB
	} else {
		if opts, ok := a.databases[p.CurrentDB]; ok {
			targetDB = database.NewDatabase(opts)
		} else {
			return "", fmt.Errorf("active database '%s' not found", p.CurrentDB)
		}
	}

	tx, err := targetDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return "", fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := targetDB.OpenKnowledgeBase(ctx, kbName, tx, a.brain, embedder, false)
	if err != nil {
		return "", fmt.Errorf("failed to open knowledge base '%s': %w", kbName, err)
	}

	err = kb.IngestThought(ctx, content, category, "", nil, map[string]any{
		"content": content,
		"source":  "minted_by_copilot",
		"ts":      time.Now().UnixMilli(),
	})
	if err != nil {
		return "", fmt.Errorf("failed to ingest thought to knowledge base: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("failed to commit transaction: %w", err)
	}
	emitSpaceMutationEvent(ctx, "mint", p.CurrentDB, kbName)

	if a.service != nil {
		// Optional: Trigger background enrichment for the created thought
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			_ = a.service.enrichSingleKB(bgCtx, targetDB, kbName)
		}()
	}

	return fmt.Sprintf("Successfully minted content to Knowledge Base '%s'.\n[[REFRESH_SPACES:%s]]\n[[REFRESH_SPACE_VIEW:%s]]", kbName, kbName, kbName), nil
}
