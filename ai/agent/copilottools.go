package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	log "log/slog"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/jsondb"
)

const (
	ExecuteScriptInstruction = "Execute a full ordered JSON AST under script for multi-step store operations. Each step should be an object such as {op, args?, input_var?, result_var?}. " +
		"Focus on orchestration semantics: begin a transaction, read or mutate stores, then commit or rollback. begin_tx defines the durability boundary for the workflow, so use it when related mutations must persist or roll back together. " +
		"For larger mutation runs, batch deliberately under explicit commits, with a practical default of about 50 to 250 CRUD operations per transaction unless business atomicity requires a different boundary. Chain multi-step reads with result_var/input_var. " +
		"Use list_stores to research stores before multi-store joins or whenever schema is uncertain. Prefer scoped calls such as stores:[\"users\",\"users_orders\",\"orders\"] so research stays compact on large databases. list_stores returns stores:[{name,schema,key_fields,value_fields,description,relations,empty}]. " +
		"When you fill args.condition or any predicate object, write the condition expression the engine should execute and assign the concrete comparison value directly. " +
		"Predicate format: for single-store operations (select, scan with filter), use bare field names like \"first_name\". After joins, use store-qualified paths like \"orders.total_amount\". " +
		"Read store.schema for field names and types (e.g., {\"key\": \"string\", \"first_name\": \"string\", \"age\": \"number\"}). Match types exactly: string values in quotes, numbers as numbers. " +
		"Relations map joins using schema field names. source_fields and target_fields reference fields from the schema. " +
		"Example: Find orders for users with first_name 'John' where total_amount > 500. Call list_stores([\"users\",\"users_orders\",\"orders\"]). Read schemas: users has first_name field, orders has total_amount field. Build: {\"script\":[{\"op\":\"begin_tx\",\"args\":{\"mode\":\"read\"},\"result_var\":\"tx\"},{\"op\":\"open_store\",\"args\":{\"transaction\":\"tx\",\"name\":\"users\"},\"result_var\":\"users_store\"},{\"op\":\"select\",\"args\":{\"store\":\"users_store\",\"condition\":{\"first_name\":{\"$eq\":\"John\"}}},\"result_var\":\"matched_users\"},{\"op\":\"join\",\"input_var\":\"matched_users\",\"args\":{\"target\":\"users_orders_store\",\"relation\":\"users_orders\"},\"result_var\":\"user_order_links\"},{\"op\":\"join\",\"input_var\":\"user_order_links\",\"args\":{\"target\":\"orders_store\",\"relation\":\"orders\"},\"result_var\":\"joined_orders\"},{\"op\":\"filter\",\"input_var\":\"joined_orders\",\"args\":{\"condition\":{\"orders.total_amount\":{\"$gt\":500}}},\"result_var\":\"filtered_orders\"},{\"op\":\"return\",\"input_var\":\"filtered_orders\"}]}. Do not emit booleans like {\"first_name\":true}. " +
		"Prefer relation + target for join repair instead of inventing a fresh on mapping; if on is still needed, rewrite only the invalid join slice by translating the confirmed relation into the exact concrete field mapping the join op expects, and never use store names where field paths are required. join and join_right emit a combined flat record by default, so reuse dotted store-qualified field paths unless a later project step intentionally reshapes the output. If the AST shape is ambiguous, call gettoolinfo('execute_script') and continue with concrete predicate objects, concrete join mappings, and boolean placeholders removed."
	ListStoresInstruction = "Research store structure before writing multi-store reads or repairs. Pass stores:[...] to scope the response to likely targets, and infer likely store names from the user's ask instead of leaving stores empty when obvious candidates are available. " +
		"The tool can narrow close singular/plural matches internally, but you should still pass the most likely store names you can infer. The result is a JSON object with stores:[{name,schema,key_fields,value_fields,description,relations,empty}]. " +
		"Each store.schema maps field names to types (e.g., {\"key\": \"string\", \"first_name\": \"string\", \"age\": \"number\"}). " +
		"When building predicates: for single-store operations, use bare field names like \"first_name\". After joins, use store-qualified names like \"orders.total_amount\". " +
		"Relations map store-to-store joins using schema field names. source_fields and target_fields reference fields from the schema. " +
		"Example: Find orders for users with first_name 'John' where total_amount > 500. Call list_stores with [\"users\", \"users_orders\", \"orders\"]. Read schemas: users has first_name, orders has total_amount. Build predicates: {\"first_name\": {\"$eq\": \"John\"}} for single-store filter, {\"orders.total_amount\": {\"$gt\": 500}} after joins."
)

