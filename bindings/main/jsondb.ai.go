package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"encoding/json"
	"fmt"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
)

// --- Vector Database & Store Management ---

// We use map[string]any as the generic type T for Python interoperability.
// PyVectorDB is now just *database.Database
type PyVectorStore = ai.VectorStore[map[string]any]

const (
	KeyID                = "ID"
	KeyVector            = "Vector"
	KeyPayload           = "Payload"
	KeyCentroidID        = "CentroidID"
	KeyScore             = "Score"
	KeyMetaID            = "id"
	KeyMetaTransactionID = "transaction_id"
)

const (
	VectorActionUnknown = iota
	UpsertVector
	UpsertBatchVector
	GetVector
	DeleteVector
	QueryVector
	VectorCount
	OptimizeVector
)

type VectorStoreConfig struct {
	UsageMode   int `json:"usage_mode"`
	ContentSize int `json:"content_size"`
}

type VectorStoreTransportOptions struct {
	TransactionID string            `json:"transaction_id"`
	Name          string            `json:"name"`
	Config        VectorStoreConfig `json:"config"`
	StoragePath   string            `json:"storage_path"`
}

type VectorQueryOptions struct {
	Vector []float32      `json:"vector"`
	K      int            `json:"k"`
	Filter map[string]any `json:"filter"` // Simple equality match for now
}

func getVectorStore(targetIDStr string) (PyVectorStore, error) {
	var meta map[string]string
	if err := json.Unmarshal([]byte(targetIDStr), &meta); err != nil {
		return nil, fmt.Errorf("invalid store metadata: %v", err)
	}
	transUUID, err := sop.ParseUUID(meta[KeyMetaTransactionID])
	if err != nil {
		return nil, fmt.Errorf("invalid transaction UUID: %v", err)
	}
	storeUUID, err := sop.ParseUUID(meta[KeyMetaID])
	if err != nil {
		return nil, fmt.Errorf("invalid store UUID: %v", err)
	}

	obj, ok := transRegistry.GetBtree(transUUID, storeUUID)
	if !ok {
		return nil, fmt.Errorf("vector store not found in transaction")
	}
	store, ok := obj.(PyVectorStore)
	if !ok {
		return nil, fmt.Errorf("object is not a Vector Store")
	}
	return store, nil
}

func getModelStore(targetIDStr string) (ai.ModelStore, error) {
	var meta map[string]string
	if err := json.Unmarshal([]byte(targetIDStr), &meta); err != nil {
		return nil, fmt.Errorf("invalid store metadata: %v", err)
	}
	transUUID, err := sop.ParseUUID(meta[KeyMetaTransactionID])
	if err != nil {
		return nil, fmt.Errorf("invalid transaction UUID: %v", err)
	}
	storeUUID, err := sop.ParseUUID(meta[KeyMetaID])
	if err != nil {
		return nil, fmt.Errorf("invalid store UUID: %v", err)
	}

	obj, ok := transRegistry.GetBtree(transUUID, storeUUID)
	if !ok {
		return nil, fmt.Errorf("model store not found in transaction")
	}
	store, ok := obj.(ai.ModelStore)
	if !ok {
		return nil, fmt.Errorf("object is not a Model Store")
	}
	return store, nil
}

