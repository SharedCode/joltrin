package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/jsondb"
)

// handleSessionCommand processes session-related commands like /create, /save, /step, etc.
// Returns (response, handled, error)
func (s *Service) handleSessionCommand(ctx context.Context, query string, db *database.Database) (string, bool, error) {
	// /script <subcommand> (Alias support)
	query = strings.TrimSpace(query)
	if strings.HasPrefix(query, "/script ") {
		subQuery := strings.TrimSpace(strings.TrimPrefix(query, "/script "))
		if subQuery != "" {
			return s.handleSessionCommand(ctx, "/"+subQuery, db)
		}
	}

	// /switch_database <db_name>
	// Switches the active database context ensuring deterministic behavior
	if strings.HasPrefix(query, "/switch_database ") {
		targetDB := strings.TrimSpace(strings.TrimPrefix(query, "/switch_database "))
		if targetDB == "" {
			return "Usage: /switch_database <db_name>", true, nil
		}

		// Check if DB exists
		isValid := false
		if targetDB == "system" && s.systemDB != nil {
			isValid = true
		} else {
			_, isValid = s.databases[targetDB]
		}

		if !isValid {
			return fmt.Sprintf("Error: Database %q not found.", targetDB), true, nil
		}

		// Check if already selected
		currentDB := ""
		if p := ai.GetSessionPayload(ctx); p != nil {
			currentDB = p.CurrentDB
		}

		if currentDB == targetDB {
			return fmt.Sprintf("Database %q is already selected.", targetDB), true, nil
		}

		return fmt.Sprintf("[[SWITCH_DATABASE: %s]]", targetDB), true, nil
	}

	// Handle last-tool command (support both "last-tool" and "/last-tool")
	if query == "last-tool" || query == "/last-tool" || query == "last_tool" || query == "/last_tool" {
		instructions := s.GetLastToolInstructions()
		if instructions == "" {
			return "No tool instructions found.", true, nil
		}
		return fmt.Sprintf("Last Tool Instructions\n```json\n%s\n```", instructions), true, nil
	}

	// /list_databases
	if query == "/list_databases" {
		var names []string
		for k := range s.databases {
			names = append(names, k)
		}
		sort.Strings(names)

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Databases: %s", strings.Join(names, ", ")))

		if s.systemDB != nil {
			sb.WriteString("\nSystem Database: system")
		}

		return sb.String(), true, nil
	}

	// /list_tools
	if query == "/list_tools" {
		var sb strings.Builder
		sb.WriteString(`### Available Tools

Use these tools via slash(/) commands. Arguments can be positional (e.g. ` + "`/cmd arg1`" + `) or named (e.g. ` + "`/cmd key=value`" + `).

**Session Commands**
- ` + "`/create <name> [--category <cat>] [--autosave]`" + `: Start drafting a new script.
- ` + "`/step [instruction]`" + `: Add the last tool call or a new instruction to the draft.
- ` + "`/save`" + `: Save the current draft.
- ` + "`/show <name> [--json]`" + `: Display a saved script.
- ` + "`/parameterize <name> <param> <value>`" + `: Replace hardcoded values with parameters.
- ` + "`/refine <name> [feedback]`" + `: Refine a script using AI.
- ` + "`/save_as <name>`" + `: Save the last executed tool as a script.
- ` + "`/run <script> [args...]`" + `: Execute a saved script.
- ` + "`/list_databases`" + `: List available databases.
- ` + "`/switch_database <name>`" + `: Switch active database.
- ` + "`/list_stores [database]`" + `: List stores in a database.
- ` + "`/last-tool`" + `: Show instructions for the last executed tool.
- ` + "`/insert_step <name> <index> <type> <desc> <name> [params...]`" + `: Insert a step.
- ` + "`/delete_step <name> <index>`" + `: Delete a step.
- ` + "`/update_step <name> <index> <desc> <name> [params...]`" + `: Update a step.
- ` + "`/reorder_steps <name> <from> <to>`" + `: Reorder steps.
- ` + "`/delete <name>`" + `: Delete a script.
- ` + "`/select ...`" + `: High-level select.
- ` + "`/add ...`" + `: High-level add.
- ` + "`/update ...`" + `: High-level update.
- ` + "`/delete_record ...`" + `: High-level delete record.

**Agent Tools**
`)

		// Delegate to the agent's tool execution to get its list

		// Find the Data Admin agent in the registry
		var adminAgent *CopilotAgent
		for _, a := range s.registry {
			if da, ok := a.(*CopilotAgent); ok {
				adminAgent = da
				break
			}
		}

		if adminAgent != nil {
			res, err := adminAgent.Execute(ctx, "list_tools", nil)
			if err == nil {
				sb.WriteString(res)
			} else {
				sb.WriteString(fmt.Sprintf("\n(Error listing agent tools: %v)\n", err))
			}
		} else {
			sb.WriteString("\n(Data Admin Agent not found - other tools unavailable)\n")
		}

		return sb.String(), true, nil
	}

	// /list_stores [database]
	if strings.HasPrefix(query, "/list_stores") {
		args := strings.Fields(strings.TrimPrefix(query, "/list_stores"))
		var dbName string
		if len(args) > 0 {
			dbName = args[0]
		}
		// Default to current DB if not specified
		if dbName == "" {
			if s.session.CurrentDB != "" {
				dbName = s.session.CurrentDB
			} else if p := ai.GetSessionPayload(ctx); p != nil {
				dbName = p.CurrentDB
			}
		}

		var db *database.Database
		if dbName != "" {
			if dbName == SystemDBName && s.systemDB != nil {
				db = s.systemDB
			} else if opts, ok := s.databases[dbName]; ok {
				db = database.NewDatabase(opts)
			}
		}

		if db == nil {
			var keys []string
			for k := range s.databases {
				keys = append(keys, k)
			}
			return fmt.Sprintf("Error: Database not found or not selected. Requested: %q, Available: %v", dbName, keys), true, nil
		}

		// Need transaction
		var tx sop.Transaction
		// Use session transaction if available and matches DB
		if s.session.Transaction != nil && s.session.CurrentDB == dbName {
			tx = s.session.Transaction
		} else if p := ai.GetSessionPayload(ctx); p != nil && p.Transaction != nil && p.CurrentDB == dbName {
			if t, ok := p.Transaction.(sop.Transaction); ok {
				tx = t
			}
		}

		var autoCommit bool
		if tx == nil {
			var err error
			tx, err = db.BeginTransaction(ctx, sop.ForReading)
			if err != nil {
				return fmt.Sprintf("Error: Failed to begin transaction: %v", err), true, nil
			}
			autoCommit = true
		}

		stores, err := tx.GetPhasedTransaction().GetStores(ctx)
		if err != nil {
			if autoCommit {
				tx.Rollback(ctx)
			}
			return fmt.Sprintf("Error: Failed to list stores: %v", err), true, nil
		}
		sort.Strings(stores)

		// Enrich with brief schema info
		var descriptions []string
		var dbOpts sop.DatabaseOptions
		var hasOpts bool
		if dbName != "system" {
			dbOpts, hasOpts = s.databases[dbName]
		}

		for _, sName := range stores {
			if strings.Contains(sName, "/") {
				continue
			}
			desc := sName
			if hasOpts {
				s, err := jsondb.OpenStore(ctx, dbOpts, sName, tx)
				if err == nil {
					info := s.GetStoreInfo()
					// Prefer stored schema from StoreInfo, fallback to runtime inference
					if len(info.Schema) > 0 {
						desc = fmt.Sprintf("%s schema=%s", sName, formatSchema(info.Schema))
					} else if ok, _ := s.First(ctx); ok {
						k := s.GetCurrentKey()
						v, _ := s.GetCurrentValue(ctx)
						flat := flattenItem(k, v)
						schema := inferSchema(flat)
						desc = fmt.Sprintf("%s schema=%s", sName, formatSchema(schema))
					}
				}
			}
			descriptions = append(descriptions, desc)
		}

		if autoCommit {
			if err := tx.Commit(ctx); err != nil {
				return fmt.Sprintf("Error: Failed to commit transaction: %v", err), true, nil
			}
		}

		return fmt.Sprintf("Stores in %q:\n%s", dbName, strings.Join(descriptions, "\n")), true, nil
	}

	// Script Drafting Commands

	// /create <name> [category]
	// Starts a new script draft.
	if strings.HasPrefix(query, "/create ") {
		parts := strings.Fields(strings.TrimPrefix(query, "/create "))
		if len(parts) == 0 {
			return "Error: Script name required", true, nil
		}
		name := parts[0]
		category := ai.DefaultScriptCategory
		autoSave := false

		for i := 1; i < len(parts); i++ {
			if parts[i] == "--category" && i+1 < len(parts) {
				category = parts[i+1]
				i++
			} else if parts[i] == "--autosave" {
				autoSave = true
			}
		}

		// Check if script already exists (warning only, as we are drafting)
		scriptDB := s.getScriptDB()
		exists := false
		if scriptDB != nil {
			tx, err := scriptDB.BeginTransaction(ctx, sop.ForReading)
			if err == nil {
				store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
				if err == nil {
					var dummy ai.Script
					if err := store.Load(ctx, category, name, &dummy); err == nil {
						exists = true
					}
				}
				tx.Commit(ctx)
			}
		}

		// Capture current database from session or argument
		// We prioritize what's in the payload/session as the user is "drafting here"
		var currentDB string
		if s.session.CurrentDB != "" {
			currentDB = s.session.CurrentDB
		} else if p := ai.GetSessionPayload(ctx); p != nil {
			currentDB = p.CurrentDB
		}

		s.session.CurrentScript = &ai.Script{
			Database: currentDB,
			Steps:    []ai.ScriptStep{},
		}
		s.session.CurrentScriptName = name
		s.session.CurrentScriptCategory = category
		s.session.AutoSave = autoSave

		msg := fmt.Sprintf("Started drafting script %q (Category: %s).\nUse tools to explore data. When you are happy with a command, type '/step' to add it to the script.\nType '/step <instruction>' to add a manual step.\nType '/save' when finished.", name, category)
		if autoSave {
			msg += " [Auto-Save Enabled]"
		}
		if exists {
			msg += "\nWarning: A script with this name already exists. Saving will overwrite it."
		}
		return msg, true, nil
	}

	// /step [instruction...]
	// Adds the last executed step or a new instruction to the current draft.
	if strings.HasPrefix(query, "/step") {
		if s.session.CurrentScript == nil {
			return "Error: No active script draft. Use '/create <name>' to start.", true, nil
		}

		args := strings.Fields(strings.TrimPrefix(query, "/step"))
		msg := ""
		if len(args) > 0 {
			// Add explicit instruction
			// /step This is a comment or instruction
			instruction := strings.TrimSpace(strings.TrimPrefix(query, "/step"))
			step := ai.ScriptStep{
				Type:    "ask", // Default to 'ask' / natural language instruction
				Prompt:  instruction,
				Message: instruction,
			}
			s.session.CurrentScript.Steps = append(s.session.CurrentScript.Steps, step)
			msg = fmt.Sprintf("Added step %d: %s", len(s.session.CurrentScript.Steps), instruction)
		} else {
			// Add last executed step
			if s.session.LastStep == nil {
				return "Error: No previous step available to add.", true, nil
			}

			// Check if the last step in the script is identical to the one we're about to add.
			// This prevents duplication when the step was already auto-recorded.
			if len(s.session.CurrentScript.Steps) > 0 {
				lastRecorded := s.session.CurrentScript.Steps[len(s.session.CurrentScript.Steps)-1]
				if lastRecorded.Type == s.session.LastStep.Type &&
					lastRecorded.Command == s.session.LastStep.Command &&
					reflect.DeepEqual(lastRecorded.Args, s.session.LastStep.Args) {
					return "Step already recorded.", true, nil
				}
			}

			// Copy the step to ensure we don't modify the session record
			newStep := *s.session.LastStep

			// Defensive: Ensure Type is set correctly.
			// History shows Type might be empty or lost, causing it to run as "ask".
			if newStep.Type == "" && newStep.Command != "" {
				newStep.Type = "command"
			}

			s.session.CurrentScript.Steps = append(s.session.CurrentScript.Steps, newStep)
			msg = fmt.Sprintf("Added step %d (from last command).", len(s.session.CurrentScript.Steps))
		}

		if s.session.AutoSave {
			if err := s.saveDraft(ctx); err != nil {
				return fmt.Sprintf("%s\nWarning: Auto-save failed: %v", msg, err), true, nil
			}
			msg += " (Auto-saved)"
		}

		return msg, true, nil
	}

	// /save OR /end
	// Saves the current draft to the database and ends the drafting helper.
	if query == "/save" || query == "/end" {
		if s.session.CurrentScript == nil {
			return "Error: No active script draft.", true, nil
		}

		// Optimization: If autosave is enabled, the script is already saved.
		if s.session.AutoSave {
			msg := fmt.Sprintf("Script %q is up-to-date (autosave enabled). Drafting ended.", s.session.CurrentScriptName)
			s.session.CurrentScript = nil
			s.session.CurrentScriptName = ""
			s.session.CurrentScriptCategory = ""
			s.session.AutoSave = false
			return msg, true, nil
		}

		if err := s.saveDraft(ctx); err != nil {
			return fmt.Sprintf("Error saving script: %v", err), true, nil
		}

		msg := fmt.Sprintf("Script %q saved successfully with %d steps. Drafting ended.", s.session.CurrentScriptName, len(s.session.CurrentScript.Steps))

		// Clear the draft after saving
		s.session.CurrentScript = nil
		s.session.CurrentScriptName = ""
		s.session.CurrentScriptCategory = ""
		s.session.AutoSave = false // Reset autosave flag

		return msg, true, nil
	}

	// Handle Script Execution & Management

	// /show <name> [--category <cat>] [--json]
	// Shows the content of a saved script.
	if strings.HasPrefix(query, "/show ") {
		parts := strings.Fields(strings.TrimPrefix(query, "/show "))
		if len(parts) == 0 {
			return "Error: Script name required", true, nil
		}
		name := parts[0]
		category := ai.DefaultScriptCategory
		showJson := false

		for i := 1; i < len(parts); i++ {
			if parts[i] == "--category" && i+1 < len(parts) {
				category = parts[i+1]
				i++
			} else if parts[i] == "--json" {
				showJson = true
			}
		}

		scriptDB := s.getScriptDB()
		if scriptDB == nil {
			return "Error: No database configured", true, nil
		}

		tx, err := scriptDB.BeginTransaction(ctx, sop.ForReading)
		if err != nil {
			return fmt.Sprintf("Error starting transaction: %v", err), true, nil
		}

		store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
		if err != nil {
			tx.Rollback(ctx)
			return fmt.Sprintf("Error opening store: %v", err), true, nil
		}

		var script ai.Script
		if err := store.Load(ctx, category, name, &script); err != nil {
			tx.Rollback(ctx)
			return fmt.Sprintf("Error: Script %q (Category: %s) not found.", name, category), true, nil
		}
		tx.Commit(ctx)

		if showJson {
			// Wrap to include name in display
			display := struct {
				Name string `json:"name"`
				ai.Script
			}{
				Name:   name,
				Script: script,
			}
			b, _ := json.MarshalIndent(display, "", "  ")
			return fmt.Sprintf("```json\n%s\n```", string(b)), true, nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Script: %s (Category: %s)\n", name, category))
		if script.Description != "" {
			sb.WriteString(fmt.Sprintf("Description: %s\n", script.Description))
		}
		if len(script.Parameters) > 0 {
			sb.WriteString(fmt.Sprintf("Parameters: %v\n", script.Parameters))
		}
		sb.WriteString("Steps:\n")
		for i, step := range script.Steps {
			prompt := step.Prompt
			if prompt == "" {
				prompt = step.Message // Fallback
			}
			if step.Type == "command" {
				prompt = step.Command
				if len(step.Args) > 0 {
					argBytes, _ := json.Marshal(step.Args)
					prompt += " " + string(argBytes)
				}
			}
			if step.Type == "call_script" {
				prompt = fmt.Sprintf("Run %q", step.ScriptName)
			}
			sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, step.Type, prompt))
		}
		return sb.String(), true, nil
	}

	// /refine <name> [feedback] | /refine apply | /refine cancel
	// Uses AI to improve a script or applies pending improvements.
	if strings.HasPrefix(query, "/refine ") {
		args := strings.Fields(strings.TrimPrefix(query, "/refine "))
		scriptDB := s.getScriptDB()
		if scriptDB == nil {
			return "Error: No database configured", true, nil
		}
		msg, err := s.scriptRefine(ctx, scriptDB, append([]string{"refine"}, args...))
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true, nil
		}
		return msg, true, nil
	}

	// /parameterize <name> <param_name> <value_to_replace> [--category <cat>]
	// Replaces hardcoded values in a script with parameters.
	if strings.HasPrefix(query, "/parameterize ") {
		args := parseCLIArgs(strings.TrimPrefix(query, "/parameterize "))
		scriptDB := s.getScriptDB()
		if scriptDB == nil {
			return "Error: No database configured", true, nil
		}
		msg, err := s.scriptParameterize(ctx, scriptDB, append([]string{"parameterize"}, args...))
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true, nil
		}
		return msg, true, nil
	}

	// /save_as <new_name> [--category <cat>]
	// Saves the last executed tool call as a new script (Shortcut).
	if strings.HasPrefix(query, "/save_as ") {
		args := strings.Fields(strings.TrimPrefix(query, "/save_as "))
		scriptDB := s.getScriptDB()
		if scriptDB == nil {
			return "Error: No database configured", true, nil
		}
		msg, err := s.scriptSaveAs(ctx, scriptDB, append([]string{"save_as"}, args...))
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true, nil
		}
		return msg, true, nil
	}

	// /insert_step <script_name> <index> [before|after]
	// Inserts the last executed step into an existing script.
	if strings.HasPrefix(query, "/insert_step ") {
		if s.session.LastStep == nil {
			return "Error: No previous step available to insert.", true, nil
		}

		parts := strings.Fields(strings.TrimPrefix(query, "/insert_step "))
		if len(parts) < 2 {
			return "Usage: /insert_step <script_name> <index> [before|after]", true, nil
		}
		scriptName := parts[0]

		idx, err := strconv.Atoi(parts[1])
		if err != nil {
			return "Error: Index must be a number.", true, nil
		}
		// Adjust 1-based index from user to 0-based
		idx = idx - 1

		position := "after"
		if len(parts) > 2 {
			position = parts[2]
		}

		scriptDB := s.getScriptDB()
		if scriptDB == nil {
			return "Error: No database configured", true, nil
		}

		tx, err := scriptDB.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			return fmt.Sprintf("Error starting transaction: %v", err), true, nil
		}
		defer tx.Rollback(ctx)

		store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
		if err != nil {
			return fmt.Sprintf("Error opening store: %v", err), true, nil
		}

		var script ai.Script
		if err := store.Load(ctx, ai.DefaultScriptCategory, scriptName, &script); err != nil {
			return fmt.Sprintf("Error loading script %q: %v", scriptName, err), true, nil
		}

		newStep := *s.session.LastStep
		if newStep.Type == "" && newStep.Command != "" {
			newStep.Type = "command"
		}

		// Insert logic
		if idx < 0 {
			idx = 0
		}

		if position == "before" {
			if idx > len(script.Steps) {
				idx = len(script.Steps)
			}
			script.Steps = append(script.Steps[:idx], append([]ai.ScriptStep{newStep}, script.Steps[idx:]...)...)
		} else {
			// after (default)
			if idx >= len(script.Steps) {
				script.Steps = append(script.Steps, newStep)
			} else {
				script.Steps = append(script.Steps[:idx+1], append([]ai.ScriptStep{newStep}, script.Steps[idx+1:]...)...)
			}
		}

		if err := store.Save(ctx, ai.DefaultScriptCategory, scriptName, &script); err != nil {
			return fmt.Sprintf("Error saving script: %v", err), true, nil
		}
		tx.Commit(ctx)
		return fmt.Sprintf("Step added to script %q.", scriptName), true, nil
	}

	if strings.HasPrefix(query, "/run ") {
		parts := parseCLIArgs(strings.TrimPrefix(query, "/run "))
		if len(parts) == 0 {
			return "Error: Script name required", true, nil
		}
		name := parts[0]
		category := ai.DefaultScriptCategory
		var rawArgs []string

		for i := 1; i < len(parts); i++ {
			arg := parts[i]
			if arg == "--category" && i+1 < len(parts) {
				category = parts[i+1]
				i++
				continue
			}
			rawArgs = append(rawArgs, arg)
		}

		scriptDB := s.getScriptDB()
		if scriptDB == nil {
			return "Error: No database configured", true, nil
		}

		tx, err := scriptDB.BeginTransaction(ctx, sop.ForReading)
		if err != nil {
			return fmt.Sprintf("Error starting transaction: %v", err), true, nil
		}

		store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
		if err != nil {
			tx.Rollback(ctx)
			return fmt.Sprintf("Error opening store: %v", err), true, nil
		}

		var script ai.Script
		if err := store.Load(ctx, category, name, &script); err != nil {
			tx.Rollback(ctx)
			return fmt.Sprintf("Error loading script: %v", err), true, nil
		}
		tx.Commit(ctx)

		// Process arguments (Named and Positional)
		args := make(map[string]string)

		// First pass: Extract named arguments
		var positionalArgs []string
		for _, arg := range rawArgs {
			kv := strings.SplitN(arg, "=", 2)
			if len(kv) == 2 {
				args[kv[0]] = kv[1]
			} else {
				positionalArgs = append(positionalArgs, arg)
			}
		}

		// Second pass: Map positional arguments to script parameters
		for i, val := range positionalArgs {
			if i < len(script.Parameters) {
				paramName := script.Parameters[i]
				// Only set if not already set by named arg (Named takes precedence? Or Positional? Usually Named overrides)
				// But here, let's say if you provide both, Named wins.
				if _, exists := args[paramName]; !exists {
					args[paramName] = val
				}
			}
		}

		// Validation: Check if all parameters are satisfied
		var missingParams []string
		for _, param := range script.Parameters {
			if _, ok := args[param]; !ok {
				missingParams = append(missingParams, param)
			}
		}
		if len(missingParams) > 0 {
			return fmt.Sprintf("Error: Missing required parameters: %v", missingParams), true, nil
		}

		var sb strings.Builder
		// Convert args to map[string]any
		scope := make(map[string]any)
		for k, v := range args {
			scope[k] = v
		}

		// Check for streaming writer in context
		var w io.Writer = &sb
		if ctxW, ok := ctx.Value(ai.CtxKeyWriter).(io.Writer); ok {
			// If we are given an explicit writer (like in scripts_test),
			// update context to prefer NDJSON if applicable for that writer.
			// But wait, TestScriptExecution_SelectTwice calls Ask via handleSessionCommand.
			// Ask DOES NOT inject CtxKeyWriter.
			// So w will be &sb.
			// PlayScript will use NewJSONStreamer(&sb).
			// So sb will contain "[...]" JSON array.
			w = ctxW
			// For external streaming, we often prefer NDJSON.
			ctx = context.WithValue(ctx, CtxKeyUseNDJSON, true)
		} else {
			// Default to internal buffer &sb.
			// PlayScript defaults to JSON array if not NDJSON.
			// We want nice text output from handleSessionCommand if possible.
			// But PlayScript outputs JSON.
			// So we should capture JSON in sb, and then parse it if we want to return text?
			// OR we just return the raw JSON string if that's what the test expects.
			// Test expects: "Response missing data" (John Doe etc).
		}

		// Use the shared PlayScript function
		// Note: PlayScript uses internal flushing.
		if err := s.PlayScript(ctx, name, category, scope, w); err != nil {
			// The error is already logged to sb/streamer if possible, but PlayScript returns error too.
			// We append the error message if not already there?
			// PlayScript writes error to writer.
			// But handleSessionCommand expects (string, bool, error).
			// If PlayScript fails, the output is in sb.
			if w == &sb {
				return sb.String(), true, nil
			}
			return fmt.Sprintf("Error: %v", err), true, nil
		}

		if w != &sb {
			return "", true, nil
		}

		// Parse the JSON output from PlayScript and format it nicely
		output := sb.String()
		if strings.HasPrefix(strings.TrimSpace(output), "[") {
			var results []StepExecutionResult // Use strict type for better parsing
			if err := json.Unmarshal([]byte(output), &results); err == nil {
				var report strings.Builder

				// Group by Step Index
				stepMap := make(map[int][]any)
				var sortedSteps []int

				for _, res := range results {
					idx := res.StepIndex

					// Init bucket if needed
					if _, exists := stepMap[idx]; !exists {
						stepMap[idx] = []any{}
						sortedSteps = append(sortedSteps, idx)
					}

					// Get Content
					if res.Record != nil {
						stepMap[idx] = append(stepMap[idx], res.Record)
					}
					if res.Result != nil {
						stepMap[idx] = append(stepMap[idx], res.Result)
					}
					if res.Error != "" {
						stepMap[idx] = append(stepMap[idx], fmt.Sprintf("Error: %s", res.Error))
					}
				}
				sort.Ints(sortedSteps)

				for _, idx := range sortedSteps {
					items := stepMap[idx]
					if len(items) == 0 {
						continue
					}

					report.WriteString(fmt.Sprintf("Step %d Result\n\n", idx))

					// If we only have 1 item and it's a list, treat it as the list of records
					// If we have multiple items (streamed records), treat them as list of records

					// Check if items are structured data that should be pretty printed
					for i, item := range items {
						// Special handling: if item is string representation of json (the Alien Format cause), try to parse it
						if str, ok := item.(string); ok && (strings.HasPrefix(str, "{") || strings.HasPrefix(str, "[")) {
							var nested any
							if err := json.Unmarshal([]byte(str), &nested); err == nil {
								item = nested
							}
						}

						b, _ := json.MarshalIndent(item, "", "  ")
						report.Write(b)
						if i < len(items)-1 {
							report.WriteString("\n")
						}
					}
					report.WriteString("\n\n")
				}

				if report.Len() > 0 {
					return report.String(), true, nil
				}
			}
		}

		return sb.String(), true, nil
	}

	if strings.HasPrefix(query, "/delete ") {
		parts := strings.Fields(strings.TrimPrefix(query, "/delete "))
		if len(parts) == 0 {
			return "Error: Script name required", true, nil
		}
		name := parts[0]
		category := ai.DefaultScriptCategory

		for i := 1; i < len(parts); i++ {
			if parts[i] == "--category" && i+1 < len(parts) {
				category = parts[i+1]
				i++
			}
		}

		scriptDB := s.getScriptDB()
		if scriptDB == nil {
			return "Error: No database configured", true, nil
		}

		tx, err := scriptDB.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			return fmt.Sprintf("Error starting transaction: %v", err), true, nil
		}

		store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
		if err != nil {
			tx.Rollback(ctx)
			return fmt.Sprintf("Error opening store: %v", err), true, nil
		}

		var dummy ai.Script
		if err := store.Load(ctx, category, name, &dummy); err != nil {
			tx.Rollback(ctx)
			return fmt.Sprintf("Error: Script %q (Category: %s) not found.", name, category), true, nil
		}

		if err := store.Delete(ctx, category, name); err != nil {
			tx.Rollback(ctx)
			return fmt.Sprintf("Error deleting script: %v", err), true, nil
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Sprintf("Error committing transaction: %v", err), true, nil
		}
		return fmt.Sprintf("Script %q (Category: %s) deleted.", name, category), true, nil
	}

	// /delete_step <script> <index>
	if strings.HasPrefix(query, "/delete_step ") {
		parts := strings.Fields(strings.TrimPrefix(query, "/delete_step "))
		if len(parts) < 2 {
			return "Usage: /delete_step <script_name> <index> [--category <cat>]", true, nil
		}
		name := parts[0]
		idxStr := parts[1]
		category := ai.DefaultScriptCategory

		for i := 2; i < len(parts); i++ {
			if parts[i] == "--category" && i+1 < len(parts) {
				category = parts[i+1]
				i++
			}
		}

		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			return "Error: Index must be a number.", true, nil
		}
		idx-- // 1-based to 0-based

		scriptDB := s.getScriptDB()
		if scriptDB == nil {
			return "Error: No database configured", true, nil
		}
		tx, err := scriptDB.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			return fmt.Sprintf("Error starting transaction: %v", err), true, nil
		}
		defer tx.Rollback(ctx)

		store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
		if err != nil {
			return fmt.Sprintf("Error opening store: %v", err), true, nil
		}

		var script ai.Script
		if err := store.Load(ctx, category, name, &script); err != nil {
			return fmt.Sprintf("Error loading script: %v", err), true, nil
		}

		if idx < 0 || idx >= len(script.Steps) {
			return fmt.Sprintf("Error: Index %d out of bounds (1-%d).", idx+1, len(script.Steps)), true, nil
		}

		// Remove step
		script.Steps = append(script.Steps[:idx], script.Steps[idx+1:]...)

		if err := store.Save(ctx, category, name, &script); err != nil {
			return fmt.Sprintf("Error saving script: %v", err), true, nil
		}
		tx.Commit(ctx)
		return fmt.Sprintf("Step %d deleted from script %q.", idx+1, name), true, nil
	}

	// /update_step <script> <index> <new_instruction>
	if strings.HasPrefix(query, "/update_step ") {
		// Args: name index instruction...
		// But we need to handle flag support too? Complex parsing.
		// Let's assume standard field splitting then reconstruction of the message.
		parts := strings.Fields(strings.TrimPrefix(query, "/update_step "))
		if len(parts) < 3 {
			return "Usage: /update_step <script_name> <index> <new_instruction>", true, nil
		}
		name := parts[0]
		idxStr := parts[1]

		// Reconstruct instruction from remaining parts (and handle category flag if strictly needed, but let's assume default for simplicity with this signature)
		// To be robust: Check for --category in parts first.
		category := ai.DefaultScriptCategory
		instructionParts := []string{}

		skipNext := false
		for i := 2; i < len(parts); i++ {
			if skipNext {
				skipNext = false
				continue
			}
			if parts[i] == "--category" && i+1 < len(parts) {
				category = parts[i+1]
				skipNext = true
				continue
			}
			instructionParts = append(instructionParts, parts[i])
		}
		instruction := strings.Join(instructionParts, " ")

		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			return "Error: Index must be a number.", true, nil
		}
		idx-- // 1-based

		scriptDB := s.getScriptDB()
		if scriptDB == nil {
			return "Error: No database configured", true, nil
		}
		tx, err := scriptDB.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			return fmt.Sprintf("Error starting transaction: %v", err), true, nil
		}
		defer tx.Rollback(ctx)

		store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
		if err != nil {
			return fmt.Sprintf("Error opening store: %v", err), true, nil
		}

		var script ai.Script
		if err := store.Load(ctx, category, name, &script); err != nil {
			return fmt.Sprintf("Error loading script: %v", err), true, nil
		}

		if idx < 0 || idx >= len(script.Steps) {
			return fmt.Sprintf("Error: Index %d out of bounds (1-%d).", idx+1, len(script.Steps)), true, nil
		}

		// Update step prompt/message
		script.Steps[idx].Prompt = instruction
		script.Steps[idx].Message = instruction
		// If it was a 'command' type, we leave it as is? Or do we convert to 'ask' if the user types natural language?
		// For now, simple text update.

		if err := store.Save(ctx, category, name, &script); err != nil {
			return fmt.Sprintf("Error saving script: %v", err), true, nil
		}
		tx.Commit(ctx)
		return fmt.Sprintf("Step %d updated in script %q.", idx+1, name), true, nil
	}

	// /reorder_steps <script> <from> <to>
	if strings.HasPrefix(query, "/reorder_steps ") {
		parts := strings.Fields(strings.TrimPrefix(query, "/reorder_steps "))
		if len(parts) < 3 {
			return "Usage: /reorder_steps <script_name> <from_index> <to_index> [--category <cat>]", true, nil
		}
		name := parts[0]
		fromDefault := "-1"
		toDefault := "-1"

		category := ai.DefaultScriptCategory

		// Parsing is tricky with mixed args.
		// Expected: name from to [flags]
		if len(parts) >= 3 {
			fromDefault = parts[1]
			toDefault = parts[2]
		}

		for i := 3; i < len(parts); i++ {
			if parts[i] == "--category" && i+1 < len(parts) {
				category = parts[i+1]
				i++
			}
		}

		fromIdx, err1 := strconv.Atoi(fromDefault)
		toIdx, err2 := strconv.Atoi(toDefault)
		if err1 != nil || err2 != nil {
			return "Error: Indices must be numbers.", true, nil
		}
		fromIdx-- // 1-based
		toIdx--   // 1-based

		scriptDB := s.getScriptDB()
		if scriptDB == nil {
			return "Error: No database configured", true, nil
		}
		tx, err := scriptDB.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			return fmt.Sprintf("Error starting transaction: %v", err), true, nil
		}
		defer tx.Rollback(ctx)

		store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
		if err != nil {
			return fmt.Sprintf("Error opening store: %v", err), true, nil
		}

		var script ai.Script
		if err := store.Load(ctx, category, name, &script); err != nil {
			return fmt.Sprintf("Error loading script: %v", err), true, nil
		}

		if fromIdx < 0 || fromIdx >= len(script.Steps) || toIdx < 0 || toIdx >= len(script.Steps) {
			return fmt.Sprintf("Error: Indices out of bounds (1-%d).", len(script.Steps)), true, nil
		}

		// Reorder
		step := script.Steps[fromIdx]
		// Remove
		script.Steps = append(script.Steps[:fromIdx], script.Steps[fromIdx+1:]...)
		// Insert
		if toIdx >= len(script.Steps) {
			script.Steps = append(script.Steps, step)
		} else {
			script.Steps = append(script.Steps[:toIdx], append([]ai.ScriptStep{step}, script.Steps[toIdx:]...)...)
		}

		if err := store.Save(ctx, category, name, &script); err != nil {
			return fmt.Sprintf("Error saving script: %v", err), true, nil
		}
		tx.Commit(ctx)
		return fmt.Sprintf("Step moved from %d to %d in script %q.", fromIdx+1, toIdx+1, name), true, nil
	}

	// Data Operations as Slash Commands

	// /list_databases
	if strings.HasPrefix(query, "/list_databases") {
		// Call to registry? No registry in s.session. We need s.agent or just use system tools.
		// But Service wraps CopilotAgent?
		// We have access to db via 'db' arg.
		// Let's implement directly for speed, or call the tool if we can.
		// Service has no direct access to agent registry from here easily without exposing it.
		// But we have s as *Service. s has Agents? No.
		// This is a "Service" which might be running an Agent.
		// But this method 'handleSessionCommand' is on Service.
		// Let's implement simple queries directly.

		// However, CopilotAgent is where these are defined.
		// To be DRY, we should invoke the tool.
		// But handleSessionCommand is called BEFORE tool selection.
		// If we return handled=true, we handle it.

		// Let's implement the basic view logic here.
		// Actually, we can assume db is the system DB (which contains registry of DBs if any?)
		// Wait, list_databases usually lists from config or connected sessions?
		// In SOP, databases are folders in the data path.
		// We need the data path.
		// The simplest way: Ask the LLM to do it? No, user wants NO LLM.
		// We need to implement the logic.
		// In copilot.go: toolListDatabases lists subdirectories of registry.RegistryPath or similar.

		// For now, let's just list what we can see from the active DB connection if possible.
		// Since we don't have a direct ListDatabases method in the public interface exposed here easily,
		// and Listing databases involves scanning the data root which might not be exposed in `db` struct.
		// We will punt on this for now or use a hardcoded check if we knew the root.

		return "Command /list_databases is limited in this context. Use system tools or LLM to list all available databases.", true, nil
	}

	// /list_stores [database]
	if strings.HasPrefix(query, "/list_stores") {
		// List stores in CURRENT db
		if db == nil {
			return "Error: No active database connection.", true, nil
		}

		// db.ListStores does not exist on the simplified struct?
		// We can try to list files in the database path if we can access it.
		// db.StoragePath() is available?
		// Actually, `db` is *database.Database.
		// Looking at usage, we don't see ListStores.
		// But valid stores are registered in the Store Repository.
		// In B-Tree mode, they are files.

		// Let's rely on standard listing if available, or just fail gracefully if not.
		// For now:
		// We can't easily list stores without a transaction and registry lookup,
		// OR filesystem scan.
		// db.Config() returns options.

		return "Error: listing stores via slash command is not yet fully linked to storage registry.", true, nil
	}

	// /select <store> [limit=10] [filter=json]
	// Simplifying /select to just a dump for now, or basic JSON filter.
	if strings.HasPrefix(query, "/select ") {
		parts := strings.Fields(strings.TrimPrefix(query, "/select "))
		if len(parts) == 0 {
			return "Usage: /select <store_name> [limit=N] [filter=json]", true, nil
		}
		storeName := parts[0]
		limit := 10

		// Parse rudimentary args
		for i := 1; i < len(parts); i++ {
			if strings.HasPrefix(parts[i], "limit=") {
				l, _ := strconv.Atoi(strings.TrimPrefix(parts[i], "limit="))
				if l > 0 {
					limit = l
				}
			}
		}

		if db == nil {
			return "Error: No active database connection.", true, nil
		}

		tx, err := db.BeginTransaction(ctx, sop.ForReading)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true, nil
		}
		defer tx.Rollback(ctx)

		// OpenBtree instead of OpenStore
		store, err := db.OpenBtree(ctx, storeName, tx)
		if err != nil {
			return fmt.Sprintf("Error opening store %q: %v", storeName, err), true, nil
		}

		// Simple scan using cursor methods
		results := []string{}
		count := 0

		if ok, err := store.First(ctx); ok && err == nil {
			for {
				if count >= limit {
					break
				}
				// GetCurrentKey returns the Item object, no error, no context
				item := store.GetCurrentKey()
				k := item.Key

				v, _ := store.GetCurrentValue(ctx)
				results = append(results, fmt.Sprintf("%v: %v", k, v))
				count++

				if ok, err := store.Next(ctx); !ok || err != nil {
					break
				}
			}
		}

		return fmt.Sprintf("Top %d records from %q:\n%s", limit, storeName, strings.Join(results, "\n")), true, nil
	}

	// /add <store> <key> <value>
	if strings.HasPrefix(query, "/add ") {
		parts := strings.Fields(strings.TrimPrefix(query, "/add "))
		if len(parts) < 3 {
			return "Usage: /add <store> <key> <value>", true, nil
		}
		storeName := parts[0]
		key := parts[1]
		// Value is the rest
		valStr := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(query, "/add "+storeName), key))

		if db == nil {
			return "Error: No active database connection.", true, nil
		}

		tx, err := db.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true, nil
		}
		defer tx.Rollback(ctx)

		store, err := db.OpenBtree(ctx, storeName, tx)
		if err != nil {
			return fmt.Sprintf("Error opening store: %v", err), true, nil
		}

		// Try to unmarshal value as JSON, else use string
		var valObj any
		if err := json.Unmarshal([]byte(valStr), &valObj); err != nil {
			valObj = valStr
		}

		if _, err := store.Add(ctx, key, valObj); err != nil {
			return fmt.Sprintf("Error adding record: %v", err), true, nil
		}
		tx.Commit(ctx)
		return fmt.Sprintf("Record added to %q.", storeName), true, nil
	}

	// /update <store> <key> <value>
	if strings.HasPrefix(query, "/update ") {
		parts := strings.Fields(strings.TrimPrefix(query, "/update "))
		if len(parts) < 3 {
			return "Usage: /update <store> <key> <value>", true, nil
		}
		storeName := parts[0]
		key := parts[1]
		// Value is the rest
		valStr := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(query, "/update "+storeName), key))

		if db == nil {
			return "Error: No active database connection.", true, nil
		}

		tx, err := db.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true, nil
		}
		defer tx.Rollback(ctx)

		store, err := db.OpenBtree(ctx, storeName, tx)
		if err != nil {
			return fmt.Sprintf("Error opening store: %v", err), true, nil
		}

		// Try to unmarshal value as JSON, else use string
		var valObj any
		if err := json.Unmarshal([]byte(valStr), &valObj); err != nil {
			valObj = valStr
		}

		if _, err := store.Update(ctx, key, valObj); err != nil {
			return fmt.Sprintf("Error updating record: %v", err), true, nil
		}
		tx.Commit(ctx)
		return fmt.Sprintf("Record updated in %q.", storeName), true, nil
	}

	// /delete_record <store> <key>
	if strings.HasPrefix(query, "/delete_record ") {
		parts := strings.Fields(strings.TrimPrefix(query, "/delete_record "))
		if len(parts) < 2 {
			return "Usage: /delete_record <store> <key>", true, nil
		}
		storeName := parts[0]
		key := parts[1]

		if db == nil {
			return "Error: No active database connection.", true, nil
		}

		tx, err := db.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), true, nil
		}
		defer tx.Rollback(ctx)

		store, err := db.OpenBtree(ctx, storeName, tx)
		if err != nil {
			return fmt.Sprintf("Error opening store: %v", err), true, nil
		}

		if _, err := store.Remove(ctx, key); err != nil {
			return fmt.Sprintf("Error deleting record: %v", err), true, nil
		}
		tx.Commit(ctx)
		return fmt.Sprintf("Record %q deleted from %q.", key, storeName), true, nil
	}

	if strings.HasPrefix(query, "/list") {
		args := strings.Fields(query)
		category := ai.DefaultScriptCategory
		for i := 1; i < len(args); i++ {
			if args[i] == "--category" && i+1 < len(args) {
				category = args[i+1]
				i++
			}
		}

		scriptDB := s.getScriptDB()
		if scriptDB == nil {
			return "Error: No database configured", true, nil
		}

		tx, err := scriptDB.BeginTransaction(ctx, sop.ForReading)
		if err != nil {
			return fmt.Sprintf("Error starting transaction: %v", err), true, nil
		}

		store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
		if err != nil {
			tx.Rollback(ctx)
			return fmt.Sprintf("Error opening store: %v", err), true, nil
		}

		names, err := store.List(ctx, category)
		if err != nil {
			tx.Rollback(ctx)
			return fmt.Sprintf("Error listing scripts: %v", err), true, nil
		}
		tx.Commit(ctx)

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Available Scripts (Category: %s):\n", category))
		for _, n := range names {
			sb.WriteString(fmt.Sprintf("- %s\n", n))
		}
		return sb.String(), true, nil
	}

	return "", false, nil
}

