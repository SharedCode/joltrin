package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"github.com/google/uuid"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

type Section struct {
	Level       int
	Title       string
	Paragraphs  []string
	Children    []*Section
	LinkedFiles []string
	FilePath    string
}

func extractTextAndLinks(n ast.Node, source []byte) (string, []string) {
	var links []string
	var buf strings.Builder

	ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if link, ok := c.(*ast.Link); ok {
				dest := string(link.Destination)
				if strings.HasSuffix(strings.ToLower(dest), ".md") && !strings.HasPrefix(dest, "http") {
					links = append(links, dest)
				}
			}

			// Natively extract all text from all children regardless of what they are (Lists, Code, P)
			if c.Kind() == ast.KindText {
				buf.Write(c.(*ast.Text).Text(source))
			} else if c.Kind() == ast.KindHTMLBlock {
				lines := c.Lines()
				for i := 0; i < lines.Len(); i++ {
					seg := lines.At(i)
					buf.Write(seg.Value(source))
				}
			} else if c.Kind() == ast.KindRawHTML {
				rawHtml := c.(*ast.RawHTML)
				if rawHtml.Segments != nil {
					for i := 0; i < rawHtml.Segments.Len(); i++ {
						seg := rawHtml.Segments.At(i)
						buf.Write(seg.Value(source))
					}
				}
			} else if c.Kind() == ast.KindCodeSpan {
				buf.Write(c.Text(source))
				buf.WriteString(" ")
			}
		} else {
			// Formatting spacings on exit
			if c.Kind() == ast.KindParagraph || c.Kind() == ast.KindListItem || c.Kind() == ast.KindFencedCodeBlock {
				buf.WriteString("\n")
			}
		}

		// If it's a FencedCodeBlock, ast.Walk doesn't traverse the internal text nodes safely out of the box in some configs.
		// Instead we must grab its lines directly.
		if entering && c.Kind() == ast.KindFencedCodeBlock {
			lines := c.Lines()
			buf.WriteString("```\n")
			for i := 0; i < lines.Len(); i++ {
				seg := lines.At(i)
				buf.Write(seg.Value(source))
			}
			buf.WriteString("```\n")
			return ast.WalkSkipChildren, nil
		}

		return ast.WalkContinue, nil
	})

	return strings.TrimSpace(buf.String()), links
}

func getHeadingText(n *ast.Heading, source []byte) string {
	var buf strings.Builder
	ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering && c.Kind() == ast.KindText {
			buf.Write(c.(*ast.Text).Text(source))
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(buf.String())
}

var parsedFiles = make(map[string]*Section)
var baseURL string
var description string
var inputFile string
var singleFileMode bool

func parseMarkdownToTree(filePath string, defaultTitle string) *Section {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil
	}

	if existing, found := parsedFiles[absPath]; found {
		return existing
	}

	if strings.Contains(absPath, "knowledge_compiler") || strings.Contains(absPath, "AI_COPILOT_USAGE.md") {
		return nil
	}

	fmt.Printf("Crawling: %s\n", absPath)

	source, err := os.ReadFile(absPath)
	if err != nil {
		return nil
	}

	md := goldmark.New()
	doc := md.Parser().Parse(text.NewReader(source))

	root := &Section{Level: 0, Title: defaultTitle, FilePath: filePath}
	stack := []*Section{root}

	ignoring := false
	ignoredLevel := 0

	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		switch node := n.(type) {
		case *ast.Heading:
			level := node.Level

			if ignoring && level <= ignoredLevel {
				ignoring = false
			}

			title := getHeadingText(node, source)
			if strings.EqualFold(title, "table of contents") {
				ignoring = true
				ignoredLevel = level
				continue
			}

			if ignoring {
				continue
			}

			for len(stack) > 1 && stack[len(stack)-1].Level >= level {
				stack = stack[:len(stack)-1]
			}

			if level >= 4 && !singleFileMode {
				// In repo-wide sweeps, keep legacy behavior and fold deep headings into the parent body.
				prefix := strings.Repeat("#", level)
				textStr := prefix + " " + title
				stack[len(stack)-1].Paragraphs = append(stack[len(stack)-1].Paragraphs, textStr)
			} else {
				sec := &Section{
					Level:    level,
					Title:    title,
					FilePath: filePath,
				}
				stack[len(stack)-1].Children = append(stack[len(stack)-1].Children, sec)
				stack = append(stack, sec)
			}
		default:
			if ignoring {
				continue
			}
			textStr, links := extractTextAndLinks(node, source)
			if len(textStr) > 0 {
				stack[len(stack)-1].Paragraphs = append(stack[len(stack)-1].Paragraphs, textStr)
			}
			stack[len(stack)-1].LinkedFiles = append(stack[len(stack)-1].LinkedFiles, links...)
		}
	}

	// Cache tree for this file to prevent infinite recursions early
	parsedFiles[absPath] = root

	return root
}