const emptyObjectArgsSchema = `{"type":"object","properties":{}}`

const listStoresArgsSchema = `{"type":"object","properties":{"database":{"type":"string","description":"Optional database override. Defaults to the active session database."},"stores":{"type":"array","description":"Optional likely store names to research. Infer likely targets from the user's ask and pass them here, for example [\"users\",\"users_orders\",\"orders\"], to keep research compact instead of listing the whole database. Close singular/plural forms are narrowed internally, but explicit likely names are preferred.","items":{"type":"string"}}}}`

type listStoresPayload struct {
	Database string             `json:"database,omitempty"`
	Stores   []listStorePayload `json:"stores"`
}

type listStorePayload struct {
	Name        string            `json:"name"`
	Schema      map[string]string `json:"schema,omitempty"`       // Flat schema without prefixes
	KeyFields   []string          `json:"key_fields,omitempty"`   // Field names in the Key
	ValueFields []string          `json:"value_fields,omitempty"` // Field names in the Value
	Description string            `json:"description,omitempty"`
	Relations   []sop.Relation    `json:"relations,omitempty"`
	Empty       bool              `json:"empty,omitempty"`
}

const (
	SelectInstruction                   = "Read or mutate one store directly when you do not need a multi-step AST. Provide the store name plus optional key/value criteria, fields, limit, and direction. For mutations set action=delete or action=update and include grounded update_values instead of placeholder objects. This tool still executes inside a transaction: it reuses an explicit transaction when one is active, otherwise it opens and auto-commits its own local transaction. For clear chained reads, prefer native pipeline tools such as begin_tx, open_store, scan, filter, join_right, and project before falling back to execute_script. Reserve execute_script for branches, loops, or larger AST orchestration."
	JoinInstruction                     = "Join two stores directly when the join fields are already grounded. Provide left_store, right_store, aligned join field arrays, and optional fields/limit/direction. For chained native reads, prefer begin_tx/open_store/scan/join_right so each step can pipe into the next call. Use execute_script only when you need a whole AST, branching, or richer orchestration."
	ExplainJoinInstruction              = "Preview how a join will execute before running it. Provide the target right_store and a grounded on mapping to see whether the engine can use an index scan or will fall back to a full scan. This is a single read-oriented operation and will use a local read transaction when no explicit transaction is active. Use this after list_stores research when join-key selection or performance is still uncertain."
	AddInstruction                      = "Insert one record into a store by providing store, key, and value. Use this for single-record writes; use execute_script when the write must be part of a larger transaction or multi-step flow. This tool reuses an explicit transaction when one is active, otherwise it opens and auto-commits its own local write transaction."
	UpdateInstruction                   = "Replace or update one record in a store by key. Provide the exact store, key, and value payload. Use execute_script when the update depends on prior reads or must participate in a broader transaction. This tool reuses an explicit transaction when one is active, otherwise it opens and auto-commits its own local write transaction."
	DeleteInstruction                   = "Delete one record from a store by exact key. Use execute_script when deletion depends on researched predicates, joins, or transaction orchestration rather than a single known key. This tool reuses an explicit transaction when one is active, otherwise it opens and auto-commits its own local write transaction."
	ManageTransactionInstruction        = "Control a transaction directly with action=begin, commit, or rollback. This is where native store tool calls establish an explicit durability boundary: the same transaction governs which related mutations persist together and which roll back together. For larger mutation runs, prefer deliberate batching under explicit commits, with a practical default of about 50 to 250 CRUD operations per transaction unless business atomicity requires a different boundary. Use this for explicit transaction control around direct native store tools. For clear chained reads, the native pipeline tools begin_tx/open_store/scan/filter/join_right/project are preferred before execute_script. Reserve execute_script for branches, loops, or larger AST orchestration."
	MintToSpaceInstruction              = "Store durable generated or discovered knowledge in a Space for future retrieval. Provide the exact kb_name the user asked for plus the content to persist; optional category groups related entries. Use this for facts, notes, solutions, or generated content that should persist beyond the current chat. Do not replace it with an external import workflow, and do not wrap it in begin_tx or commit_tx because mint_to_space manages its own transaction."
	DeleteSpaceInstruction              = "Delete an entire Space and all of its stored knowledge. Use only when the user explicitly wants the whole knowledge base removed, not when they only want to change content or configuration. Provide the exact kb_name to remove, and do not wrap delete_space in begin_tx or commit_tx because it runs through its own deletion path."
	EnrichSpaceInstruction              = "Run the Space enrichment pipeline so stored items can be normalized, linked, or expanded by the knowledge workflow. Use this after meaningful content changes only when the user explicitly wants derived knowledge refreshed or enrichment rerun; it is not the default follow-up to every mint or config change."
	UpdateSpaceConfigInstruction        = "Change Space-level configuration such as routing, system prompts, persona behavior, or enabled tool access. Provide the exact kb_name and a grounded config object with the intended settings. Read the current config first when you need to inspect or preserve existing values instead of guessing a partial patch from natural language alone."
	ReadSpaceConfigInstruction          = "Read the current configuration for a Space before changing it or when the user asks how the Space behaves. Use this to inspect routing rules, system prompts, persona settings, and enabled tool access for the target kb_name so later updates stay grounded."
	VectorizeSpaceInstruction           = "Generate or refresh embeddings for every eligible item in a Space. Use this only when the user explicitly asks for vectorization, embeddings, semantic refresh, or full reindexing of the whole knowledge base; do not call it automatically after normal content writes."
	VectorizeSpaceCategoriesInstruction = "Generate or refresh embeddings for specific categories within a Space. Provide kb_name and categories when the user wants semantic refresh for selected sections instead of the whole Space, and prefer this over full-space vectorization when the request is narrower."
	VectorizeSpaceItemsInstruction      = "Generate or refresh embeddings for specific items inside a Space category. Provide kb_name, category, and item_names when the refresh should stay tightly scoped to known changed items, and prefer this over category-wide or full-space vectorization when possible."
)

