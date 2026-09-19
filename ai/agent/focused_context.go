package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/jsondb"
)

var crudOrder = []string{"C", "R", "U", "D"}

func collectCRUDFlags(layers []LayerInfo) map[string]bool {
	flags := make(map[string]bool)
	for _, layer := range layers {
		for _, crud := range layer.CRUD {
			crud = strings.ToUpper(strings.TrimSpace(crud))
			if crud != "" {
				flags[crud] = true
			}
		}
	}
	return flags
}

func orderedCRUDFlags(flags map[string]bool) []string {
	ordered := make([]string, 0, len(flags))
	for _, crud := range crudOrder {
		if flags[crud] {
			ordered = append(ordered, crud)
		}
	}
	return ordered
}

func (a *CopilotAgent) getFocusedExecutionContext(ctx context.Context, taskClassification TaskContextClassification) string {
	if isCrossDomain(taskClassification.Layers) {
		return a.buildCrossDomainFocusedExecutionContext(ctx, taskClassification)
	}

	switch {
	case strings.EqualFold(taskClassification.Domain, StoresDomain):
		return a.buildStoresFocusedExecutionContext(ctx, taskClassification)
	case strings.EqualFold(taskClassification.Domain, SpacesDomain):
		return a.buildSpacesFocusedExecutionContext(taskClassification)
	default:
		return ""
	}
}

func (a *CopilotAgent) buildCrossDomainFocusedExecutionContext(ctx context.Context, taskClassification TaskContextClassification) string {
	storesArtifacts := taskClassification.StoresArtifacts
	if len(storesArtifacts) == 0 && strings.EqualFold(taskClassification.Domain, StoresDomain) {
		storesArtifacts = taskClassification.DBArtifacts
	}

	spacesArtifacts := taskClassification.SpacesArtifacts
	if len(spacesArtifacts) == 0 && strings.EqualFold(taskClassification.Domain, SpacesDomain) {
		spacesArtifacts = taskClassification.DBArtifacts
	}

	storesCtx := a.buildStoresFocusedExecutionContext(ctx, TaskContextClassification{
		Domain:      StoresDomain,
		DBArtifacts: storesArtifacts,
		Layers:      taskClassification.Layers,
	})
	spacesCtx := a.buildSpacesFocusedExecutionContext(TaskContextClassification{
		Domain:      SpacesDomain,
		DBArtifacts: spacesArtifacts,
		Layers:      taskClassification.Layers,
	})

	sections := make([]string, 0, 2)
	if storesCtx != "" {
		sections = append(sections, "Stores Execution Context:\n"+storesCtx)
	}
	if spacesCtx != "" {
		sections = append(sections, "Spaces Execution Context:\n"+spacesCtx)
	}

	return strings.Join(sections, "\n\n")
}

func (a *CopilotAgent) buildStoresFocusedExecutionContext(ctx context.Context, taskClassification TaskContextClassification) string {
	crudFlags := collectCRUDFlags(taskClassification.Layers)
	var sb strings.Builder

	sb.WriteString("Domain: Stores\n")
	if ordered := orderedCRUDFlags(crudFlags); len(ordered) > 0 {
		sb.WriteString(fmt.Sprintf("CRUD Scope: %s\n", strings.Join(ordered, ", ")))
	}

	db, currentDB := a.getActiveSessionDatabase(ctx)
	if currentDB != "" {
		sb.WriteString(fmt.Sprintf("Active Database: %s\n", currentDB))
	}

	if len(taskClassification.DBArtifacts) > 0 {
		sb.WriteString("Target Stores:\n")
		storeCtx := a.describeFocusedStores(ctx, db, taskClassification.DBArtifacts)
		schemaPreGrounded := storeCtx != ""
		if schemaPreGrounded {
			sb.WriteString(storeCtx)
		} else {
			for _, artifact := range taskClassification.DBArtifacts {
				sb.WriteString(fmt.Sprintf("- %s\n", artifact))
			}
		}
		quotedStores := make([]string, 0, len(taskClassification.DBArtifacts))
		for _, artifact := range taskClassification.DBArtifacts {
			artifact = strings.TrimSpace(artifact)
			if artifact == "" {
				continue
			}
			quotedStores = append(quotedStores, fmt.Sprintf("%q", artifact))
		}

		storesHint := ""
		if len(quotedStores) > 0 {
			storesHint = fmt.Sprintf("stores:[%s]", strings.Join(quotedStores, ","))
		}

		if schemaPreGrounded {
			if storesHint != "" {
				sb.WriteString(fmt.Sprintf("Schema pre-grounded. Proceed to execute_script. Verify with %s if needed.\n", storesHint))
			} else {
				sb.WriteString("Schema pre-grounded. Proceed to execute_script.\n")
			}
		} else if storesHint != "" {
			sb.WriteString(fmt.Sprintf("Research Hint: Use %s with list_stores before execute_script if uncertain.\n", storesHint))
		}
	}

	if ops := buildStoresCRUDOperationsContext(crudFlags); ops != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("Relevant Store Operations:\n")
		sb.WriteString(ops)
	}

	if taskClassification.ScriptAuthoring {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("Relevant Script Authoring Operations:\n")
		sb.WriteString(buildScriptAuthoringContext(taskClassification.Domain, crudFlags))
	}

	return strings.TrimSpace(sb.String())
}

