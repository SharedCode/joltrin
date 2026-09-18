package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"io"
	"sync"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
)

// PlayScript executes a script by name with provided arguments and streams the output to the writer.
func (s *Service) PlayScript(ctx context.Context, name string, category string, args map[string]any, w io.Writer) error {
	scriptDB := s.getScriptDB()
	if scriptDB == nil {
		return fmt.Errorf("no database configured")
	}

	tx, err := scriptDB.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return fmt.Errorf("error starting transaction: %v", err)
	}

	store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Errorf("error opening store: %v", err)
	}

	var script ai.Script
	if err := store.Load(ctx, category, name, &script); err != nil {
		tx.Rollback(ctx)
		return fmt.Errorf("error loading script: %v", err)
	}
	tx.Commit(ctx)

	// Validation: Check if all parameters are satisfied
	var missingParams []string
	for _, param := range script.Parameters {
		if _, ok := args[param]; !ok {
			missingParams = append(missingParams, param)
		}
	}
	if len(missingParams) > 0 {
		return fmt.Errorf("missing required parameters: %v", missingParams)
	}

	// Reset LastInteractionToolCalls for this execution so /last-tool reflects this run
	if s.session != nil {
		s.session.LastInteractionToolCalls = []ai.ScriptStep{}
	}

	var scopeMu sync.RWMutex

	// Handle Database Switching for Script
	var scriptCtx context.Context = ctx
	// Set the current category context for nested script calls
	scriptCtx = context.WithValue(scriptCtx, CtxKeyCurrentScriptCategory, category)

	var db *database.Database = s.systemDB

	// Check for NDJSON request
	useNDJSON, _ := ctx.Value(CtxKeyUseNDJSON).(bool)

	// Determine flush policy (Default: true)
	shouldFlush := true
	if v, ok := ctx.Value(ai.CtxKeyAutoFlush).(bool); ok {
		shouldFlush = v
	}

	// Initialize streamer for structured output
	var streamer *JSONStreamer
	if useNDJSON {
		streamer = NewNDJSONStreamer(w)
	} else {
		streamer = NewJSONStreamer(w)
		fmt.Fprint(w, "[\n") // Start JSON array
	}
	streamer.SetFlush(shouldFlush)
	if len(script.Steps) == 1 {
		streamer.SetSuppressStepStart(true)
	}
	scriptCtx = context.WithValue(scriptCtx, CtxKeyJSONStreamer, streamer)

	if script.Database != "" && !script.Portable {
		// Resolve Database from Service Options
		if opts, ok := s.databases[script.Database]; ok {
			targetDB := database.NewDatabase(opts)
			db = targetDB

			// Update Payload in Context
			if p := ai.GetSessionPayload(ctx); p != nil {
				newPayload := *p
				newPayload.CurrentDB = script.Database
				newPayload.Transaction = nil // Ensure Open starts a new one
				scriptCtx = context.WithValue(scriptCtx, "session_payload", &newPayload)
			}
		} else {
			return fmt.Errorf("script %q requires database %q which is not configured", name, script.Database)
		}
	} else {
		// If portable or no DB specified, use the DB from context payload if available
		if p := ai.GetSessionPayload(ctx); p != nil && p.CurrentDB != "" {
			if opts, ok := s.databases[p.CurrentDB]; ok {
				db = database.NewDatabase(opts)
			}
		}
	}

	// Lifecycle Management for Script Execution
	if err := s.Open(scriptCtx); err != nil {
		return fmt.Errorf("error initializing session: %v", err)
	}
	// Ensure we close the session (commit transaction)
	defer func() {
		if err := s.Close(scriptCtx); err != nil {
			fmt.Fprintf(w, "\nError closing session: %v", err)
		}
		// Safety: If the script left a transaction open (Explicitly), rollback it now.
		if s.session.Transaction != nil {
			_ = s.session.Transaction.Rollback(scriptCtx)
			s.session.Transaction = nil
			s.session.Variables = nil
			fmt.Fprint(w, "\nWarning: Uncommitted transaction was automatically rolled back for safety.")
		}
		if !useNDJSON {
			fmt.Fprint(w, "\n]") // End JSON array
		}
	}()

	// Capture transaction to prevent loss during execution
	var preservedTx any
	if p := ai.GetSessionPayload(scriptCtx); p != nil {
		preservedTx = p.Transaction
	}

	// We need a strings.Builder for the executeScript signature, but we want to stream.
	// The executeScript function writes to sb, but also uses the streamer in context.
	// We can pass a dummy builder or one that we discard, since we rely on the streamer.
	// However, executeScript might write non-JSON text to sb.
	// Let's pass a builder that writes to our writer? No, executeScript takes *strings.Builder.
	// We will just capture the text output in a builder, but the structured output goes to streamer.
	var sb strings.Builder

	// Create empty ConfigMap for script execution (scripts don't use Ask options)
	cfg := ai.NewConfigMap()

	if err := s.executeScript(scriptCtx, &script, args, &scopeMu, &sb, db, cfg); err != nil {
		errMsg := fmt.Sprintf("Error executing script: %v", err)

		// Also add to streamer
		streamer.Write(StepExecutionResult{
			Type:  "error",
			Error: errMsg,
		})

		// If error, we might want to rollback.
		if p := ai.GetSessionPayload(scriptCtx); p != nil && p.Transaction != nil {
			if tx, ok := p.Transaction.(sop.Transaction); ok {
				tx.Rollback(scriptCtx)
				p.Transaction = nil // Prevent Close from committing
			}
		}
		return fmt.Errorf("execution error: %v", err)
	}

	if p := ai.GetSessionPayload(scriptCtx); p != nil {
		// Restore transaction if it was lost
		if p.Transaction == nil && preservedTx != nil {
			p.Transaction = preservedTx
		}
	}

	return nil
}

