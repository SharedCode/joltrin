package agent

import (
	"time"

	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

// Space Lifecycle Types

// CreateSpaceArgs represents arguments for creating/opening a Space
type CreateSpaceArgs struct {
	Database string `json:"database,omitempty" example:"dev_db"`
	KBName   string `json:"kb_name" binding:"required" example:"Notes"`
}

// DeleteSpaceArgs represents arguments for deleting a Space
type DeleteSpaceArgs struct {
	Database string `json:"database,omitempty" example:"dev_db"`
	KBName   string `json:"kb_name" binding:"required" example:"Notes"`
}

// EnrichSpaceArgs represents arguments for enriching a Space
type EnrichSpaceArgs struct {
	Database string `json:"database,omitempty" example:"dev_db"`
	KBName   string `json:"kb_name" binding:"required" example:"Notes"`
}

// MintToSpaceArgs represents arguments for minting content to a Space
type MintToSpaceArgs struct {
	KBName   string `json:"kb_name" binding:"required" example:"Notes"`
	Content  string `json:"content" binding:"required"`
	Category string `json:"category,omitempty" example:"General"`
}

// Space Configuration Types

// UpdateSpaceConfigArgs represents arguments for updating Space configuration
type UpdateSpaceConfigArgs struct {
	Database string                     `json:"database,omitempty" example:"dev_db"`
	KBName   string                     `json:"kb_name" binding:"required" example:"Notes"`
	Config   memory.KnowledgeBaseConfig `json:"config" binding:"required"`
}

// ReadSpaceConfigArgs represents arguments for reading Space configuration
type ReadSpaceConfigArgs struct {
	Database string `json:"database,omitempty" example:"dev_db"`
	KBName   string `json:"kb_name" binding:"required" example:"Notes"`
}

// Vectorization Types

// VectorizeSpaceArgs represents arguments for vectorizing an entire Space
type VectorizeSpaceArgs struct {
	Database  string `json:"database,omitempty" example:"dev_db"`
	KBName    string `json:"kb_name" binding:"required" example:"Notes"`
	BatchSize int    `json:"batch_size,omitempty" default:"100" example:"250"`
}

// BulkVectorizeCategoriesArgs represents arguments for vectorizing multiple categories
type BulkVectorizeCategoriesArgs struct {
	Database    string     `json:"database,omitempty" example:"dev_db"`
	KBName      string     `json:"kb_name" binding:"required" example:"Notes"`
	CategoryIDs []sop.UUID `json:"category_ids" binding:"required"`
	BatchSize   int        `json:"batch_size,omitempty" default:"100" example:"250"`
}

// BulkVectorizeItemsArgs represents arguments for vectorizing multiple items
type BulkVectorizeItemsArgs struct {
	Database   string     `json:"database,omitempty" example:"dev_db"`
	KBName     string     `json:"kb_name" binding:"required" example:"Notes"`
	CategoryID *sop.UUID  `json:"category_id,omitempty"`
	ItemIDs    []sop.UUID `json:"item_ids,omitempty"`
	BatchSize  int        `json:"batch_size,omitempty" default:"100" example:"250"`
}

// Category Operations Types

// UpsertCategoryArgs represents arguments for upserting a single category
type UpsertCategoryArgs struct {
	Database      string                     `json:"database,omitempty" example:"dev_db"`
	KBName        string                     `json:"kb_name" binding:"required" example:"Notes"`
	Parameter     memory.UpsertCategoryParam `json:"parameter" binding:"required"`
	TransactionID string                     `json:"transaction_id,omitempty"`
}

// BulkUpsertCategoriesArgs represents arguments for upserting multiple categories
type BulkUpsertCategoriesArgs struct {
	Database        string                       `json:"database,omitempty" example:"dev_db"`
	KBName          string                       `json:"kb_name" binding:"required" example:"Notes"`
	Parameters      []memory.UpsertCategoryParam `json:"parameters" binding:"required"`
	TransactionID   string                       `json:"transaction_id,omitempty"`
	TransactionMode TransactionMode              `json:"transaction_mode,omitempty" default:"single" enums:"single,explicit"`
}

// DeleteCategoryArgs represents arguments for deleting a single category
type DeleteCategoryArgs struct {
	Database      string   `json:"database,omitempty" example:"dev_db"`
	KBName        string   `json:"kb_name" binding:"required" example:"Notes"`
	CategoryID    sop.UUID `json:"category_id" binding:"required"`
	TransactionID string   `json:"transaction_id,omitempty"`
}

// BulkDeleteCategoriesArgs represents arguments for deleting multiple categories
type BulkDeleteCategoriesArgs struct {
	Database        string          `json:"database,omitempty" example:"dev_db"`
	KBName          string          `json:"kb_name" binding:"required" example:"Notes"`
	CategoryIDs     []sop.UUID      `json:"category_ids" binding:"required"`
	TransactionID   string          `json:"transaction_id,omitempty"`
	TransactionMode TransactionMode `json:"transaction_mode,omitempty" default:"single" enums:"single,explicit"`
}

// ListCategoriesArgs represents arguments for listing categories
type ListCategoriesArgs struct {
	Database      string `json:"database,omitempty" example:"dev_db"`
	KBName        string `json:"kb_name" binding:"required" example:"Notes"`
	Limit         int    `json:"limit,omitempty" default:"100" example:"50"`
	Offset        int    `json:"offset,omitempty" default:"0" example:"0"`
	ParentPath    string `json:"parent_path,omitempty" example:"Root/SubCategory"`
	TransactionID string `json:"transaction_id,omitempty"`
}

// CategoryListResult represents the result of listing categories
type CategoryListResult struct {
	Categories []memory.Category `json:"categories"`
	Total      int               `json:"total"`
}

// Item Operations Types

// UpsertItemArgs represents arguments for upserting a single item
type UpsertItemArgs struct {
	Database      string                                 `json:"database,omitempty" example:"dev_db"`
	KBName        string                                 `json:"kb_name" binding:"required" example:"Notes"`
	Parameter     memory.UpsertItemParam[map[string]any] `json:"parameter" binding:"required"`
	TransactionID string                                 `json:"transaction_id,omitempty"`
}

// BulkUpsertItemsArgs represents arguments for upserting multiple items
type BulkUpsertItemsArgs struct {
	Database        string                                   `json:"database,omitempty" example:"dev_db"`
	KBName          string                                   `json:"kb_name" binding:"required" example:"Notes"`
	Parameters      []memory.UpsertItemParam[map[string]any] `json:"parameters" binding:"required"`
	TransactionID   string                                   `json:"transaction_id,omitempty"`
	TransactionMode TransactionMode                          `json:"transaction_mode,omitempty" default:"single" enums:"single,explicit"`
}

// DeleteItemArgs represents arguments for deleting a single item
type DeleteItemArgs struct {
	Database      string   `json:"database,omitempty" example:"dev_db"`
	KBName        string   `json:"kb_name" binding:"required" example:"Notes"`
	CategoryID    sop.UUID `json:"category_id" binding:"required"`
	ItemID        sop.UUID `json:"item_id" binding:"required"`
	TransactionID string   `json:"transaction_id,omitempty"`
}

// BulkDeleteItemsArgs represents arguments for deleting multiple items
type BulkDeleteItemsArgs struct {
	Database        string          `json:"database,omitempty" example:"dev_db"`
	KBName          string          `json:"kb_name" binding:"required" example:"Notes"`
	CategoryID      sop.UUID        `json:"category_id" binding:"required"`
	ItemIDs         []sop.UUID      `json:"item_ids" binding:"required"`
	TransactionID   string          `json:"transaction_id,omitempty"`
	TransactionMode TransactionMode `json:"transaction_mode,omitempty" default:"single" enums:"single,explicit"`
}

// ListItemsArgs represents arguments for listing items
type ListItemsArgs struct {
	Database      string   `json:"database,omitempty" example:"dev_db"`
	KBName        string   `json:"kb_name" binding:"required" example:"Notes"`
	CategoryID    sop.UUID `json:"category_id,omitempty"`
	Limit         int      `json:"limit,omitempty" default:"100" example:"50"`
	Offset        int      `json:"offset,omitempty" default:"0" example:"0"`
	TransactionID string   `json:"transaction_id,omitempty"`
}

// ItemListResult represents the result of listing items
type ItemListResult struct {
	Items []memory.Item[map[string]any] `json:"items"`
	Total int                           `json:"total"`
}

// SearchItemsByPathArgs represents arguments for searching items by path
type SearchItemsByPathArgs struct {
	Database      string                   `json:"database,omitempty" example:"dev_db"`
	KBName        string                   `json:"kb_name" binding:"required" example:"Notes"`
	Parameters    []memory.PathSearchParam `json:"parameters" binding:"required"`
	TransactionID string                   `json:"transaction_id,omitempty"`
}

// Bulk Operation Result Types

// SpaceBulkOperationResult represents the result of a bulk Space operation
type SpaceBulkOperationResult struct {
	Success   bool                      `json:"success"`
	Processed int                       `json:"processed"`
	Failed    int                       `json:"failed"`
	Duration  time.Duration             `json:"duration"`
	Errors    []SpaceBulkOperationError `json:"errors,omitempty"`
	Metrics   SpaceBulkOperationMetrics `json:"metrics"`
}

// SpaceBulkOperationError represents an error in a bulk Space operation
type SpaceBulkOperationError struct {
	Index   int      `json:"index"`
	ID      sop.UUID `json:"id,omitempty"`
	Message string   `json:"message"`
}

// SpaceBulkOperationMetrics contains performance metrics for bulk Space operations
type SpaceBulkOperationMetrics struct {
	TotalItems     int           `json:"total_items"`
	ItemsPerSecond float64       `json:"items_per_second"`
	AvgItemTime    time.Duration `json:"avg_item_time"`
}
