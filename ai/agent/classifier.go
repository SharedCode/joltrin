package agent

import (
	"context"
	"encoding/json"
	"fmt"
	log "log/slog"
	"sort"
	"strings"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/internal/logsafe"
)

type LayerInfo struct {
	Name string   `json:"name"`
	CRUD []string `json:"crud"`
}

type TaskContextClassification struct {
	Entity          string      `json:"entity"`
	Domain          string      `json:"domain"`
	DBArtifacts     []string    `json:"db_artifacts"`
	StoresArtifacts []string    `json:"stores_artifacts,omitempty"`
	SpacesArtifacts []string    `json:"spaces_artifacts,omitempty"`
	Layers          []LayerInfo `json:"layers"`
	ScriptAuthoring bool        `json:"-"`
	RoutingGate     string      `json:"-"`
	// KB Search Results (for specialized KB routing)
	KBSearchResults string `json:"-"`
	KBMatchCount    int    `json:"-"`
	LLMInstruction  string `json:"-"`
	CleanQuery      string `json:"-"` // Query without :llm meta-token
	DirectDisplay   bool   `json:"-"`
}

type continuityDigest struct {
	Summary            string   `json:"summary,omitempty"`
	CurrentGoal        string   `json:"current_goal,omitempty"`
	ConfirmedFacts     []string `json:"confirmed_facts,omitempty"`
	OpenQuestions      []string `json:"open_questions,omitempty"`
	RecentPatterns     []string `json:"recent_patterns,omitempty"`
	SuggestedNextMoves []string `json:"suggested_next_moves,omitempty"`
	ActiveDomains      []string `json:"active_domains,omitempty"`
	ActiveArtifacts    []string `json:"active_artifacts,omitempty"`
	ExplicitAnchor     string   `json:"explicit_anchor,omitempty"`
	CurrentQuerySignal string   `json:"current_query_signal,omitempty"`
}

var validCRUDFlags = map[string]bool{
	"C": true,
	"R": true,
	"U": true,
	"D": true,
}

const (
	StoresDomain = "Stores"
	SpacesDomain = "Spaces"

	RoutingGateFocused    = "focused"
	RoutingGateContinuity = "continuity"
	RoutingGateDiscovery  = "discovery"
)

func (a *CopilotAgent) GetSampleArtifacts(ctx context.Context) ([]string, []string, error) {
	storesList := []string{}
	spacesList := []string{}

	var err error
	if a.systemDB != nil {
		spacesList, err = a.systemDB.GetDomains(ctx)
		if err != nil {
			return nil, nil, err
		}
	}

	if p := ai.GetSessionPayload(ctx); p != nil && p.CurrentDB != "" {
		if dbOpts, ok := a.databases[p.CurrentDB]; ok {
			userDB := database.NewDatabase(dbOpts)
			if storesList, err = userDB.GetStores(ctx); err != nil {
				return nil, nil, err
			}
		}
	} else if a.systemDB != nil {
		if storesList, err = a.systemDB.GetStores(ctx); err != nil {
			return nil, nil, err
		}
	}

	if len(storesList) > 10 {
		storesList = append(storesList[:10], "...and others")
	}
	if len(spacesList) > 10 {
		spacesList = append(spacesList[:10], "...and others")
	}

	return storesList, spacesList, nil
}

func (a *CopilotAgent) ClassifyTaskContext(ctx context.Context, query string, gen ai.Generator) (*TaskContextClassification, error) {
	storesList, spacesList, err := a.GetSampleArtifacts(ctx)
	if err != nil {
		return nil, err
	}

	prompt := fmt.Sprintf(promptClassifyDiscovery, strings.Join(storesList, ", "), strings.Join(spacesList, ", "))

	opts := ai.GenOptions{
		SystemPrompt:  prompt,
		Temperature:   0.0,
		ThinkingLevel: "low", // Strict JSON schema adherence for classification
	}

	resp, err := gen.Generate(ctx, query, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to generate classification: %w", err)
	}

	return parseClassificationResponse(resp.Text)
}