func (a *CopilotAgent) buildSpacesFocusedExecutionContext(taskClassification TaskContextClassification) string {
	crudFlags := collectCRUDFlags(taskClassification.Layers)
	var sb strings.Builder

	sb.WriteString("Domain: Spaces\n")
	if ordered := orderedCRUDFlags(crudFlags); len(ordered) > 0 {
		sb.WriteString(fmt.Sprintf("CRUD Scope: %s\n", strings.Join(ordered, ", ")))
	}
	if len(taskClassification.DBArtifacts) > 0 {
		sb.WriteString(fmt.Sprintf("Target Spaces: %s\n", strings.Join(taskClassification.DBArtifacts, ", ")))
	}

	if ops := buildSpacesCRUDOperationsContext(crudFlags); ops != "" {
		sb.WriteString("\nRelevant Space Operations:\n")
		sb.WriteString(ops)
	}

	return strings.TrimSpace(sb.String())
}

func (a *CopilotAgent) getActiveSessionDatabase(ctx context.Context) (*database.Database, string) {
	p := ai.GetSessionPayload(ctx)
	if p == nil || p.CurrentDB == "" {
		return nil, ""
	}
	if p.CurrentDB == SystemDBName {
		return a.systemDB, p.CurrentDB
	}
	if dbOpts, ok := a.databases[p.CurrentDB]; ok {
		return database.NewDatabase(dbOpts), p.CurrentDB
	}
	return nil, p.CurrentDB
}

func (a *CopilotAgent) describeFocusedStores(ctx context.Context, db *database.Database, artifacts []string) string {
	if db == nil || len(artifacts) == 0 {
		return ""
	}

	tx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return ""
	}
	defer tx.Rollback(ctx)

	stores, err := tx.GetStores(ctx)
	if err != nil {
		return ""
	}

	available := make(map[string]string, len(stores))
	for _, store := range stores {
		if strings.Contains(store, "/") {
			continue
		}
		available[strings.ToLower(store)] = store
	}

	var sb strings.Builder
	for _, artifact := range artifacts {
		resolved := strings.TrimSpace(artifact)
		if resolved == "" {
			continue
		}
		if exact, ok := available[strings.ToLower(resolved)]; ok {
			resolved = exact
		}

		// if _, knownDB := a.databases[resolved]; knownDB {
		// 	sb.WriteString(fmt.Sprintf("- %s (database switch; use open_db(\"%s\") before opening stores)\n", artifact, resolved))
		// 	continue
		// }

		storeAccessor, err := jsondb.OpenStore(ctx, db.Config(), resolved, tx)
		if err != nil {
			sb.WriteString(fmt.Sprintf("- %s (not found in active database)\n", artifact))
			continue
		}

		info := storeAccessor.GetStoreInfo()
		parts := make([]string, 0, 4)
		if info.Description != "" {
			parts = append(parts, fmt.Sprintf("description=%q", info.Description))
		}
		// Prefer stored schema from StoreInfo, fallback to runtime inference
		if len(info.Schema) > 0 {
			parts = append(parts, fmt.Sprintf("schema=%s", formatSchema(info.Schema)))
		} else if ok, _ := storeAccessor.First(ctx); ok {
			key := storeAccessor.GetCurrentKey()
			if key != nil {
				if val, err := storeAccessor.GetCurrentValue(ctx); err == nil {
					flat := flattenItem(key, val)
					parts = append(parts, fmt.Sprintf("schema=%s", formatSchema(inferSchema(flat))))
				}
			}
		}
		if len(info.Relations) > 0 {
			rels := make([]string, 0, len(info.Relations))
			for _, rel := range info.Relations {
				rels = append(rels, fmt.Sprintf("[%s] -> %s([%s])", strings.Join(rel.SourceFields, ", "), rel.TargetStore, strings.Join(rel.TargetFields, ", ")))
			}
			sort.Strings(rels)
			parts = append(parts, fmt.Sprintf("Relations: %s", strings.Join(rels, "; ")))
		}
		if info.MapKeyIndexSpecification != "" {
			parts = append(parts, fmt.Sprintf("Key Schema: %s", info.MapKeyIndexSpecification))
		}

		if len(parts) == 0 {
			sb.WriteString(fmt.Sprintf("- %s\n", resolved))
			continue
		}
		sb.WriteString(fmt.Sprintf("- %s %s\n", resolved, strings.Join(parts, " ")))
	}

	return sb.String()
}

