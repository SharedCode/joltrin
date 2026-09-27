package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sharedcode/joltrin/v5"
)

type KnowledgeBaseConfig struct {
	Type                string            `json:"type,omitempty"`
	IsPersona           bool              `json:"is_persona,omitempty"`
	IsExclusive         bool              `json:"is_exclusive,omitempty"`
	Description         string            `json:"description,omitempty"`
	SystemPrompt        string            `json:"system_prompt,omitempty"`
	Embedder            string            `json:"embedder,omitempty"`
	EmbedderDimension   int               `json:"embedder_dimension,omitempty"`
	AllowAutoEnrichment bool              `json:"allowAutoEnrichment,omitempty"`
	AllowedTools        []string          `json:"allowed_tools,omitempty"`
	ToolQueries         []PathSearchParam `json:"tool_queries,omitempty"`
	LastModified        int64             `json:"last_modified,omitempty"`   // Unix timestamp
	LastVectorized      int64             `json:"last_vectorized,omitempty"` // Unix timestamp
	RoutingPrefix       string            `json:"routing_prefix,omitempty"`
	DomainReference     []float32         `json:"domain_reference,omitempty"`
	// DocumentMode flags whether this KB operates in traditional payload mode (Item.Data holds data),
	// or in decoupled RAG references mode where Item.Data points to the canonical large Document(MD).
	DocumentMode      bool `json:"document_mode,omitempty"`
	TextSearchEnabled bool `json:"text_search_enabled,omitempty"` // Controls if keyword/BM25 search is indexed and available
}

// DocIDs stores one or more source document references for an item.
// It accepts both a single string and a JSON array, which keeps older exports compatible.
type DocIDs []string

func (d DocIDs) First() string {
	if len(d) == 0 {
		return ""
	}
	return d[0]
}

func (d DocIDs) String() string {
	return strings.Join(d, ",")
}

func (d DocIDs) MarshalJSON() ([]byte, error) {
	if len(d) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal([]string(d))
}

func (d *DocIDs) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*d = nil
		return nil
	}

	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*d = DocIDs{single}
		return nil
	}

	var multi []string
	if err := json.Unmarshal(data, &multi); err == nil {
		*d = DocIDs(multi)
		return nil
	}

	return fmt.Errorf("doc_id must be a string or an array of strings")
}

// Item represents the actual content (The "Thought" or Document).
// Singular form as requested. It is fundamentally mapped to one or more Vector embeddings.
type Item[T any] struct {
	ID         sop.UUID    `json:"id"`
	CategoryID sop.UUID    `json:"category_id"`
	DocID      DocIDs      `json:"doc_id,omitempty"`      // UUID of uploaded documents OR string URI for external docs
	Summaries  []string    `json:"summaries,omitempty"`   // 1 or more distinct, clean sentences for vector indexing
	Data       T           `json:"data"`                  // The application data or structured thought
	Positions  []VectorKey `json:"positions,omitempty"`   // Direct links to its Vectors for O(1) cleanup during Category moves
	VectorHash string      `json:"vector_hash,omitempty"` // Hash of EmbedderName + Content to avoid re-vectorizing unchanged items
}

// Returns true if Item is considered the KnowledgeBase's Config item.
func (item *Item[T]) IsConfig() bool {
	return item.ID == sop.NilUUID
}

// IsInternalDocument returns true if the DocID is a valid SOP UUID, meaning the document is stored natively in the KB.
func (item *Item[T]) IsInternalDocument() bool {
	if len(item.DocID) == 0 {
		return false
	}
	_, err := sop.ParseUUID(item.DocID.First())
	return err == nil
}

// IsExternalDocument returns true if the DocID is populated but is not a valid SOP UUID (e.g. an HTTP/File URI).
func (item *Item[T]) IsExternalDocument() bool {
	if len(item.DocID) == 0 {
		return false
	}
	_, err := sop.ParseUUID(item.DocID.First())
	return err != nil
}

// Vector represents the pointer/index fragment mapping the math to the Item.
type Vector struct {
	ID         sop.UUID  `json:"id"`
	Data       []float32 `json:"data"`        // Math coordinate
	ItemID     sop.UUID  `json:"item_id"`     // Points to the actual Item
	CategoryID sop.UUID  `json:"category_id"` // Critical for category-partitioned semantic searches
}