// RunScript executes a script by name with provided arguments and returns the output as a string.
// This is a convenience wrapper around PlayScript for non-streaming use cases.
func (s *Service) RunScript(ctx context.Context, name string, category string, args map[string]any) (string, error) {
	var sb strings.Builder
	err := s.PlayScript(ctx, name, category, args, &sb)
	return sb.String(), err
}

func (s *Service) scriptList(ctx context.Context, scriptDB *database.Database, args []string) (string, error) {
	category := ai.DefaultScriptCategory
	for i := 1; i < len(args); i++ {
		if args[i] == "--category" && i+1 < len(args) {
			category = args[i+1]
			i++
		}
	}

	tx, err := scriptDB.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return fmt.Sprintf("Error starting transaction: %v", err), nil
	}
	store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error opening store: %v", err), nil
	}
	names, err := store.List(ctx, category)
	tx.Commit(ctx)
	if err != nil {
		return fmt.Sprintf("Error listing scripts: %v", err), nil
	}
	if len(names) == 0 {
		return fmt.Sprintf("No scripts found in category %q.", category), nil
	}
	return fmt.Sprintf("Scripts (Category: %s):\n- %s", category, strings.Join(names, "\n- ")), nil
}

func (s *Service) scriptCreate(ctx context.Context, scriptDB *database.Database, args []string) (string, error) {
	// /script create <name> [description] [--category <cat>] [--force]
	if len(args) < 2 {
		return "Usage: /script create <name> [description] [--category <cat>] [--force]", nil
	}
	name := args[1]
	description := ""
	category := ai.DefaultScriptCategory
	force := false

	for i := 2; i < len(args); i++ {
		if args[i] == "--category" && i+1 < len(args) {
			category = args[i+1]
			i++
		} else if args[i] == "--force" {
			force = true
		} else {
			if description == "" {
				description = args[i]
			} else {
				description += " " + args[i]
			}
		}
	}

	tx, err := scriptDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return fmt.Sprintf("Error starting transaction: %v", err), nil
	}
	store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error opening store: %v", err), nil
	}

	// Check if exists
	var dummy ai.Script
	if err := store.Load(ctx, category, name, &dummy); err == nil {
		if !force {
			tx.Rollback(ctx)
			return fmt.Sprintf("Error: Script %q (Category: %s) already exists. Use --force to overwrite.", name, category), nil
		}
	}

	// Create new script
	// We default to current DB if available in payload, else empty
	var dbName string
	if p := ai.GetSessionPayload(ctx); p != nil {
		dbName = p.CurrentDB
	}

	newScript := ai.Script{
		Description: description,
		Database:    dbName,
		Steps:       []ai.ScriptStep{},
	}
	newScript.Steps = RefineScriptSteps(newScript.Steps)

	if err := store.Save(ctx, category, name, newScript); err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error saving script: %v", err), nil
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Sprintf("Error committing transaction: %v", err), nil
	}

	return fmt.Sprintf("Script %q created successfully.", name), nil
}

