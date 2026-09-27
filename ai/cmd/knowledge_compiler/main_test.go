package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

func TestParseExplicitItemBlocks(t *testing.T) {
	paragraphs := []string{
		"Intro paragraph for category description.",
		"Item: Gemini embedding contract\nSummary: Gemini embedder supports gemini-embedding-2 via batchEmbedContents.\nSummary: Requests set outputDimensionality to 768.\nBody:\nThe Gemini embedder uses the Google batch embedding endpoint and emits retrieval-oriented requests.\nSources: ai/embed/gemini2.go, ai/embed/gemini2_test.go",
	}

	blocks, leftover := parseExplicitItemBlocks(paragraphs)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 explicit item block, got %d", len(blocks))
	}
	if len(leftover) != 1 {
		t.Fatalf("expected 1 leftover paragraph, got %d (%v)", len(leftover), leftover)
	}

	block := blocks[0]
	if block.Title != "Gemini embedding contract" {
		t.Fatalf("unexpected item title: %q", block.Title)
	}
	if len(block.Summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d (%v)", len(block.Summaries), block.Summaries)
	}
	if block.Summaries[0] != "Gemini embedder supports gemini-embedding-2 via batchEmbedContents." {
		t.Fatalf("unexpected first summary: %q", block.Summaries[0])
	}
	if block.Body == "" {
		t.Fatal("expected non-empty body")
	}
	if len(block.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %d (%v)", len(block.Sources), block.Sources)
	}
	if leftover[0] != "Intro paragraph for category description." {
		t.Fatalf("unexpected leftover paragraph: %q", leftover[0])
	}
}

func TestBuildExportItems_UsesBaseURLForSourcesWhenConfigured(t *testing.T) {
	oldBaseURL := baseURL
	baseURL = "https://github.com/SharedCode/joltrin"
	defer func() { baseURL = oldBaseURL }()

	catGraphMap = make(map[string]*memory.Category)
	chunks := []KnowledgeChunk{{
		ID:          "item_1",
		Category:    "AI & Knowledge Systems/Embedders",
		Title:       "Source citation sample",
		Summaries:   []string{"Summary one"},
		Description: "Full explanation body.",
		Sources:     []string{"README.md", "ai/README.md", "GO_CORE_ENGINE.md"},
		DocumentID:  sop.NewUUID(),
		Explicit:    true,
	}}

	exportItems := buildExportItems(chunks)
	got, ok := exportItems[0].Data["sources"].([]string)
	if !ok {
		t.Fatalf("expected []string sources in payload, got %#v", exportItems[0].Data["sources"])
	}

	want := []string{
		"https://github.com/SharedCode/joltrin/README.md",
		"https://github.com/SharedCode/joltrin/ai/README.md",
		"https://github.com/SharedCode/joltrin/GO_CORE_ENGINE.md",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected sources: got %v, want %v", got, want)
	}
}

func TestBuildExportItemsUsesExplicitSummariesAndPayload(t *testing.T) {
	catGraphMap = make(map[string]*memory.Category)
	chunks := []KnowledgeChunk{{
		ID:          "gemini_item_1",
		Category:    "AI & Knowledge Systems/Embedders",
		Title:       "Gemini embedding contract",
		Summaries:   []string{"Summary one", "Summary two"},
		Description: "Full explanation body.",
		Sources:     []string{"ai/embed/gemini2.go", "ai/embed/gemini2_test.go"},
		DocumentID:  sop.NewUUID(),
		Explicit:    true,
	}}

	exportItems := buildExportItems(chunks)
	if len(exportItems) != 1 {
		t.Fatalf("expected 1 export item, got %d", len(exportItems))
	}
	if len(exportItems[0].Summaries) != 2 {
		t.Fatalf("expected 2 export summaries, got %d", len(exportItems[0].Summaries))
	}
	if exportItems[0].Summaries[0] != "Summary one" {
		t.Fatalf("unexpected export summary: %q", exportItems[0].Summaries[0])
	}
	if exportItems[0].Data["title"] != "Gemini embedding contract" {
		t.Fatalf("expected title in payload, got %#v", exportItems[0].Data["title"])
	}
	if exportItems[0].Data["description"] != "Full explanation body." {
		t.Fatalf("expected description in payload, got %#v", exportItems[0].Data["description"])
	}
	sources, ok := exportItems[0].Data["sources"].([]string)
	if !ok {
		t.Fatalf("expected []string sources in payload, got %#v", exportItems[0].Data["sources"])
	}
	if len(sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(sources))
	}
}

