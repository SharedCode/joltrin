package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/v5"
)

const listScriptsArgsSchema = `{"type":"object","properties":{"category":{"type":"string","description":"Optional script category to list. Defaults to the standard script category."}}}`

const createScriptArgsSchema = `{"type":"object","properties":{"name":{"type":"string","description":"Name of the script to create."},"description":{"type":"string","description":"Optional human-readable description of the script."},"database":{"type":"string","description":"Set the target database for this script so the saved script carries an explicit runtime database context."},"script":{"type":"array","description":"Preferred array of reusable script steps.","items":{"type":"object"}},"steps":{"type":"array","description":"Legacy alias of script.","items":{"type":"object"}}},"required":["name"]}`

const saveScriptArgsSchema = `{"type":"object","properties":{"name":{"type":"string","description":"Name of the script to save or replace."},"description":{"type":"string","description":"Optional human-readable description of the script."},"database":{"type":"string","description":"Set the target database for this script so the saved script carries an explicit runtime database context."},"script":{"type":"array","description":"Preferred array of reusable script steps.","items":{"type":"object"}},"steps":{"type":"array","description":"Legacy alias of script.","items":{"type":"object"}}},"required":["name"]}`

const getScriptDetailsArgsSchema = `{"type":"object","properties":{"name":{"type":"string","description":"Name of the script to inspect."},"category":{"type":"string","description":"Optional script category. Defaults to the standard script category."}},"required":["name"]}`

const saveLastStepArgsSchema = `{"type":"object","properties":{"script":{"type":"string","description":"Target script that should receive the last executed tool call as a step."},"index":{"type":"integer","description":"Optional insertion index."},"position":{"type":"string","enum":["before","after","append"],"description":"Where to place the captured step relative to index."},"description":{"type":"string","description":"Optional description for the inserted step."},"name":{"type":"string","description":"Optional step name for the inserted step."}},"required":["script"]}`

const refactorLastInteractionArgsSchema = `{"type":"object","properties":{"mode":{"type":"string","enum":["script","block"],"description":"Whether to refactor the last interaction into a new script or a block step."},"name":{"type":"string","description":"Optional name for the resulting script when mode is script."}},"required":["mode"]}`

const deleteStepArgsSchema = `{"type":"object","properties":{"script":{"type":"string","description":"Target script name."},"index":{"type":"integer","description":"Zero-based step index to delete."}},"required":["script","index"]}`

const reorderStepsArgsSchema = `{"type":"object","properties":{"script":{"type":"string","description":"Target script name."},"from_index":{"type":"integer","description":"Current zero-based step index."},"to_index":{"type":"integer","description":"Destination zero-based step index."}},"required":["script","from_index","to_index"]}`

const saveStepArgsSchema = `{"type":"object","properties":{"script":{"type":"string","description":"Target script name."},"type":{"type":"string","description":"Optional step type. When omitted, the handler may infer it from other provided fields."},"name":{"type":"string","description":"Optional step name."},"description":{"type":"string","description":"Optional step description."},"prompt":{"type":"string","description":"Prompt for ask-style steps."},"message":{"type":"string","description":"Message for informational steps."},"script_name":{"type":"string","description":"Referenced script name for call_script steps."},"command":{"type":"string","description":"Command/tool name for command steps."},"condition":{"type":"string","description":"Condition expression for if steps."},"iterator":{"type":"string","description":"Iterator variable for loop steps."},"list":{"type":"string","description":"List expression for loop steps."},"output_variable":{"type":"string","description":"Optional output variable name."},"variable":{"type":"string","description":"Variable name for set-style steps."},"value":{"type":"string","description":"String value payload for set-style steps."},"database":{"type":"string","description":"Optional database override for the step."},"source":{"type":"string","description":"Source field for fetch-style steps."},"resource":{"type":"string","description":"Resource field for fetch-style steps."},"filter":{"type":"string","description":"Filter string for fetch-style steps."},"script_args":{"type":"object","description":"String-coercible argument map for nested script calls."},"is_async":{"type":"boolean","description":"Whether the step runs asynchronously."},"continue_on_error":{"type":"boolean","description":"Whether execution should continue after this step errors."},"args":{"type":"object","description":"Raw argument payload for command steps."}},"required":["script"]}`

const insertStepArgsSchema = `{"type":"object","properties":{"script":{"type":"string","description":"Target script name."},"index":{"type":"integer","description":"Zero-based insertion index."},"type":{"type":"string","description":"Step type to insert."},"name":{"type":"string","description":"Optional step name."},"description":{"type":"string","description":"Optional step description."},"prompt":{"type":"string","description":"Prompt for ask-style steps."},"message":{"type":"string","description":"Message for informational steps."},"script_name":{"type":"string","description":"Referenced script name for call_script steps."},"command":{"type":"string","description":"Command/tool name for command steps."},"condition":{"type":"string","description":"Condition expression for if steps."},"iterator":{"type":"string","description":"Iterator variable for loop steps."},"list":{"type":"string","description":"List expression for loop steps."},"output_variable":{"type":"string","description":"Optional output variable name."},"variable":{"type":"string","description":"Variable name for set-style steps."},"value":{"type":"string","description":"String value payload for set-style steps."},"database":{"type":"string","description":"Optional database override for the step."},"source":{"type":"string","description":"Source field for fetch-style steps."},"resource":{"type":"string","description":"Resource field for fetch-style steps."},"filter":{"type":"string","description":"Filter string for fetch-style steps."},"script_args":{"type":"object","description":"String-coercible argument map for nested script calls."},"is_async":{"type":"boolean","description":"Whether the step runs asynchronously."},"continue_on_error":{"type":"boolean","description":"Whether execution should continue after this step errors."},"args":{"type":"object","description":"Raw argument payload for command steps."}},"required":["script","index","type"]}`