func resetCompilerState() {
	parsedFiles = make(map[string]*Section)
	allChunks = nil
	allDocuments = nil
	catGraphMap = make(map[string]*memory.Category)
	catDescriptions = make(map[string]string)
}

func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}

const categorySlashPlaceholder = "\u0000SLASH\u0000"

var allChunks []KnowledgeChunk
var allDocuments []*memory.Document
var catGraphMap = make(map[string]*memory.Category)
var catDescriptions = make(map[string]string)

type KnowledgeChunk struct {
	ID          string   `json:"id"`
	Category    string   `json:"category"`
	CategoryKey string   `json:"-"`
	Title       string   `json:"title"`
	Summaries   []string `json:"summaries"`
	Description string   `json:"description"`
	Sources     []string `json:"sources"`
	DocumentID  sop.UUID `json:"document_id"`
	Explicit    bool     `json:"explicit"`
}

type explicitItemBlock struct {
	Title     string
	Summaries []string
	Body      string
	Sources   []string
}

func escapeCategoryPart(value string) string {
	return strings.ReplaceAll(value, "/", categorySlashPlaceholder)
}

func unescapeCategoryPart(value string) string {
	return strings.ReplaceAll(value, categorySlashPlaceholder, "/")
}

func getCat(catPath string) *memory.Category {
	if c, ok := catGraphMap[catPath]; ok {
		return c
	}

	parts := strings.Split(catPath, "/")
	displayParts := make([]string, len(parts))
	for i, part := range parts {
		displayParts[i] = unescapeCategoryPart(part)
	}

	id := sop.UUID(uuid.New())
	displayPath := strings.Join(displayParts, "/")
	c := &memory.Category{
		ID:   id,
		Name: removePrefix(displayParts[len(displayParts)-1], prefix),
		Path: displayPath,
	}
	if len(parts) > 1 {
		parentPath := strings.Join(parts[:len(parts)-1], "/")
		parentCat := getCat(parentPath)
		c.ParentIDs = append(c.ParentIDs, memory.CategoryParent{ParentID: parentCat.ID})
	}
	catGraphMap[catPath] = c
	return c
}

func buildExportItems(chunks []KnowledgeChunk) []memory.ExportItem[map[string]any] {
	exportItems := make([]memory.ExportItem[map[string]any], 0, len(chunks))
	for _, chunk := range chunks {
		catKey := chunk.CategoryKey
		if catKey == "" {
			parts := strings.Split(chunk.Category, "/")
			encodedParts := make([]string, len(parts))
			for i, part := range parts {
				encodedParts[i] = escapeCategoryPart(part)
			}
			catKey = strings.Join(encodedParts, "/")
		}
		cat := getCat(catKey)
		summaries := chunk.Summaries
		sources := normalizeSourceURLs(chunk.Sources)
		if len(summaries) == 0 && chunk.Title != "" {
			summaries = []string{chunk.Title}
		}
		exportItems = append(exportItems, memory.ExportItem[map[string]any]{
			CategoryPath: cat.ID.String(),
			DocID:        memory.DocIDs(sources),
			Data: map[string]any{
				"title":         chunk.Title,
				"text":          chunk.Title,
				"category":      cat.Name,
				"category_path": cat.Path,
				"description":   chunk.Description,
				"sources":       sources,
				"original_id":   chunk.ID,
			},
			Summaries: summaries,
		})
	}
	return exportItems
}