func TestParseExplicitItemBlocks_NormalizesFlattenedParagraphs(t *testing.T) {
	root := parseMarkdownToTree("../../SOP_CURATED_KB.md", "SOP_CURATED_KB")
	if root == nil {
		t.Fatal("expected curated KB tree")
	}

	platform := root.Children[0]
	architecture := platform.Children[0]
	blocks, leftover := parseExplicitItemBlocks([]string{architecture.Paragraphs[1]})

	if len(blocks) != 2 {
		t.Fatalf("expected 2 explicit blocks, got %d (%v)", len(blocks), blocks)
	}
	if len(leftover) != 0 {
		t.Fatalf("expected 0 leftovers, got %d (%v)", len(leftover), leftover)
	}
	if blocks[0].Title != "Filesystem and hybrid backend model" {
		t.Fatalf("unexpected first block title: %q", blocks[0].Title)
	}
	if blocks[1].Title != "Registry as source of truth" {
		t.Fatalf("unexpected second block title: %q", blocks[1].Title)
	}
}

func TestExtractCategoryDescription_UsesPlainParagraphUnderHeading(t *testing.T) {
	desc := extractCategoryDescription([]string{"This category explains the backend stack."}, "Fallback Title")
	if desc != "This category explains the backend stack." {
		t.Fatalf("unexpected category description: %q", desc)
	}
}

func TestExtractCategoryDescription_FallsBackToHeading(t *testing.T) {
	desc := extractCategoryDescription(nil, "Filesystem / Redis / Cassandra")
	if desc != "Filesystem / Redis / Cassandra" {
		t.Fatalf("expected heading fallback, got %q", desc)
	}
}

func TestParseOneMarkdownFile_UsesParagraphBeforeItemAsCategoryDescription(t *testing.T) {
	resetCompilerState()
	repoRoot := t.TempDir()
	filePath := filepath.Join(repoRoot, "category_description.md")
	content := `# Platform Foundations

## Architecture

This category covers backend topology, transaction flow, and registry-centered consistency.

- Item: Core architecture
  Summary: Core summary.
  Body:
  Core body.
  Sources: arch.md
`
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	parseOneMarkdownFile(repoRoot, filePath)

	if got := catDescriptions["Platform Foundations/Architecture"]; got != "This category covers backend topology, transaction flow, and registry-centered consistency." {
		t.Fatalf("unexpected category description: %q", got)
	}

	var explicitCount int
	for _, chunk := range allChunks {
		if chunk.Category == "Platform Foundations/Architecture" {
			explicitCount++
		}
	}
	if explicitCount != 1 {
		t.Fatalf("expected exactly one compiled chunk for the category description path, got %d", explicitCount)
	}
}

func TestParseOneMarkdownFile_ReducesOnlySyntheticRootCategory(t *testing.T) {
	resetCompilerState()
	repoRoot := t.TempDir()
	filePath := filepath.Join(repoRoot, "single_input.md")
	content := `# Synthetic Root

Intro paragraph.

## Category A

- Item: Alpha item
  Summary: Alpha summary line.
  Body:
  Alpha body details.
  Sources: alpha.md

### Child A

- Item: Beta item
  Summary: Beta summary line.
  Body:
  Beta body details.
  Sources: beta.md

#### Grandchild A

- Item: Gamma item
  Summary: Gamma summary line.
  Body:
  Gamma body details.
  Sources: gamma.md
`
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	parseOneMarkdownFile(repoRoot, filePath)

	if len(allChunks) < 2 {
		t.Fatalf("expected compiled chunks, got %d", len(allChunks))
	}

	seen := map[string]bool{}
	for _, chunk := range allChunks {
		seen[chunk.Category] = true
	}
	if !seen["Synthetic Root/Category A"] {
		t.Fatalf("expected category path %q, got %+v", "Synthetic Root/Category A", seen)
	}
	if !seen["Synthetic Root/Category A/Child A"] {
		t.Fatalf("expected nested category path %q, got %+v", "Synthetic Root/Category A/Child A", seen)
	}
	if !seen["Synthetic Root/Category A/Child A/Grandchild A"] {
		t.Fatalf("expected deeper nested category path %q, got %+v", "Synthetic Root/Category A/Child A/Grandchild A", seen)
	}
}