const updateStepArgsSchema = `{"type":"object","properties":{"script":{"type":"string","description":"Target script name."},"index":{"type":"integer","description":"Zero-based step index to update."},"type":{"type":"string","description":"Optional replacement step type."},"name":{"type":"string","description":"Optional replacement step name."},"description":{"type":"string","description":"Optional replacement step description."},"prompt":{"type":"string","description":"Prompt for ask-style steps."},"message":{"type":"string","description":"Message for informational steps."},"script_name":{"type":"string","description":"Referenced script name for call_script steps."},"command":{"type":"string","description":"Command/tool name for command steps."},"condition":{"type":"string","description":"Condition expression for if steps."},"iterator":{"type":"string","description":"Iterator variable for loop steps."},"list":{"type":"string","description":"List expression for loop steps."},"output_variable":{"type":"string","description":"Optional output variable name."},"variable":{"type":"string","description":"Variable name for set-style steps."},"value":{"type":"string","description":"String value payload for set-style steps."},"database":{"type":"string","description":"Optional database override for the step."},"source":{"type":"string","description":"Source field for fetch-style steps."},"resource":{"type":"string","description":"Resource field for fetch-style steps."},"filter":{"type":"string","description":"Filter string for fetch-style steps."},"script_args":{"type":"object","description":"String-coercible argument map for nested script calls."},"is_async":{"type":"boolean","description":"Whether the step runs asynchronously."},"continue_on_error":{"type":"boolean","description":"Whether execution should continue after this step errors."},"args":{"type":"object","description":"Raw argument payload for command steps."}},"required":["script","index"]}`

func (a *CopilotAgent) toolListScripts(ctx context.Context, args map[string]any) (string, error) {
	p := ai.GetSessionPayload(ctx)
	if p == nil {
		return "", fmt.Errorf("no session payload found")
	}

	// Look for "system" database
	db := a.systemDB
	if db == nil {
		if opts, ok := a.databases["system"]; ok {
			db = database.NewDatabase(opts)
		}
	}

	if db == nil {
		return "", fmt.Errorf("system database not found")
	}

	tx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return "", fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	store, err := db.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		return "", fmt.Errorf("failed to open scripts store: %w", err)
	}

	// Extract optional category
	category, _ := args["category"].(string)
	if category == "" {
		category = ai.DefaultScriptCategory
	}

	names, err := store.List(ctx, category)
	if err != nil {
		return "", fmt.Errorf("failed to list scripts in category '%s': %w", category, err)
	}

	if len(names) == 0 {
		return "No scripts found.", nil
	}
	sort.Strings(names)
	return fmt.Sprintf("Scripts: %s", strings.Join(names, ", ")), nil
}

func (a *CopilotAgent) toolGetScriptDetails(ctx context.Context, args map[string]any) (string, error) {
	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("script name required")
	}

	p := ai.GetSessionPayload(ctx)
	if p == nil {
		return "", fmt.Errorf("no session payload found")
	}

	// Look for "system" database
	db := a.systemDB
	if db == nil {
		if opts, ok := a.databases["system"]; ok {
			db = database.NewDatabase(opts)
		}
	}

	if db == nil {
		return "", fmt.Errorf("system database not found")
	}

	tx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return "", fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	store, err := db.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		return "", fmt.Errorf("failed to open scripts store: %w", err)
	}

	category, _ := args["category"].(string)
	if category == "" {
		category = ai.DefaultScriptCategory
	}

	var script ai.Script
	if err := store.Load(ctx, category, name, &script); err != nil {
		return "", fmt.Errorf("failed to load script '%s' from category '%s': %w", name, category, err)
	}

	// Format details
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Script: %s\n", name))
	if script.Description != "" {
		sb.WriteString(fmt.Sprintf("Description: %s\n", script.Description))
	}
	if script.Database != "" {
		sb.WriteString(fmt.Sprintf("Database: %s\n", script.Database))
	}
	if script.Portable {
		sb.WriteString("Portable: true\n")
	}
	sb.WriteString("Steps:\n")
	printSteps(&sb, script.Steps, 0)

	return sb.String(), nil
}

