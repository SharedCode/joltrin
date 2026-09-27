package memory

import (
	"context"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

// Database is an interface that allows the memory layer to orchestrate its own batched transactions.
type Database interface {
	ai.Database
	OpenKnowledgeBase(ctx context.Context, name string, tx sop.Transaction, llm ai.Generator, embedder ai.Embeddings, documentMode bool, enableTextSearch ...bool) (*KnowledgeBase[map[string]any], error)
	NewBtree(ctx context.Context, name string, t sop.Transaction) (btree.BtreeInterface[string, any], error)
}

// MemoryStore is the m-way tree dynamic capability database interface.
type MemoryStore[T any] interface {

	// Returns this Memory Store's name.
	Name() string

	// Upsert adds or updates a single item in the store.
	Upsert(ctx context.Context, item Item[T], vec []float32) error

	// UpsertByCategoryPath explicitly assigns a category ignoring spatial routing.
	UpsertByCategoryPath(ctx context.Context, categoryName string, item Item[T], vecs [][]float32) error

	// UpsertByCategoryID inserts data bypassing Category lookup.
	// vecs are DocumentTexts (768 dim), classificationVecs are CategoryTexts (256 dim) for DistanceToCategory.
	UpsertByCategoryID(ctx context.Context, catID sop.UUID, catCenterVector []float32, item Item[T], vecs [][]float32, classificationVecs [][]float32) error

	// UpsertBatch adds or updates multiple items in the store efficiently.
	UpsertBatch(ctx context.Context, items []Item[T], vecs [][]float32) error

	// FindClosestCategory explores the category tree using spatial distance to find the closest matching category.
	FindClosestCategory(ctx context.Context, vector []float32) (*Category, float32, error)

	// SemanticCategoryByPath resolves a category path expressed as pre-embedded vectors into the
	// closest matching Categories at each level of the hierarchy.
	//
	// Given a path "a/b/c" whose parts have been embedded into vectors [va, vb, vc]:
	//   - Level 0 (root): searches CategoriesByDistance with ParentID=NilUUID, anchor=DomainReference
	//   - Level N:        searches CategoriesByDistance with ParentID=prev.ID, anchor=prev.CenterVector
	// The function keeps all best-distance ties per level and returns the final best candidates.
	SemanticCategoryByPath(ctx context.Context, pathVectors [][]float32) ([]*Category, error)

	// Get retrieves a item by its logical ID.
	Get(ctx context.Context, key ItemKey) (*Item[T], error)
	// Delete removes an item by its logical ID.
	Delete(ctx context.Context, key ItemKey) error

	// Query searches for the nearest neighbors to the given vector coordinates.
	// filters is a function that returns true if the item should be included.
	Query(ctx context.Context, vec []float32, opts *SearchOptions[T]) ([]ai.Hit[T], error)

	// QueryItems searches stored items for the already-resolved category.
	// This lets the KnowledgeBase path reuse resolved categories instead of resolving them again.
	QueryItems(ctx context.Context, vec []float32, category *Category, opts *SearchOptions[T]) ([]ai.Hit[T], error)

	// QueryBatch searches for the nearest neighbors for a slice of query vectors.
	QueryBatch(ctx context.Context, vecs [][]float32, opts *SearchOptions[T]) ([][]ai.Hit[T], error)

	// QueryText performs a BM25 or keyword text search on the stored text representation of the thoughts.
	QueryText(ctx context.Context, text string, opts *SearchOptions[T]) ([]ai.Hit[T], error)

	// QueryTextBatch performs a BM25 or keyword text search for an array of queries.
	QueryTextBatch(ctx context.Context, texts []string, opts *SearchOptions[T]) ([][]ai.Hit[T], error)

	// Count returns the total number of items in the store.
	Count(ctx context.Context) (int64, error)

	// Categories returns a B-Tree interface to manually read/update hierarchical categories.
	Categories(ctx context.Context) (btree.BtreeInterface[sop.UUID, *Category], error)

	// CategoriesByPath returns a B-Tree interface to manually read/update path-indexed categories.
	CategoriesByPath(ctx context.Context) (btree.BtreeInterface[string, sop.UUID], error)

	// CategoriesByDistance returns a B-Tree interface for reading/updating distance-indexed categories.
	CategoriesByDistance(ctx context.Context) (btree.BtreeInterface[DistanceKey, byte], error)

	// AddCategory adds a new category to the store dynamically.
	// This allows for runtime expansion of the concept space without full rebalancing.
	AddCategory(ctx context.Context, c *Category) (sop.UUID, error)

	// AddCategoryParent connects an existing category to an additional parent, supporting
	// the polyhierarchy DAG structure. This is often leveraged during LLM Sleep Cycles.
	AddCategoryParent(ctx context.Context, categoryID sop.UUID, parent CategoryParent) error

	// Consolidate reads accumulated vectors from short-term memory (TempVectors),
	// dynamically routes them into existing Categories using AssignAndIndex logic,
	// and clears them from short-term memory.
	Consolidate(ctx context.Context) error

	// UpdateEmbedderInfo updates the configuration defining which embedder was used
	// to index the vectors, persisting it in the system configuration of the store.
	UpdateEmbedderInfo(ctx context.Context, provider string, model string, dimensions int) error

	// SetDomainReference sets the anchor vector for O(log N) category indexing.
	SetDomainReference(vec []float32)

	// DomainReference returns the anchor vector for O(log N) category indexing.
	DomainReference() []float32

	// SetLLM sets the LLM interface used to generate categories dynamically.
	SetLLM(llm LLM[T])

	// Vectors returns the Vectors B-Tree for advanced manipulation (Mathematical layout).
	Vectors(ctx context.Context) (btree.BtreeInterface[VectorKey, Vector], error)

	// Content returns the Content B-Tree for advanced manipulation (The actual Item Data).
	Items(ctx context.Context) (btree.BtreeInterface[ItemKey, Item[T]], error)

	// Documents returns the Documents B-Tree for reading the raw canonical documents.
	Documents(ctx context.Context) (btree.BtreeInterface[sop.UUID, Document], error)

	// UpsertDocument adds or updates a full canonical document.
	UpsertDocument(ctx context.Context, doc Document) error

	// GetDocument retrieves a full document by its ID.
	GetDocument(ctx context.Context, id sop.UUID) (*Document, error)

	// Version returns the Vector store's version number, which is a unix elapsed time.
	Version(ctx context.Context) (int64, error)
}

// SearchOptions provides optional parameters for querying the vector store
type SearchOptions[T any] struct {
	Limit int
	// CategoryPath can serve as a cheaper SearchByPath-style alternative to TextSearch
	// when the use-case has a stable, meaningful category taxonomy to route through.
	CategoryPath string
	// CategoryVector can be used to search for items within a given category.
	// The Category whose CenterVector is closest to this vector will be used as the search Category.
	CategoryVector []float32
	Filter         func(T) bool
}

// SearchRequest is the reusable public contract for single and batch retrieval.
type SearchRequest[T any] struct {
	Text           string
	Vector         []float32
	CategoryPath   string
	CategoryVector []float32
	Limit          int
	Filter         func(T) bool
}
