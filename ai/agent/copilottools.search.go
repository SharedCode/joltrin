package agent

import (
	"context"
	"encoding/json"
	"fmt"
	log "log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
)

// Only strip routing tokens that the system recognizes at the start of the query.
var routingTokenPattern = regexp.MustCompile(`(?i)^(?:omni|sop)(?:\s*[:/]\s*|\s*->\s*)`)

// stripRoutingPrefix removes only system-recognized routing prefixes.
// It strips the active KB name prefix when present and then strips
// leading OMNI/SOP routing tokens, but leaves unknown prefixes untouched.
func stripRoutingPrefix(query, kbName string) string {
	trimmed := strings.TrimSpace(query)
	trimmed = strings.Trim(trimmed, "\"'`")
	if trimmed == "" {
		return trimmed
	}

	startsWithOMNI := routingTokenPattern.MatchString(trimmed) && strings.EqualFold(strings.FieldsFunc(trimmed, func(r rune) bool { return r == ':' || r == '/' || r == '-' || r == '>' })[0], "omni")

	if startsWithOMNI {
		trimmed = strings.TrimSpace(routingTokenPattern.ReplaceAllString(trimmed, ""))
		trimmed = strings.TrimSpace(routingTokenPattern.ReplaceAllString(trimmed, ""))
	} else if routingTokenPattern.MatchString(trimmed) {
		trimmed = strings.TrimSpace(routingTokenPattern.ReplaceAllString(trimmed, ""))
	}

	canonicalName := strings.ToLower(ai.CanonicalKBName(kbName))
	for _, prefix := range []string{"sop:" + canonicalName + ":", canonicalName + ":", "sop:"} {
		if strings.HasPrefix(strings.ToLower(trimmed), strings.ToLower(prefix)) {
			trimmed = strings.TrimSpace(trimmed[len(prefix):])
			break
		}
	}

	// If what remains is just the KB name itself (e.g., "sop" from "omni:sop"), strip it
	if strings.EqualFold(trimmed, canonicalName) {
		trimmed = ""
	}

	return strings.TrimSpace(trimmed)
}

func normalizeCategoryPathSeparators(query string) string {
	trimmed := strings.TrimSpace(query)
	trimmed = strings.Trim(trimmed, "\"'`")
	if trimmed == "" {
		return trimmed
	}

	match := regexp.MustCompile(`(?i)^(?P<path>.+?)(?:\s*:\s*llm\b\s*(?P<instruction>.*))?$`).FindStringSubmatch(trimmed)
	if len(match) == 3 {
		path := strings.TrimSpace(match[1])
		return strings.ReplaceAll(path, ":", "/")
	}

	return strings.ReplaceAll(trimmed, ":", "/")
}

func looksLikeCategoryPath(query string) bool {
	trimmed := strings.TrimSpace(query)
	trimmed = strings.Trim(trimmed, "\"'`")
	if trimmed == "" {
		return false
	}

	normalized := normalizeCategoryPathSeparators(trimmed)
	if strings.ContainsAny(normalized, "/\\") {
		parts := strings.FieldsFunc(normalized, func(r rune) bool {
			return r == '/' || r == '\\'
		})
		return len(parts) >= 2
	}
	return false
}

func extractCategoryPathQuery(query string) string {
	trimmed := strings.TrimSpace(query)
	trimmed = strings.Trim(trimmed, "\"'`")
	trimmed = stripRoutingPrefix(trimmed, "")

	path, _ := splitCategoryPathInstruction(trimmed)
	if path != "" {
		return path
	}

	normalized := normalizeCategoryPathSeparators(trimmed)
	if strings.Contains(normalized, "/") || strings.Contains(normalized, "\\") {
		return normalized
	}

	return ""
}