func (a *CopilotAgent) toolCreateScript(ctx context.Context, args map[string]any) (string, error) {
	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("script name is required")
	}

	description, _ := args["description"].(string)
	steps, err := mapScriptStepsArg(args, true)
	if err != nil {
		return "", fmt.Errorf("invalid steps: %v", err)
	}

	// Apply automatic refinements to make implicit conventions explicit
	steps = RefineScriptSteps(steps)

	var currentDB string
	if p := ai.GetSessionPayload(ctx); p != nil {
		currentDB = p.CurrentDB
	}
	script := ai.Script{
		Description: description,
		Database:    currentDB,
		Steps:       steps,
	}

	// Validate the script steps.
	if err := ai.ValidateScript(steps); err != nil {
		return "", fmt.Errorf("script validation failed: %w", err)
	}

	// Also perform grammar validation using the same execution-path helpers.
	if err := ValidateScriptSteps(steps); err != nil {
		return "", fmt.Errorf("script grammar validation failed: %w", err)
	}

	// Persist the new script
	// Using a dedicated Create or overwriting 'updatedScript'?
	// updateScript function handles open/save/commit. We can reuse it or create a new one.
	// But updateScript expects the script to exist or creates it?
	// The implementation of updateScript reads it first.
	// Let's implement a create logic using store operations directly OR modify updateScript to handle creation.
	// Actually, looking at toolListScripts, it opens the "scripts" store.

	// Let's implement independent logic for clarity or reuse updateScript if it supports "not found".
	// The current updateScript function:
	/*
			func (a *CopilotAgent) updateScript(ctx context.Context, name string, updateFunc func(*ai.Script) error) error {
			    ...
				var script ai.Script
				if err := store.Get(ctx, ai.DefaultScriptCategory, name, &script); err != nil {
					return err
				}
		        ...
		    }
	*/
	// It assumes existence. So we need a dedicated create flow.

	db := a.systemDB
	if db == nil {
		if opts, ok := a.databases["system"]; ok {
			db = database.NewDatabase(opts)
		}
	}
	if db == nil {
		return "", fmt.Errorf("system database not found")
	}

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return "", fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	store, err := db.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		return "", fmt.Errorf("failed to open scripts store: %w", err)
	}

	// Check if already exists
	// store.Get returns error if not found? Or false?
	// The modelStore interface usually returns error if not found in SOP (if KeyNotFound).
	// We can try to load into a dummy var.
	var dummy ai.Script
	err = store.Load(ctx, ai.DefaultScriptCategory, name, &dummy)
	if err == nil {
		return "", fmt.Errorf("script '%s' already exists", name)
	}
	// Warning: We are assuming error means Not Found. SOP might have specific error types.
	// Ideally we check error type. But for now, assuming any error is good enough to proceed to Create.

	if err := store.Save(ctx, ai.DefaultScriptCategory, name, &script); err != nil {
		return "", fmt.Errorf("failed to save script: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("failed to commit transaction: %w", err)
	}

	return fmt.Sprintf("Script '%s' created successfully.", name), nil
}

func (a *CopilotAgent) toolSaveScript(ctx context.Context, args map[string]any) (string, error) {
	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("script name is required")
	}

	description, _ := args["description"].(string)
	steps, err := mapScriptStepsArg(args, false)
	if err != nil {
		return "", fmt.Errorf("invalid steps: %v", err)
	}

	// Apply automatic refinements
	steps = RefineScriptSteps(steps)

	var currentDB string
	if p := ai.GetSessionPayload(ctx); p != nil {
		currentDB = p.CurrentDB
	}
	script := ai.Script{
		Description: description,
		Database:    currentDB,
		Steps:       steps,
	}

	db := a.systemDB
	if db == nil {
		if opts, ok := a.databases["system"]; ok {
			db = database.NewDatabase(opts)
		}
	}
	if db == nil {
		return "", fmt.Errorf("system database not found")
	}

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return "", fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	store, err := db.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		return "", fmt.Errorf("failed to open scripts store: %w", err)
	}

	if err := store.Save(ctx, ai.DefaultScriptCategory, name, &script); err != nil {
		return "", fmt.Errorf("failed to save script: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("failed to commit transaction: %w", err)
	}

	return fmt.Sprintf("Script '%s' saved successfully with %d steps.", name, len(steps)), nil
}

func mapScriptStepsArg(args map[string]any, allowEmpty bool) ([]ai.ScriptStep, error) {
	if stepsRaw, ok := args["steps"]; ok {
		stepsList, ok := stepsRaw.([]any)
		if !ok {
			return nil, fmt.Errorf("'steps' must be an array of step objects")
		}
		return mapToScriptSteps(stepsList)
	}

	if scriptRaw, ok := args["script"]; ok {
		scriptList, ok := scriptRaw.([]any)
		if !ok {
			return nil, fmt.Errorf("'script' must be an array of step objects")
		}
		return mapToScriptSteps(scriptList)
	}

	if allowEmpty {
		return nil, nil
	}

	return nil, fmt.Errorf("missing required script steps: provide 'script' or legacy alias 'steps'")
}

