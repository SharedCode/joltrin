package agent

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
)

const (
	beginTxArgsSchema       = `{"type":"object","description":"Begin an explicit transaction for chained native store tool calls.","properties":{"database":{"type":"string","description":"Optional database override. Defaults to the active session database."},"mode":{"type":"string","enum":["read","write"],"description":"Transaction mode for the native pipeline."},"result_var":{"type":"string","description":"Optional variable name to store the transaction handle. Defaults to tx."}},"required":["mode"]}`
	commitTxArgsSchema      = `{"type":"object","description":"Commit the explicit native transaction. The current pipeline result is preserved for follow-up serialization.","properties":{"transaction":{"type":"string","description":"Optional transaction handle variable name. Defaults to the active explicit transaction."}}}`
	rollbackTxArgsSchema    = `{"type":"object","description":"Rollback the explicit native transaction. The current pipeline result is preserved for follow-up serialization.","properties":{"transaction":{"type":"string","description":"Optional transaction handle variable name. Defaults to the active explicit transaction."}}}`
	openStoreArgsSchema     = `{"type":"object","description":"Open a store inside the explicit native transaction and save the handle for later scan or join_right steps.","properties":{"database":{"type":"string","description":"Optional database override. Defaults to the active session database."},"transaction":{"type":"string","description":"Optional transaction handle variable name. Defaults to the active explicit transaction."},"name":{"type":"string","description":"Concrete store name to open."},"result_var":{"type":"string","description":"Optional variable name to store the store handle. Defaults to the store name."}},"required":["name"]}`
	scanAtomicArgsSchema    = `{"type":"object","description":"Scan a store handle or grounded store name and emit a stream/list for the next native pipeline step.","properties":{"store":{"type":"string","description":"Store handle from open_store or a grounded store name."},"stream":{"type":"boolean","description":"When true, keep the result in stream form for later filter, join_right, or project steps."},"direction":{"type":"string","enum":["asc","desc","ascending","descending"],"description":"Optional scan direction when supported."},"result_var":{"type":"string","description":"Optional variable name to store the scan result."}},"required":["store"]}`
	filterAtomicArgsSchema  = `{"type":"object","description":"Filter the current implicit pipeline result using a grounded condition object.","properties":{"condition":{"type":"object","description":"Concrete predicate object. After joins, keep dotted store-qualified field names such as orders.total_amount."},"result_var":{"type":"string","description":"Optional variable name to store the filtered result."}},"required":["condition"]}`
	projectAtomicArgsSchema = `{"type":"object","description":"Project fields from the current implicit pipeline result.","properties":{"fields":{"type":"array","description":"Fields to keep. After joins, use dotted store-qualified field paths unless you intentionally want aliases.","items":{"type":"string"}},"result_var":{"type":"string","description":"Optional variable name to store the projected result."}},"required":["fields"]}`
	joinRightArgsSchema     = `{"type":"object","description":"Join the current implicit pipeline result to a target store using a grounded on mapping.","properties":{"store":{"type":"string","description":"Store handle from open_store or a grounded target store name."},"on":{"type":"object","description":"Concrete mapping from current-result fields to target-store fields."},"stream":{"type":"boolean","description":"When true, keep the joined result in stream form for later pipeline steps."},"result_var":{"type":"string","description":"Optional variable name to store the joined result."}},"required":["store","on"]}`
	limitAtomicArgsSchema   = `{"type":"object","description":"Keep only the first N records from the current implicit pipeline result.","properties":{"limit":{"type":"integer","description":"Maximum number of records to keep."},"result_var":{"type":"string","description":"Optional variable name to store the limited result."}},"required":["limit"]}`
	sortAtomicArgsSchema    = `{"type":"object","description":"Sort the current implicit pipeline result.","properties":{"field":{"type":"string","description":"Field to sort by. After joins, use dotted store-qualified field names when needed."},"direction":{"type":"string","enum":["asc","desc","ascending","descending"],"description":"Sort direction."},"result_var":{"type":"string","description":"Optional variable name to store the sorted result."}},"required":["field"]}`
)