// CategoryParent represents a relationship to a parent category,
// capturing the explicit operational use-case for this edge in the DAG.
type CategoryParent struct {
	ParentID sop.UUID `json:"parent_id"`
	UseCase  string   `json:"use_case,omitempty"` // The explicit justification (can be empty "" for the primary/obvious parent)
}

// Category represents the semantic Map/Hierarchy (formerly Centroid).
// Singular form matching Item.
type Category struct {
	ID sop.UUID `json:"id"`
	// ParentIDs points to parent Categories, allowing a Directed Acyclic Graph (DAG) / Polyhierarchy.
	// Use-case: A category like "Database Migrations" can belong to both "Release Management"
	// and "Database Administration". Adding the explicit UseCase here gives the LLM
	// the ability to review, validate, and improve these graph edges during deep sleep cycles.
	ParentIDs       []CategoryParent `json:"parents,omitempty"`
	CenterVector    []float32        `json:"center_vector"`               // Mathematical center of this chunk/category
	ChildrenIDs     []sop.UUID       `json:"children_ids,omitempty"`      // IDs of Sub-Categories
	Radius          float32          `json:"radius,omitempty"`            // Size of the cluster
	ItemCount       int              `json:"item_count,omitempty"`        // Number of vectors/items in this bucket
	Name            string           `json:"name,omitempty"`              // Human-readable concept name
	Path            string           `json:"path,omitempty"`              // Full contextual taxonomy path (e.g. "tools / execute_script")
	Description     string           `json:"description,omitempty"`       // Broader context
	SummaryMaxCount int              `json:"summary_max_count,omitempty"` // Maximum number of summaries for items in this category
	VectorHash      string           `json:"vector_hash,omitempty"`       // Hash of EmbedderName + Content to deduplicate vectorization
}

// VectorKey is the key for the Vectors B-Tree. It dictates how vectors are sorted
// mathematically relative to their parent Category.
type VectorKey struct {
	CategoryID         sop.UUID // Points to the hierarchical Category ID
	DistanceToCategory float32
	VectorID           sop.UUID // Points to the specific Vector ID
}

// ItemKey composites the Category and Item identity for physical clustering
type ItemKey struct {
	CategoryID sop.UUID
	ItemID     sop.UUID
}

// Compare implements btree.Comparer for ItemKey to ensure fast B-Tree physical clustering.
func (k ItemKey) Compare(other any) int {
	o, ok := other.(ItemKey)
	if !ok {
		return -1
	}
	if c := k.CategoryID.Compare(o.CategoryID); c != 0 {
		return c
	}
	return k.ItemID.Compare(o.ItemID)
}

// DistanceKey represents a distance-based index for sorting categories by distance to the Domain Reference CenterVector.
type DistanceKey struct {
	ParentID sop.UUID // NilUUID for Level 1 Macro-Categories
	Distance float32  // Mathematical distance relative to the bounding anchor
	ID       sop.UUID // ID of the Category
}

// Compare implements btree.Comparer for DistanceKey to enable fast distance-based in
// indexing, grouped by taxonomy depth and parent node.
func (k DistanceKey) Compare(other any) int {
	o, ok := other.(DistanceKey)
	if !ok {
		return -1
	}

	if c := k.ParentID.Compare(o.ParentID); c != 0 {
		return c
	}

	if k.Distance < o.Distance {
		return -1
	} else if k.Distance > o.Distance {
		return 1
	}

	return k.ID.Compare(o.ID)
}

// Document represents a large source asset (markdown file, text blob, PDF parsed text, etc).
// It acts as the canonical reading interface to prevent bloated indexes and context-loss in RAG.
// Multiple Items (with unique Vectors/Summaries) can point back to this same Document.
type Document struct {
	ID          sop.UUID `json:"id"`
	Title       string   `json:"title,omitempty"`
	URL         string   `json:"url,omitempty"`
	Source      string   `json:"source,omitempty"`
	ContentType string   `json:"content_type,omitempty"` // e.g. "text/markdown", "text/plain"
	Content     string   `json:"content,omitempty"`
	Data        []byte   `json:"data,omitempty"` // For pure blobs, pdf binaries, etc
}

// ChunkData is a standard struct designed for the generic T in Item[T],
// specifically formulated for two-stage Retrieval-Augmented Generation (RAG).
type ChunkData struct {
	Text        string   `json:"text,omitempty"`        // Small snippet or chunk directly answerable
	Description string   `json:"description,omitempty"` // Contextual description or rationale
	DocumentID  sop.UUID `json:"document_id,omitempty"` // Pointer to the heavyweight Document/Blob (can be NilUUID)
}