func normalizeStructuredLine(line string) string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "- ")
	return strings.TrimSpace(trimmed)
}

func splitSources(value string) []string {
	parts := strings.Split(value, ",")
	seen := make(map[string]bool)
	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func normalizeSourceURL(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return source
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "file://") {
		return source
	}
	if baseURL == "" {
		return source
	}

	cleaned := filepath.ToSlash(source)
	cleaned = strings.TrimPrefix(cleaned, "./")
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." {
		return strings.TrimSuffix(baseURL, "/")
	}

	resolved, err := url.JoinPath(baseURL, cleaned)
	if err != nil {
		return strings.TrimSuffix(baseURL, "/") + "/" + cleaned
	}
	return resolved
}

func normalizeSourceURLs(sources []string) []string {
	if len(sources) == 0 {
		return nil
	}
	out := make([]string, 0, len(sources))
	seen := make(map[string]bool)
	for _, source := range sources {
		resolved := normalizeSourceURL(source)
		if resolved == "" || seen[resolved] {
			continue
		}
		seen[resolved] = true
		out = append(out, resolved)
	}
	return out
}

func normalizeExplicitParagraphs(paragraphs []string) []string {
	markers := []string{"Summary:", "Body:", "Sources:", "Source:"}
	normalized := make([]string, 0, len(paragraphs))

	for _, paragraph := range paragraphs {
		var b strings.Builder
		for i := 0; i < len(paragraph); {
			matched := ""
			for _, marker := range markers {
				if strings.HasPrefix(paragraph[i:], marker) {
					matched = marker
					break
				}
			}
			if matched != "" {
				if b.Len() > 0 && b.String()[b.Len()-1] != '\n' {
					b.WriteByte('\n')
				}
				b.WriteString(matched)
				i += len(matched)
				continue
			}
			b.WriteByte(paragraph[i])
			i++
		}
		normalized = append(normalized, b.String())
	}

	return normalized
}