func printSteps(sb *strings.Builder, steps []ai.ScriptStep, indent int) {
	indentStr := strings.Repeat("  ", indent)
	for i, step := range steps {
		desc := step.Message
		if step.Type == "ask" {
			desc = step.Prompt
		} else if step.Type == "call_script" {
			desc = fmt.Sprintf("Run '%s'", step.ScriptName)
		} else if step.Type == "command" {
			argsJSON, _ := json.Marshal(step.Args)
			desc = fmt.Sprintf("Execute '%s' %s", step.Command, string(argsJSON))
		} else if step.Type == "block" {
			desc = fmt.Sprintf("Sequence of %d steps", len(step.Steps))
		} else if step.Type == "loop" {
			desc = fmt.Sprintf("For %s in %s", step.Iterator, step.List)
		} else if step.Type == "if" {
			desc = fmt.Sprintf("If %s", step.Condition)
		}

		sb.WriteString(fmt.Sprintf("%s%d. [%s] %s\n", indentStr, i+1, step.Type, desc))

		if step.Type == "block" || step.Type == "loop" {
			printSteps(sb, step.Steps, indent+1)
		} else if step.Type == "if" {
			if len(step.Then) > 0 {
				sb.WriteString(fmt.Sprintf("%s  Then:\n", indentStr))
				printSteps(sb, step.Then, indent+2)
			}
			if len(step.Else) > 0 {
				sb.WriteString(fmt.Sprintf("%s  Else:\n", indentStr))
				printSteps(sb, step.Else, indent+2)
			}
		}
	}
}

func (a *CopilotAgent) toolRefactorScript(ctx context.Context, args map[string]any) (string, error) {
	mode, _ := args["mode"].(string)
	if mode == "" {
		return "", fmt.Errorf("mode is required (script or block)")
	}
	name, _ := args["name"].(string)

	recorder, ok := ctx.Value(ai.CtxKeyScriptRecorder).(ai.ScriptRecorder)
	if !ok {
		return "", fmt.Errorf("no script recorder available")
	}

	// We don't know the count here, but RefactorLastSteps can handle count=0 (use buffer)
	if err := recorder.RefactorLastSteps(0, mode, name); err != nil {
		return "", fmt.Errorf("refactor failed: %w", err)
	}

	if mode == "script" {
		return fmt.Sprintf("Last interaction refactored into new script '%s' and added as step.", name), nil
	}
	return "Last interaction refactored into a block step.", nil
}

func (a *CopilotAgent) toolScriptInsertStep(ctx context.Context, args map[string]any) (string, error) {
	scriptName, _ := args["script"].(string)
	index, _ := args["index"].(float64)
	stepType, _ := args["type"].(string)

	if scriptName == "" {
		return "", fmt.Errorf("script name required")
	}
	if stepType == "" {
		return "", fmt.Errorf("step type required")
	}

	newStep := ai.ScriptStep{
		Type: stepType,
	}

	// Populate fields based on args
	if v, ok := args["prompt"].(string); ok {
		newStep.Prompt = v
	}
	if v, ok := args["message"].(string); ok {
		newStep.Message = v
	}
	if v, ok := args["script_name"].(string); ok {
		newStep.ScriptName = v
	}
	if v, ok := args["command"].(string); ok {
		newStep.Command = v
	}
	if v, ok := args["condition"].(string); ok {
		newStep.Condition = v
	}
	if v, ok := args["iterator"].(string); ok {
		newStep.Iterator = v
	}
	if v, ok := args["list"].(string); ok {
		newStep.List = v
	}
	if v, ok := args["description"].(string); ok {
		newStep.Description = v
	}
	if v, ok := args["name"].(string); ok {
		newStep.Name = v
	}

	// Additional fields mapping
	if v, ok := args["output_variable"].(string); ok {
		newStep.OutputVariable = v
	}
	if v, ok := args["variable"].(string); ok {
		newStep.Variable = v
	}
	if v, ok := args["value"].(string); ok {
		newStep.Value = v
	}
	if v, ok := args["database"].(string); ok {
		newStep.Database = v
	}
	if v, ok := args["source"].(string); ok {
		newStep.Source = v
	}
	if v, ok := args["resource"].(string); ok {
		newStep.Resource = v
	}
	if v, ok := args["filter"].(string); ok {
		newStep.Filter = v
	}
	if v, ok := args["script_args"].(map[string]any); ok {
		// Convert map[string]any to map[string]string for ScriptArgs
		sa := make(map[string]string)
		for k, val := range v {
			sa[k] = fmt.Sprintf("%v", val)
		}
		newStep.ScriptArgs = sa
	}
	if v, ok := args["is_async"].(bool); ok {
		newStep.IsAsync = v
	}
	if v, ok := args["continue_on_error"].(bool); ok {
		newStep.ContinueOnError = v
	}

	if v, ok := args["args"].(map[string]any); ok {
		newStep.Args = v
	}

	err := a.updateScript(ctx, scriptName, func(m *ai.Script) error {
		idx := int(index)
		if idx < 0 || idx > len(m.Steps) {
			return fmt.Errorf("index out of bounds")
		}
		// Insert
		m.Steps = append(m.Steps[:idx], append([]ai.ScriptStep{newStep}, m.Steps[idx:]...)...)
		return nil
	})

	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Step inserted into script '%s' at index %d", scriptName, int(index)), nil
}