var atomicToolSpecs = map[string]struct {
	short       string
	instruction string
	schema      string
}{
	"begin_tx": {
		short:       "Begins an explicit native pipeline transaction.",
		instruction: "Begin an explicit native transaction for chained store tool calls. Prefer this direct pipeline path for clear multi-step reads instead of packing the same sequence into execute_script. Save the handle with result_var, usually tx, and reuse it from later open_store calls.",
		schema:      beginTxArgsSchema,
	},
	"commit_tx": {
		short:       "Commits the explicit native pipeline transaction.",
		instruction: "Commit the active explicit native transaction after the chained store calls are complete. This closes the durability boundary without discarding the current pipeline result.",
		schema:      commitTxArgsSchema,
	},
	"rollback_tx": {
		short:       "Rolls back the explicit native pipeline transaction.",
		instruction: "Rollback the active explicit native transaction when the chained store calls should not persist. This closes the durability boundary without discarding the current pipeline result.",
		schema:      rollbackTxArgsSchema,
	},
	"open_store": {
		short:       "Opens a store handle for native pipeline steps.",
		instruction: "Open a concrete store inside the explicit transaction and save the handle with result_var. Later scan or join_right calls can reference that handle by name.",
		schema:      openStoreArgsSchema,
	},
	"scan": {
		short:       "Scans a store into the native pipeline.",
		instruction: "Scan a store handle or grounded store name and emit a stream/list for the next native pipeline step. Prefer stream=true when a later filter, join_right, or project step will consume the result.",
		schema:      scanAtomicArgsSchema,
	},
	"filter": {
		short:       "Filters the current native pipeline result.",
		instruction: "Filter the current implicit pipeline result using a grounded condition object. After joins, keep dotted store-qualified field names such as orders.total_amount.",
		schema:      filterAtomicArgsSchema,
	},
	"sort": {
		short:       "Sorts the current native pipeline result.",
		instruction: "Sort the current implicit pipeline result by one grounded field. After joins, use dotted store-qualified field names when needed.",
		schema:      sortAtomicArgsSchema,
	},
	"project": {
		short:       "Projects fields from the current native pipeline result.",
		instruction: "Project fields from the current implicit pipeline result. After joins, keep dotted store-qualified field paths unless you intentionally rename them.",
		schema:      projectAtomicArgsSchema,
	},
	"limit": {
		short:       "Limits the current native pipeline result.",
		instruction: "Keep only the first N records from the current implicit pipeline result.",
		schema:      limitAtomicArgsSchema,
	},
	"join_right": {
		short:       "Joins the current native pipeline result to another store.",
		instruction: "Join the current implicit pipeline result to a target store using a grounded on mapping. Prefer join_right for chained native multi-store reads once list_stores has confirmed the relation fields.",
		schema:      joinRightArgsSchema,
	},
}

// toolAtomicOpWrapper creates a tool function that executes a single atomic operation
// using the shared session ScriptEngine.
func (a *CopilotAgent) toolAtomicOpWrapper(op string) func(context.Context, map[string]any) (any, error) {
	return func(ctx context.Context, args map[string]any) (any, error) {
		// Ensure session context exists (Scoped to Request via generic helper)
		scriptCtx := getOrInitScriptContext(ctx)

		// Initialize Engine reusing the session context
		resolver := func(name string) (Database, error) {
			if name == "" || name == "@db" || name == "current" {
				if p := ai.GetSessionPayload(ctx); p != nil && p.CurrentDB != "" {
					name = p.CurrentDB
				}
			}
			return a.resolveDatabase(name)
		}

		engine := NewScriptEngine(scriptCtx, resolver)
		engine.StoreOpener = a.StoreOpener
		// Function handler not strictly needed for single atomic ops unless they call scripts
		engine.FunctionHandler = func(c context.Context, name string, args map[string]any) (any, error) {
			return a.opCallScript(c, engine.Context, map[string]any{"name": name, "params": args})
		}

		// Get the operation function
		opFunc, err := bindOperation(op)
		if err != nil {
			return "", fmt.Errorf("unknown operation '%s': %v", op, err)
		}

		// Use LastResult from agent state as input (Implicit Piping)
		input := getLastAtomicResult(ctx)
		engine.LastResult = input

		// Execute
		result, err := opFunc(ctx, engine, args, input)
		if err != nil {
			return "", fmt.Errorf("operation '%s' failed: %v", op, err)
		}
		if result == nil && preserveLastResultOnNil(op) {
			result = input
		}

		// Update LastResult in agent state
		setLastAtomicResult(ctx, result)
		engine.LastResult = result

		// Handle result_var if present in args
		resVar, _ := args["result_var"].(string)

		// Convention: Set Default Variable Names if not specified
		if resVar == "" {
			if op == "begin_tx" {
				resVar = "tx"
			} else if op == "open_store" {
				if name, ok := args["name"].(string); ok && name != "" {
					resVar = name
				}
			} else if op == "open_db" {
				if name, ok := args["name"].(string); ok && name != "" {
					resVar = name
				}
			}
		}

		if resVar != "" {
			if engine.Context.Variables == nil {
				engine.Context.Variables = make(map[string]any)
			}
			engine.Context.Variables[resVar] = result

			// Register specific types
			if db, ok := result.(Database); ok {
				if engine.Context.Databases == nil {
					engine.Context.Databases = make(map[string]Database)
				}
				engine.Context.Databases[resVar] = db
			}
			if tx, ok := result.(sop.Transaction); ok {
				if engine.Context.Transactions == nil {
					engine.Context.Transactions = make(map[string]sop.Transaction)
				}
				engine.Context.Transactions[resVar] = tx
			}
			if store, ok := result.(jsondb.StoreAccessor); ok {
				if engine.Context.Stores == nil {
					engine.Context.Stores = make(map[string]jsondb.StoreAccessor)
				}
				engine.Context.Stores[resVar] = store
			}
		}

		return result, nil
	}
}

// Helper to register all atomic tools
func (a *CopilotAgent) registerAtomicTools() {
	for opName, spec := range atomicToolSpecs {
		a.registry.RegisterWithUI(opName, spec.short, spec.instruction, spec.schema, a.toolAtomicOpWrapper(opName))
	}
}