// registerSystemTools registers the core system inspection tools.
func (a *CopilotAgent) registerSystemTools(ctx context.Context) {
	a.registry.Register("list_databases", "Lists all available databases.", emptyObjectArgsSchema, wrapStringTool(a.toolListDatabases))
	a.registry.Register("list_stores", ListStoresInstruction, listStoresArgsSchema, wrapStringTool(a.toolListStores))
	a.registry.Register("list_tools", "Lists all available tools and their usage instructions.", emptyObjectArgsSchema, wrapStringTool(a.toolListTools))
	a.registry.Register("set_verbose", "Toggle session verbosity for follow-up tool results and progress streaming.", `{"type":"object","properties":{"verbose":{"type":"boolean","description":"Set verbosity on or off. If omitted, the current setting is toggled."}}}`, wrapStringTool(a.toolSetVerbose))
}

// registerTools registers all available tools for the CopilotAgent.
func (a *CopilotAgent) registerTools(ctx context.Context) {
	log.Debug("CopilotAgent.registerTools: Starting registration...")
	if a.registry == nil {
		a.registry = NewRegistry()
	}

	a.registerSystemTools(ctx)
	a.registerStoresTools(ctx)
	a.registerAtomicTools()
	a.registerSpaceTools(ctx)
	a.registerScriptTools(ctx)
	a.registerRoutingTools(ctx)
	a.registerAutomationTools(ctx)

}

func (a *CopilotAgent) toolListTools(ctx context.Context, args map[string]any) (string, error) {
	tools := a.registry.List()

	// Sort tools by name for consistent output
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})

	var sb strings.Builder
	// Note: Header is handled by the caller or omitted to allow clean concatenation
	// But since this tool can be called independently by the agent, we should perhaps skip heavy headers if it's meant to be a simple list.
	// However, the prompt says "it should display complete set". I'll just list them.

	for _, t := range tools {
		if t.Hidden {
			continue
		}

		// Use ShortDescription if available for UI brevity, otherwise fallback and clean Description
		var desc string
		if t.ShortDescription != "" {
			desc = t.ShortDescription
		} else {
			desc = strings.ReplaceAll(t.Description, "\n", " ")
			// Limit to the first sentence to keep the UI clean (hide heavy LLM instructions)
			if idx := strings.Index(desc, "."); idx > 0 {
				desc = desc[:idx+1]
			}
			if len(desc) > 150 {
				desc = desc[:147] + "..."
			}
		}

		// Simplify ArgsSchema for CLI display
		argsSchema := t.ArgsSchema
		var simpleArgs []string
		if strings.HasPrefix(strings.TrimSpace(argsSchema), "{") {
			var schema map[string]any
			if err := json.Unmarshal([]byte(argsSchema), &schema); err == nil {
				if props, ok := schema["properties"].(map[string]any); ok {
					names := make([]string, 0, len(props))
					for name := range props {
						names = append(names, name)
					}
					sort.Strings(names)
					for _, name := range names {
						simpleArgs = append(simpleArgs, "<"+name+">")
					}
				}
			}
		} else {
			argsSchema = strings.TrimPrefix(argsSchema, "(")
			argsSchema = strings.TrimSuffix(argsSchema, ")")
			if len(argsSchema) > 0 {
				parts := strings.Split(argsSchema, ",")
				for _, p := range parts {
					argName := strings.Split(p, ":")[0]
					argName = strings.TrimSpace(argName)
					if argName != "" {
						simpleArgs = append(simpleArgs, "<"+argName+">")
					}
				}
			}
		}

		prettyArgs := strings.Join(simpleArgs, " ")

		cmdStr := fmt.Sprintf("/%s", t.Name)
		if prettyArgs != "" {
			cmdStr += " " + prettyArgs
		}

		sb.WriteString(fmt.Sprintf("- `%s`: %s\n", cmdStr, desc))
	}

	return sb.String(), nil
}