func (a *CopilotAgent) toolScriptSaveStep(ctx context.Context, args map[string]any) (string, error) {
	// Wrapper to Append a step.
	// We construct a ScriptStep from args then append it.

	scriptName, _ := args["script"].(string)
	if scriptName == "" {
		return "", fmt.Errorf("script name required")
	}
	stepType, _ := args["type"].(string)
	if stepType == "" {
		// Try to infer type?
		// If command provided -> command
		// If variable provided -> set
		// Better to require type or default to 'command' if command present.
		if _, ok := args["command"]; ok {
			stepType = "command"
		} else if _, ok := args["variable"]; ok {
			stepType = "set"
		} else if _, ok := args["prompt"]; ok {
			stepType = "ask"
		} else if _, ok := args["source"]; ok {
			stepType = "fetch"
		} else {
			return "", fmt.Errorf("step type required")
		}
	}

	newStep := ai.ScriptStep{
		Type: stepType,
	}

	// Populate standard fields
	if v, ok := args["name"].(string); ok {
		newStep.Name = v
	}
	if v, ok := args["description"].(string); ok {
		newStep.Description = v
	}

	if v, ok := args["prompt"].(string); ok {
		newStep.Prompt = v
	}
	if v, ok := args["message"].(string); ok {
		newStep.Message = v
	}
	if v, ok := args["script_name"].(string); ok {
		newStep.ScriptName = v
	}
	if v, ok := args["command"].(string); ok {
		newStep.Command = v
	}
	if v, ok := args["condition"].(string); ok {
		newStep.Condition = v
	}
	if v, ok := args["iterator"].(string); ok {
		newStep.Iterator = v
	}
	if v, ok := args["list"].(string); ok {
		newStep.List = v
	}

	if v, ok := args["output_variable"].(string); ok {
		newStep.OutputVariable = v
	}
	if v, ok := args["variable"].(string); ok {
		newStep.Variable = v
	}
	if v, ok := args["value"].(string); ok {
		newStep.Value = v
	}
	if v, ok := args["database"].(string); ok {
		newStep.Database = v
	}
	if v, ok := args["source"].(string); ok {
		newStep.Source = v
	}
	if v, ok := args["resource"].(string); ok {
		newStep.Resource = v
	}
	if v, ok := args["filter"].(string); ok {
		newStep.Filter = v
	}

	if v, ok := args["script_args"].(map[string]any); ok {
		sa := make(map[string]string)
		for k, val := range v {
			sa[k] = fmt.Sprintf("%v", val)
		}
		newStep.ScriptArgs = sa
	}
	if v, ok := args["is_async"].(bool); ok {
		newStep.IsAsync = v
	}
	if v, ok := args["continue_on_error"].(bool); ok {
		newStep.ContinueOnError = v
	}

	if v, ok := args["args"].(map[string]any); ok {
		newStep.Args = v
	}

	err := a.updateScript(ctx, scriptName, func(m *ai.Script) error {
		m.Steps = append(m.Steps, newStep)
		return nil
	})

	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Step '%s' saved to script '%s'.", newStep.Name, scriptName), nil
}

func (a *CopilotAgent) toolScriptDeleteStep(ctx context.Context, args map[string]any) (string, error) {
	scriptName, _ := args["script"].(string)
	index, _ := args["index"].(float64)

	if scriptName == "" {
		return "", fmt.Errorf("script name required")
	}

	err := a.updateScript(ctx, scriptName, func(m *ai.Script) error {
		idx := int(index)
		if idx < 0 || idx >= len(m.Steps) {
			return fmt.Errorf("index out of bounds")
		}
		// Delete
		m.Steps = append(m.Steps[:idx], m.Steps[idx+1:]...)
		return nil
	})

	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Step deleted from script '%s' at index %d", scriptName, int(index)), nil
}

func (a *CopilotAgent) toolScriptUpdateStep(ctx context.Context, args map[string]any) (string, error) {
	scriptName, _ := args["script"].(string)
	index, _ := args["index"].(float64)

	if scriptName == "" {
		return "", fmt.Errorf("script name required")
	}

	err := a.updateScript(ctx, scriptName, func(m *ai.Script) error {
		idx := int(index)
		if idx < 0 || idx >= len(m.Steps) {
			return fmt.Errorf("index out of bounds")
		}

		step := &m.Steps[idx]

		// Update fields if present
		if v, ok := args["type"].(string); ok {
			step.Type = v
		}
		if v, ok := args["prompt"].(string); ok {
			step.Prompt = v
		}
		if v, ok := args["message"].(string); ok {
			step.Message = v
		}
		if v, ok := args["description"].(string); ok {
			step.Description = v
		}
		if v, ok := args["name"].(string); ok {
			step.Name = v
		}

		// Additional fields mapping
		if v, ok := args["output_variable"].(string); ok {
			step.OutputVariable = v
		}
		if v, ok := args["variable"].(string); ok {
			step.Variable = v
		}
		if v, ok := args["value"].(string); ok {
			step.Value = v
		}
		if v, ok := args["database"].(string); ok {
			step.Database = v
		}
		if v, ok := args["source"].(string); ok {
			step.Source = v
		}
		if v, ok := args["resource"].(string); ok {
			step.Resource = v
		}
		if v, ok := args["filter"].(string); ok {
			step.Filter = v
		}
		if v, ok := args["script_args"].(map[string]any); ok {
			sa := make(map[string]string)
			for k, val := range v {
				sa[k] = fmt.Sprintf("%v", val)
			}
			step.ScriptArgs = sa
		}
		if v, ok := args["is_async"].(bool); ok {
			step.IsAsync = v
		}
		if v, ok := args["continue_on_error"].(bool); ok {
			step.ContinueOnError = v
		}

		if v, ok := args["script_name"].(string); ok {
			step.ScriptName = v
		}

		// PROTECT COMMAND: Only update if explicitly provided and non-empty.
		// UI might send empty "command" if it's mapping "Function" to "Name" but clearing command.
		// However, if the intent is to update "Function" (Name), strictly update Name.
		if v, ok := args["command"].(string); ok && v != "" {
			step.Command = v
		}
		if v, ok := args["condition"].(string); ok {
			step.Condition = v
		}
		if v, ok := args["iterator"].(string); ok {
			step.Iterator = v
		}
		if v, ok := args["list"].(string); ok {
			step.List = v
		}
		if v, ok := args["args"].(map[string]any); ok {
			step.Args = v
		}

		return nil
	})

	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Step updated in script '%s' at index %d", scriptName, int(index)), nil
}

