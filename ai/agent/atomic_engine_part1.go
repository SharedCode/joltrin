package agent

import (
	"context"
	"encoding/json"
	"fmt"
	log "log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
)

// Join Strategy Constants
const (
	StrategyUnset     = 0
	StrategyIndexSeek = 1
	StrategyInMemory  = 2
	StrategyFullScan  = 3
)

// Database interface for script execution
type Database interface {
	BeginTransaction(ctx context.Context, mode sop.TransactionMode, maxTime ...time.Duration) (sop.Transaction, error)
	Config() sop.DatabaseOptions
}

func (e *ScriptEngine) getStore(name string) (jsondb.StoreAccessor, bool) {
	if s, ok := e.Context.Stores[name]; ok {
		return s, true
	}
	for k, s := range e.Context.Stores {
		if strings.EqualFold(k, name) {
			return s, true
		}
		if s != nil && strings.EqualFold(strings.TrimSpace(s.GetStoreInfo().Name), strings.TrimSpace(name)) {
			return s, true
		}
	}
	return nil, false
}

func (e *ScriptEngine) getDatabase(name string) (Database, bool) {
	if d, ok := e.Context.Databases[name]; ok {
		return d, true
	}
	for k, d := range e.Context.Databases {
		if strings.EqualFold(k, name) {
			return d, true
		}
	}
	return nil, false
}

// ScriptInstruction represents a single operation in the script.
type ScriptInstruction struct {
	Name      string         `json:"name"`       // User-defined name for the step
	Op        string         `json:"op"`         // Operation name
	Args      map[string]any `json:"args"`       // Arguments
	InputVar  string         `json:"input_var"`  // Variable to use as input (optional)
	ResultVar string         `json:"result_var"` // Variable to store result (optional)
}

// ScriptContext holds the state of the script execution.
type ScriptContext struct {
	Variables      map[string]any
	Transactions   map[string]sop.Transaction
	TxToDB         map[sop.Transaction]Database // Mapping from Transaction to its Database
	Stores         map[string]jsondb.StoreAccessor
	Databases      map[string]Database
	LastUpdatedVar string // helper to track the prioritization of variable draining
}

func NewScriptContext() *ScriptContext {
	return &ScriptContext{
		Variables:    make(map[string]any),
		Transactions: make(map[string]sop.Transaction),
		TxToDB:       make(map[sop.Transaction]Database),
		Stores:       make(map[string]jsondb.StoreAccessor),
		Databases:    make(map[string]Database),
	}
}

// ScriptEngine executes scripts.
type ScriptEngine struct {
	Context         *ScriptContext
	ResolveDatabase func(name string) (Database, error)
	FunctionHandler func(ctx context.Context, name string, args map[string]any) (any, error)
	LastResult      any
	ReturnValue     any
	HasReturned     bool
	StoreOpener     func(ctx context.Context, dbOpts sop.DatabaseOptions, storeName string, tx sop.Transaction) (jsondb.StoreAccessor, error)
	Deferred        []func(context.Context, *ScriptEngine) error
}

func NewScriptEngine(ctx *ScriptContext, dbResolver func(string) (Database, error)) *ScriptEngine {
	if ctx == nil {
		ctx = NewScriptContext()
	}
	return &ScriptEngine{
		Context:         ctx,
		ResolveDatabase: dbResolver,
	}
}

func (a *CopilotAgent) resolveDatabase(name string) (Database, error) {
	if name == "system" && a.systemDB != nil {
		return a.systemDB, nil
	}
	if opts, ok := a.databases[name]; ok {
		return database.NewDatabase(opts), nil
	}
	return nil, fmt.Errorf("database '%s' not found", name)
}

