package agent

import (
	"context"
	"encoding/json"
	"fmt"
	log "log/slog"
	"sort"
	"strings"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/jsondb"
)

func (e *ScriptEngine) Execute(ctx context.Context, script []ScriptInstruction) error {
	compiled, err := CompileScript(script)
	if err != nil {
		return err
	}
	return compiled(ctx, e)
}

func (e *ScriptEngine) Compile(script []ScriptInstruction) (CompiledScript, error) {
	return CompileScript(script)
}

func CompileScript(script []ScriptInstruction) (CompiledScript, error) {
	type compiledStep struct {
		Name      string
		Op        string
		Args      map[string]any
		InputVar  string
		ResultVar string
		Func      func(context.Context, *ScriptEngine) error
	}
	var steps []compiledStep

	for _, instr := range script {

		instr := instr

		argResolvers := make(map[string]func(*ScriptEngine) any)
		for k, v := range instr.Args {
			val := v
			if str, ok := val.(string); ok && strings.Contains(str, "{{") {

				argResolvers[k] = func(e *ScriptEngine) any {
					return e.resolveTemplate(str)
				}
			} else {
				argResolvers[k] = func(_ *ScriptEngine) any { return val }
			}
		}

		opFunc, err := bindOperation(instr.Op)
		if err != nil {
			return nil, fmt.Errorf("instruction '%s': %v", instr.Op, err)
		}

		stepFn := func(ctx context.Context, e *ScriptEngine) error {

			args := make(map[string]any)
			for k, resolver := range argResolvers {
				args[k] = resolver(e)
			}

			// Resolve Input Var
			var input any
			if instr.InputVar != "" {
				var ok bool
				inputVar := e.resolveVarName(instr.InputVar)
				input, ok = e.Context.Variables[inputVar]
				if !ok {
					return fmt.Errorf("input variable '%s' not found", instr.InputVar)
				}
			} else {

				input = e.LastResult
			}

			result, err := opFunc(ctx, e, args, input)
			if err != nil {
				return fmt.Errorf("operation '%s' failed: %v", instr.Op, err)
			}

			if result != nil || !preserveLastResultOnNil(instr.Op) {
				e.LastResult = result
			}

			if instr.ResultVar != "" {
				e.Context.Variables[instr.ResultVar] = result
				e.Context.LastUpdatedVar = instr.ResultVar

				if db, ok := result.(Database); ok {
					e.Context.Databases[instr.ResultVar] = db
				}
				if tx, ok := result.(sop.Transaction); ok {
					e.Context.Transactions[instr.ResultVar] = tx
				}
				if store, ok := result.(jsondb.StoreAccessor); ok {
					e.Context.Stores[instr.ResultVar] = store
				}
			}
			return nil
		}
		steps = append(steps, compiledStep{
			Name:      instr.Name,
			Op:        instr.Op,
			Args:      instr.Args,
			InputVar:  instr.InputVar,
			ResultVar: instr.ResultVar,
			Func:      stepFn,
		})
	}

	return func(ctx context.Context, e *ScriptEngine) (runErr error) {

		defer func() {
			if attachDeferredCleanupCursor(ctx, e) {
				return
			}

			for len(e.Deferred) > 0 {
				i := len(e.Deferred) - 1
				task := e.Deferred[i]
				e.Deferred = e.Deferred[:i]

				log.Debug("Executing deferred operation", "index", i)
				if err := task(ctx, e); err != nil {
					log.Error("Deferred execution failed", "error", err)
					if runErr == nil {
						runErr = fmt.Errorf("deferred operation failed: %w", err)
					}
				}
			}
		}()

		for i, step := range steps {
			if e.HasReturned {
				log.Debug("Script returned early", "step", i)
				break
			}
			log.Debug("ExecuteStep", "step", i+1, "op", step.Op)
			log.Info("Script Engine Tool Call",
				"step", i+1,
				"op", step.Op,
				"name", step.Name,
				"input_var", step.InputVar,
				"result_var", step.ResultVar,
				"arg_keys", summarizeScriptArgKeys(step.Args),
			)

			if err := step.Func(ctx, e); err != nil {
				log.Error("ExecuteStep failed", "step", i+1, "op", step.Op, "error", err)
				return err
			}

			log.Debug("ExecuteStep success", "step", i+1, "lastResultType", fmt.Sprintf("%T", e.LastResult))
		}
		return nil
	}, nil
}