// ClassifyFocusedTaskContext is Gate 1: The "Focused" Classifier.
// Used when the user explicitly provides Hard Constraints via prefix (e.g. omni:stores:users:).
// Skips building the Context Outline entirely.
func (a *CopilotAgent) ClassifyFocusedTaskContext(ctx context.Context, query, entity, domain, artifact string, gen ai.Generator) (*TaskContextClassification, error) {
	if handledTaskCtx, handled, err := a.trySpecializedFocusedRouting(ctx, query, entity, domain, artifact); err != nil {
		return nil, err
	} else if handled {
		return handledTaskCtx, nil
	}

	availableContext := ""
	if domain == "" || artifact == "" {
		storesList, spacesList, _ := a.GetSampleArtifacts(ctx)
		availableContext = fmt.Sprintf("Available Artifact Samples to choose from if missing:\n- Stores: [%s]\n- Spaces: [%s]\n", strings.Join(storesList, ", "), strings.Join(spacesList, ", "))
	}

	storesArtifact := ""
	spacesArtifact := ""
	if strings.EqualFold(domain, StoresDomain) {
		storesArtifact = artifact
	} else if strings.EqualFold(domain, SpacesDomain) {
		spacesArtifact = artifact
	}

	prompt := fmt.Sprintf(promptClassifyFocused, entity, domain, artifact, availableContext, query, entity, domain, artifact, storesArtifact, spacesArtifact)

	opts := ai.GenOptions{
		SystemPrompt:  prompt,
		Temperature:   0.0,
		ThinkingLevel: "low", // Strict JSON schema adherence for focused classification
	}

	resp, err := gen.Generate(ctx, query, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to generate focused classification: %w", err)
	}

	taskCtx, err := parseClassificationResponse(resp.Text)
	if err != nil {
		return nil, err
	}

	return enforceFocusedConstraints(taskCtx, entity, domain, artifact), nil
}

func looksLikeSpecializedRoutingQuery(query string) bool {

	log.Info("routing ", "query", logsafe.V(query))

	trimmed := strings.TrimSpace(query)
	lower := strings.ToLower(trimmed)

	if strings.HasPrefix(lower, "omni:") {
		parts := strings.Split(trimmed, ":")
		// Allow omni:KB (2 parts) for root category navigation, or omni:KB:path (3+ parts)
		if len(parts) >= 2 && strings.TrimSpace(parts[1]) != "" {
			kbPart := strings.TrimSpace(strings.ToLower(parts[1]))

			// Exclude domain names (Stores, Spaces) from specialized routing
			// These should go through normal Gate1 classification
			if kbPart == "stores" || kbPart == "spaces" {
				return false
			}

			// If exactly 2 parts (omni:KB), allow it for root categories
			if len(parts) == 2 {
				return true
			}
			// If 3+ parts, ensure the third part isn't empty (unless it's a trailing colon)
			return len(parts) >= 3
		}
		return false
	}

	// Support direct KB queries without omni: prefix
	if strings.HasPrefix(lower, "sop:") || strings.HasPrefix(lower, "omni/sop/") || strings.HasPrefix(lower, "omni->sop->") {
		return true
	}

	// Support just "sop" for root category navigation
	if lower == "sop" {
		return true
	}

	return false
}

func routingKBName(query string) string {
	trimmed := strings.TrimSpace(query)
	lower := strings.ToLower(trimmed)

	if strings.HasPrefix(lower, "omni:") {
		parts := strings.Split(trimmed, ":")
		// Extract KB name from omni:KB or omni:KB:path patterns
		if len(parts) >= 2 && strings.TrimSpace(parts[1]) != "" {
			return strings.TrimSpace(parts[1])
		}
	}

	// Support direct KB name patterns: "sop", "sop:path", etc.
	if strings.HasPrefix(lower, "sop:") || lower == "sop" {
		return "sop"
	}

	return ""
}