// toolExecuteScript executes a sequence of instructions.
// Args:
// - script: []ScriptInstruction (JSON array)
func (a *CopilotAgent) toolExecuteScript(ctx context.Context, args map[string]any) (any, error) {
	log.Debug("toolExecuteScript: Called", "args", args)
	scriptRaw, ok := args["script"]
	if !ok {
		return "", fmt.Errorf("script argument is required")
	}

	// Parse Script first to ensure it's valid and normalized.
	// Some provider-owned loops (notably Anthropic tool execution) can emit
	// either a JSON stringified script array or a single object-shaped step,
	// so normalize both forms before compilation.
	var rawSteps []map[string]any
	var script []ScriptInstruction

	rawSteps, err := normalizeScriptSteps(scriptRaw)
	if err != nil {
		return "", err
	}

	currentQuery := currentQueryForScriptGrounding(ai.GetSessionPayload(ctx))
	normalizationState := a.newScriptCompatibilityNormalizerState(ctx, rawSteps)

	for _, step := range rawSteps {
		normalizeScriptStepForCompatibilityWithQueryAndState(step, currentQuery, normalizationState)

		if _, hasOp := step["op"]; !hasOp {
			if cmd, ok := step["command"].(string); ok && cmd != "" {
				step["op"] = cmd
			}
		}

		// Auto-correct flat structures: move extra fields into "args"
		argsObj, hasArgs := step["args"].(map[string]any)
		if !hasArgs {
			argsObj = make(map[string]any)
			step["args"] = argsObj
		}

		for k, v := range step {
			if k != "op" && k != "command" && k != "name" && k != "input_var" && k != "result_var" && k != "args" {
				argsObj[k] = v
			}
		}

		if normalizationState != nil {
			normalizationState.observeStep(step)
		}

	}

	bytes, _ := json.Marshal(rawSteps)
	if err := json.Unmarshal(bytes, &script); err != nil {
		return "", fmt.Errorf("failed to re-parse normalized script: %v", err)
	}

	if err := validateExecuteScriptPlaceholders(ctx, script); err != nil {
		return "", err
	}

	script = SanitizeScript(script)

	// Grammar validation: Check structural and logical issues AFTER sanitization.
	// Reuse the same execution-path validation helpers as create_script.
	if err := ValidateScriptGrammar(script); err != nil {
		return "", fmt.Errorf("script grammar validation failed: %w", err)
	}
	writeNormalizedScriptArgs(args, script)
	if scriptJSON, err := json.MarshalIndent(script, "", "  "); err == nil {
		log.Info("toolExecuteScript: Running sanitized script",
			"steps", len(script),
			"script", string(scriptJSON),
		)
	} else {
		log.Warn("toolExecuteScript: Failed to marshal sanitized script for logging", "error", err)
	}

	if a.Config.StubMode {

		bytes, err := json.MarshalIndent(script, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal script for display: %v", err)
		}

		log.Debug("toolExecuteScript called in STUB MODE", "script", string(bytes))
		return fmt.Sprintf("Script prepared successfully (STUBBED). execution skipped.\nGenerated Script:\n```json\n%s\n```", string(bytes)), nil
	}

	// Initialize Engine
	var scriptCtx *ScriptContext

	scriptCtx = getOrInitScriptContext(ctx)

	if scriptCtx.Variables != nil {
		delete(scriptCtx.Variables, "output")
		delete(scriptCtx.Variables, "final_result")
		delete(scriptCtx.Variables, "result")
	}

	scriptCtx.LastUpdatedVar = ""

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
	engine.FunctionHandler = func(c context.Context, name string, args map[string]any) (any, error) {

		return a.opCallScript(c, engine.Context, map[string]any{"name": name, "params": args})
	}

	compiled, err := engine.Compile(script)
	if err != nil {
		return "", fmt.Errorf("compilation failed: %v", err)
	}
	if err := compiled(ctx, engine); err != nil {
		return "", err
	}

	log.Debug("toolExecuteScript: Checking for return value candidates...")
	choice := selectExecuteScriptReturn(engine, scriptCtx, script)
	log.Debug("toolExecuteScript: Selected return value",
		"source", choice.Source,
		"detail", choice.Detail,
		"type", fmt.Sprintf("%T", choice.Value),
		"last_updated_var", scriptCtx.LastUpdatedVar,
	)
	if choice.SuccessMessage != "" {
		return choice.SuccessMessage, nil
	}

	if streamed, err := streamReturnCursorResult(ctx, choice.Value); err != nil {
		return "", err
	} else if streamed {
		return "", nil
	}

	return choice.Value, nil
}