func attachDeferredCleanupCursor(ctx context.Context, e *ScriptEngine) bool {
	if len(e.Deferred) == 0 {
		return false
	}

	var cursor ScriptCursor
	if sc, ok := e.ReturnValue.(ScriptCursor); ok {
		cursor = sc
	} else if sc, ok := e.LastResult.(ScriptCursor); ok {
		cursor = sc
	} else if sc, ok := e.Context.Variables["output"].(ScriptCursor); ok {
		cursor = sc
	} else if sc, ok := e.Context.Variables["final_result"].(ScriptCursor); ok {
		cursor = sc
	} else if sc, ok := e.Context.Variables["result"].(ScriptCursor); ok {
		cursor = sc
	} else if e.Context.LastUpdatedVar != "" {
		if sc, ok := e.Context.Variables[e.Context.LastUpdatedVar].(ScriptCursor); ok {
			cursor = sc
		}
	}

	if cursor == nil {
		return false
	}

	log.Debug("Transferring deferred cleanup to returned cursor")
	wrapper := &DeferredCleanupCursor{
		source:  cursor,
		cleanup: e.Deferred,
		ctx:     ctx,
		engine:  e,
	}

	if current, ok := e.ReturnValue.(ScriptCursor); ok && current == cursor {
		e.ReturnValue = wrapper
	}
	if current, ok := e.LastResult.(ScriptCursor); ok && current == cursor {
		e.LastResult = wrapper
	}
	if current, ok := e.Context.Variables["output"].(ScriptCursor); ok && current == cursor {
		e.Context.Variables["output"] = wrapper
	}
	if current, ok := e.Context.Variables["final_result"].(ScriptCursor); ok && current == cursor {
		e.Context.Variables["final_result"] = wrapper
	}
	if current, ok := e.Context.Variables["result"].(ScriptCursor); ok && current == cursor {
		e.Context.Variables["result"] = wrapper
	}
	if e.Context.LastUpdatedVar != "" {
		if current, ok := e.Context.Variables[e.Context.LastUpdatedVar].(ScriptCursor); ok && current == cursor {
			e.Context.Variables[e.Context.LastUpdatedVar] = wrapper
		}
	}

	e.Deferred = nil
	return true
}

func (e *ScriptEngine) resolveTemplate(tmpl string) any {

	trimmed := strings.TrimSuffix(strings.TrimPrefix(tmpl, "{{"), "}}")
	parts := strings.Split(trimmed, ".")

	var current any
	var ok bool

	if len(parts) > 0 {
		current, ok = e.Context.Variables[parts[0]]
		if !ok {
			log.Debug("resolveTemplate var not found", "var", parts[0])
			return tmpl
		}
	}

	for _, part := range parts[1:] {
		if currentMap, ok := current.(map[string]any); ok {
			current, ok = currentMap[part]
			if !ok {

				if valMap, isMap := currentMap["value"].(map[string]any); isMap {
					current, ok = valMap[part]
				}
			}
			if !ok {
				log.Debug("resolveTemplate field not found", "field", part, "map", currentMap)
				return nil
			}
		} else {
			log.Debug("resolveTemplate current is not a map", "current", current)
			return nil
		}
	}
	log.Debug("resolveTemplate result", "tmpl", tmpl, "current", current)
	return current
}