func (a *CopilotAgent) trySpecializedFocusedRouting(ctx context.Context, query, entity, domain, artifact string) (*TaskContextClassification, bool, error) {
	if !looksLikeSpecializedRoutingQuery(query) {
		return nil, false, nil
	}

	log.Info("Specialized focused routing activated", "query", logsafe.V(query), "entity", logsafe.V(entity), "domain", logsafe.V(domain), "artifact", logsafe.V(artifact))

	kbName := ai.CanonicalKBName("sop")
	if routedKB := routingKBName(query); routedKB != "" {
		kbName = ai.CanonicalKBName(routedKB)
	} else if domain != "" {
		kbName = ai.CanonicalKBName(domain)
	}

	normalizedQuery := stripRoutingPrefix(query, kbName)

	// Extract page number first (before other parsing)
	normalizedQuery, pageNum := extractPageNumber(normalizedQuery)

	pathQuery, llmInstruction := splitCategoryPathInstruction(normalizedQuery)
	llmMode := strings.TrimSpace(llmInstruction) != "" || strings.Contains(strings.ToLower(normalizedQuery), ":llm")
	if pathQuery == "" {
		pathQuery = extractCategoryPathQuery(normalizedQuery)
	}

	// Always extract clean query by stripping :llm instruction, even if no category path
	cleanQuery, extractedInstruction := stripLLMInstruction(normalizedQuery)
	if extractedInstruction != "" && llmInstruction == "" {
		llmInstruction = extractedInstruction
		llmMode = true
	}

	log.Info("Specialized focused routing parsed", "normalized_query", logsafe.V(normalizedQuery), "path_query", logsafe.V(pathQuery), "clean_query", logsafe.V(cleanQuery), "llm_instruction", logsafe.V(llmInstruction), "llm_mode", llmMode, "page", pageNum, "kb_name", logsafe.V(kbName))

	db := a.resolveDBForKB(ctx, kbName)
	if db == nil {
		log.Info("Specialized focused routing skipped: no KB database resolved", "kb_name", logsafe.V(kbName))
		return nil, false, nil
	}

	// Special case: if normalizedQuery is empty (just "omni:KB"), show root categories
	showSubcategories := false
	if normalizedQuery == "" || (pathQuery == "" && cleanQuery == "") {
		showSubcategories = true
	}

	var candidateText string
	var candidateCount int
	var err error

	if showSubcategories {
		// Display root categories for navigation
		candidateText, err = a.getSubcategories(ctx, db, kbName, "", pageNum)
		if err != nil {
			return nil, false, err
		}
		log.Info("Specialized focused routing: showing root categories", "kb_name", logsafe.V(kbName), "page", pageNum)
	} else {
		// Normal KB search
		candidateText, err = a.searchKnowledgeBase(ctx, db, kbName, normalizedQuery, pathQuery, "", 5)
		if err != nil {
			return nil, false, err
		}

		// Count actual hits (each hit has both CategoryPath and Score, so count only one)
		candidateCount = strings.Count(candidateText, "CategoryPath:")
		if strings.Contains(strings.ToLower(candidateText), "no results found") {
			candidateCount = 0
			// If no results found but we have a category path AND no :llm instruction, show subcategories
			if pathQuery != "" && !llmMode {
				subcatText, err := a.getSubcategories(ctx, db, kbName, pathQuery, pageNum)
				if err == nil && subcatText != "" {
					candidateText = fmt.Sprintf("No items found at this path.\n\n%s", subcatText)
					showSubcategories = true
					log.Info("Specialized focused routing: showing subcategories for path", "path", logsafe.V(pathQuery), "page", pageNum)
				}
			}
		}
	}

	handled := looksLikeSpecializedRoutingQuery(query) || llmMode || candidateCount > 0 || showSubcategories
	log.Info("Specialized focused routing decision", "handled", handled, "llm_mode", llmMode, "candidate_count", candidateCount, "show_subcategories", showSubcategories, "candidate_text_preview", logsafe.V(summarizeCandidatePreview(candidateText)))
	if !handled {
		return nil, false, nil
	}

	taskCtx := enrichFocusedTaskContext(nil, entity, domain, artifact)
	if taskCtx == nil {
		taskCtx = &TaskContextClassification{}
	}
	if pathQuery != "" {
		taskCtx.SpacesArtifacts = append(taskCtx.SpacesArtifacts, pathQuery)
	}

	// Populate KB search results
	taskCtx.KBSearchResults = candidateText
	taskCtx.KBMatchCount = candidateCount
	taskCtx.LLMInstruction = llmInstruction
	// Store clean query without :llm meta-token for LLM context
	// Use the cleanQuery which has :llm instruction stripped out
	taskCtx.CleanQuery = cleanQuery

	// Three-way routing decision:
	// Case 0: :llm instruction present -> Always use LLM (even with 0 results)
	// Case 1: Showing subcategories -> Direct display for navigation
	// Case 2: Few matches (1-5) -> Direct display, bypass LLM
	// Case 3: Too many matches (>5) -> LLM reduces/summarizes matches
	if llmMode {
		// Case 0: User wants LLM to process (highest priority)
		taskCtx.DirectDisplay = false
		taskCtx.Layers = append(taskCtx.Layers, LayerInfo{Name: "LLMFilter", CRUD: []string{"R"}})
		log.Info("KB routing: LLM filter mode", "instruction", logsafe.V(llmInstruction))
	} else if showSubcategories {
		// Case 1: Showing subcategories for navigation - direct display
		taskCtx.DirectDisplay = true
		taskCtx.Layers = append(taskCtx.Layers, LayerInfo{Name: "KBRoute", CRUD: []string{"R"}})
		log.Info("KB routing: Direct display (subcategories navigation)")
	} else if candidateCount > 0 && candidateCount <= 5 {
		// Case 2: Few matches - direct display
		taskCtx.DirectDisplay = true
		taskCtx.Layers = append(taskCtx.Layers, LayerInfo{Name: "KBRoute", CRUD: []string{"R"}})
		log.Info("KB routing: Direct display", "match_count", candidateCount)
	} else if candidateCount > 5 {
		// Case 3: Too many matches - LLM reduction
		taskCtx.DirectDisplay = false
		taskCtx.LLMInstruction = "Please analyze these search results and present the most relevant matches to the user."
		taskCtx.Layers = append(taskCtx.Layers, LayerInfo{Name: "LLMFilter", CRUD: []string{"R"}})
		log.Info("KB routing: LLM reduction mode", "match_count", candidateCount)
	} else {
		// Fallback for no matches
		taskCtx.Layers = append(taskCtx.Layers, LayerInfo{Name: "KBRoute", CRUD: []string{"R"}})
	}
	log.Info("Specialized focused routing resolved", "routing_gate", taskCtx.RoutingGate, "layers", taskCtx.Layers, "spaces_artifacts", logsafe.V(taskCtx.SpacesArtifacts), "direct_display", taskCtx.DirectDisplay)

	normalizeTaskContext(taskCtx)
	taskCtx.RoutingGate = RoutingGateFocused
	annotateTaskContextIntent(taskCtx, query)
	a.persistRoutingState(ctx, taskCtx)
	return taskCtx, true, nil
}