func streamReturnCursorResult(ctx context.Context, val any) (bool, error) {
	cursor, ok := val.(ScriptCursor)
	if !ok {
		return false, nil
	}

	resultStreamer, closeableStreamer := resolveResultStreamer(ctx)
	if resultStreamer == nil {
		return false, nil
	}

	resultStreamer.BeginArray()
	defer resultStreamer.EndArray()
	if closeableStreamer != nil {
		defer closeableStreamer.Close()
	}
	defer cursor.Close()

	for {
		item, ok, err := cursor.Next(ctx)
		if err != nil {
			return true, fmt.Errorf("failed to stream cursor result: %w", err)
		}
		if !ok {
			break
		}
		resultStreamer.WriteItem(item)
	}

	return true, nil
}

func normalizeScriptSteps(raw any) ([]map[string]any, error) {
	switch typed := raw.(type) {
	case string:
		var decoded any
		if err := json.Unmarshal([]byte(typed), &decoded); err != nil {
			return nil, fmt.Errorf("failed to parse script JSON: %v", err)
		}
		return normalizeScriptSteps(decoded)

	case []any:
		flat := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if nested, ok := unwrapScriptEnvelope(item); ok {
				recursed, err := normalizeScriptSteps(nested)
				if err != nil {
					return nil, err
				}
				flat = append(flat, recursed...)
				continue
			}

			bytes, err := json.Marshal(item)
			if err != nil {
				return nil, fmt.Errorf("failed to encode script item: %v", err)
			}
			var step map[string]any
			if err := json.Unmarshal(bytes, &step); err != nil {
				return nil, fmt.Errorf("failed to parse script item: %v", err)
			}
			flat = append(flat, step)
		}
		return flat, nil

	case []map[string]any:
		return typed, nil

	case map[string]any:
		if nested, ok := unwrapScriptEnvelope(typed); ok {
			return normalizeScriptSteps(nested)
		}
		return []map[string]any{typed}, nil

	default:
		return nil, fmt.Errorf("script must be a JSON string, array, or single object")
	}
}

func unwrapScriptEnvelope(raw any) (any, bool) {
	step, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}

	if _, hasOp := step["op"]; hasOp {
		return nil, false
	}
	if _, hasCmd := step["command"]; hasCmd {
		return nil, false
	}

	if args, ok := step["args"].(map[string]any); ok {
		if nested, hasSteps := args["steps"]; hasSteps {
			return nested, true
		}
		if nested, hasScript := args["script"]; hasScript {
			return nested, true
		}
	}

	if nested, hasSteps := step["steps"]; hasSteps {
		return nested, true
	}
	if nested, hasScript := step["script"]; hasScript {
		return nested, true
	}

	return nil, false
}

func writeNormalizedScriptArgs(args map[string]any, script []ScriptInstruction) {
	if args == nil {
		return
	}
	bytes, err := json.Marshal(script)
	if err != nil {
		return
	}
	var normalized []any
	if err := json.Unmarshal(bytes, &normalized); err != nil {
		return
	}
	args["script"] = normalized
}

type executeScriptReturnChoice struct {
	Source         string
	Detail         string
	Value          any
	SuccessMessage string
}