func parseExplicitItemBlocks(paragraphs []string) ([]explicitItemBlock, []string) {
	lines := strings.Split(strings.Join(normalizeExplicitParagraphs(paragraphs), "\n"), "\n")
	var blocks []explicitItemBlock
	var current *explicitItemBlock
	var bodyLines []string
	inBody := false
	var leftover []string

	finishCurrent := func() {
		if current == nil {
			return
		}
		current.Body = strings.TrimSpace(strings.Join(bodyLines, "\n"))
		if current.Title != "" && len(current.Summaries) > 0 && current.Body != "" {
			blocks = append(blocks, *current)
		} else {
			if current.Title != "" {
				leftover = append(leftover, "Item: "+current.Title)
			}
			for _, s := range current.Summaries {
				leftover = append(leftover, "Summary: "+s)
			}
			if current.Body != "" {
				leftover = append(leftover, "Body:")
				leftover = append(leftover, current.Body)
			}
			if len(current.Sources) > 0 {
				leftover = append(leftover, "Sources: "+strings.Join(current.Sources, ", "))
			}
		}
		current = nil
		bodyLines = nil
		inBody = false
	}

	for _, rawLine := range lines {
		normalized := normalizeStructuredLine(rawLine)
		if current == nil {
			if strings.HasPrefix(normalized, "Item:") {
				current = &explicitItemBlock{Title: strings.TrimSpace(strings.TrimPrefix(normalized, "Item:"))}
				continue
			}
			if strings.TrimSpace(rawLine) != "" {
				leftover = append(leftover, rawLine)
			}
			continue
		}

		if strings.HasPrefix(normalized, "Item:") {
			finishCurrent()
			current = &explicitItemBlock{Title: strings.TrimSpace(strings.TrimPrefix(normalized, "Item:"))}
			continue
		}

		if strings.HasPrefix(normalized, "Summary:") {
			inBody = false
			summary := strings.TrimSpace(strings.TrimPrefix(normalized, "Summary:"))
			if summary != "" && len(current.Summaries) < 5 {
				current.Summaries = append(current.Summaries, summary)
			}
			continue
		}

		if strings.HasPrefix(normalized, "Body:") {
			inBody = true
			bodyText := strings.TrimSpace(strings.TrimPrefix(normalized, "Body:"))
			if bodyText != "" {
				bodyLines = append(bodyLines, bodyText)
			}
			continue
		}

		if strings.HasPrefix(normalized, "Sources:") {
			inBody = false
			current.Sources = append(current.Sources, splitSources(strings.TrimSpace(strings.TrimPrefix(normalized, "Sources:")))...)
			continue
		}

		if strings.HasPrefix(normalized, "Source:") {
			inBody = false
			current.Sources = append(current.Sources, splitSources(strings.TrimSpace(strings.TrimPrefix(normalized, "Source:")))...)
			continue
		}

		if inBody {
			bodyLines = append(bodyLines, rawLine)
		} else if strings.TrimSpace(rawLine) != "" {
			leftover = append(leftover, rawLine)
		}
	}

	finishCurrent()

	for i := range blocks {
		blocks[i].Body = cleanText(blocks[i].Body)
		if len(blocks[i].Sources) > 1 {
			sort.Strings(blocks[i].Sources)
		}
	}

	var cleanedLeftover []string
	for _, line := range leftover {
		if strings.TrimSpace(line) != "" {
			cleanedLeftover = append(cleanedLeftover, line)
		}
	}

	return blocks, cleanedLeftover
}

func extractCategoryDescription(paragraphs []string, fallback string) string {
	if len(paragraphs) == 0 {
		return cleanText(fallback)
	}

	desc := strings.TrimSpace(paragraphs[0])
	if desc == "" {
		desc = fallback
	}
	return cleanText(desc)
}

