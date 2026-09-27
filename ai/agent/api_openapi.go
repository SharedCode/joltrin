package agent

// Package agent provides AI-powered database operations with OpenAPI support
//
// @title SOP Agent API
// @version 2.0
// @description AI-powered database operations with strongly typed API and bulk operations
// @description
// @description ## Key Features
// @description - **Bulk Operations**: Process 10K+ items with automatic batching
// @description - **Transaction Control**: Three modes (auto_batch, single, explicit)
// @description - **Type Safety**: Strongly typed request/response structures
// @description - **LLM Integration**: Compatible with function calling and text tools
//
// @contact.name SOP Team
// @contact.url https://github.com/sharedcode/joltrin/v5
//
// @license.name MIT
// @license.url https://opensource.org/licenses/MIT
//
// @BasePath /api/v1
// @schemes http https
//
// @tag.name Bulk Operations
// @tag.description High-performance bulk insert/update/delete with automatic transaction batching
//
// @tag.name Transactions
// @tag.description Transaction lifecycle management (begin, commit, rollback)
//
// @tag.name Single Operations
// @tag.description Individual item operations (add, update, delete, select)
//
// @tag.name Advanced
// @tag.description Script execution and join operations

// OpenAPI Annotations for API methods

// BulkAdd godoc
// @Summary Bulk insert items
// @Description Insert multiple items with automatic transaction batching. Use auto_batch mode for 10K+ items.
// @Tags Bulk Operations
// @Accept json
// @Produce json
// @Param args body BulkAddArgs true "Bulk add parameters"
// @Success 200 {object} BulkOperationResult "Operation successful"
// @Failure 400 {object} ErrorResponse "Invalid request"
// @Failure 500 {object} ErrorResponse "Internal error"
// @Router /bulk-add [post]
// @Example request {"database": "dev_db", "store": "users", "items": [{"key": "user_1", "value": {"name": "John"}}], "transaction_mode": "auto_batch", "batch_size": 250}

// BulkUpdate godoc
// @Summary Bulk update items
// @Description Update multiple items with automatic transaction batching
// @Tags Bulk Operations
// @Accept json
// @Produce json
// @Param args body BulkUpdateArgs true "Bulk update parameters"
// @Success 200 {object} BulkOperationResult
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /bulk-update [post]

// BulkDelete godoc
// @Summary Bulk delete items
// @Description Delete multiple items with automatic transaction batching
// @Tags Bulk Operations
// @Accept json
// @Produce json
// @Param args body BulkDeleteArgs true "Bulk delete parameters"
// @Success 200 {object} BulkOperationResult
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /bulk-delete [post]

// BeginTransaction godoc
// @Summary Begin transaction
// @Description Start a new database transaction
// @Tags Transactions
// @Accept json
// @Produce json
// @Param args body TransactionArgs true "Transaction parameters"
// @Success 200 {object} TransactionHandle
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /transaction/begin [post]

// CommitTransaction godoc
// @Summary Commit transaction
// @Description Commit an active transaction
// @Tags Transactions
// @Accept json
// @Produce json
// @Param args body TransactionCommitArgs true "Transaction commit parameters"
// @Success 200 {string} string "Transaction committed"
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /transaction/commit [post]

// RollbackTransaction godoc
// @Summary Rollback transaction
// @Description Rollback an active transaction
// @Tags Transactions
// @Accept json
// @Produce json
// @Param args body TransactionRollbackArgs true "Transaction rollback parameters"
// @Success 200 {string} string "Transaction rolled back"
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /transaction/rollback [post]

// Add godoc
// @Summary Add item
// @Description Insert a single item
// @Tags Single Operations
// @Accept json
// @Produce json
// @Param args body AddArgs true "Add parameters"
// @Success 200 {string} string "Operation result"
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /add [post]

// Update godoc
// @Summary Update item
// @Description Update a single item
// @Tags Single Operations
// @Accept json
// @Produce json
// @Param args body UpdateArgs true "Update parameters"
// @Success 200 {string} string "Operation result"
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /update [post]

// Delete godoc
// @Summary Delete item
// @Description Delete a single item
// @Tags Single Operations
// @Accept json
// @Produce json
// @Param args body DeleteArgs true "Delete parameters"
// @Success 200 {string} string "Operation result"
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /delete [post]

// Select godoc
// @Summary Select items
// @Description Query items from a store
// @Tags Single Operations
// @Accept json
// @Produce json
// @Param args body SelectArgs true "Select parameters"
// @Success 200 {string} string "Query results"
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /select [post]

// ExecuteScript godoc
// @Summary Execute script
// @Description Execute a multi-step database script
// @Tags Advanced
// @Accept json
// @Produce json
// @Param args body ExecuteScriptArgs true "Script parameters"
// @Success 200 {string} string "Script results"
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /execute-script [post]

// Join godoc
// @Summary Join stores
// @Description Perform a join operation across stores
// @Tags Advanced
// @Accept json
// @Produce json
// @Param args body JoinArgs true "Join parameters"
// @Success 200 {string} string "Join results"
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /join [post]

// ErrorResponse represents an API error
type ErrorResponse struct {
	Error   string `json:"error" example:"store is required"`
	Code    string `json:"code,omitempty" example:"INVALID_REQUEST"`
	Details any    `json:"details,omitempty"`
}