func selectExecuteScriptReturn(engine *ScriptEngine, scriptCtx *ScriptContext, script []ScriptInstruction) executeScriptReturnChoice {
	if val, ok := scriptCtx.Variables["output"]; ok && val != nil {
		return executeScriptReturnChoice{Source: "output", Value: val}
	}

	if val, ok := scriptCtx.Variables["final_result"]; ok && val != nil {
		return executeScriptReturnChoice{Source: "final_result", Value: val}
	}

	if val, ok := scriptCtx.Variables["result"]; ok && val != nil {
		return executeScriptReturnChoice{Source: "result", Value: val}
	}

	if len(script) > 0 {
		lastInstr := script[len(script)-1]

		if lastInstr.Op == "return" {
			if engine.ReturnValue != nil {
				return executeScriptReturnChoice{Source: "return_value", Value: engine.ReturnValue}
			}
			if engine.LastResult == nil {
				return executeScriptReturnChoice{Source: "success_message", Detail: "explicit_return_nil", SuccessMessage: "Script executed successfully."}
			}
			return executeScriptReturnChoice{Source: "return_last_result", Value: engine.LastResult}
		}

		if lastInstr.ResultVar != "" {
			if val, ok := scriptCtx.Variables[lastInstr.ResultVar]; ok && val != nil {
				return executeScriptReturnChoice{Source: "last_instruction_result_var", Detail: lastInstr.ResultVar, Value: val}
			}
		}
	}

	if scriptCtx.LastUpdatedVar != "" {
		if val, ok := scriptCtx.Variables[scriptCtx.LastUpdatedVar]; ok && val != nil {
			if !isInternalScriptHandle(val) {
				return executeScriptReturnChoice{Source: "last_updated_var", Detail: scriptCtx.LastUpdatedVar, Value: val}
			}
		}
	}

	if engine.LastResult != nil {
		return executeScriptReturnChoice{Source: "last_result", Value: engine.LastResult}
	}

	return executeScriptReturnChoice{Source: "success_message", Detail: "no_result", SuccessMessage: "Script executed successfully."}
}

func validateExecuteScriptPlaceholders(ctx context.Context, script []ScriptInstruction) error {
	var currentQuery string
	currentQuery = currentQueryForScriptGrounding(ai.GetSessionPayload(ctx))
	resultOrigins := make(map[string]string, len(script))
	validationErrors := make([]*executeScriptValidationError, 0)

	for _, instr := range script {
		if err := validateExecuteScriptInputShape(instr, resultOrigins); err != nil {
			validationErrors = append(validationErrors, err)
		}
		if (instr.Op == "join" || instr.Op == "join_right") && instr.Args != nil {
			if onMap, ok := instr.Args["on"].(map[string]any); ok {
				for leftField, rightField := range onMap {
					if isInvalidPlaceholderFieldName(leftField) {
						validationErrors = append(validationErrors, newExecuteScriptValidationError(
							"invalid_join_on_field_placeholder",
							fmt.Sprintf("invalid join.on field %q: expected a real left-hand field path such as %q", leftField, "users.key"),
							fmt.Sprintf(`{"op":"%s","args":{"relation":"users_orders","target":"orders_store"}}`, instr.Op),
						))
						continue
					}

					if placeholder, ok := rightField.(bool); ok {
						validationErrors = append(validationErrors, newExecuteScriptValidationError(
							"invalid_join_on_placeholder",
							fmt.Sprintf("invalid type for join.on[%q]: got boolean placeholder %t; expected a field path string such as %q", leftField, placeholder, "key"),
							fmt.Sprintf(`{"op":"%s","args":{"relation":"users_orders","target":"orders_store"}}`, instr.Op),
						))
						continue
					}
					if rightField == nil {
						validationErrors = append(validationErrors, newExecuteScriptValidationError(
							"invalid_join_on_placeholder",
							fmt.Sprintf("invalid type for join.on[%q]: got null placeholder; expected a field path string such as %q", leftField, "key"),
							fmt.Sprintf(`{"op":"%s","args":{"relation":"users_orders","target":"orders_store"}}`, instr.Op),
						))
						continue
					}
					if rightFieldStr, ok := rightField.(string); ok && isInvalidPlaceholderFieldName(rightFieldStr) {
						validationErrors = append(validationErrors, newExecuteScriptValidationError(
							"invalid_join_on_placeholder",
							fmt.Sprintf("invalid join.on[%q] field path %q: expected a real right-hand field path such as %q", leftField, rightFieldStr, "key"),
							fmt.Sprintf(`{"op":"%s","args":{"relation":"users_orders","target":"orders_store"}}`, instr.Op),
						))
					}
				}
			}
		}

		if instr.Op == "filter" && instr.Args != nil {
			if condition, ok := instr.Args["condition"].(map[string]any); ok {
				validationErrors = append(validationErrors, validateFilterConditionPlaceholders(condition, currentQuery)...)
			}
		}

		if instr.ResultVar != "" {
			resultOrigins[instr.ResultVar] = instr.Op
		}
	}

	return collapseExecuteScriptValidationErrors(validationErrors)
}

