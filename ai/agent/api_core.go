package agent

import (
	"context"
	"fmt"
)

// Add inserts a single item into a store (first-class API)
func (a *CopilotAgent) Add(ctx context.Context, args AddArgs) (string, error) {
	// Validate required fields
	if args.Store == "" {
		return "", fmt.Errorf("store is required")
	}
	if args.Key == nil {
		return "", fmt.Errorf("key is required")
	}
	if args.Value == nil {
		return "", fmt.Errorf("value is required")
	}

	// Convert to map and delegate to existing implementation
	argsMap := map[string]any{
		"database": args.Database,
		"store":    args.Store,
		"key":      args.Key,
		"value":    args.Value,
	}
	return a.toolAdd(ctx, argsMap)
}

// Update updates a single item in a store (first-class API)
func (a *CopilotAgent) Update(ctx context.Context, args UpdateArgs) (string, error) {
	if args.Store == "" {
		return "", fmt.Errorf("store is required")
	}
	if args.Key == nil {
		return "", fmt.Errorf("key is required")
	}
	if args.Value == nil {
		return "", fmt.Errorf("value is required")
	}

	argsMap := map[string]any{
		"database": args.Database,
		"store":    args.Store,
		"key":      args.Key,
		"value":    args.Value,
	}
	return a.toolUpdate(ctx, argsMap)
}

// Delete deletes a single item from a store (first-class API)
func (a *CopilotAgent) Delete(ctx context.Context, args DeleteArgs) (string, error) {
	if args.Store == "" {
		return "", fmt.Errorf("store is required")
	}
	if args.Key == nil {
		return "", fmt.Errorf("key is required")
	}

	argsMap := map[string]any{
		"database": args.Database,
		"store":    args.Store,
		"key":      args.Key,
	}
	return a.toolDelete(ctx, argsMap)
}

// Select retrieves data from a store (first-class API)
func (a *CopilotAgent) Select(ctx context.Context, args SelectArgs) (string, error) {
	if args.Store == "" {
		return "", fmt.Errorf("store is required")
	}

	argsMap := map[string]any{
		"database":      args.Database,
		"store":         args.Store,
		"key":           args.Key,
		"key_match":     args.KeyMatch,
		"value":         args.Value,
		"filter":        args.Filter,
		"fields":        args.Fields,
		"limit":         args.Limit,
		"order_by":      args.OrderBy,
		"direction":     args.Direction,
		"action":        args.Action,
		"update_values": args.UpdateValues,
	}
	return a.toolSelect(ctx, argsMap)
}

// ExecuteScript executes a multi-step script (first-class API)
func (a *CopilotAgent) ExecuteScript(ctx context.Context, args ExecuteScriptArgs) (string, error) {
	if len(args.Script) == 0 {
		return "", fmt.Errorf("script is required")
	}

	// Convert Script to []any for existing implementation
	scriptAny := make([]any, len(args.Script))
	for i, instr := range args.Script {
		scriptAny[i] = map[string]any{
			"name":       instr.Name,
			"op":         instr.Op,
			"args":       instr.Args,
			"input_var":  instr.InputVar,
			"result_var": instr.ResultVar,
		}
	}

	argsMap := map[string]any{
		"database": args.Database,
		"script":   scriptAny,
	}
	res, err := a.toolExecuteScript(ctx, argsMap)
	if err != nil {
		return "", err
	}
	return formatToolResult(ctx, res)
}

// Join performs a join operation between two stores (first-class API)
func (a *CopilotAgent) Join(ctx context.Context, args JoinArgs) (string, error) {
	if args.LeftStore == "" {
		return "", fmt.Errorf("left_store is required")
	}
	if args.RightStore == "" {
		return "", fmt.Errorf("right_store is required")
	}
	if len(args.LeftJoinFields) == 0 {
		return "", fmt.Errorf("left_join_fields is required")
	}
	if len(args.RightJoinFields) == 0 {
		return "", fmt.Errorf("right_join_fields is required")
	}

	argsMap := map[string]any{
		"database":          args.Database,
		"left_store":        args.LeftStore,
		"right_store":       args.RightStore,
		"left_join_fields":  args.LeftJoinFields,
		"right_join_fields": args.RightJoinFields,
		"join_type":         args.JoinType,
		"fields":            args.Fields,
		"limit":             args.Limit,
		"direction":         args.Direction,
		"action":            args.Action,
		"update_values":     args.UpdateValues,
	}
	res, err := a.toolJoin(ctx, argsMap)
	if err != nil {
		return "", err
	}
	return formatToolResult(ctx, res)
}