func TestParseOneMarkdownFile_PreservesFirstNestedCategoryLevel(t *testing.T) {
	resetCompilerState()
	repoRoot := t.TempDir()
	filePath := filepath.Join(repoRoot, "language_bindings.md")
	content := `## Language Bindings

### Go

- Item: Go binding
  Summary: Go summary.
  Body:
  Go binding details.
  Sources: go.md

### Java

- Item: Java binding
  Summary: Java summary.
  Body:
  Java binding details.
  Sources: java.md
`
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	parseOneMarkdownFile(repoRoot, filePath)

	seen := map[string]bool{}
	for _, chunk := range allChunks {
		seen[chunk.Category] = true
	}

	if seen["Go"] || seen["Java"] {
		t.Fatalf("expected language categories to remain under Language Bindings, got %+v", seen)
	}
	if !seen["Language Bindings/Go"] {
		t.Fatalf("expected nested category path %q, got %+v", "Language Bindings/Go", seen)
	}
	if !seen["Language Bindings/Java"] {
		t.Fatalf("expected nested category path %q, got %+v", "Language Bindings/Java", seen)
	}
}

func TestParseOneMarkdownFile_MapsMultipleH1SectionsIntoMultipleL1Categories(t *testing.T) {
	resetCompilerState()
	repoRoot := t.TempDir()
	filePath := filepath.Join(repoRoot, "multi_h1.md")
	content := `# Platform Foundations

## Architecture

- Item: Core architecture
  Summary: Core summary.
  Body:
  Core body.
  Sources: arch.md

# Installation & Setup

## Prerequisites

- Item: Setup prerequisites
  Summary: Setup summary.
  Body:
  Setup body.
  Sources: setup.md
`
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	parseOneMarkdownFile(repoRoot, filePath)

	seen := map[string]bool{}
	for _, chunk := range allChunks {
		seen[chunk.Category] = true
	}

	if seen["Platform Foundations/Architecture"] == false {
		t.Fatalf("expected H1 section to remain as L1 category, got %+v", seen)
	}
	if seen["Installation & Setup/Prerequisites"] == false {
		t.Fatalf("expected second H1 section to remain as separate L1 category, got %+v", seen)
	}
	if seen["Platform Foundations"] || seen["Installation & Setup"] {
		t.Fatalf("expected only real H1-derived paths, not synthetic file-root aliases, got %+v", seen)
	}
}

func TestParseOneMarkdownFile_PreservesTopHeadingCategory(t *testing.T) {
	resetCompilerState()
	repoRoot := t.TempDir()
	filePath := filepath.Join(repoRoot, "curated_bindings.md")
	content := `# Language Bindings & Tooling

## Go

- Item: Go binding
  Summary: Go summary.
  Body:
  Go binding details.
  Sources: go.md

## Java

- Item: Java binding
  Summary: Java summary.
  Body:
  Java binding details.
  Sources: java.md
`
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	parseOneMarkdownFile(repoRoot, filePath)

	seen := map[string]bool{}
	for _, chunk := range allChunks {
		seen[chunk.Category] = true
	}

	if seen["Go"] || seen["Java"] {
		t.Fatalf("expected top heading category to remain in the path, got %+v", seen)
	}
	if !seen["Language Bindings & Tooling/Go"] {
		t.Fatalf("expected nested category path %q, got %+v", "Language Bindings & Tooling/Go", seen)
	}
	if !seen["Language Bindings & Tooling/Java"] {
		t.Fatalf("expected nested category path %q, got %+v", "Language Bindings & Tooling/Java", seen)
	}
}

func TestBuildKnowledgeBaseConfig_UsesDescriptionFromCLI(t *testing.T) {
	cfg := buildKnowledgeBaseConfig("Scalable Objects Persistence (SOP)", "persona prompt", true)

	if cfg.Description != "Scalable Objects Persistence (SOP)" {
		t.Fatalf("expected Description to be propagated, got %q", cfg.Description)
	}
	if cfg.SystemPrompt != "persona prompt" {
		t.Fatalf("expected SystemPrompt to be preserved, got %q", cfg.SystemPrompt)
	}
	if !cfg.DocumentMode {
		t.Fatal("expected DocumentMode to remain enabled")
	}
}

func TestBuildExportItems_IncludesLeafCategoryForUI(t *testing.T) {
	catGraphMap = make(map[string]*memory.Category)
	catDescriptions = make(map[string]string)

	items := buildExportItems([]KnowledgeChunk{{
		ID:          "chunk-1",
		Category:    "Root/Child",
		Title:       "compiled title",
		Summaries:   []string{"compiled summary"},
		Description: "compiled description",
		DocumentID:  sop.NewUUID(),
	}})

	if len(items) != 1 {
		t.Fatalf("expected 1 export item, got %d", len(items))
	}

	data := items[0].Data
	if got := data["category"]; got != "Child" {
		t.Fatalf("expected leaf category 'Child', got %v", got)
	}
	if got := data["category_path"]; got != "Root/Child" {
		t.Fatalf("expected category path 'Root/Child', got %v", got)
	}
	if items[0].CategoryPath == "" {
		t.Fatal("expected category path id to be populated")
	}
}