func validateExecuteScriptInputShape(instr ScriptInstruction, resultOrigins map[string]string) *executeScriptValidationError {
	if instr.Op != "filter" || strings.TrimSpace(instr.InputVar) == "" {
		return nil
	}
	originOp := strings.TrimSpace(resultOrigins[instr.InputVar])
	if originOp != "open_store" {
		return nil
	}
	storeName := "<store_var>"
	if instr.Args != nil {
		if named, ok := instr.Args["store"].(string); ok && strings.TrimSpace(named) != "" {
			storeName = strings.TrimSpace(named)
		}
	}
	return newExecuteScriptValidationError(
		"invalid_filter_input_shape",
		fmt.Sprintf("filter input_var %q resolves to an open_store handle; expected a scanned cursor or list before filtering", instr.InputVar),
		fmt.Sprintf(`{"op":"scan","args":{"store":%q},"result_var":"%s_cursor"}`, storeName, instr.InputVar),
	)
}

func validateFilterConditionPlaceholders(condition map[string]any, currentQuery string) []*executeScriptValidationError {
	validationErrors := make([]*executeScriptValidationError, 0)
	for field, raw := range condition {
		if strings.HasPrefix(field, "$") {
			switch nested := raw.(type) {
			case []any:
				for _, item := range nested {
					if nestedMap, ok := item.(map[string]any); ok {
						validationErrors = append(validationErrors, validateFilterConditionPlaceholders(nestedMap, currentQuery)...)
					}
				}
			case map[string]any:
				validationErrors = append(validationErrors, validateFilterConditionPlaceholders(nested, currentQuery)...)
			}
			continue
		}

		if isInvalidPlaceholderFieldName(field) {
			queryHint := ""
			if currentQuery != "" {
				queryHint = fmt.Sprintf(" for current query %q", currentQuery)
			}
			validationErrors = append(validationErrors, newExecuteScriptValidationError(
				"invalid_filter_field_placeholder",
				fmt.Sprintf("invalid filter condition field %q: expected a real field path such as %q%s", field, "first_name", queryHint),
				fmt.Sprintf(`{"op":"filter","args":{"condition":{"%s":{"$eq":"<value>"}}}}`, "first_name"),
			))
			continue
		}

		if raw == nil {
			queryHint := ""
			if currentQuery != "" {
				queryHint = fmt.Sprintf(" for current query %q", currentQuery)
			}
			validationErrors = append(validationErrors, newExecuteScriptValidationError(
				"invalid_filter_placeholder",
				fmt.Sprintf("invalid type for filter condition field %q: got null placeholder; expected an operator/value predicate such as {\"$eq\": value}%s", field, queryHint),
				fmt.Sprintf(`{"op":"filter","args":{"condition":{"%s":{"$eq":"<value>"}}}}`, field),
			))
			continue
		}

		if placeholder, ok := raw.(bool); ok {
			if isLikelyBooleanFieldName(field) {
				continue
			}
			queryHint := ""
			if currentQuery != "" {
				queryHint = fmt.Sprintf(" for current query %q", currentQuery)
			}
			validationErrors = append(validationErrors, newExecuteScriptValidationError(
				"invalid_filter_placeholder",
				fmt.Sprintf("invalid type for filter condition field %q: got boolean placeholder %t; expected an operator/value predicate such as {\"$eq\": value}%s", field, placeholder, queryHint),
				fmt.Sprintf(`{"op":"filter","args":{"condition":{"%s":{"$eq":"<value>"}}}}`, field),
			))
			continue
		}

		// CEL remains supported when condition itself is a string expression.
		// This only rejects malformed map-based AST placeholders like {"field":"$eq"}.
		if placeholder, ok := raw.(string); ok {
			trimmed := strings.TrimSpace(strings.Trim(placeholder, "\"'"))
			if strings.HasPrefix(trimmed, "$") && !strings.Contains(trimmed, ":") {
				queryHint := ""
				if currentQuery != "" {
					queryHint = fmt.Sprintf(" for current query %q", currentQuery)
				}
				validationErrors = append(validationErrors, newExecuteScriptValidationError(
					"invalid_filter_operator_placeholder",
					fmt.Sprintf("invalid filter condition field %q: got operator placeholder %q without a comparison value; expected a predicate object such as {\"%s\": value}%s", field, trimmed, trimmed, queryHint),
					fmt.Sprintf(`{"op":"filter","args":{"condition":{"%s":{"%s":"<value>"}}}}`, field, trimmed),
				))
				continue
			}
		}

		if nested, ok := raw.(map[string]any); ok {
			validationErrors = append(validationErrors, validateFilterConditionPlaceholders(nested, currentQuery)...)
			validationErrors = append(validationErrors, validateFilterConditionGrounding(field, nested, currentQuery)...)
			continue
		}

		validationErrors = append(validationErrors, validateScalarFilterConditionGrounding(field, raw, currentQuery)...)
	}

	return validationErrors
}

