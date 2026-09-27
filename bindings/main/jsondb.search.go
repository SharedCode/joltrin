package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"encoding/json"
	"fmt"

	"github.com/sharedcode/joltrin/search"
	"github.com/sharedcode/joltrin/v5"
)

type SearchOptions struct {
	TransactionID string `json:"transaction_id"`
	Name          string `json:"name"`
}

const (
	SearchActionUnknown = iota
	SearchAdd
	SearchSearch
)

type SearchAddPayload struct {
	DocID string `json:"doc_id"`
	Text  string `json:"text"`
}

type SearchQueryPayload struct {
	Query string `json:"query"`
}

func getSearchStore(targetIDStr string) (*search.Index, error) {
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
		return nil, fmt.Errorf("search index not found in transaction")
	}
	store, ok := obj.(*search.Index)
	if !ok {
		return nil, fmt.Errorf("object is not a Search Index")
	}
	return store, nil
}

//export manageSearch
func manageSearch(ctxID C.longlong, action C.int, targetID *C.char, payload *C.char) *C.char {
	ctx := getContext(ctxID)
	if ctx == nil {
		return C.CString(fmt.Sprintf("context with ID %v not found", int64(ctxID)))
	}

	targetIDStr := C.GoString(targetID)
	jsonPayload := C.GoString(payload)

	switch int(action) {
	case SearchAdd:
		store, err := getSearchStore(targetIDStr)
		if err != nil {
			return C.CString(err.Error())
		}

		var item SearchAddPayload
		if err := json.Unmarshal([]byte(jsonPayload), &item); err != nil {
			return C.CString(fmt.Sprintf("invalid payload: %v", err))
		}

		if err := store.Add(ctx, item.DocID, item.Text); err != nil {
			return C.CString(err.Error())
		}

	case SearchSearch:
		store, err := getSearchStore(targetIDStr)
		if err != nil {
			return C.CString(err.Error())
		}

		var item SearchQueryPayload
		if err := json.Unmarshal([]byte(jsonPayload), &item); err != nil {
			return C.CString(fmt.Sprintf("invalid payload: %v", err))
		}

		results, err := store.Search(ctx, item.Query)
		if err != nil {
			return C.CString(err.Error())
		}

		data, _ := json.Marshal(results)
		return C.CString(string(data))
	}

	return nil
}