func (a *CopilotAgent) toolScriptReorderSteps(ctx context.Context, args map[string]any) (string, error) {
	scriptName, _ := args["script"].(string)
	fromIndex, _ := args["from_index"].(float64)
	toIndex, _ := args["to_index"].(float64)

	if scriptName == "" {
		return "", fmt.Errorf("script name required")
	}

	err := a.updateScript(ctx, scriptName, func(m *ai.Script) error {
		from := int(fromIndex)
		to := int(toIndex)
		if from < 0 || from >= len(m.Steps) {
			return fmt.Errorf("from_index out of bounds")
		}
		if to < 0 || to > len(m.Steps) { // Allow moving to end
			return fmt.Errorf("to_index out of bounds")
		}

		step := m.Steps[from]
		// Remove
		m.Steps = append(m.Steps[:from], m.Steps[from+1:]...)

		// Adjust 'to' if we removed an item before it
		if from < to {
			to--
		}

		// Insert
		m.Steps = append(m.Steps[:to], append([]ai.ScriptStep{step}, m.Steps[to:]...)...)
		return nil
	})

	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Step moved from %d to %d in script '%s'", int(fromIndex), int(toIndex), scriptName), nil
}

// Helper to update a script transactionally
func (a *CopilotAgent) updateScript(ctx context.Context, name string, updateFunc func(*ai.Script) error) error {
	// Look for "system" database
	db := a.systemDB
	if db == nil {
		if opts, ok := a.databases["system"]; ok {
			db = database.NewDatabase(opts)
		}
	}
	if db == nil {
		return fmt.Errorf("system database not found")
	}

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	store, err := db.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		return fmt.Errorf("failed to open scripts store: %w", err)
	}

	var script ai.Script
	if err := store.Load(ctx, ai.DefaultScriptCategory, name, &script); err != nil {
		return fmt.Errorf("failed to load script '%s': %w", name, err)
	}

	if err := updateFunc(&script); err != nil {
		return err
	}

	if err := store.Save(ctx, ai.DefaultScriptCategory, name, &script); err != nil {
		return fmt.Errorf("failed to save script '%s': %w", name, err)
	}

	return tx.Commit(ctx)
}

func (a *CopilotAgent) toolScriptAddStepFromLast(ctx context.Context, args map[string]any) (string, error) {
	if a.lastToolCall == nil {
		return "", fmt.Errorf("no last tool call found")
	}

	scriptName, _ := args["script"].(string)
	if scriptName == "" {
		return "", fmt.Errorf("script name required")
	}

	// Optional: index
	var index int = -1
	if v, ok := args["index"].(float64); ok {
		index = int(v)
	}

	// Optional: position (before, after, append)
	position, _ := args["position"].(string)
	if position == "" {
		if index == -1 {
			position = "append"
		} else {
			position = "after" // Default to after if index is provided
		}
	}

	err := a.updateScript(ctx, scriptName, func(m *ai.Script) error {
		// Convert generic tool call to ScriptStep
		// Assuming lastToolCall is generic or we map it
		// For now, let's assume we construct a simple command step
		step := ai.ScriptStep{
			Type:    "command",
			Command: a.lastToolCall.Command, // Assuming a.lastToolCall has Command
			Args:    a.lastToolCall.Args,
		}

		// Optional: description
		if v, ok := args["description"].(string); ok {
			step.Description = v
		}
		// Optional: name
		if v, ok := args["name"].(string); ok {
			step.Name = v
		}

		if position == "append" || index == -1 {
			m.Steps = append(m.Steps, step)
			return nil
		}

		if index < 0 || index >= len(m.Steps) {
			if index == len(m.Steps) {
				m.Steps = append(m.Steps, step)
				return nil
			}
			return fmt.Errorf("index out of bounds")
		}

		if position == "before" {
			// Insert at index
			m.Steps = append(m.Steps[:index], append([]ai.ScriptStep{step}, m.Steps[index:]...)...)
		} else {
			// "after" -> Insert at index + 1
			if index+1 >= len(m.Steps) {
				m.Steps = append(m.Steps, step)
			} else {
				m.Steps = append(m.Steps[:index+1], append([]ai.ScriptStep{step}, m.Steps[index+1:]...)...)
			}
		}
		return nil
	})

	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Step added to script '%s'", scriptName), nil
}