type executeScriptValidationError struct {
	Category string
	Message  string
	Example  string
}

type executeScriptValidationErrors struct {
	Errors []*executeScriptValidationError
}

func (e *executeScriptValidationErrors) Error() string {
	if e == nil || len(e.Errors) == 0 {
		return ""
	}
	parts := make([]string, 0, len(e.Errors))
	for _, item := range e.Errors {
		if item == nil {
			continue
		}
		parts = append(parts, item.Error())
	}
	return strings.Join(parts, "\n")
}

func (e *executeScriptValidationErrors) Unwrap() []error {
	if e == nil {
		return nil
	}
	errs := make([]error, 0, len(e.Errors))
	for _, item := range e.Errors {
		if item != nil {
			errs = append(errs, item)
		}
	}
	return errs
}

func (e *executeScriptValidationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Example == "" {
		return fmt.Sprintf("execute_script validation error [%s]: %s", e.Category, e.Message)
	}
	return fmt.Sprintf("execute_script validation error [%s]: %s Example fix: %s", e.Category, e.Message, e.Example)
}

func newExecuteScriptValidationError(category, message, example string) *executeScriptValidationError {
	return &executeScriptValidationError{
		Category: category,
		Message:  message,
		Example:  example,
	}
}

func collapseExecuteScriptValidationErrors(validationErrors []*executeScriptValidationError) error {
	filtered := make([]*executeScriptValidationError, 0, len(validationErrors))
	seen := make(map[string]struct{}, len(validationErrors))
	for _, item := range validationErrors {
		if item == nil {
			continue
		}
		key := item.Category + "\n" + item.Message + "\n" + item.Example
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, item)
	}
	switch len(filtered) {
	case 0:
		return nil
	case 1:
		return filtered[0]
	default:
		return &executeScriptValidationErrors{Errors: filtered}
	}
}

func validateFilterConditionGrounding(field string, raw map[string]any, currentQuery string) []*executeScriptValidationError {
	expected, ok := inferPredicateFromCurrentQuery(field, currentQuery)
	if !ok || filterPredicateMatchesExpected(raw, expected) {
		return nil
	}
	if len(raw) == 0 {
		return []*executeScriptValidationError{newExecuteScriptValidationError(
			"invalid_filter_query_mismatch",
			fmt.Sprintf("filter condition field %q does not preserve the user-requested predicate from the current query %q; expected %v", field, currentQuery, expected),
			fmt.Sprintf(`{"op":"filter","args":{"condition":{"%s":%s}}}`, field, mustJSON(expected)),
		)}
	}
	for op, value := range expected {
		actual, ok := raw[op]
		if ok && valuesEquivalentForFilterGrounding(actual, value) {
			return nil
		}
	}
	return []*executeScriptValidationError{newExecuteScriptValidationError(
		"invalid_filter_query_mismatch",
		fmt.Sprintf("filter condition field %q does not preserve the user-requested predicate from the current query %q; expected %v, got %v", field, currentQuery, expected, raw),
		fmt.Sprintf(`{"op":"filter","args":{"condition":{"%s":%s}}}`, field, mustJSON(expected)),
	)}
}