func (a *CopilotAgent) toolSetVerbose(ctx context.Context, args map[string]any) (string, error) {
	rs := runnerSessionFromContext(ctx)
	if rs == nil {
		return "", fmt.Errorf("no runner session found")
	}
	current := rs.Verbose

	newState := !current
	if v, ok := args["verbose"].(bool); ok {
		newState = v
	} else if v, ok := args["enabled"].(bool); ok {
		newState = v
	}

	rs.SetVerbose(newState)
	systemDB := a.systemDB
	if systemDB == nil && a.service != nil {
		systemDB = a.service.systemDB
	}
	if err := persistPreference(ctx, systemDB, ai.GetSessionPayload(ctx), memory.NewBoolPreference(memory.PreferenceKeyVerbose, newState)); err != nil {
		log.Warn("Failed to persist verbose preference", "error", err)
	}

	status := "OFF"
	if newState {
		status = "ON"
	}
	return fmt.Sprintf("Session verbosity set to %s.", status), nil
}

func (a *CopilotAgent) toolListDatabases(ctx context.Context, args map[string]any) (string, error) {
	var names []string
	for k := range a.databases {
		names = append(names, k)
	}
	sort.Strings(names)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Databases: %s", strings.Join(names, ", ")))

	if a.systemDB != nil {
		sb.WriteString("\nSystem Database: system")
	}

	return sb.String(), nil
}