func (a *CopilotAgent) registerScriptTools(ctx context.Context) {
	a.registry.Register("list_scripts", "Lists all available scripts.", listScriptsArgsSchema, wrapStringTool(a.toolListScripts))
	a.registry.Register("create_script", "Creates a new named script. Use this when the script does not already exist. Provide the full script as `script` (preferred) or legacy alias `steps`.", createScriptArgsSchema, wrapStringTool(a.toolCreateScript))
	a.registry.Register("save_script", "Saves or replaces a full script definition. Use this to overwrite/update an existing script. Provide the full script as `script` (preferred) or legacy alias `steps`.", saveScriptArgsSchema, wrapStringTool(a.toolSaveScript))
	a.registry.Register("get_script_details", "Get details of a specific script.", getScriptDetailsArgsSchema, wrapStringTool(a.toolGetScriptDetails))
	a.registry.Register("save_step", "Appends a new step to a script. Usage: save_step(script='MyScript', type='command', command='select', ...).", saveStepArgsSchema, wrapStringTool(a.toolScriptSaveStep))
	a.registry.Register("insert_step", "Insert a step into a script.", insertStepArgsSchema, wrapStringTool(a.toolScriptInsertStep))
	a.registry.Register("delete_step", "Delete a step from a script.", deleteStepArgsSchema, wrapStringTool(a.toolScriptDeleteStep))
	a.registry.Register("update_step", "Update a step in a script.", updateStepArgsSchema, wrapStringTool(a.toolScriptUpdateStep))
	a.registry.Register("reorder_steps", "Move a step in a script to a new position.", reorderStepsArgsSchema, wrapStringTool(a.toolScriptReorderSteps))
	a.registry.Register("save_last_step", "Add the last executed tool call as a new step to a script. If 'index' is not provided, it appends to the end. If 'index' is provided, it inserts 'after' that index by default, unless 'position' is set to 'before'.", saveLastStepArgsSchema, wrapStringTool(a.toolScriptAddStepFromLast))
	a.registry.Register("refactor_last_interaction", "Refactor the last interaction's steps into a new script or block.", refactorLastInteractionArgsSchema, wrapStringTool(a.toolRefactorScript))

	schema := buildExecuteScriptArgsSchema()
	a.registry.RegisterWithUI("execute_script", "Executes a multi-step programmatic script for advanced queries.", ExecuteScriptInstruction, schema, a.toolExecuteScript)
}