func (s *Service) scriptShow(ctx context.Context, scriptDB *database.Database, args []string) (string, error) {
	if len(args) < 2 {
		return "Usage: /script show <name> [--json] [--category <cat>]", nil
	}
	name := args[1]
	category := ai.DefaultScriptCategory
	showJSON := false

	for i := 2; i < len(args); i++ {
		if args[i] == "--json" {
			showJSON = true
		} else if args[i] == "--category" && i+1 < len(args) {
			category = args[i+1]
			i++
		}
	}

	tx, err := scriptDB.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return fmt.Sprintf("Error starting transaction: %v", err), nil
	}
	store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error opening store: %v", err), nil
	}
	var script ai.Script
	err = store.Load(ctx, category, name, &script)
	tx.Commit(ctx)
	if err != nil {
		return fmt.Sprintf("Error loading script: %v", err), nil
	}

	if showJSON {
		b, err := json.MarshalIndent(script, "", "  ")
		if err != nil {
			return fmt.Sprintf("Error marshaling script: %v", err), nil
		}
		return fmt.Sprintf("```json\n%s\n```", string(b)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Script: %s\n", name))
	if len(script.Parameters) > 0 {
		sb.WriteString(fmt.Sprintf("Parameters: %v\n", script.Parameters))
	}
	if script.Description != "" {
		sb.WriteString(fmt.Sprintf("Description: %s\n", script.Description))
	}
	for i, step := range script.Steps {
		desc := step.Message
		if step.Type == "ask" {
			desc = step.Prompt
		} else if step.Type == "call_script" || step.Type == "script" {
			desc = fmt.Sprintf("Run %q", step.ScriptName)
		} else if step.Type == "command" {
			argsJSON, _ := json.Marshal(step.Args)
			desc = fmt.Sprintf("Execute %q %s", step.Command, string(argsJSON))
		}
		sb.WriteString(fmt.Sprintf("%d. [%s] %s", i+1, step.Type, desc))
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

func (s *Service) scriptDelete(ctx context.Context, scriptDB *database.Database, args []string) (string, error) {
	if len(args) < 2 {
		return "Usage: /script delete <name> [--category <cat>]", nil
	}
	name := args[1]
	category := ai.DefaultScriptCategory
	if len(args) > 3 && args[2] == "--category" {
		category = args[3]
	}

	tx, err := scriptDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return fmt.Sprintf("Error starting transaction: %v", err), nil
	}
	store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error opening store: %v", err), nil
	}

	var dummy ai.Script
	if err := store.Load(ctx, category, name, &dummy); err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error: Script %q (Category: %s) not found.", name, category), nil
	}

	err = store.Delete(ctx, category, name)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error deleting script: %v", err), nil
	}
	tx.Commit(ctx)
	return fmt.Sprintf("Script %q (Category: %s) deleted.", name, category), nil
}