func parseUnlinkedFiles(repoRoot string) {
	filepath.WalkDir(repoRoot, func(file string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") && name != "." && name != ".." {
				return filepath.SkipDir
			}
			if name == "node_modules" || name == "vendor" || name == "server" || name == "server_bin" {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(strings.ToLower(file), ".md") {
			return nil
		}

		absPath, _ := filepath.Abs(file)
		if strings.Contains(absPath, "knowledge_compiler") {
			return nil
		}

		filename := d.Name()
		upperName := strings.ToUpper(filename)
		if strings.Contains(upperName, "CODE_OF_CONDUCT") ||
			strings.Contains(upperName, "LICENSE") ||
			strings.Contains(upperName, "CHANGELOG") ||
			strings.Contains(upperName, "POST") ||
			strings.Contains(upperName, "ANNOUNCEMENT") ||
			strings.Contains(upperName, "RELEASE") ||
			strings.Contains(upperName, "PROPOSAL") ||
			strings.Contains(upperName, "CONTRIBUTING") ||
			strings.Contains(upperName, "LINKEDIN") ||
			strings.Contains(upperName, "DEV_TO_POST") ||
			strings.Contains(upperName, "AI_COPILOT") ||
			strings.Contains(upperName, "SYSTEM_KNOWLEDGE") ||
			strings.HasPrefix(upperName, "CLASSIFY_") ||
			strings.Contains(upperName, "KB_CURATION_MANIFEST") ||
			strings.Contains(upperName, "CURRENT_DESIGN_PLAN") {
			return nil
		}

		fmt.Printf("Sweeping file as L1 Category: %s\n", absPath)
		docTitle := strings.TrimSuffix(filepath.Base(absPath), filepath.Ext(absPath))
		title := docTitle
		tree := parseMarkdownToTree(absPath, title)
		if tree != nil {
			// --- Build Document metadata for Import ---
			// Since we intend to KEEP these docs out of the blob limits
			// and just process them dynamically on queries, we generate URLs
			// representing their local existence in repo, without pushing huge text chunks.
			relPath, _ := filepath.Rel(repoRoot, absPath)
			docID := sop.NewUUID()

			urlVal := "file://" + relPath
			if baseURL != "" {
				if strings.HasSuffix(baseURL, "/") {
					urlVal = baseURL + relPath
				} else {
					urlVal = baseURL + "/" + relPath
				}
			}

			doc := &memory.Document{
				ID:          docID,
				Title:       title,
				URL:         urlVal,
				ContentType: "text/markdown",
				Content:     "", // Omitted from physical DB storage because we referenced its filesystem URL
			}
			allDocuments = append(allDocuments, doc)

			// Extract title from top heading (H1, H2, H3, etc.) and description from its first paragraph
			if len(tree.Children) > 0 {
				title = tree.Children[0].Title
				if len(tree.Children[0].Paragraphs) > 0 {
					catDescriptions[normalizeCategoryName(title)] = cleanText(title + "\n\n" + tree.Children[0].Paragraphs[0])
				}
			} else if len(tree.Paragraphs) > 0 {
				catDescriptions[normalizeCategoryName(title)] = cleanText(title + "\n\n" + tree.Paragraphs[0])
			}

			if len(tree.Children) > 0 {
				if len(tree.Paragraphs) > 0 {
					tree.Children[0].Paragraphs = append(tree.Paragraphs, tree.Children[0].Paragraphs...)
				}
				for _, child := range tree.Children {
					if strings.EqualFold(title, child.Title) {
						if len(child.Children) == 0 && len(child.Paragraphs) == 0 {
							continue
						}
						processFlattenedTreeIntoChunks(child, []string{title}, docID)
					} else {
						if len(child.Children) == 0 && len(child.Paragraphs) == 0 {
							continue
						}
						processFlattenedTreeIntoChunks(child, []string{title, child.Title}, docID)
					}
				}
			} else {
				processFlattenedTreeIntoChunks(tree, []string{title}, docID)
			}
		}

		return nil
	})
}

func parseOneMarkdownFile(repoRoot string, file string) {
	absPath, err := filepath.Abs(file)
	if err != nil {
		return
	}

	filename := strings.ToUpper(filepath.Base(absPath))
	if strings.Contains(filename, "CODE_OF_CONDUCT") ||
		strings.Contains(filename, "LICENSE") ||
		strings.Contains(filename, "CHANGELOG") ||
		strings.Contains(filename, "POST") ||
		strings.Contains(filename, "ANNOUNCEMENT") ||
		strings.Contains(filename, "RELEASE") ||
		strings.Contains(filename, "PROPOSAL") ||
		strings.Contains(filename, "CONTRIBUTING") ||
		strings.Contains(filename, "LINKEDIN") ||
		strings.Contains(filename, "DEV_TO_POST") ||
		strings.Contains(filename, "AI_COPILOT") ||
		strings.Contains(filename, "SYSTEM_KNOWLEDGE") ||
		strings.HasPrefix(filename, "CLASSIFY_") ||
		strings.Contains(filename, "KB_CURATION_MANIFEST") ||
		strings.Contains(filename, "CURRENT_DESIGN_PLAN") {
		fmt.Printf("Skipping ineligible markdown file: %s\n", absPath)
		return
	}

	title := strings.TrimSuffix(filepath.Base(absPath), filepath.Ext(absPath))
	singleFileMode = true
	tree := parseMarkdownToTree(absPath, title)
	if tree == nil {
		return
	}

	relPath, _ := filepath.Rel(repoRoot, absPath)
	docID := sop.NewUUID()

	urlVal := "file://" + relPath
	if baseURL != "" {
		if strings.HasSuffix(baseURL, "/") {
			urlVal = baseURL + relPath
		} else {
			urlVal = baseURL + "/" + relPath
		}
	}

	doc := &memory.Document{
		ID:          docID,
		Title:       title,
		URL:         urlVal,
		ContentType: "text/markdown",
		Content:     "",
	}
	allDocuments = append(allDocuments, doc)

	if len(tree.Children) > 0 {
		for _, rootSection := range tree.Children {
			if len(rootSection.Children) == 0 && len(rootSection.Paragraphs) == 0 {
				continue
			}
			processFlattenedTreeIntoChunks(rootSection, []string{rootSection.Title}, docID)
		}
	} else {
		processFlattenedTreeIntoChunks(tree, []string{tree.Title}, docID)
	}
}