// ClassifyContinuityTaskContext is Gate 2: The "Continuity/Switch" Classifier.
// Used to check if the user is maintaining MRU context or switching topics.
func (a *CopilotAgent) ClassifyContinuityTaskContext(ctx context.Context, query string, mru *TaskContextClassification, anchor *TaskContextClassification, gen ai.Generator) (*TaskContextClassification, bool, error) {
	digestJSON, _ := json.MarshalIndent(a.buildContinuityDigest(query, mru, anchor), "", "  ")
	routingJSON, _ := json.MarshalIndent(mru, "", "  ")
	anchorJSON, _ := json.MarshalIndent(anchor, "", "  ")
	prompt := fmt.Sprintf(promptClassifyContinuity, string(digestJSON), string(routingJSON), string(anchorJSON), query)

	opts := ai.GenOptions{
		SystemPrompt:  prompt,
		Temperature:   0.0,
		ThinkingLevel: "low", // Strict JSON schema adherence for continuity classification
	}

	resp, err := gen.Generate(ctx, query, opts)
	if err != nil {
		return nil, false, fmt.Errorf("failed to generate continuity classification: %w", err)
	}

	raw := strings.TrimSpace(resp.Text)
	if strings.HasPrefix(raw, "```json") {
		raw = strings.TrimPrefix(raw, "```json")
		raw = strings.TrimSuffix(raw, "```")
	} else if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```")
		raw = strings.TrimSuffix(raw, "```")
	}

	type continuityResponse struct {
		Intent          string       `json:"intent"`
		Entity          *string      `json:"entity"`
		Domain          *string      `json:"domain"`
		DBArtifacts     *[]string    `json:"db_artifacts"`
		StoresArtifacts *[]string    `json:"stores_artifacts"`
		SpacesArtifacts *[]string    `json:"spaces_artifacts"`
		Layers          *[]LayerInfo `json:"layers"`
	}

	var parsed continuityResponse
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, false, fmt.Errorf("failed to parse continuity JSON: %w (raw: %s)", err, raw)
	}

	intent := strings.ToUpper(strings.TrimSpace(parsed.Intent))
	if intent != "CONTINUE" && intent != "SWITCH" {
		return nil, false, fmt.Errorf("invalid continuity intent %q (raw: %s)", parsed.Intent, raw)
	}

	isSwitch := intent == "SWITCH"
	if isSwitch {
		return nil, true, nil
	}

	// It's a continuation. Clone the MRU and merge the updated context shape.
	updatedMRU := *mru
	if parsed.Entity != nil {
		updatedMRU.Entity = strings.TrimSpace(*parsed.Entity)
	}
	if parsed.Domain != nil {
		updatedMRU.Domain = strings.TrimSpace(*parsed.Domain)
	}
	if parsed.DBArtifacts != nil {
		updatedMRU.DBArtifacts = append([]string(nil), (*parsed.DBArtifacts)...)
	}
	if parsed.StoresArtifacts != nil {
		updatedMRU.StoresArtifacts = append([]string(nil), (*parsed.StoresArtifacts)...)
	}
	if parsed.SpacesArtifacts != nil {
		updatedMRU.SpacesArtifacts = append([]string(nil), (*parsed.SpacesArtifacts)...)
	}
	if parsed.Layers != nil {
		updatedMRU.Layers = append([]LayerInfo(nil), (*parsed.Layers)...)
	}
	normalizeTaskContext(&updatedMRU)
	return &updatedMRU, false, nil
}