// stripLLMInstruction extracts and removes the :llm <instruction> suffix from any query
// Returns (cleanQuery, instruction) where cleanQuery has the :llm part removed
func stripLLMInstruction(query string) (string, string) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return "", ""
	}

	re := regexp.MustCompile(`(?i)^(?P<query>.+?)(?:\s*:\s*llm\b\s*(?P<instruction>.*))?$`)
	matches := re.FindStringSubmatch(trimmed)
	if len(matches) != 3 {
		return trimmed, ""
	}

	cleanQuery := strings.TrimSpace(matches[1])
	instruction := strings.TrimSpace(matches[2])
	return cleanQuery, instruction
}

// extractPageNumber extracts and removes the :page:<number> or /page/<number> suffix from a query
// Returns (cleanQuery, pageNumber) where pageNumber is 1 if not specified
func extractPageNumber(query string) (string, int) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return "", 1
	}

	// Match :page:<number> or /page/<number> at the end (case insensitive)
	// Supports both : and / as separators (e.g., :page:2 or /page/2)
	re := regexp.MustCompile(`(?i)^(?P<query>.+?)(?:\s*[:/]\s*page\s*[:/]\s*(?P<page>\d+))$`)
	matches := re.FindStringSubmatch(trimmed)
	if len(matches) != 3 {
		return trimmed, 1
	}

	cleanQuery := strings.TrimSpace(matches[1])
	pageStr := strings.TrimSpace(matches[2])

	page := 1
	if pageNum, err := strconv.Atoi(pageStr); err == nil && pageNum > 0 {
		page = pageNum
	}

	return cleanQuery, page
}

func splitCategoryPathInstruction(query string) (string, string) {
	trimmed := strings.TrimSpace(query)
	trimmed = strings.Trim(trimmed, "\"'`")
	if trimmed == "" {
		return "", ""
	}

	trimmed = stripRoutingPrefix(trimmed, "")

	re := regexp.MustCompile(`(?i)^(?P<path>.+?)(?:\s*:\s*llm\b\s*(?P<instruction>.*))?$`)
	matches := re.FindStringSubmatch(trimmed)
	if len(matches) != 3 {
		return "", ""
	}

	path := strings.TrimSpace(matches[1])
	instruction := strings.TrimSpace(matches[2])
	normalizedPath := normalizeCategoryPathSeparators(path)
	if normalizedPath == "" || !looksLikeCategoryPath(normalizedPath) {
		return "", ""
	}

	return normalizedPath, instruction
}

func (a *CopilotAgent) searchKnowledgeBase(ctx context.Context, db *database.Database, kbName string, query string, catPath string, category string, limit int) (string, error) {
	if db == nil {
		return "", fmt.Errorf("database is null")
	}
	kbName = ai.CanonicalKBName(kbName)
	query = stripRoutingPrefix(query, kbName)
	pathQuery, _ := splitCategoryPathInstruction(query)

	log.Info("searchKnowledgeBase start", "kb_name", kbName, "query", query, "category_path_query", pathQuery, "category", category, "category_path", catPath, "limit", limit)

	tx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return "", fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil && a.service.Domain().Embedder() != nil {
		embedder = a.service.Domain().Embedder()
	}

	kb, err := db.OpenKnowledgeBase(ctx, kbName, tx, a.brain, embedder, false)
	if err != nil {
		log.Error("searchKnowledgeBase open failed", "kb_name", kbName, "error", err)
		return "", fmt.Errorf("failed to open kb %s: %w", kbName, err)
	}

	// Determine effective category path: pathQuery takes precedence, then catPath, then category
	effectivePath := pathQuery
	if effectivePath == "" {
		effectivePath = catPath
	}
	if effectivePath == "" {
		effectivePath = category
	}

	// Single unified search - let Search() handle all intelligent routing:
	// - CategoryPath resolution (lexical + semantic fallback)
	// - Text-based category discovery
	// - Vector search across all candidate categories
	searchReq := memory.SearchRequest[map[string]any]{
		Text:         query,
		CategoryPath: effectivePath,
		Limit:        limit,
	}

	batch, err := kb.Search(ctx, []memory.SearchRequest[map[string]any]{searchReq})
	if err != nil {
		return "", err
	}

	if len(batch) == 0 || len(batch[0]) == 0 {
		return "No results found.", nil
	}

	// Unified result formatting
	var results []string
	for _, h := range batch[0] {
		text := extractHitText(h.Payload)
		categoryVal := extractHitCategory(h.Payload)
		link := a.formatKBSourceLinks(ctx, memory.DocIDs(h.DocID))
		results = append(results, fmt.Sprintf("Score: %.2f | CategoryPath: %s\nText: %s%s", h.Score, categoryVal, text, link))
	}

	a.MarkMRUCategory(kbName, fmt.Sprintf("Last searched query: %s", query))
	return strings.Join(results, "\n\n"), nil
}