//export manageVectorDB
func manageVectorDB(ctxID C.longlong, action C.int, targetID *C.char, payload *C.char) *C.char {
	ctx := getContext(ctxID)
	if ctx == nil {
		return C.CString(fmt.Sprintf("context with ID %v not found", int64(ctxID)))
	}

	targetIDStr := C.GoString(targetID)
	jsonPayload := C.GoString(payload)

	switch int(action) {
	case UpsertVector:
		store, err := getVectorStore(targetIDStr)
		if err != nil {
			return C.CString(err.Error())
		}

		var item ai.Item[map[string]any]
		if err := json.Unmarshal([]byte(jsonPayload), &item); err != nil {
			return C.CString(fmt.Sprintf("invalid item: %v", err))
		}

		if err := store.Upsert(ctx, item); err != nil {
			return C.CString(err.Error())
		}

	case UpsertBatchVector:
		store, err := getVectorStore(targetIDStr)
		if err != nil {
			return C.CString(err.Error())
		}

		var items []ai.Item[map[string]any]
		if err := json.Unmarshal([]byte(jsonPayload), &items); err != nil {
			return C.CString(fmt.Sprintf("invalid items: %v", err))
		}

		if err := store.UpsertBatch(ctx, items); err != nil {
			return C.CString(err.Error())
		}

	case GetVector:
		store, err := getVectorStore(targetIDStr)
		if err != nil {
			return C.CString(err.Error())
		}

		itemID := jsonPayload
		item, err := store.Get(ctx, itemID)
		if err != nil {
			return C.CString(err.Error())
		}

		response := map[string]any{
			KeyID:         item.ID,
			KeyVector:     item.Vector,
			KeyPayload:    item.Payload,
			KeyCentroidID: item.CentroidID,
		}

		data, _ := json.Marshal(response)
		return C.CString(string(data))

	case DeleteVector:
		store, err := getVectorStore(targetIDStr)
		if err != nil {
			return C.CString(err.Error())
		}

		itemID := jsonPayload
		if err := store.Delete(ctx, itemID); err != nil {
			return C.CString(err.Error())
		}

	case QueryVector:
		store, err := getVectorStore(targetIDStr)
		if err != nil {
			return C.CString(err.Error())
		}

		var opts VectorQueryOptions
		if err := json.Unmarshal([]byte(jsonPayload), &opts); err != nil {
			return C.CString(fmt.Sprintf("invalid query options: %v", err))
		}

		// Simple equality filter
		var filterFunc func(map[string]any) bool
		if len(opts.Filter) > 0 {
			filterFunc = func(payload map[string]any) bool {
				for k, v := range opts.Filter {
					if val, ok := payload[k]; !ok || val != v {
						return false
					}
				}
				return true
			}
		}

		hits, err := store.Query(ctx, opts.Vector, opts.K, filterFunc)
		if err != nil {
			return C.CString(err.Error())
		}

		response := make([]map[string]any, len(hits))
		for i, h := range hits {
			response[i] = map[string]any{
				KeyID:      h.ID,
				KeyScore:   h.Score,
				KeyPayload: h.Payload,
			}
		}

		data, _ := json.Marshal(response)
		return C.CString(string(data))

	case VectorCount:
		store, err := getVectorStore(targetIDStr)
		if err != nil {
			return C.CString(err.Error())
		}

		count, err := store.Count(ctx)
		if err != nil {
			return C.CString(err.Error())
		}
		return C.CString(fmt.Sprintf("%d", count))

	case OptimizeVector:
		store, err := getVectorStore(targetIDStr)
		if err != nil {
			return C.CString(err.Error())
		}

		if err := store.Optimize(ctx); err != nil {
			return C.CString(err.Error())
		}
	}

	return nil
}

// --- Model Store Management ---

const (
	ModelActionUnknown = iota
	SaveModel
	LoadModel
	ListModels
	DeleteModel
)

type ModelStoreOptions struct {
	Name string `json:"name"`
	// Deprecated: Use Name instead.
	Path          string `json:"path"`
	TransactionID string `json:"transaction_id"`
}

type ModelItem struct {
	Category string `json:"category"`
	Name     string `json:"name"`
	Model    any    `json:"model"`
}

//export manageModelStore
func manageModelStore(ctxID C.longlong, action C.int, targetID *C.char, payload *C.char) *C.char {
	ctx := getContext(ctxID)
	if ctx == nil {
		return C.CString(fmt.Sprintf("context with ID %v not found", int64(ctxID)))
	}

	targetIDStr := C.GoString(targetID)
	jsonPayload := C.GoString(payload)

	switch int(action) {
	case SaveModel:
		store, err := getModelStore(targetIDStr)
		if err != nil {
			return C.CString(err.Error())
		}

		var item ModelItem
		if err := json.Unmarshal([]byte(jsonPayload), &item); err != nil {
			return C.CString(fmt.Sprintf("invalid item: %v", err))
		}

		if err := store.Save(ctx, item.Category, item.Name, item.Model); err != nil {
			return C.CString(err.Error())
		}

	case LoadModel:
		store, err := getModelStore(targetIDStr)
		if err != nil {
			return C.CString(err.Error())
		}

		var item ModelItem
		if err := json.Unmarshal([]byte(jsonPayload), &item); err != nil {
			return C.CString(fmt.Sprintf("invalid item: %v", err))
		}

		var target any
		if err := store.Load(ctx, item.Category, item.Name, &target); err != nil {
			return C.CString(err.Error())
		}

		data, _ := json.Marshal(target)
		return C.CString(string(data))

	case ListModels:
		store, err := getModelStore(targetIDStr)
		if err != nil {
			return C.CString(err.Error())
		}

		// Payload is the category string
		category := jsonPayload
		names, err := store.List(ctx, category)
		if err != nil {
			return C.CString(err.Error())
		}

		data, _ := json.Marshal(names)
		return C.CString(string(data))

	case DeleteModel:
		store, err := getModelStore(targetIDStr)
		if err != nil {
			return C.CString(err.Error())
		}

		var item ModelItem
		if err := json.Unmarshal([]byte(jsonPayload), &item); err != nil {
			return C.CString(fmt.Sprintf("invalid item: %v", err))
		}

		if err := store.Delete(ctx, item.Category, item.Name); err != nil {
			return C.CString(err.Error())
		}
	}

	return nil
}