func (s *Service) scriptSaveAs(ctx context.Context, scriptDB *database.Database, args []string) (string, error) {
	if len(args) < 2 {
		return "Usage: /script save_as <name> [--category <cat>]", nil
	}
	name := args[1]
	category := ai.DefaultScriptCategory
	if len(args) > 3 && args[2] == "--category" {
		category = args[3]
	}

	if s.session.LastStep == nil {
		return "Error: No previous step available to save. Run a command first.", nil
	}

	tx, err := scriptDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return fmt.Sprintf("Error starting transaction: %v", err), nil
	}
	store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error opening store: %v", err), nil
	}

	// Check if script exists
	var dummy ai.Script
	if err := store.Load(ctx, category, name, &dummy); err == nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error: Script %q (Category: %s) already exists. Use %q first.", name, category, "/script delete "+name), nil
	}

	newScript := ai.Script{
		Steps: []ai.ScriptStep{*s.session.LastStep},
	}
	newScript.Steps = RefineScriptSteps(newScript.Steps)

	if err := store.Save(ctx, category, name, newScript); err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error saving script: %v", err), nil
	}
	tx.Commit(ctx)
	return fmt.Sprintf("Script %q (Category: %s) created from last step.", name, category), nil
}

func (s *Service) scriptStep(ctx context.Context, scriptDB *database.Database, args []string) (string, error) {
	if len(args) < 3 {
		return "Usage: /script step <add|delete|update> <script_name> ... [--category <cat>]", nil
	}
	subCmd := args[1]
	name := args[2]

	category := ai.DefaultScriptCategory
	var cleanArgs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--category" && i+1 < len(args) {
			category = args[i+1]
			i++
		} else {
			cleanArgs = append(cleanArgs, args[i])
		}
	}
	args = cleanArgs

	if subCmd == "add" {
		return s.scriptStepAdd(ctx, scriptDB, name, category, args)
	}

	if subCmd == "delete" {
		return s.scriptStepDelete(ctx, scriptDB, name, category, args)
	}

	if subCmd == "update" {
		return s.scriptStepUpdate(ctx, scriptDB, name, category, args)
	}

	return "Unknown step command. Usage: /script step <delete|add|update> ...", nil
}

func (s *Service) scriptStepAdd(ctx context.Context, scriptDB *database.Database, name string, category string, args []string) (string, error) {
	// /script step add <script_name> <position> [target_index]
	if len(args) < 4 {
		return "Usage: /script step add <script_name> <position> [target_index]", nil
	}
	if s.session.LastStep == nil {
		return "Error: No previous step available to add. Run a command first.", nil
	}

	position := args[3]
	targetIdx := -1
	if position == "before" || position == "after" {
		if len(args) < 5 {
			return "Usage: /script step add <script_name> <before|after> <target_index>", nil
		}
		var err error
		targetIdx, err = strconv.Atoi(args[4])
		if err != nil || targetIdx < 1 {
			return "Error: Invalid target index", nil
		}
		targetIdx-- // 0-based
	}

	tx, err := scriptDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return fmt.Sprintf("Error starting transaction: %v", err), nil
	}
	store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error opening store: %v", err), nil
	}
	var script ai.Script
	if err := store.Load(ctx, category, name, &script); err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error loading script: %v", err), nil
	}

	newStep := *s.session.LastStep

	switch position {
	case "top":
		script.Steps = append([]ai.ScriptStep{newStep}, script.Steps...)
	case "bottom":
		script.Steps = append(script.Steps, newStep)
	case "before":
		if targetIdx >= len(script.Steps) {
			tx.Rollback(ctx)
			return "Error: Target index out of range", nil
		}
		script.Steps = append(script.Steps[:targetIdx], append([]ai.ScriptStep{newStep}, script.Steps[targetIdx:]...)...)
	case "after":
		if targetIdx >= len(script.Steps) {
			tx.Rollback(ctx)
			return "Error: Target index out of range", nil
		}
		// Insert after targetIdx (so at targetIdx + 1)
		targetIdx++
		script.Steps = append(script.Steps[:targetIdx], append([]ai.ScriptStep{newStep}, script.Steps[targetIdx:]...)...)
	default:
		tx.Rollback(ctx)
		return "Error: Invalid position. Use top, bottom, before, or after.", nil
	}

	script.Steps = RefineScriptSteps(script.Steps)

	if err := store.Save(ctx, category, name, script); err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error saving script: %v", err), nil
	}
	tx.Commit(ctx)
	return fmt.Sprintf("Step added to script %q (Category: %s) at %s.", name, category, position), nil
}