func (a *CopilotAgent) toolListStores(ctx context.Context, args map[string]any) (string, error) {
	p := ai.GetSessionPayload(ctx)
	if p == nil {
		return "", fmt.Errorf("no session payload found")
	}

	requestedStores := make(map[string]struct{})
	if rawStores, ok := args["stores"].([]any); ok {
		for _, raw := range rawStores {
			if name, ok := raw.(string); ok && strings.TrimSpace(name) != "" {
				requestedStores[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
			}
		}
	} else if rawStores, ok := args["stores"].([]string); ok {
		for _, name := range rawStores {
			if strings.TrimSpace(name) != "" {
				requestedStores[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
			}
		}
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

	if db == nil {
		// Debugging
		var keys []string
		for k := range a.databases {
			keys = append(keys, k)
		}
		return "", fmt.Errorf("database not found or not selected. Requested: '%s', Available: %v", dbName, keys)
	}

	// Need transaction
	var tx sop.Transaction
	var autoCommit bool

	if p.Transaction != nil {
		if t, ok := p.Transaction.(sop.Transaction); ok {
			tx = t
		}
	}
	if tx == nil {
		// If no transaction in payload, try to start one on the DB
		var err error
		tx, err = db.BeginTransaction(ctx, sop.ForReading)
		if err != nil {
			return "", fmt.Errorf("failed to begin transaction: %w", err)
		}
		// Auto-commit if we started it locally
		autoCommit = true
	}
	stores, err := tx.GetPhasedTransaction().GetStores(ctx)
	if err != nil {
		if autoCommit {
			tx.Rollback(ctx)
		}
		return "", fmt.Errorf("failed to list stores: %w", err)
	}
	sort.Strings(stores)
	resolvedStores := resolveListStoresScope(stores, requestedStores, p.CurrentUserQuery)

	payload := listStoresPayload{Database: dbName}
	var descriptions []string

	// Resolve DatabaseOptions for inspection
	var dbOpts sop.DatabaseOptions
	var hasOpts bool

	if dbName != "system" {
		dbOpts, hasOpts = a.databases[dbName]
	}

	for _, sName := range stores {
		if strings.Contains(sName, "/") {
			continue
		}
		if len(resolvedStores) > 0 {
			if _, ok := resolvedStores[strings.ToLower(strings.TrimSpace(sName))]; !ok {
				continue
			}
		}
		storePayload := listStorePayload{Name: sName}
		desc := sName
		if hasOpts {
			// Peek for schema to guide the LLM
			// Use a sub-scope or just open. jsondb.OpenStore is idempotent-ish for the same tx.
			// Note: We ignore errors here because listing stores should succeed even if inspection fails.
			s, err := jsondb.OpenStore(ctx, dbOpts, sName, tx)
			if err == nil {
				info := s.GetStoreInfo()
				storePayload.Description = info.Description
				storePayload.Relations = append([]sop.Relation(nil), info.Relations...)
				var extras string
				if info.Description != "" {
					extras += fmt.Sprintf(" description=\"%s\"", info.Description)
				}
				if len(info.Relations) > 0 {
					rels, _ := json.Marshal(info.Relations)
					extras += fmt.Sprintf(" relations=%s", string(rels))
				}

				// Prefer stored schema from StoreInfo
				if len(info.Schema) > 0 {
					storePayload.Schema = info.Schema
					storePayload.KeyFields = info.KeyFields
					storePayload.ValueFields = info.ValueFields
					desc = fmt.Sprintf("%s schema=%s%s", sName, formatSchema(info.Schema), extras)
				} else if ok, _ := s.First(ctx); ok {
					k := s.GetCurrentKey()
					v, _ := s.GetCurrentValue(ctx)
					flat := flattenItem(k, v)
					schema := inferSchema(flat)
					storePayload.Schema = schema
					desc = fmt.Sprintf("%s schema=%s%s", sName, formatSchema(schema), extras)
				} else {
					storePayload.Empty = true
					desc = fmt.Sprintf("%s (empty store)%s", sName, extras)
				}
			}
		}
		payload.Stores = append(payload.Stores, storePayload)
		descriptions = append(descriptions, desc)
	}
	if len(requestedStores) > 0 && len(descriptions) == 0 {
		return "", fmt.Errorf("requested stores not found in database '%s'", dbName)
	}

	if autoCommit {
		if err := tx.Commit(ctx); err != nil {
			return "", fmt.Errorf("failed to commit transaction: %w", err)
		}
	}
	resultBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal list_stores payload: %w", err)
	}
	result := string(resultBytes)
	if nativeToolHintsEnabled(ctx) {
		return wrapToolResultWithListStoresHint(result, descriptions), nil
	}
	return result, nil
}

func resolveListStoresScope(availableStores []string, requestedStores map[string]struct{}, userQuery string) map[string]struct{} {
	if matches := matchRequestedStoreNames(availableStores, requestedStores); len(matches) > 0 {
		return matches
	}
	if len(requestedStores) == 0 {
		return inferStoreNamesFromQuery(availableStores, userQuery)
	}
	return nil
}

func matchRequestedStoreNames(availableStores []string, requestedStores map[string]struct{}) map[string]struct{} {
	if len(requestedStores) == 0 {
		return nil
	}
	matches := make(map[string]struct{})
	for requested := range requestedStores {
		canonicalRequested := canonicalStorePhrase(requested)
		if canonicalRequested == "" {
			continue
		}
		var exact []string
		var close []string
		for _, available := range availableStores {
			if strings.Contains(available, "/") {
				continue
			}
			canonicalAvailable := canonicalStorePhrase(available)
			if canonicalAvailable == canonicalRequested {
				exact = append(exact, strings.ToLower(strings.TrimSpace(available)))
				continue
			}
			availableTokens := canonicalStoreTokens(available)
			requestedTokens := canonicalStoreTokens(requested)
			if len(requestedTokens) == 0 || len(availableTokens) == 0 {
				continue
			}
			if len(requestedTokens) == 1 && len(availableTokens) > 1 {
				continue
			}
			if containsAllCanonicalTokens(availableTokens, requestedTokens) {
				close = append(close, strings.ToLower(strings.TrimSpace(available)))
			}
		}
		selected := exact
		if len(selected) == 0 {
			selected = close
		}
		for _, name := range selected {
			matches[name] = struct{}{}
		}
	}
	if len(matches) == 0 {
		return nil
	}
	return matches
}

func inferStoreNamesFromQuery(availableStores []string, userQuery string) map[string]struct{} {
	queryTokens := canonicalStoreTokens(userQuery)
	if len(queryTokens) == 0 {
		return nil
	}
	querySet := make(map[string]struct{}, len(queryTokens))
	for _, token := range queryTokens {
		querySet[token] = struct{}{}
	}
	matches := make(map[string]struct{})
	for _, available := range availableStores {
		if strings.Contains(available, "/") {
			continue
		}
		availableTokens := canonicalStoreTokens(available)
		if len(availableTokens) == 0 {
			continue
		}
		allPresent := true
		for _, token := range availableTokens {
			if _, ok := querySet[token]; !ok {
				allPresent = false
				break
			}
		}
		if allPresent {
			matches[strings.ToLower(strings.TrimSpace(available))] = struct{}{}
		}
	}
	if len(matches) == 0 {
		return nil
	}
	return matches
}

func containsAllCanonicalTokens(haystack []string, needles []string) bool {
	if len(needles) == 0 {
		return false
	}
	haystackSet := make(map[string]struct{}, len(haystack))
	for _, token := range haystack {
		haystackSet[token] = struct{}{}
	}
	for _, token := range needles {
		if _, ok := haystackSet[token]; !ok {
			return false
		}
	}
	return true
}

func canonicalStorePhrase(value string) string {
	return strings.Join(canonicalStoreTokens(value), " ")
}

func canonicalStoreTokens(value string) []string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return nil
	}
	var builder strings.Builder
	builder.Grow(len(normalized))
	lastSpace := false
	for _, r := range normalized {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			builder.WriteByte(' ')
			lastSpace = true
		}
	}
	parts := strings.Fields(builder.String())
	if len(parts) == 0 {
		return nil
	}
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		singular := singularStoreToken(part)
		if singular != "" {
			tokens = append(tokens, singular)
		}
	}
	return tokens
}