func extractHitText(v any) string {
	switch data := v.(type) {
	case map[string]any:
		for _, field := range []string{"text", "description", "content", "page_content", "_raw_content"} {
			if text, ok := data[field].(string); ok && strings.TrimSpace(text) != "" {
				return text
			}
		}
		b, _ := json.Marshal(data)
		return string(b)
	case string:
		return data
	default:
		b, _ := json.Marshal(data)
		return string(b)
	}
}

func extractHitCategory(v any) string {
	switch data := v.(type) {
	case map[string]any:
		// Prefer category_path (full hierarchical path) over category (leaf name only)
		if categoryPath, ok := data["category_path"].(string); ok && strings.TrimSpace(categoryPath) != "" {
			return categoryPath
		}
		if category, ok := data["category"].(string); ok {
			return category
		}
		if category, ok := data["path"].(string); ok {
			return category
		}
	}
	return ""
}

// getSubcategories retrieves subcategories for navigation display with pagination support.
// If categoryPath is empty, it returns root categories (categories with no parents).
// If categoryPath is provided, it returns children of that category.
// page parameter controls pagination (1-based, 0 or negative means page 1).
func (a *CopilotAgent) getSubcategories(ctx context.Context, db *database.Database, kbName string, categoryPath string, page int) (string, error) {
	if db == nil {
		return "", fmt.Errorf("database is null")
	}

	if page <= 0 {
		page = 1
	}

	tx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return "", fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil && a.service.Domain().Embedder() != nil {
		embedder = a.service.Domain().Embedder()
	}

	kb, err := db.OpenKnowledgeBase(ctx, kbName, tx, a.brain, embedder, false)
	if err != nil {
		return "", fmt.Errorf("failed to open kb %s: %w", kbName, err)
	}

	categoriesTree, err := kb.Store.Categories(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get categories tree: %w", err)
	}

	var targetCategories []*memory.Category

	// If no category path, get root categories (categories with no parents)
	if categoryPath == "" {
		// Scan all categories to find roots
		ok, err := categoriesTree.First(ctx)
		if err != nil {
			return "", err
		}
		for ok {
			cat, err := categoriesTree.GetCurrentValue(ctx)
			if err != nil {
				return "", err
			}
			// Root categories have no parents
			if len(cat.ParentIDs) == 0 {
				targetCategories = append(targetCategories, cat)
			}
			ok, err = categoriesTree.Next(ctx)
			if err != nil {
				return "", err
			}
		}
	} else {
		// Find the category by path and get its children
		catsByPath, err := kb.Store.CategoriesByPath(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to get categories by path: %w", err)
		}

		found, err := catsByPath.Find(ctx, categoryPath, false)
		if err != nil {
			return "", fmt.Errorf("failed to find category by path: %w", err)
		}
		if !found {
			return fmt.Sprintf("Category path %q not found.", categoryPath), nil
		}

		catID, err := catsByPath.GetCurrentValue(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to get category ID: %w", err)
		}

		ok, err := categoriesTree.Find(ctx, catID, false)
		if err != nil || !ok {
			return fmt.Sprintf("Category path %q not found.", categoryPath), nil
		}

		parentCat, err := categoriesTree.GetCurrentValue(ctx)
		if err != nil {
			return "", err
		}

		// Get children of this category
		for _, childID := range parentCat.ChildrenIDs {
			ok, err := categoriesTree.Find(ctx, childID, false)
			if err != nil {
				continue
			}
			if ok {
				childCat, err := categoriesTree.GetCurrentValue(ctx)
				if err != nil {
					continue
				}
				targetCategories = append(targetCategories, childCat)
			}
		}
	}

	if len(targetCategories) == 0 {
		if categoryPath == "" {
			return "No root categories found in this knowledge base.", nil
		}
		return fmt.Sprintf("No subcategories found under %q.", categoryPath), nil
	}

	// Apply pagination
	const pageSize = 20
	totalCategories := len(targetCategories)
	totalPages := (totalCategories + pageSize - 1) / pageSize // Ceiling division

	// Clamp page to valid range
	if page > totalPages {
		page = totalPages
	}

	// Calculate slice bounds
	startIdx := (page - 1) * pageSize
	endIdx := startIdx + pageSize
	if endIdx > totalCategories {
		endIdx = totalCategories
	}

	displayCategories := targetCategories[startIdx:endIdx]

	// Format subcategories for display
	var results []string
	header := "Available Categories:"
	if categoryPath != "" {
		header = fmt.Sprintf("Subcategories under %q:", categoryPath)
	}

	// Add page info if multiple pages exist
	if totalPages > 1 {
		header = fmt.Sprintf("%s (Page %d of %d, showing %d-%d of %d)",
			header, page, totalPages, startIdx+1, endIdx, totalCategories)
	} else if totalCategories > 0 {
		header = fmt.Sprintf("%s (%d total)", header, totalCategories)
	}

	results = append(results, header)
	results = append(results, "")

	for _, cat := range displayCategories {
		name := cat.Name
		if name == "" {
			name = cat.Path
		}
		itemInfo := fmt.Sprintf("%d items", cat.ItemCount)
		subcatInfo := ""
		if len(cat.ChildrenIDs) > 0 {
			subcatInfo = fmt.Sprintf(", %d subcategories", len(cat.ChildrenIDs))
		}

		description := ""
		if cat.Description != "" {
			description = fmt.Sprintf("\n  %s", cat.Description)
		}

		// Show the path to navigate to this category
		navPath := cat.Path
		if navPath == "" {
			navPath = cat.Name
		}

		entry := fmt.Sprintf("• %s (%s%s)%s\n  Navigate: omni:%s:%s", name, itemInfo, subcatInfo, description, kbName, navPath)
		results = append(results, entry)
	}

	// Separate each category entry with a blank line for readability
	if len(results) > 2 {
		joined := strings.Join(results[2:], "\n\n")
		results = []string{results[0], results[1], joined}
	}

	// Add pagination navigation if multiple pages exist
	if totalPages > 1 {
		results = append(results, "")
		var navHints []string

		pathPrefix := fmt.Sprintf("omni:%s", kbName)
		if categoryPath != "" {
			pathPrefix = fmt.Sprintf("%s:%s", pathPrefix, categoryPath)
		}

		if page > 1 {
			navHints = append(navHints, fmt.Sprintf("Previous: %s:page:%d", pathPrefix, page-1))
		}
		if page < totalPages {
			navHints = append(navHints, fmt.Sprintf("Next: %s:page:%d", pathPrefix, page+1))
		}

		if len(navHints) > 0 {
			results = append(results, strings.Join(navHints, " | "))
		}

		// Also suggest LLM filtering for large sets
		if totalCategories > 40 {
			results = append(results, fmt.Sprintf("💡 Tip: Use `%s:llm list categories matching <name>` to filter results.", pathPrefix))
		}
	}

	return strings.Join(results, "\n"), nil
}

func (a *CopilotAgent) resolveDBForKB(ctx context.Context, kbName string) *database.Database {
	p := ai.GetSessionPayload(ctx)
	if p != nil && p.CurrentDB != "" {
		if opts, ok := a.databases[p.CurrentDB]; ok {
			domainDB := database.NewDatabase(opts)
			if exists, _ := domainDB.KnowledgeBaseExists(ctx, kbName); exists {
				return domainDB
			}
		}
	}

	return a.systemDB
}