func (s *Service) scriptStepDelete(ctx context.Context, scriptDB *database.Database, name string, category string, args []string) (string, error) {
	if len(args) < 4 {
		return "Usage: /script step delete <script_name> <step_index>", nil
	}
	idxStr := args[3]
	idx, err := strconv.Atoi(idxStr)
	if err != nil || idx < 1 {
		return "Error: Invalid step index", nil
	}
	// Adjust to 0-based
	idx--

	tx, err := scriptDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return fmt.Sprintf("Error starting transaction: %v", err), nil
	}
	store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error opening store: %v", err), nil
	}
	var script ai.Script
	if err := store.Load(ctx, category, name, &script); err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error loading script: %v", err), nil
	}

	if idx >= len(script.Steps) {
		tx.Rollback(ctx)
		return "Error: Step index out of range", nil
	}

	// Remove step
	script.Steps = append(script.Steps[:idx], script.Steps[idx+1:]...)

	script.Steps = RefineScriptSteps(script.Steps)

	if err := store.Save(ctx, category, name, script); err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error saving script: %v", err), nil
	}
	tx.Commit(ctx)
	return fmt.Sprintf("Step %d deleted from script %q (Category: %s).", idx+1, name, category), nil
}

func (s *Service) scriptStepUpdate(ctx context.Context, scriptDB *database.Database, name string, category string, args []string) (string, error) {
	// /script step update <script_name> <step_index>
	if len(args) < 4 {
		return "Usage: /script step update <script_name> <step_index>", nil
	}
	if s.session.LastStep == nil {
		return "Error: No previous step available to update with. Run a command first.", nil
	}

	idxStr := args[3]
	idx, err := strconv.Atoi(idxStr)
	if err != nil || idx < 1 {
		return "Error: Invalid step index", nil
	}
	idx-- // 0-based

	tx, err := scriptDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return fmt.Sprintf("Error starting transaction: %v", err), nil
	}
	store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error opening store: %v", err), nil
	}
	var script ai.Script
	if err := store.Load(ctx, category, name, &script); err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error loading script: %v", err), nil
	}

	if idx >= len(script.Steps) {
		tx.Rollback(ctx)
		return "Error: Step index out of range", nil
	}

	// Update step
	script.Steps[idx] = *s.session.LastStep

	script.Steps = RefineScriptSteps(script.Steps)

	if err := store.Save(ctx, category, name, script); err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error saving script: %v", err), nil
	}
	tx.Commit(ctx)
	return fmt.Sprintf("Step %d updated in script %q (Category: %s).", idx+1, name, category), nil
}

func (s *Service) scriptParameters(ctx context.Context, scriptDB *database.Database, args []string) (string, error) {
	// /script parameters <name> <p1> <p2> ... [--category <cat>]
	if len(args) < 2 {
		return "Usage: /script parameters <name> <p1> <p2> ... [--category <cat>]", nil
	}
	name := args[1]
	category := ai.DefaultScriptCategory
	var params []string

	for i := 2; i < len(args); i++ {
		if args[i] == "--category" && i+1 < len(args) {
			category = args[i+1]
			i++
		} else {
			params = append(params, args[i])
		}
	}

	tx, err := scriptDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return fmt.Sprintf("Error starting transaction: %v", err), nil
	}
	store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error opening store: %v", err), nil
	}

	var script ai.Script
	if err := store.Load(ctx, category, name, &script); err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error loading script: %v", err), nil
	}

	script.Parameters = params

	script.Steps = RefineScriptSteps(script.Steps)

	if err := store.Save(ctx, category, name, script); err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error saving script: %v", err), nil
	}
	tx.Commit(ctx)

	if len(params) == 0 {
		return fmt.Sprintf("Parameters cleared for script %q (Category: %s).", name, category), nil
	}
	return fmt.Sprintf("Parameters updated for script %q (Category: %s): %v", name, category, params), nil
}