func validateScalarFilterConditionGrounding(field string, raw any, currentQuery string) []*executeScriptValidationError {
	expected, ok := inferPredicateFromCurrentQuery(field, currentQuery)
	if !ok {
		return nil
	}
	if eq, hasEq := expected["$eq"]; hasEq && valuesEquivalentForFilterGrounding(eq, raw) {
		return nil
	}
	return []*executeScriptValidationError{newExecuteScriptValidationError(
		"invalid_filter_query_mismatch",
		fmt.Sprintf("filter condition field %q uses scalar value %v but the current query %q implies predicate %v", field, raw, currentQuery, expected),
		fmt.Sprintf(`{"op":"filter","args":{"condition":{"%s":%s}}}`, field, mustJSON(expected)),
	)}
}

func mustJSON(value any) string {
	bytes, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(bytes)
}

func filterPredicateMatchesExpected(raw map[string]any, expected map[string]any) bool {
	if len(raw) != len(expected) {
		return false
	}
	for op, expectedValue := range expected {
		actualValue, ok := raw[op]
		if !ok || !valuesEquivalentForFilterGrounding(actualValue, expectedValue) {
			return false
		}
	}
	return true
}

func valuesEquivalentForFilterGrounding(actual, expected any) bool {
	if reflect.DeepEqual(actual, expected) {
		return true
	}
	actualString, actualIsString := actual.(string)
	expectedString, expectedIsString := expected.(string)
	if actualIsString && expectedIsString {
		return strings.EqualFold(strings.TrimSpace(actualString), strings.TrimSpace(expectedString))
	}
	actualNumber, actualIsNumber := coerceFilterGroundingNumber(actual)
	expectedNumber, expectedIsNumber := coerceFilterGroundingNumber(expected)
	if actualIsNumber && expectedIsNumber {
		return actualNumber == expectedNumber
	}
	return false
}

func coerceFilterGroundingNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int8:
		return float64(number), true
	case int16:
		return float64(number), true
	case int32:
		return float64(number), true
	case int64:
		return float64(number), true
	case uint:
		return float64(number), true
	case uint8:
		return float64(number), true
	case uint16:
		return float64(number), true
	case uint32:
		return float64(number), true
	case uint64:
		return float64(number), true
	case float32:
		return float64(number), true
	case float64:
		return number, true
	default:
		return 0, false
	}
}

func isLikelyBooleanFieldName(field string) bool {
	field = strings.ToLower(strings.TrimSpace(field))
	field = strings.TrimPrefix(field, "!")
	if idx := strings.LastIndex(field, "."); idx >= 0 {
		field = field[idx+1:]
	}

	if strings.HasPrefix(field, "is_") || strings.HasPrefix(field, "has_") || strings.HasPrefix(field, "can_") || strings.HasPrefix(field, "should_") {
		return true
	}

	switch field {
	case "active", "enabled", "disabled", "deleted", "archived", "visible", "public", "private", "verified", "locked", "done", "complete", "completed":
		return true
	default:
		return false
	}
}

func isInvalidPlaceholderFieldName(field string) bool {
	field = strings.ToLower(strings.TrimSpace(strings.Trim(field, "\"'")))
	if field == "" {
		return true
	}

	switch field {
	case "null", "nil", "<nil>", "undefined", "none":
		return true
	default:
		return false
	}
}

// CompiledScript is a function that executes the compiled script against an engine.
type CompiledScript func(ctx context.Context, e *ScriptEngine) error

type CachedScript struct {
	Script CompiledScript
	Hash   string
}