func (a *CopilotAgent) buildContinuityDigest(query string, routing *TaskContextClassification, anchor *TaskContextClassification) continuityDigest {
	digest := continuityDigest{
		CurrentQuerySignal: compactMRUText(query, 180),
	}

	if routing != nil {
		if domain := strings.TrimSpace(routing.Domain); domain != "" {
			digest.ActiveDomains = append(digest.ActiveDomains, domain)
		}
		digest.ActiveArtifacts = append(digest.ActiveArtifacts, routing.DBArtifacts...)
		digest.ActiveArtifacts = append(digest.ActiveArtifacts, routing.StoresArtifacts...)
		digest.ActiveArtifacts = append(digest.ActiveArtifacts, routing.SpacesArtifacts...)
	}
	if anchor != nil {
		if domain := strings.TrimSpace(anchor.Domain); domain != "" {
			digest.ActiveDomains = append(digest.ActiveDomains, domain)
		}
		digest.ActiveArtifacts = append(digest.ActiveArtifacts, anchor.DBArtifacts...)
		digest.ActiveArtifacts = append(digest.ActiveArtifacts, anchor.StoresArtifacts...)
		digest.ActiveArtifacts = append(digest.ActiveArtifacts, anchor.SpacesArtifacts...)
		digest.ExplicitAnchor = compactMRUText(summarizeTaskContextForLog(*anchor), 220)
	}

	for _, item := range a.getMRUSnapshot() {
		if normalizeMRUScope(item.Scope) != MRUScopeSession {
			continue
		}
		contextText := compactMRUText(item.Context, 220)
		if contextText == "" {
			continue
		}

		switch {
		case item.Source == MRUSourceAskOutcome && item.Category == askOutcomeMRUCategoryQuery:
			if digest.CurrentGoal == "" {
				digest.CurrentGoal = trimMRUPrefix(contextText)
			}
		case item.Source == MRUSourceAskOutcome && item.Category == askOutcomeMRUCategoryResult:
			if digest.Summary == "" {
				digest.Summary = trimMRUPrefix(contextText)
			}
		case item.Source == MRUSourceAskOutcome && strings.HasPrefix(item.Category, askOutcomeMRUCategoryToolPattern):
			digest.RecentPatterns = appendUniqueCompact(digest.RecentPatterns, trimMRUPrefix(contextText), 180)
		case item.Source == MRUSourceAskOutcome && item.Category == askOutcomeMRUCategoryGuidance:
			digest.SuggestedNextMoves = appendUniqueCompact(digest.SuggestedNextMoves, trimMRUPrefix(contextText), 180)
		case item.Source == MRUSourceAskOutcome && (strings.HasPrefix(item.Category, askOutcomeMRUCategoryStoreSchema+"_") || strings.HasPrefix(item.Category, askOutcomeMRUCategoryRelations+"_") || strings.HasPrefix(item.Category, askOutcomeMRUCategoryJoinSelection+"_") || strings.HasPrefix(item.Category, askOutcomeMRUCategoryFilterSelection+"_") || strings.HasPrefix(item.Category, askOutcomeMRUCategoryConfirmed+"_")):
			digest.ConfirmedFacts = appendUniqueCompact(digest.ConfirmedFacts, trimMRUPrefix(contextText), 180)
		case item.Source == MRUSourceAskOutcome:
			// Keep other ask-outcome lines out of the digest to avoid replaying boilerplate.
		case item.Source == MRUSourcePlaybook:
			digest.SuggestedNextMoves = appendUniqueCompact(digest.SuggestedNextMoves, contextText, 180)
		case item.Source == MRUSourcePersona:
			if digest.Summary == "" {
				digest.Summary = contextText
			}
		default:
			digest.OpenQuestions = appendUniqueCompact(digest.OpenQuestions, contextText, 180)
		}
	}

	if digest.CurrentGoal == "" {
		digest.CurrentGoal = digest.CurrentQuerySignal
	}
	if digest.Summary == "" && digest.CurrentGoal != "" {
		digest.Summary = digest.CurrentGoal
	}
	digest.ActiveDomains = uniqueSortedStrings(digest.ActiveDomains)
	digest.ActiveArtifacts = uniqueSortedStrings(digest.ActiveArtifacts)
	return digest
}