func (s *Service) scriptParameterize(ctx context.Context, scriptDB *database.Database, args []string) (string, error) {
	// /script parameterize <name> <param_name> <value_to_replace> [...] [--category <cat>]
	if len(args) < 4 {
		return "Usage: /script parameterize <name> <param1> <value1> [<param2> <value2>...] [--category <cat>]", nil
	}
	name := args[1]

	type replacePair struct {
		Param string
		Value string
	}
	var pairs []replacePair
	category := ai.DefaultScriptCategory

	// Parse arguments for pairs and flags
	for i := 2; i < len(args); i++ {
		// Check for flags
		if args[i] == "--category" && i+1 < len(args) {
			category = args[i+1]
			i++ // Skip value
			continue
		}

		// If not a flag, treat as param-value pair
		if i+1 >= len(args) {
			return fmt.Sprintf("Error: Missing value for parameter %q", args[i]), nil
		}
		pairs = append(pairs, replacePair{Param: args[i], Value: args[i+1]})
		i++ // Skip value
	}

	if len(pairs) == 0 {
		return "No parameters specified.", nil
	}

	// SORT: Longest values first (Maximal Munch) to prevent substring collisions
	sort.Slice(pairs, func(i, j int) bool {
		return len(pairs[i].Value) > len(pairs[j].Value)
	})

	tx, err := scriptDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return fmt.Sprintf("Error starting transaction: %v", err), nil
	}
	store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error opening store: %v", err), nil
	}

	var script ai.Script
	if err := store.Load(ctx, category, name, &script); err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error loading script: %v", err), nil
	}

	totalCount := 0

	// Apply all replacements in order
	for _, pair := range pairs {
		paramName := pair.Param
		valueToReplace := pair.Value
		pairCount := 0

		// 1. Scan all steps and replace valueToReplace with {{.paramName}}
		templateVar := fmt.Sprintf("{{.%s}}", paramName)

		for i := range script.Steps {
			step := &script.Steps[i]

			// Helper to replace in string
			replace := func(s string) string {
				if strings.Contains(s, valueToReplace) {
					totalCount++
					pairCount++
					return strings.ReplaceAll(s, valueToReplace, templateVar)
				}
				return s
			}

			step.Prompt = replace(step.Prompt)
			step.Message = replace(step.Message)
			step.Value = replace(step.Value)
			step.Condition = replace(step.Condition)
			step.List = replace(step.List)

			// Recursive helper for deep replacement
			var deepReplace func(any) any
			deepReplace = func(v any) any {
				switch val := v.(type) {
				case string:
					if strings.Contains(val, valueToReplace) {
						totalCount++
						pairCount++
						return strings.ReplaceAll(val, valueToReplace, templateVar)
					}
					return val
				case map[string]any:
					newMap := make(map[string]any)
					for k, subV := range val {
						newMap[k] = deepReplace(subV)
					}
					return newMap
				case []any:
					newSlice := make([]any, len(val))
					for i, subV := range val {
						newSlice[i] = deepReplace(subV)
					}
					return newSlice
				default:
					return v
				}
			}

			// Replace in Args
			for k, v := range step.Args {
				step.Args[k] = deepReplace(v)
			}

			// Replace in ScriptArgs (for nested scripts)
			for k, v := range step.ScriptArgs {
				if strings.Contains(v, valueToReplace) {
					step.ScriptArgs[k] = strings.ReplaceAll(v, valueToReplace, templateVar)
					totalCount++
					pairCount++
				}
			}
		}

		// 2. Add parameter to list only if replacements were made and it doesn't already exist
		if pairCount > 0 {
			exists := false
			for _, p := range script.Parameters {
				if p == paramName {
					exists = true
					break
				}
			}
			if !exists {
				script.Parameters = append(script.Parameters, paramName)
			}
		}
	}

	script.Steps = RefineScriptSteps(script.Steps)

	if err := store.Save(ctx, category, name, script); err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error saving script: %v", err), nil
	}
	tx.Commit(ctx)

	return fmt.Sprintf("Script %q parameterized. Performed %d replacements across %d parameters.", name, totalCount, len(pairs)), nil
}