func buildStoresCRUDOperationsContext(flags map[string]bool) string {
	sections := make([]string, 0, len(flags))
	dbNote := "- open_db optional; begin_tx uses active DB. If you emit open_db, use the active Current Database name from context."
	if flags["R"] {
		sections = append(sections,
			"- R = Read. Flow: begin_tx(mode=read) -> open_store -> scan/filter/project/sort/limit -> commit_tx.",
			"- Set the target database explicitly.",
			"- Stores plans go inside execute_script.script. Do not display as text.",
			"- Use list_stores to research schema, relations, field types, and field mappings whenever there is uncertainty. Once grounded, call execute_script immediately.",
			dbNote,
			"- Keep filters/joins concrete. Reuse researched schema, relations, and field types. Match predicate values to field types.",
			"- If filter/join rejected, replace only malformed slice.",
		)
	}
	if flags["C"] {
		sections = append(sections,
			"- C = Create. Use write transactions. If key shape, field names, or value shape are uncertain, research them with list_stores first. For multi-step creates, generate a concrete write script and run it with execute_script.",
			"- Set the target database explicitly.",
			dbNote,
		)
	}
	if flags["U"] {
		sections = append(sections,
			"- U = Update. Use write transactions and narrow the target records before update. If field names, predicates, or value shape are uncertain, research them with list_stores first. For multi-step updates, generate a concrete write script and run it with execute_script.",
			"- Set the target database explicitly.",
			dbNote,
		)
	}
	if flags["D"] {
		sections = append(sections,
			"- D = Delete. Use write transactions and narrow the target records before delete. If key fields, filters, or relation mapping are uncertain, research them with list_stores first. For multi-step deletes, generate a concrete write script and run it with execute_script.",
			"- Set the target database explicitly.",
			dbNote,
		)
	}
	if len(sections) == 0 {
		sections = append(sections, "- No CRUD flags. Default: read-first inspection before mutations.")
	}
	return strings.Join(sections, "\n")
}

func buildSpacesCRUDOperationsContext(flags map[string]bool) string {
	sections := make([]string, 0, len(flags))
	if flags["R"] {
		sections = append(sections,
			"- R = Read. Use list_space_categories, list_space_items, search_space, and read_space_config for discovery before asking follow-up questions.",
			"- Note: list_space_categories, list_space_items, search_space, and read_space_config manage their own read transactions. Do NOT wrap these direct Space API tools in begin_tx/commit_tx.",
		)
	}
	if flags["C"] {
		sections = append(sections,
			"- C = Create. Use mint_to_space to add generated content to the target Space.",
			"- Note: When the user asks to generate content and put it into a Space, do not attempt an external import workflow. Generate the content first, then call mint_to_space.",
			"- Note: When the user names a Space, pass that exact name in mint_to_space.kb_name even if the Space does not exist yet.",
			"- Note: mint_to_space manages its own write transaction. Do NOT wrap it in begin_tx/commit_tx.",
			"- Note: Do NOT call vectorize_space_items, vectorize_space_categories, or vectorize_space unless the user explicitly asks for vectorization, embeddings, or semantic search refresh.",
			"- Note: If vectorization is explicitly requested, vectorization APIs manage their own transactions and should be called outside the enclosing write transaction.",
		)
	}
	if flags["U"] {
		sections = append(sections,
			"- U = Update. Use mint_to_space for newly generated knowledge content or update_space_config for routing/persona changes.",
			"- Note: update_space_config requires its normal write path, but mint_to_space manages its own transaction internally.",
			"- Note: Do NOT call vectorization APIs unless the user explicitly asks for vectorization, embeddings, or semantic search refresh.",
			"- Note: If vectorization is explicitly requested, vectorization APIs manage their own transactions and should be called outside the enclosing write transaction.",
		)
	}
	if flags["D"] {
		sections = append(sections,
			"- D = Delete. Use delete_space for full Space deletion when the user explicitly asks to remove the entire Space.",
			"- Note: Full Space deletion is destructive. Prefer a confirmation step with the user before executing it.",
			"- Note: delete_space manages its own deletion path and should not be wrapped in begin_tx/commit_tx.",
		)
	}
	if len(sections) == 0 {
		sections = append(sections, "- No CRUD flags were classified. Default to search_space or list operations before mutating a space. Direct Space API tools manage their own transactions; do not add begin_tx/commit_tx unless a specific tool contract says otherwise.")
	}
	return strings.Join(sections, "\n")
}