func processFlattenedTreeIntoChunks(node *Section, currentPathContext []string, docID sop.UUID) {
	var normalizedPaths []string
	for _, p := range currentPathContext {
		normalizedPaths = append(normalizedPaths, normalizeCategoryName(p))
	}

	var catPath string
	if len(normalizedPaths) > 0 {
		catPath = strings.Join(normalizedPaths, "/")
	} else {
		catPath = normalizeCategoryName(node.Title)
	}

	encodedPaths := make([]string, 0, len(normalizedPaths))
	for _, p := range normalizedPaths {
		encodedPaths = append(encodedPaths, escapeCategoryPart(p))
	}
	catKey := strings.Join(encodedPaths, "/")

	explicitBlocks, remainingParagraphs := parseExplicitItemBlocks(node.Paragraphs)

	cleanedDesc := extractCategoryDescription(remainingParagraphs, node.Title)
	if len(cleanedDesc) > 500 {
		runes := []rune(cleanedDesc)
		if len(runes) > 500 {
			cleanedDesc = string(runes[:500]) + "..."
		}
	}
	catDescriptions[catPath] = cleanedDesc

	for _, block := range explicitBlocks {
		idPrefix := strings.ReplaceAll(block.Title, " ", "_")
		allChunks = append(allChunks, KnowledgeChunk{
			ID:          fmt.Sprintf("%s_item_%d", idPrefix, len(allChunks)),
			Category:    catPath,
			CategoryKey: catKey,
			Title:       removePrefix(cleanText(block.Title), prefix),
			Summaries:   block.Summaries,
			Description: block.Body,
			Sources:     block.Sources,
			DocumentID:  docID,
			Explicit:    true,
		})
	}

	body := strings.TrimSpace(strings.Join(remainingParagraphs, "\n"))

	if strings.Contains(node.Title, "Execute Script Tool") {
		fmt.Printf("Execute Script Tool -> bodyLen: %d, children: %d\n", len(body), len(node.Children))
	}
	// In curated input-file mode, the paragraph under a category heading is treated as
	// description metadata rather than a synthetic section item. Keep the legacy
	// synthetic-item behavior only for repo-wide sweeps.
	if !singleFileMode && len(body) >= 10 && (len(body) > 500 || len(node.Children) == 0) {
		idPrefix := strings.ReplaceAll(node.Title, " ", "_")
		allChunks = append(allChunks, KnowledgeChunk{
			ID:          fmt.Sprintf("%s_section_%d", idPrefix, len(allChunks)),
			Category:    catPath,
			CategoryKey: catKey,
			Title:       removePrefix(cleanText(node.Title), prefix),
			Summaries:   []string{removePrefix(cleanText(node.Title), prefix)},
			Description: cleanText(body),
			DocumentID:  docID,
			Explicit:    false,
		})
	}

	for _, child := range node.Children {
		childPathContext := append([]string{}, currentPathContext...)
		childPathContext = append(childPathContext, child.Title)
		processFlattenedTreeIntoChunks(child, childPathContext, docID)
	}
}