func appendUniqueCompact(items []string, value string, maxLen int) []string {
	value = compactMRUText(trimMRUPrefix(value), maxLen)
	if value == "" {
		return items
	}
	for _, existing := range items {
		if existing == value {
			return items
		}
	}
	return append(items, value)
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		unique = append(unique, trimmed)
	}
	sort.Strings(unique)
	return unique
}

func trimMRUPrefix(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "- ")
	value = strings.TrimSpace(value)
	for _, prefix := range []string{"Last user ask:", "Last outcome:", "Tool pattern:", "Confirmed:", "Database:", "Domain:"} {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(value, prefix))
		}
	}
	return value
}

// Helper to parse standard classification responses
func parseClassificationResponse(raw string) (*TaskContextClassification, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```json") {
		raw = strings.TrimPrefix(raw, "```json")
		raw = strings.TrimSuffix(raw, "```")
	} else if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```")
		raw = strings.TrimSuffix(raw, "```")
	}
	raw = strings.TrimSpace(raw)

	jsonPayload := raw
	if !strings.HasPrefix(jsonPayload, "{") {
		if braceIdx := strings.Index(jsonPayload, "{"); braceIdx >= 0 {
			candidate, err := extractBalancedJSONObject(jsonPayload[braceIdx:])
			if err == nil {
				jsonPayload = candidate
			}
		}
	}

	var result TaskContextClassification
	if err := json.Unmarshal([]byte(jsonPayload), &result); err != nil {
		return nil, fmt.Errorf("failed to parse classification JSON: %w (raw: %s)", err, raw)
	}
	normalizeTaskContext(&result)
	return &result, nil
}

func normalizeTaskContext(taskCtx *TaskContextClassification) {
	if taskCtx == nil {
		return
	}

	taskCtx.Entity = canonicalizeEntity(taskCtx.Entity)
	taskCtx.Domain = canonicalizeDomain(taskCtx.Domain)
	taskCtx.DBArtifacts = normalizeArtifacts(taskCtx.DBArtifacts)
	taskCtx.StoresArtifacts = normalizeArtifacts(taskCtx.StoresArtifacts)
	taskCtx.SpacesArtifacts = normalizeArtifacts(taskCtx.SpacesArtifacts)
	taskCtx.Layers = normalizeLayers(taskCtx.Layers)

	if taskCtx.Entity == "" {
		taskCtx.Entity = "Omni"
	}

	if taskCtx.Domain == "" {
		switch {
		case len(taskCtx.StoresArtifacts) > 0 && len(taskCtx.SpacesArtifacts) == 0:
			taskCtx.Domain = StoresDomain
		case len(taskCtx.SpacesArtifacts) > 0 && len(taskCtx.StoresArtifacts) == 0:
			taskCtx.Domain = SpacesDomain
		case len(taskCtx.DBArtifacts) > 0 && hasLayer(taskCtx.Layers, "Single-Domain"):
			// Preserve legacy single-array compatibility by biasing toward Stores when the domain is still unknown.
			taskCtx.Domain = StoresDomain
		}
	}

	if len(taskCtx.StoresArtifacts) == 0 && len(taskCtx.DBArtifacts) > 0 && strings.EqualFold(taskCtx.Domain, StoresDomain) {
		taskCtx.StoresArtifacts = append([]string(nil), taskCtx.DBArtifacts...)
	}
	if len(taskCtx.SpacesArtifacts) == 0 && len(taskCtx.DBArtifacts) > 0 && strings.EqualFold(taskCtx.Domain, SpacesDomain) {
		taskCtx.SpacesArtifacts = append([]string(nil), taskCtx.DBArtifacts...)
	}

	if len(taskCtx.DBArtifacts) == 0 {
		switch {
		case len(taskCtx.StoresArtifacts) > 0 && len(taskCtx.SpacesArtifacts) == 0 && strings.EqualFold(taskCtx.Domain, StoresDomain):
			taskCtx.DBArtifacts = append([]string(nil), taskCtx.StoresArtifacts...)
		case len(taskCtx.SpacesArtifacts) > 0 && len(taskCtx.StoresArtifacts) == 0 && strings.EqualFold(taskCtx.Domain, SpacesDomain):
			taskCtx.DBArtifacts = append([]string(nil), taskCtx.SpacesArtifacts...)
		}
	}

	if len(taskCtx.StoresArtifacts) > 0 && len(taskCtx.SpacesArtifacts) > 0 && !hasLayer(taskCtx.Layers, "Cross-Domain") {
		taskCtx.Layers = append(taskCtx.Layers, LayerInfo{Name: "Cross-Domain", CRUD: inferredCRUD(taskCtx.Layers)})
	}

	if len(taskCtx.Layers) == 0 {
		switch {
		case len(taskCtx.StoresArtifacts) > 0 && len(taskCtx.SpacesArtifacts) > 0:
			taskCtx.Layers = []LayerInfo{{Name: "Cross-Domain", CRUD: []string{"R"}}}
		case taskCtx.Domain == StoresDomain || taskCtx.Domain == SpacesDomain || len(taskCtx.DBArtifacts) > 0:
			taskCtx.Layers = []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}}
		}
	}
}