func buildScriptAuthoringContext(domain string, flags map[string]bool) string {
	sections := []string{
		"- Use create_script for new reusable scripts; use save_script only to replace an existing full definition.",
		"- Set the target database explicitly when you create or save scripts so the saved script carries its runtime database context.",
		"- Put reusable steps under the `script` field. Legacy alias `steps` is accepted but not preferred.",
		"- For reusable data workflows, prefer a command step that calls execute_script with the inner AST in args.script.",
		"- In stored execute_script AST, preserve real predicates and exact dotted field paths. Do not flatten them into placeholders or underscore names.",
		"- When correcting a stored execute_script AST, keep valid stores, step order, and field names intact. Rewrite only the invalid slice.",
	}
	if strings.EqualFold(domain, StoresDomain) && flags["R"] && !flags["C"] && !flags["U"] && !flags["D"] {
		sections = append(sections, "- This request is read-oriented. Keep the stored execute_script AST read-only unless the user explicitly asks for mutations.")
	}
	return strings.Join(sections, "\n")
}

func buildScriptToolDescriptionContext(domain string, flags map[string]bool) string {
	return "Structured Context: Script Authoring Tools\n" + buildScriptAuthoringContext(domain, flags)
}

func isCrossDomain(layers []LayerInfo) bool {
	for _, layer := range layers {
		if strings.Contains(layer.Name, "Cross-Domain") {
			return true
		}
	}
	return false
}

func compactFocusedToolContextAgainstBaseline(baseline string, focused string) string {
	baseline = strings.TrimSpace(baseline)
	focused = strings.TrimSpace(focused)
	if focused == "" {
		return ""
	}
	if baseline == "" || !strings.Contains(focused, "Structured Context:") {
		return focused
	}

	sections := splitStructuredContextSections(focused)
	if len(sections) == 0 {
		if strings.Contains(baseline, focused) {
			return ""
		}
		return focused
	}

	kept := make([]string, 0, len(sections))
	for _, section := range sections {
		heading := structuredContextSectionHeading(section)
		if heading != "" && strings.Contains(baseline, heading) {
			continue
		}
		if heading == "" && strings.Contains(baseline, section) {
			continue
		}
		kept = append(kept, strings.TrimSpace(section))
	}
	return strings.TrimSpace(strings.Join(kept, "\n\n"))
}

func splitStructuredContextSections(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	sections := make([]string, 0, 4)
	var current []string
	flush := func() {
		if len(current) == 0 {
			return
		}
		sections = append(sections, strings.TrimSpace(strings.Join(current, "\n")))
		current = nil
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Structured Context:") && len(current) > 0 {
			flush()
		}
		current = append(current, line)
	}
	flush()
	return sections
}

func structuredContextSectionHeading(section string) string {
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Structured Context:") {
			return trimmed
		}
		if trimmed != "" {
			break
		}
	}
	return ""
}

func (a *CopilotAgent) buildFocusedToolContext(taskCtx *TaskContextClassification) string {
	if taskCtx == nil {
		return ""
	}

	if isCrossDomain(taskCtx.Layers) {
		scriptSection := ""
		if taskCtx.ScriptAuthoring {
			scriptSection = buildScriptToolDescriptionContext(taskCtx.Domain, collectCRUDFlags(taskCtx.Layers)) + "\n\n"
		}
		parts := make([]string, 0, 3)
		if scriptSection != "" {
			parts = append(parts, strings.TrimSpace(scriptSection))
		}
		if storesContext := a.buildStoresToolDescriptionContext(); storesContext != "" {
			parts = append(parts, storesContext)
		}
		if spacesContext := a.buildSpacesToolDescriptionContext(); spacesContext != "" {
			parts = append(parts, spacesContext)
		}
		return strings.Join(parts, "\n\n")
	}

	switch {
	case strings.EqualFold(taskCtx.Domain, StoresDomain):
		manual := a.buildStoresToolDescriptionContext()
		if taskCtx.ScriptAuthoring {
			manual = buildScriptToolDescriptionContext(taskCtx.Domain, collectCRUDFlags(taskCtx.Layers)) + "\n\n" + a.buildStoresToolDescriptionContext()
		}
		return manual
	case strings.EqualFold(taskCtx.Domain, SpacesDomain):
		return a.buildSpacesToolDescriptionContext()
	default:
		return ""
	}
}