func bindOperation(op string) (func(context.Context, *ScriptEngine, map[string]any, any) (any, error), error) {
	switch op {
	case "open_db":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return e.OpenDB(args)
		}, nil
	case "begin_tx":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return e.BeginTx(ctx, args)
		}, nil
	case "commit_tx":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return nil, e.CommitTx(ctx, args)
		}, nil
	case "rollback_tx":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return nil, e.RollbackTx(ctx, args)
		}, nil
	case "open_store":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return e.OpenStore(ctx, args)
		}, nil
	case "scan", "select":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, input any) (any, error) {
			return e.Scan(ctx, args, input)
		}, nil
	case "filter":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, input any) (any, error) {
			return e.Filter(ctx, input, args)
		}, nil
	case "sort":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, input any) (any, error) {
			return e.Sort(ctx, input, args)
		}, nil
	case "project":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, input any) (any, error) {
			return e.Project(ctx, input, args)
		}, nil
	case "limit":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, input any) (any, error) {
			return e.Limit(ctx, input, args)
		}, nil
	case "join":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, input any) (any, error) {
			return e.Join(ctx, input, args)
		}, nil
	case "join_right":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, input any) (any, error) {
			return e.JoinRight(ctx, input, args)
		}, nil
	case "update":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, input any) (any, error) {
			if l, ok := input.([]map[string]any); ok {
				return e.Update(ctx, l, args)
			}
			return nil, fmt.Errorf("input must be list of maps")
		}, nil
	case "delete":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, input any) (any, error) {
			if l, ok := input.([]map[string]any); ok {
				return e.Delete(ctx, l, args)
			}
			return nil, fmt.Errorf("input must be list of maps")
		}, nil
	case "inspect":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return e.Inspect(ctx, args)
		}, nil
	case "defer":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			// Extract command to defer
			// We support simplified syntax where args IS the command, or args contains "command"
			var cmdToDefer map[string]any

			if cmd, ok := args["command"].(map[string]any); ok {
				cmdToDefer = cmd
			} else if cmd, ok := args["execute"].(map[string]any); ok {
				cmdToDefer = cmd
			} else {

				if _, ok := args["op"].(string); ok {
					cmdToDefer = args
				} else {
					return nil, fmt.Errorf("defer requires 'op' or nested 'command'")
				}
			}

			script := []ScriptInstruction{
				{
					Op:   cmdToDefer["op"].(string),
					Args: cmdToDefer,
				},
			}

			compiledCmd, err := CompileScript(script)
			if err != nil {
				return nil, fmt.Errorf("failed to compile deferred command: %v", err)
			}

			e.Deferred = append(e.Deferred, func(c context.Context, se *ScriptEngine) error {

				wasReturned := se.HasReturned
				se.HasReturned = false
				defer func() { se.HasReturned = wasReturned }()

				return compiledCmd(c, se)
			})

			return nil, nil
		}, nil
	case "assign":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, input any) (any, error) {
			if val, ok := args["value"]; ok {
				return val, nil
			}
			return input, nil
		}, nil
	case "if":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return nil, e.If(ctx, args)
		}, nil
	case "loop":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return nil, e.Loop(ctx, args)
		}, nil
	case "call_script", "script":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return e.CallScript(ctx, args)
		}, nil
	case "call_function":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return e.CallFunction(ctx, args)
		}, nil
	case "list_new":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return make([]any, 0), nil
		}, nil
	case "list_append":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, input any) (any, error) {
			return e.ListAppend(ctx, input, args)
		}, nil
	case "map_merge":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return e.MapMerge(ctx, args)
		}, nil
	case "first":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return e.First(ctx, args)
		}, nil
	case "last":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return e.Last(ctx, args)
		}, nil
	case "next":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return e.Next(ctx, args)
		}, nil
	case "previous":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return e.Previous(ctx, args)
		}, nil
	case "find":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			res, err := e.Find(ctx, args)
			log.Debug("Find result", "result", res, "err", err)
			return res, err
		}, nil
	case "add":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return e.Add(ctx, args)
		}, nil
	case "get_current_key":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			res, err := e.GetCurrentKey(ctx, args)
			log.Debug("GetCurrentKey result", "result", res, "err", err)
			return res, err
		}, nil
	case "get_current_value":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, _ any) (any, error) {
			return e.GetCurrentValue(ctx, args)
		}, nil
	case "return":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, input any) (any, error) {
			if val, ok := args["value"]; ok {

				// Helper to resolve a value (string or structure)
				var resolve func(v any) any
				resolve = func(v any) any {
					if strVal, ok := v.(string); ok && e.Context != nil && e.Context.Variables != nil {
						varName := e.resolveVarName(strVal)
						if res, found := e.Context.Variables[varName]; found {
							return res
						}
					}
					if sliceVal, ok := v.([]any); ok {
						newSlice := make([]any, len(sliceVal))
						for i, item := range sliceVal {
							newSlice[i] = resolve(item)
						}
						return newSlice
					}

					return v
				}

				res := resolve(val)
				e.ReturnValue = res
				e.HasReturned = true
				return res, nil
			}

			if input != nil {
				e.ReturnValue = input
				e.HasReturned = true
				return input, nil
			}

			return nil, nil
		}, nil
	case "search_space", "upsert_space_items", "delete_space_categories", "delete_space_items", "vectorize_space", "vectorize_space_categories", "vectorize_space_items", "list_space_categories", "list_space_items":
		return func(ctx context.Context, e *ScriptEngine, args map[string]any, input any) (any, error) {
			return e.ExecuteKBManagement(ctx, op, args, input)
		}, nil
	default:
		return nil, fmt.Errorf("unknown operation: %s", op)
	}
}

