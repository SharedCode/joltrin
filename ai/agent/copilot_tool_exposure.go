package agent

import (
	"context"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
)

var nativeSpaceToolNames = map[string]bool{
	"mint_to_space":              true,
	"delete_space":               true,
	"enrich_space":               true,
	"update_space_config":        true,
	"read_space_config":          true,
	"vectorize_space":            true,
	"vectorize_space_categories": true,
	"vectorize_space_items":      true,
	"search_space":               true,
	"list_space_categories":      true,
	"list_space_items":           true,
	"delete_space_items":         true,
	"delete_space_categories":    true,
}

var nativeStoreToolNames = map[string]bool{
	"execute_script":     true,
	"list_stores":        true,
	"begin_tx":           true,
	"commit_tx":          true,
	"rollback_tx":        true,
	"open_store":         true,
	"select":             true,
	"join":               true,
	"join_right":         true,
	"explain_join":       true,
	"add":                true,
	"update":             true,
	"delete":             true,
	"manage_transaction": true,
	"scan":               true,
	"filter":             true,
	"sort":               true,
	"project":            true,
	"limit":              true,
}

type nativeToolExposure struct {
	restrict bool
	allowed  map[string]bool
}

func activeRoutingState(ctx context.Context) *TaskContextClassification {
	if p := ai.GetSessionPayload(ctx); p != nil && p.Variables != nil {
		if rs, ok := p.Variables["RoutingState"].(*TaskContextClassification); ok {
			return rs
		}
	}
	return nil
}

func buildNativeToolExposure(routingState *TaskContextClassification) nativeToolExposure {
	if routingState == nil {
		return nativeToolExposure{}
	}
	allowedSpacesTools, allowedStoresTools := allowedNativeDomainTools(routingState)
	allowed := make(map[string]bool, len(allowedSpacesTools)+len(allowedStoresTools))
	for name := range allowedSpacesTools {
		allowed[name] = true
	}
	for name := range allowedStoresTools {
		allowed[name] = true
	}
	return nativeToolExposure{restrict: true, allowed: allowed}
}

func isNativeDomainTool(name string) bool {
	return nativeSpaceToolNames[name] || nativeStoreToolNames[name]
}

func shouldExposeNativeTool(name string, exposure nativeToolExposure) bool {
	if !exposure.restrict {
		return true
	}
	if !isNativeDomainTool(name) {
		return true
	}
	return exposure.allowed[name]
}

func (a *CopilotAgent) listRegisteredTools(exposure nativeToolExposure) []ai.ToolDefinition {
	if a.registry == nil {
		return nil
	}
	tools := make([]ai.ToolDefinition, 0)
	for _, tool := range a.registry.List() {
		// Skip hidden tools only when routing is restricted AND tool is not explicitly allowed
		if tool.Hidden && exposure.restrict && !exposure.allowed[tool.Name] {
			continue
		}
		if !shouldExposeNativeTool(tool.Name, exposure) {
			continue
		}
		tools = append(tools, ai.ToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			Schema:      tool.ArgsSchema,
		})
	}
	return tools
}

func (a *CopilotAgent) listStoredScriptTools(ctx context.Context) []ai.ToolDefinition {
	if a.systemDB == nil {
		return nil
	}
	tx, err := a.systemDB.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return nil
	}
	defer tx.Rollback(ctx)

	store, err := a.systemDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		return nil
	}

	const defaultArgsSchema = `{"type": "object", "properties": {"database": {"type": "string", "description": "Target database constraint (optional)"}}}`
	keys, _ := store.List(ctx, ai.DefaultScriptCategory)
	tools := make([]ai.ToolDefinition, 0, len(keys))
	for _, scriptName := range keys {
		var script ai.Script
		if err := store.Load(ctx, ai.DefaultScriptCategory, scriptName, &script); err != nil {
			continue
		}
		desc := script.Description
		if desc == "" {
			desc = "Executes the script '" + scriptName + "'"
		}
		tools = append(tools, ai.ToolDefinition{
			Name:        scriptName,
			Description: "Execute pre-saved user script. " + desc,
			Schema:      defaultArgsSchema,
		})
	}
	return tools
}