func (s *Service) scriptRefine(ctx context.Context, scriptDB *database.Database, args []string) (string, error) {
	// /script refine <name> [instructions...] [--category <cat>]
	// Subcommands: apply, cancel
	if len(args) >= 2 && args[1] == "apply" {
		return s.scriptRefineApply(ctx, scriptDB)
	}
	if len(args) >= 2 && args[1] == "cancel" {
		s.session.PendingRefinement = nil
		return "Refinement cancelled.", nil
	}

	if len(args) < 2 {
		return "Usage: /script refine <name> [instructions...] [--category <cat>]", nil
	}

	name := args[1]
	category := ai.DefaultScriptCategory
	var instructionsParts []string

	for i := 2; i < len(args); i++ {
		if args[i] == "--category" && i+1 < len(args) {
			category = args[i+1]
			i++
		} else {
			instructionsParts = append(instructionsParts, args[i])
		}
	}
	instructions := strings.Join(instructionsParts, " ")

	// 1. Load Script
	tx, err := scriptDB.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return fmt.Sprintf("Error starting transaction: %v", err), nil
	}
	store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error opening store: %v", err), nil
	}
	var script ai.Script
	if err := store.Load(ctx, category, name, &script); err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error: Script %q (Category: %s) not found.", name, category), nil
	}
	tx.Commit(ctx)

	// 2. Prepare LLM Prompt
	scriptJSON, _ := json.MarshalIndent(script, "", "  ")
	prompt := fmt.Sprintf(`You are an expert script optimizer. Your task is to refine the following script.

Script JSON:
%s

User Instructions: %q

Tasks:
1. Identify hardcoded values in the steps that should be parameters (e.g., specific regions, dates, IDs).
   - If the user provided instructions, follow them strictly for parameterization.
   - If no instructions, use your best judgment to identify likely variables.
2. Generate a concise "Summary Description" (1-2 sentences) for the script metadata.
3. Return a JSON object with the following structure:
{
  "summary": "Short description...",
  "new_parameters": ["param1", "param2"],
  "replacements": [
    {"value": "hardcoded_value", "parameter": "param_name", "description": "Replaced 'hardcoded_value' with '{{.param_name}}'"}
  ]
}

IMPORTANT:
- Only return the JSON object. No markdown formatting around it.
- Ensure the "replacements" list is accurate.
- Do not change the logic of the script, only replace values with template variables.
`, string(scriptJSON), instructions)

	// 3. Call LLM
	genOut, err := s.generator.Generate(ctx, prompt, ai.GenOptions{
		Temperature:   0.2,   // Low temperature for precision
		ThinkingLevel: "low", // Strict JSON schema adherence for script refinement
	})
	if err != nil {
		return fmt.Sprintf("Error generating refinement: %v", err), nil
	}

	// 4. Parse Response
	var result struct {
		Summary       string   `json:"summary"`
		NewParameters []string `json:"new_parameters"`
		Replacements  []struct {
			Value       string `json:"value"`
			Parameter   string `json:"parameter"`
			Description string `json:"description"`
		} `json:"replacements"`
	}

	// Clean up potential markdown code blocks from LLM
	cleanJSON := strings.TrimSpace(genOut.Text)
	cleanJSON = strings.TrimPrefix(cleanJSON, "```json")
	cleanJSON = strings.TrimPrefix(cleanJSON, "```")
	cleanJSON = strings.TrimSuffix(cleanJSON, "```")

	if err := json.Unmarshal([]byte(cleanJSON), &result); err != nil {
		return fmt.Sprintf("Error parsing AI response: %v\nResponse: %s", err, cleanJSON), nil
	}

	// 5. Apply Changes to a Copy
	newScript := script
	newScript.Description = result.Summary
	// Merge parameters
	existingParams := make(map[string]bool)
	for _, p := range newScript.Parameters {
		existingParams[p] = true
	}
	for _, p := range result.NewParameters {
		if !existingParams[p] {
			newScript.Parameters = append(newScript.Parameters, p)
		}
	}

	// Apply replacements
	count := 0
	for _, rep := range result.Replacements {
		templateVar := fmt.Sprintf("{{.%s}}", rep.Parameter)
		for i := range newScript.Steps {
			step := &newScript.Steps[i]
			// Helper to replace
			replace := func(s string) string {
				if strings.Contains(s, rep.Value) {
					return strings.ReplaceAll(s, rep.Value, templateVar)
				}
				return s
			}
			step.Prompt = replace(step.Prompt)
			step.Message = replace(step.Message)
			step.Value = replace(step.Value)
			step.Condition = replace(step.Condition)
			step.List = replace(step.List)
			for k, v := range step.Args {
				if strVal, ok := v.(string); ok {
					if strings.Contains(strVal, rep.Value) {
						step.Args[k] = strings.ReplaceAll(strVal, rep.Value, templateVar)
						count++
					}
				}
			}
			for k, v := range step.ScriptArgs {
				if strings.Contains(v, rep.Value) {
					step.ScriptArgs[k] = strings.ReplaceAll(v, rep.Value, templateVar)
					count++
				}
			}
		}
	}

	// 6. Store Proposal
	var replacementDescs []string
	for _, r := range result.Replacements {
		replacementDescs = append(replacementDescs, fmt.Sprintf("- %s -> {{.%s}}", r.Value, r.Parameter))
	}

	s.session.PendingRefinement = &RefinementProposal{
		ScriptName:     name,
		Category:       category,
		OriginalScript: script,
		NewScript:      newScript,
		Description:    result.Summary,
		NewParams:      result.NewParameters,
		Replacements:   replacementDescs,
	}

	// 7. Generate Preview Output
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Refinement Proposal for %q:\n\n", name))
	sb.WriteString(fmt.Sprintf("Summary:\n  Old: %s\n  New: %s\n\n", script.Description, result.Summary))
	sb.WriteString("New Parameters:\n")
	for _, p := range result.NewParameters {
		sb.WriteString(fmt.Sprintf("  - %s\n", p))
	}
	sb.WriteString("\nReplacements:\n")
	for _, r := range replacementDescs {
		sb.WriteString(fmt.Sprintf("  %s\n", r))
	}
	sb.WriteString("\n\nRun '/script refine apply' to save these changes, or '/script refine cancel' to discard.")

	return sb.String(), nil
}

func (s *Service) scriptRefineApply(ctx context.Context, scriptDB *database.Database) (string, error) {
	proposal := s.session.PendingRefinement
	if proposal == nil {
		return "Error: No pending refinement. Run '/script refine <name>' first.", nil
	}

	tx, err := scriptDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return fmt.Sprintf("Error starting transaction: %v", err), nil
	}

	store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error opening scripts store: %v", err), nil
	}

	proposal.NewScript.Steps = RefineScriptSteps(proposal.NewScript.Steps)

	if err := store.Save(ctx, proposal.Category, proposal.ScriptName, proposal.NewScript); err != nil {
		tx.Rollback(ctx)
		return fmt.Sprintf("Error saving script: %v", err), nil
	}

	tx.Commit(ctx)
	s.session.PendingRefinement = nil

	return fmt.Sprintf("Script %q updated successfully with new parameters and documentation.", proposal.ScriptName), nil
}