func summarizeScriptArgKeys(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func serializeResult(ctx context.Context, val any) (string, error) {

	if _, ok := val.(Database); ok {
		return "Database object (success)", nil
	}
	if _, ok := val.(sop.Transaction); ok {
		return "Transaction object (success)", nil
	}
	if _, ok := val.(jsondb.StoreAccessor); ok {
		return "Store object (success)", nil
	}

	alreadyCollapsed := false

	if cursor, ok := val.(ScriptCursor); ok {
		defer cursor.Close()

		// Helper to force field order if provider available
		var orderedFields []string
		if provider, ok := cursor.(OrderedFieldsProvider); ok {
			orderedFields = provider.GetOrderedFields()
		}
		reducer := newResultReducer()

		// Setup streaming for the final cursor result.
		// Prefer the live tool-streamer path (ai.CtxKeyResultStreamer) so rows are visible
		// as they are generated; fall back to the legacy JSON streamer path for script playback.
		resultStreamer, closeableStreamer := resolveResultStreamer(ctx)
		if resultStreamer != nil {
			resultStreamer.BeginArray()
			if len(orderedFields) > 0 {
				resultStreamer.SetMetadata(map[string]any{"columns": orderedFields})
			}
		}

		for {
			itemObj, ok, err := cursor.Next(ctx)
			if err != nil {
				return "", fmt.Errorf("failed to read cursor: %v", err)
			}
			if !ok {
				break
			}
			if itemObj == nil {
				continue
			}

			var itemMap map[string]any

			isOrdered := false
			if om, ok := itemObj.(*OrderedMap); ok {
				if !om.isImplicit {
					isOrdered = true
				} else {
					itemMap = om.m
				}
			} else if om, ok := itemObj.(OrderedMap); ok {
				if !om.isImplicit {
					isOrdered = true
				} else {
					itemMap = om.m
				}
			}

			if !isOrdered {
				if m, ok := itemObj.(map[string]any); ok {
					itemMap = m
				}
			} else {

				itemMap = nil
			}

			if itemMap != nil {

				itemObj = collapseUniqueKeys(itemMap)
			}

			var item any = itemObj

			if itemMap != nil && len(orderedFields) > 0 {
				item = filterFields(itemMap, orderedFields)
			}

			if resultStreamer != nil {
				resultStreamer.WriteItem(item)
			}

			reducer.Append(item)
		}

		if resultStreamer != nil {
			resultStreamer.EndArray()
		}
		if closeableStreamer != nil {
			closeableStreamer.Close()
		}

		val = reducer.Write()
		alreadyCollapsed = true
	}

	if !alreadyCollapsed {
		// Friendly Output: Collapse unique keys for cleaner JSON
		resultStreamer, closeableStreamer := resolveResultStreamer(ctx)
		if resultStreamer != nil {
			resultStreamer.BeginArray()
		}

		var list []any
		if l, ok := val.([]any); ok {
			list = l
		} else if lMap, ok := val.([]map[string]any); ok {
			list = make([]any, len(lMap))
			for i, v := range lMap {
				list[i] = v
			}
			val = list
		}

		if list != nil {
			reducer := newResultReducer()
			for _, itemObj := range list {
				if itemObj == nil {
					continue
				}

				var itemMap map[string]any

				isOrdered := false
				if om, ok := itemObj.(*OrderedMap); ok {
					if !om.isImplicit {
						isOrdered = true
					} else {
						itemMap = om.m
					}
				} else if om, ok := itemObj.(OrderedMap); ok {
					if !om.isImplicit {
						isOrdered = true
					} else {
						itemMap = om.m
					}
				}

				if !isOrdered {
					if itemMap == nil {
						if m, ok := itemObj.(map[string]any); ok {
							itemMap = m
						}
					}
				}

				finalItem := itemObj
				if itemMap != nil {

					collapsed := collapseUniqueKeys(itemMap)

					finalItem = collapsed
				}
				if resultStreamer != nil {
					resultStreamer.WriteItem(finalItem)
				}
				reducer.Append(finalItem)
			}
			if resultStreamer != nil {
				resultStreamer.EndArray()
			}
			if closeableStreamer != nil {
				closeableStreamer.Close()
			}
			val = reducer.Write()
		}
	}

	bytes, err := json.MarshalIndent(val, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to serialize result: %v", err)
	}
	resStr := string(bytes)

	return resStr, nil
}

func resolveResultStreamer(ctx context.Context) (ai.ResultStreamer, interface{ Close() }) {
	if streamer, ok := ctx.Value(ai.CtxKeyEventStreamer).(func(string, any)); ok && streamer != nil {
		eventStreamer := NewEventResultStreamer(streamer)
		return eventStreamer, eventStreamer
	}

	if streamer, ok := ctx.Value(ai.CtxKeyResultStreamer).(ai.ResultStreamer); ok && streamer != nil {
		return streamer, nil
	}
	if streamer, ok := ctx.Value(CtxKeyJSONStreamer).(*JSONStreamer); ok && streamer != nil {
		streamer.SetSuppressStepStart(true)
		stepStreamer := streamer.StartStreamingStep("result_stream", "final_result", "", 0)
		return stepStreamer, stepStreamer
	}
	return nil, nil
}

func (e *ScriptEngine) CallScript(ctx context.Context, args map[string]any) (any, error) {
	name, _ := args["name"].(string)
	params, _ := args["params"].(map[string]any)

	if e.FunctionHandler != nil {
		return e.FunctionHandler(ctx, name, params)
	}
	return nil, fmt.Errorf("function handler not available")
}

// Deprecated: Use Compile/Execute instead
func (e *ScriptEngine) Dispatch(ctx context.Context, instr ScriptInstruction) error {
	var input any
	if instr.InputVar != "" {
		var ok bool
		input, ok = e.Context.Variables[instr.InputVar]
		if !ok {
			return fmt.Errorf("input variable '%s' not found", instr.InputVar)
		}
	}

	var result any
	var err error

	switch instr.Op {
	case "open_db":
		result, err = e.OpenDB(instr.Args)
	case "begin_tx":
		result, err = e.BeginTx(ctx, instr.Args)
	case "commit_tx":
		err = e.CommitTx(ctx, instr.Args)
	case "rollback_tx":
		err = e.RollbackTx(ctx, instr.Args)
	case "open_store":
		result, err = e.OpenStore(ctx, instr.Args)
	case "scan", "select":
		result, err = e.Scan(ctx, instr.Args, input)
	case "filter":
		result, err = e.Filter(ctx, input, instr.Args)
	case "sort":
		result, err = e.Sort(ctx, input, instr.Args)
	case "project":
		result, err = e.Project(ctx, input, instr.Args)
	case "limit":
		result, err = e.Limit(ctx, input, instr.Args)
	case "join":
		result, err = e.Join(ctx, input, instr.Args)
	case "update":
		result, err = e.Update(ctx, input, instr.Args)
	case "delete":
		result, err = e.Delete(ctx, input, instr.Args)
	case "inspect":
		result, err = e.Inspect(ctx, instr.Args)
	case "assign":

		if val, ok := instr.Args["value"]; ok {
			result = val
		} else {
			result = input
		}
	case "if":
		err = e.If(ctx, instr.Args)
	case "loop":
		err = e.Loop(ctx, instr.Args)
	case "call_script", "script":
		result, err = e.CallScript(ctx, instr.Args)

	case "list_new":
		result = make([]any, 0)
	case "list_append":
		result, err = e.ListAppend(ctx, input, instr.Args)
	case "map_merge":
		result, err = e.MapMerge(ctx, instr.Args)

	case "first":
		result, err = e.First(ctx, instr.Args)
	case "last":
		result, err = e.Last(ctx, instr.Args)
	case "next":
		result, err = e.Next(ctx, instr.Args)
	case "previous":
		result, err = e.Previous(ctx, instr.Args)
	case "find":
		result, err = e.Find(ctx, instr.Args)
		log.Debug("Find result", "result", result, "err", err)
	case "add":
		result, err = e.Add(ctx, instr.Args)
	case "get_current_key":
		result, err = e.GetCurrentKey(ctx, instr.Args)
		log.Debug("GetCurrentKey result", "result", result, "err", err)
	case "get_current_value":
		result, err = e.GetCurrentValue(ctx, instr.Args)
	default:
		return fmt.Errorf("unknown operation: %s", instr.Op)
	}

	if err != nil {
		return err
	}

	if instr.ResultVar != "" {
		e.Context.Variables[instr.ResultVar] = result

		if db, ok := result.(Database); ok {
			e.Context.Databases[instr.ResultVar] = db
		}
		if tx, ok := result.(sop.Transaction); ok {
			e.Context.Transactions[instr.ResultVar] = tx
			// DEBUG: Transaction now stored in TWO places (Variables + Transactions)
			log.Info("ExecuteStep: Stored transaction in multiple bags",
				"var_name", instr.ResultVar,
				"tx_id", tx.GetID(),
				"also_in_variables", true,
				"also_in_transactions", true,
				"also_in_txtodb", e.Context.TxToDB[tx] != nil)
		}
		if store, ok := result.(jsondb.StoreAccessor); ok {
			e.Context.Stores[instr.ResultVar] = store
		}
	}

	return nil
}