func singularStoreToken(token string) string {
	if len(token) > 3 && strings.HasSuffix(token, "ies") {
		return strings.TrimSuffix(token, "ies") + "y"
	}
	if len(token) > 2 && strings.HasSuffix(token, "sses") {
		return strings.TrimSuffix(token, "es")
	}
	if len(token) > 2 && strings.HasSuffix(token, "ses") {
		return strings.TrimSuffix(token, "s")
	}
	if len(token) > 1 && strings.HasSuffix(token, "s") && !strings.HasSuffix(token, "ss") {
		return strings.TrimSuffix(token, "s")
	}
	return token
}

func wrapToolResultWithListStoresHint(result string, descriptions []string) string {
	hint := &ai.ToolProgressHint{
		Status:             "progressing",
		CompletionDelta:    0.25,
		Tips:               []string{"Reuse the exact store names, grounded schema fields, and relations from list_stores in the next tool call."},
		SuggestedNextTools: []string{"execute_script"},
	}
	for _, desc := range descriptions {
		trimmed := strings.TrimSpace(desc)
		if trimmed == "" {
			continue
		}
		hint.Clues = append(hint.Clues, "Grounded store info: "+trimmed)
		if len(hint.Clues) == 3 {
			break
		}
	}
	return wrapNativeToolResultEnvelope(result, hint)
}

func nativeToolHintsEnabled(ctx context.Context) bool {
	return ctx.Value(ai.CtxKeyNativeToolHints) == true
}

func wrapNativeTerminalToolResult(ctx context.Context, result string, status string, tips ...string) string {
	if !nativeToolHintsEnabled(ctx) {
		return result
	}
	return wrapNativeToolResultEnvelope(result, &ai.ToolProgressHint{
		Status: strings.TrimSpace(status),
		Tips:   append([]string(nil), tips...),
	})
}

func wrapNativeToolResultEnvelope(result string, hint *ai.ToolProgressHint) string {
	envelope := ai.ToolResultEnvelope{
		ToolResult:   normalizeToolResultPayload(result),
		ProgressHint: hint,
	}
	bytes, err := json.Marshal(envelope)
	if err != nil {
		return result
	}
	return string(bytes)
}

func normalizeToolResultPayload(result string) json.RawMessage {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return json.RawMessage(strconv.Quote(result))
	}
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	return json.RawMessage(strconv.Quote(result))
}

// toolConcludeTopic is a placeholder. The actual logic requires Session access and is handled/overridden in Service.
func (a *CopilotAgent) toolConcludeTopic(ctx context.Context, args map[string]interface{}) (string, error) {
	return "Topic concluded.", nil
}