func annotateTaskContextIntent(taskCtx *TaskContextClassification, query string) {
	if taskCtx == nil {
		return
	}
	taskCtx.ScriptAuthoring = queryImpliesScriptAuthoring(query)
}

func queryImpliesScriptAuthoring(query string) bool {
	q := strings.ToLower(query)
	if !strings.Contains(q, "script") {
		return false
	}
	keywords := []string{"create", "save", "named", "name it", "build", "write", "generate", "refactor", "reusable", "workflow"}
	for _, keyword := range keywords {
		if strings.Contains(q, keyword) {
			return true
		}
	}
	return false
}

func normalizeArtifacts(artifacts []string) []string {
	if len(artifacts) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		artifact = strings.TrimSpace(artifact)
		if artifact != "" {
			normalized = append(normalized, artifact)
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func canonicalizeDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	switch {
	case strings.EqualFold(domain, StoresDomain):
		return StoresDomain
	case strings.EqualFold(domain, SpacesDomain):
		return SpacesDomain
	case strings.EqualFold(domain, "Store"):
		return StoresDomain
	case strings.EqualFold(domain, "Space"):
		return SpacesDomain
	default:
		return domain
	}
}

func normalizeLayers(layers []LayerInfo) []LayerInfo {
	if len(layers) == 0 {
		return nil
	}

	normalized := make([]LayerInfo, 0, len(layers))
	seen := make(map[string]bool)
	for _, layer := range layers {
		name := canonicalizeLayerName(layer.Name)
		if name == "" || seen[name] {
			continue
		}
		crud := normalizeCRUD(layer.CRUD)
		normalized = append(normalized, LayerInfo{Name: name, CRUD: crud})
		seen[name] = true
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func canonicalizeLayerName(name string) string {
	name = strings.TrimSpace(name)
	switch {
	case strings.EqualFold(name, "Single-Domain"), strings.EqualFold(name, "Layer 1"), strings.EqualFold(name, "Layer 2"):
		return "Single-Domain"
	case strings.EqualFold(name, "Cross-Domain"), strings.EqualFold(name, "Layer 3"):
		return "Cross-Domain"
	case strings.EqualFold(name, "KBRoute"):
		return "KBRoute"
	case strings.EqualFold(name, "LLMFilter"):
		return "LLMFilter"
	default:
		return ""
	}
}

func normalizeCRUD(crud []string) []string {
	if len(crud) == 0 {
		return nil
	}
	ordered := []string{"C", "R", "U", "D"}
	seen := make(map[string]bool)
	for _, item := range crud {
		item = strings.ToUpper(strings.TrimSpace(item))
		if validCRUDFlags[item] {
			seen[item] = true
		}
	}
	result := make([]string, 0, len(seen))
	for _, item := range ordered {
		if seen[item] {
			result = append(result, item)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func hasLayer(layers []LayerInfo, name string) bool {
	for _, layer := range layers {
		if layer.Name == name {
			return true
		}
	}
	return false
}

func inferredCRUD(layers []LayerInfo) []string {
	flags := make(map[string]bool)
	for _, layer := range layers {
		for _, crud := range layer.CRUD {
			if validCRUDFlags[crud] {
				flags[crud] = true
			}
		}
	}
	if len(flags) == 0 {
		return []string{"R"}
	}
	ordered := []string{"C", "R", "U", "D"}
	result := make([]string, 0, len(flags))
	for _, crud := range ordered {
		if flags[crud] {
			result = append(result, crud)
		}
	}
	return result
}

func buildFocusedFallbackTaskContext(entity, domain, artifact string) *TaskContextClassification {
	taskCtx := &TaskContextClassification{
		Entity: canonicalizeEntity(entity),
		Domain: canonicalizeDomain(domain),
	}

	artifact = strings.TrimSpace(artifact)
	if artifact != "" {
		taskCtx.DBArtifacts = []string{artifact}
		switch taskCtx.Domain {
		case StoresDomain:
			taskCtx.StoresArtifacts = []string{artifact}
		case SpacesDomain:
			taskCtx.SpacesArtifacts = []string{artifact}
		}
	}

	if taskCtx.Domain == StoresDomain || taskCtx.Domain == SpacesDomain {
		taskCtx.Layers = []LayerInfo{{
			Name: "Single-Domain",
			CRUD: []string{"R"},
		}}
	}

	normalizeTaskContext(taskCtx)
	return taskCtx
}

func enrichFocusedTaskContext(taskCtx *TaskContextClassification, entity, domain, artifact string) *TaskContextClassification {
	if taskCtx == nil {
		return buildFocusedFallbackTaskContext(entity, domain, artifact)
	}

	if strings.TrimSpace(taskCtx.Entity) == "" {
		taskCtx.Entity = canonicalizeEntity(entity)
	}
	if strings.TrimSpace(taskCtx.Domain) == "" {
		taskCtx.Domain = canonicalizeDomain(domain)
	}

	artifact = strings.TrimSpace(artifact)
	if artifact != "" && len(taskCtx.DBArtifacts) == 0 && len(taskCtx.StoresArtifacts) == 0 && len(taskCtx.SpacesArtifacts) == 0 {
		taskCtx.DBArtifacts = []string{artifact}
		switch canonicalizeDomain(taskCtx.Domain) {
		case StoresDomain:
			taskCtx.StoresArtifacts = []string{artifact}
		case SpacesDomain:
			taskCtx.SpacesArtifacts = []string{artifact}
		}
	}

	if len(taskCtx.Layers) == 0 {
		switch canonicalizeDomain(taskCtx.Domain) {
		case StoresDomain, SpacesDomain:
			taskCtx.Layers = []LayerInfo{{
				Name: "Single-Domain",
				CRUD: []string{"R"},
			}}
		}
	}

	normalizeTaskContext(taskCtx)
	return taskCtx
}

func enforceFocusedConstraints(taskCtx *TaskContextClassification, entity, domain, artifact string) *TaskContextClassification {
	taskCtx = enrichFocusedTaskContext(taskCtx, entity, domain, artifact)

	if strings.TrimSpace(entity) != "" {
		taskCtx.Entity = canonicalizeEntity(entity)
	}

	forcedDomain := canonicalizeDomain(domain)
	if forcedDomain != "" {
		taskCtx.Domain = forcedDomain
	}

	artifact = strings.TrimSpace(artifact)
	if artifact != "" {
		taskCtx.DBArtifacts = []string{artifact}
		taskCtx.StoresArtifacts = nil
		taskCtx.SpacesArtifacts = nil
		switch taskCtx.Domain {
		case StoresDomain:
			taskCtx.StoresArtifacts = []string{artifact}
		case SpacesDomain:
			taskCtx.SpacesArtifacts = []string{artifact}
		}
	}

	if forcedDomain == StoresDomain || forcedDomain == SpacesDomain {
		taskCtx.Layers = normalizeLayers(taskCtx.Layers)
		if hasLayer(taskCtx.Layers, "Cross-Domain") {
			filtered := make([]LayerInfo, 0, len(taskCtx.Layers))
			for _, layer := range taskCtx.Layers {
				if layer.Name != "Cross-Domain" {
					filtered = append(filtered, layer)
				}
			}
			taskCtx.Layers = filtered
		}
		if len(taskCtx.Layers) == 0 {
			taskCtx.Layers = []LayerInfo{{Name: "Single-Domain", CRUD: []string{"R"}}}
		}
	}

	normalizeTaskContext(taskCtx)
	return taskCtx
}

func summarizeCandidatePreview(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "<empty>"
	}
	if len(trimmed) > 180 {
		return trimmed[:180] + "..."
	}
	return trimmed
}

func canonicalizeEntity(entity string) string {
	entity = strings.TrimSpace(entity)
	if strings.EqualFold(entity, "omni") {
		return "Omni"
	}
	return entity
}