func (s *Service) saveDraft(ctx context.Context) error {
	if s.session.CurrentScript == nil {
		return fmt.Errorf("no active script draft")
	}
	if len(s.session.CurrentScript.Steps) == 0 && len(s.session.LastInteractionToolCalls) > 0 {
		bootstrapped := make([]ai.ScriptStep, len(s.session.LastInteractionToolCalls))
		copy(bootstrapped, s.session.LastInteractionToolCalls)
		s.session.CurrentScript.Steps = RefineScriptSteps(bootstrapped)
	}
	scriptDB := s.getScriptDB()
	if scriptDB == nil {
		return fmt.Errorf("no database configured")
	}

	tx, err := scriptDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return fmt.Errorf("transaction start failed: %w", err)
	}

	store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Errorf("store open failed: %w", err)
	}

	if err := store.Save(ctx, s.session.CurrentScriptCategory, s.session.CurrentScriptName, *s.session.CurrentScript); err != nil {
		tx.Rollback(ctx)
		return fmt.Errorf("save failed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}
	return nil
}

// parseCLIArgs parses a command line string into arguments, respecting quotes.
func parseCLIArgs(input string) []string {
	var args []string
	var current strings.Builder
	var inQuote rune
	var escaped bool
	var inToken bool

	for _, r := range input {
		if escaped {
			current.WriteRune(r)
			escaped = false
			inToken = true
			continue
		}

		if r == '\\' {
			escaped = true
			inToken = true
			continue
		}

		if inQuote != 0 {
			if r == inQuote {
				inQuote = 0
			} else {
				current.WriteRune(r)
			}
			inToken = true
			continue
		}

		if r == '"' || r == '\'' {
			inQuote = r
			inToken = true
			continue
		}

		if unicode.IsSpace(r) {
			if inToken {
				args = append(args, current.String())
				current.Reset()
				inToken = false
			}
			continue
		}

		current.WriteRune(r)
		inToken = true
	}

	if inToken {
		args = append(args, current.String())
	}

	return args
}