func buildExecuteScriptArgsSchema() string {
	var ops = `["open_db", "begin_tx", "commit_tx", "rollback_tx", "open_store", "scan", "select", "filter", "sort", "project", "limit", "join", "join_right", "update", "delete", "inspect", "defer", "assign", "if", "loop", "call_script", "script", "call_function", "list_new", "list_append", "map_merge", "first", "last", "next", "previous", "find", "add", "get_current_key", "get_current_value", "return"]`
	scriptDesc := "Ordered AST steps for multi-step store orchestration. Chain steps with result_var/input_var. For multi-store reads or uncertain schema, call list_stores first. list_stores returns a JSON object with stores:[{name,schema,description,relations,empty}]. Read each store.schema object for exact field names and value types, then align those researched field names with the user's criteria values. Read each store.relations entry literally: source_fields are the current-store field paths, target_store is the joined store, and target_fields are the target-store join fields. Worked example: infer likely stores [\"users\",\"users_orders\",\"orders\"], call list_stores, read users.schema.first_name:string and orders.schema.total_amount:number, align expression names to first_name and orders.total_amount, align literal values to string John and number 500, then build the next AST with grounded filters and relation-driven joins. Example AST: {\"script\":[{\"op\":\"begin_tx\",\"args\":{\"mode\":\"read\"},\"result_var\":\"tx\"},{\"op\":\"open_store\",\"args\":{\"transaction\":\"tx\",\"name\":\"users\"},\"result_var\":\"users_store\"},{\"op\":\"open_store\",\"args\":{\"transaction\":\"tx\",\"name\":\"users_orders\"},\"result_var\":\"users_orders_store\"},{\"op\":\"open_store\",\"args\":{\"transaction\":\"tx\",\"name\":\"orders\"},\"result_var\":\"orders_store\"},{\"op\":\"select\",\"args\":{\"store\":\"users_store\",\"condition\":{\"first_name\":{\"$eq\":\"John\"}}},\"result_var\":\"matched_users\"},{\"op\":\"join\",\"input_var\":\"matched_users\",\"args\":{\"target\":\"users_orders_store\",\"relation\":\"users_orders\"},\"result_var\":\"user_order_links\"},{\"op\":\"join\",\"input_var\":\"user_order_links\",\"args\":{\"target\":\"orders_store\",\"relation\":\"orders\"},\"result_var\":\"joined_orders\"},{\"op\":\"filter\",\"input_var\":\"joined_orders\",\"args\":{\"condition\":{\"orders.total_amount\":{\"$gt\":500}}},\"result_var\":\"filtered_orders\"},{\"op\":\"return\",\"input_var\":\"filtered_orders\"}]}. join and join_right emit a combined flat record by default, so continue using dotted store-qualified field paths unless a later project step reshapes the output. If the AST shape is ambiguous, call gettoolinfo('execute_script')."
	scriptDesc += " Use store.schema to choose the field path and value type, then write the condition expression directly with the concrete comparison value assigned."
	argsDesc := "Operation arguments are op-sensitive. begin_tx expects {database?: string, mode: \"read\"|\"write\"}. open_store expects {transaction: string, name: string}. filter expects {condition: object}, for example {\"first_name\":{\"$eq\":\"John\"}} or {\"orders.total_amount\":{\"$gt\":500}}. Use the researched schema field name as the predicate key and the user's criterion as the predicate value; match the literal type to the schema type, for example string for first_name and number for total_amount. Do not emit booleans such as {\"first_name\":true}. join and join_right must reuse researched relation mappings instead of inventing field pairs."
	argsDesc += " args.condition should be written as the executable condition expression with the concrete operator and value already assigned."
	onDesc := "Concrete left-to-right join mapping. Keys are current or left-record field names, values are right-side field names. Reuse the confirmed relation mapping exactly, for example {\"value\":\"key\"}. If list_stores returned a relation with source_fields [\"value\"], target_store \"users_orders\", and target_fields [\"key\"], translate that grounded mapping directly into the join step. Do not invent placeholder keys or ambiguous store-qualified names."
	conditionDesc := "Concrete predicate object for filter/select steps, for example {\"first_name\":{\"$eq\":\"John\"}} or {\"orders.total_amount\":{\"$gt\":500}}. Use the schema-confirmed field path as the key and the user-requested literal or comparison value as the predicate value; match the value type to the schema and never use booleans or nulls as stand-ins."
	conditionDesc += " Write this as the finished condition expression the engine will execute, with the predicate field, operator, and concrete value already assigned."
	scriptDesc += " In multi-store queries, and in any filter or select that runs after a join, predicate keys must use store-qualified dotted field paths such as \"users.first_name\" or \"orders.total_amount\" rather than bare field names."
	argsDesc += " In multi-store queries, and after joins, use store-qualified dotted predicate keys instead of bare field names."
	conditionDesc += " In multi-store queries, and for post-join filters, predicate keys must use store-qualified dotted field paths."
	scriptDesc = strconv.Quote(scriptDesc)[1 : len(strconv.Quote(scriptDesc))-1]
	argsDesc = strconv.Quote(argsDesc)[1 : len(strconv.Quote(argsDesc))-1]
	onDesc = strconv.Quote(onDesc)[1 : len(strconv.Quote(onDesc))-1]
	conditionDesc = strconv.Quote(conditionDesc)[1 : len(strconv.Quote(conditionDesc))-1]
	var schemaTemplate = `{"type":"object","properties":{"script":{"type":"array","description":"%s","items":{"type":"object","properties":{"op":{"type":"string","enum":%s,"description":"Operation name."},"args":{"type":"object","description":"%s","properties":{"database":{"type":"string","description":"Database name for steps that override the current database, such as begin_tx or open_db."},"mode":{"type":"string","enum":["read","write"],"description":"Transaction mode for begin_tx."},"transaction":{"type":"string","description":"Transaction variable produced by begin_tx and reused by open_store, commit_tx, or rollback_tx."},"name":{"type":"string","description":"Store name for open_store."},"store":{"type":"string","description":"Store name or previously opened store variable. For join/join_right this is the preferred explicit right-side store reference when the join target is already grounded."},"with":{"type":"string","description":"Legacy alias for the right-side join store or store variable."},"target":{"type":"string","description":"Relation-target store variable for relation-driven joins, for example {\"relation\":\"users_orders\",\"target\":\"orders_store\"}."},"relation":{"type":"string","description":"Confirmed relation name from list_stores output. Prefer relation+target when list_stores returned an explicit relation path."},"on":{"type":"object","description":"%s","additionalProperties":{"type":"string"}},"type":{"type":"string","enum":["left","right"],"description":"Optional join flavor when a step needs to force left or right join semantics."},"condition":{"type":"object","description":"%s"},"field":{"type":"string","description":"Single field name used by compact filter/sort forms."},"op":{"type":"string","description":"Compact operator for filter/update forms, such as ==, >, or <=."},"value":{"description":"Literal value payload for compact filter/update/return forms."},"fields":{"type":"array","description":"Projection field list used by project steps. Keep dotted store-qualified field paths after joins unless project intentionally reshapes the output.","items":{"type":"string"}},"direction":{"type":"string","enum":["asc","desc"],"description":"Scan or sort direction when supported by the step."},"limit":{"type":"integer","description":"Maximum number of records to keep for limit-style steps."}},"additionalProperties":true},"input_var":{"type":"string","description":"Optional variable name used as input for pipeline chaining."},"result_var":{"type":"string","description":"Optional variable name to store the result for later steps."}},"required":["op"]}}},"required":["script"]}`
	return fmt.Sprintf(schemaTemplate, scriptDesc, ops, argsDesc, onDesc, conditionDesc)
}