var prefix string

func buildKnowledgeBaseConfig(desc, systemPrompt string, documentMode bool) *memory.KnowledgeBaseConfig {
	return &memory.KnowledgeBaseConfig{
		IsPersona:    true,
		Description:  strings.TrimSpace(desc),
		SystemPrompt: strings.TrimSpace(systemPrompt),
		DocumentMode: documentMode,
	}
}

func main() {
	flag.StringVar(&baseURL, "base-url", "", "Base URL for documents")
	flag.StringVar(&description, "desc", "", "Optional description to persist on the generated KB config")
	flag.StringVar(&prefix, "prefix", "", "Prefix to remove from category names")
	flag.StringVar(&inputFile, "input", "", "Optional single markdown file to compile instead of sweeping the repo")
	flag.Parse()

	singleFileMode = inputFile != ""
	resetCompilerState()
	repoRoot := findRepoRoot()

	curatedMode := inputFile != ""
	if curatedMode {
		fmt.Printf("Compiling single markdown file: %s\n", inputFile)
		parseOneMarkdownFile(repoRoot, inputFile)
	} else {
		fmt.Println("Sweeping unlinked Markdown files...")
		parseUnlinkedFiles(repoRoot)
	}

	if !curatedMode {
		fmt.Println("Rolling up single-item sub-categories...")
		changed := true
		for changed {
			changed = false
			itemsPerCat := make(map[string][]int)
			for i, chunk := range allChunks {
				itemsPerCat[chunk.Category] = append(itemsPerCat[chunk.Category], i)
			}

			for catPath, indices := range itemsPerCat {
				if len(indices) == 1 && strings.Contains(catPath, "/") {
					hasChildren := false
					for otherCat := range itemsPerCat {
						if strings.HasPrefix(otherCat, catPath+"/") {
							hasChildren = true
							break
						}
					}

					if !hasChildren {
						parts := strings.Split(catPath, "/")
						parentPath := strings.Join(parts[:len(parts)-1], "/")

						idx := indices[0]
						if allChunks[idx].Explicit {
							continue
						}

						// Ensure the original ## header name is not lost when flattening
						headerName := parts[len(parts)-1]
						if len(allChunks[idx].Summaries) > 0 && !strings.HasPrefix(allChunks[idx].Summaries[0], headerName) {
							if allChunks[idx].Summaries[0] != headerName {
								allChunks[idx].Summaries[0] = headerName + " - " + allChunks[idx].Summaries[0]
							}
						}

						allChunks[idx].Category = parentPath
						changed = true
					}
				}
			}
		}
	}

	exportItems := buildExportItems(allChunks)

	var categories []*memory.Category
	for _, c := range catGraphMap {
		if desc, ok := catDescriptions[c.Path]; ok {
			c.Description = desc
		} else if desc, ok := catDescriptions[c.Name]; ok {
			c.Description = desc
		}
		categories = append(categories, c)
	}

	var systemPrompt string
	personaBytes, err := os.ReadFile(filepath.Join(repoRoot, "ai", "OMNI_PERSONA.md"))
	if err == nil {
		systemPrompt = string(personaBytes)
	}

	outObj := memory.ExportData[map[string]any]{
		Config:     buildKnowledgeBaseConfig(description, systemPrompt, baseURL != ""),
		Categories: categories,
		Documents:  allDocuments,
		Items:      exportItems,
	}

	outData, _ := json.MarshalIndent(outObj, "", "  ")
	outputPath := filepath.Join(repoRoot, "ai", "sop_base_knowledge.json")
	os.WriteFile(outputPath, outData, 0644)

	fmt.Printf("Success! Compiled %d knowledge items and %d categories into %s\n", len(exportItems), len(categories), outputPath)
}
