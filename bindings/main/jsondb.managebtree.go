package main

/*
#include <stdlib.h> // For free
*/
import "C"
import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/sharedcode/joltrin/v5/encoding"
	"github.com/sharedcode/joltrin/jsondb"
)

type btreeAction int

const (
	BtreeActionUnknown = iota
	Add
	AddIfNotExist
	Update
	Upsert
	Remove
	Find
	FindWithID
	GetItems
	GetValues
	GetKeys
	First
	Last
	IsUnique
	Count
	GetStoreInfo
	UpdateKey
	UpdateCurrentKey
	GetCurrentKey
	Next
	Previous
	GetCurrentValue
)

// Manage Btree payload struct is used for communication between SOP language binding, e.g. Python,
// and the SOP's jsondb package each of the B-tree management action parameters including data payload.
//
// BtreeID is used to lookup the Btree from the Btree lookup table.
type ManageBtreeMetaData struct {
	IsPrimitiveKey bool      `json:"is_primitive_key"`
	TransactionID  uuid.UUID `json:"transaction_id"`
	BtreeID        uuid.UUID `json:"btree_id"`
}
type ManageBtreePayload[TK, TV any] struct {
	Items      []jsondb.Item[TK, TV] `json:"items"`
	PagingInfo *jsondb.PagingInfo    `json:"paging_info"`
}

//export manageBtree
func manageBtree(ctxID C.longlong, action C.int, payload *C.char, payload2 *C.char) *C.char {
	ctx := getContext(ctxID)
	if ctx == nil {
		return C.CString(fmt.Sprintf("context with ID %v not found", int64(ctxID)))
	}

	switch int(action) {
	case Add:
		fallthrough
	case AddIfNotExist:
		fallthrough
	case Upsert:
		fallthrough
	case Update:
		fallthrough
	case UpdateKey:
		fallthrough
	case UpdateCurrentKey:
		return manage(ctx, int(action), payload, payload2)
	case Remove:
		return remove(ctx, payload, payload2)
	default:
		errMsg := fmt.Sprintf("unsupported manage action(%d) of item to B-tree (unknown)", int(action))
		return C.CString(errMsg)
	}
}

func manage(ctx context.Context, action int, payload, payload2 *C.char) *C.char {
	p, b32, errMsg := extractMetaData(payload)
	if errMsg != nil {
		return errMsg
	}
	if b3, ok := b32.(*jsondb.JsonDBMapKey); ok {
		ps2 := C.GoString(payload2)
		var payload ManageBtreePayload[map[string]any, any]
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(ps2), &payload); err != nil {
			errMsg := fmt.Sprintf("error Unmarshal ManageBtreePayload, details: %v", err)
			return C.CString(errMsg)
		}
		var err error
		switch action {
		case Add:
			ok, err = b3.Add(ctx, payload.Items)
		case AddIfNotExist:
			ok, err = b3.AddIfNotExist(ctx, payload.Items)
		case Update:
			ok, err = b3.Update(ctx, payload.Items)
		case UpdateKey:
			ok, err = b3.UpdateKey(ctx, payload.Items)
		case UpdateCurrentKey:
			if len(payload.Items) == 0 {
				err = fmt.Errorf("UpdateCurrentKey requires at least one item")
			} else {
				ok, err = b3.UpdateCurrentKey(ctx, payload.Items[0])
			}
		case Upsert:
			ok, err = b3.Upsert(ctx, payload.Items)
		default:
			errMsg := fmt.Sprintf("unsupported manage action(%d) of item to B-tree (id=%v)", action, p.BtreeID)
			return C.CString(errMsg)
		}

		if err != nil {
			errMsg := fmt.Sprintf("error manage of item to B-tree (id=%v), details: %v", p.BtreeID, err)
			return C.CString(errMsg)
		}
		return C.CString(fmt.Sprintf("%v", ok))
	} else {
		b3, ok := b32.(*jsondb.JsonDBAnyKey[any, any])
		if !ok {
			errMsg := fmt.Sprintf("found B-tree(id=%v) from lookup is of wrong type", p.BtreeID)
			return C.CString(errMsg)
		}

		ps2 := C.GoString(payload2)
		var payload ManageBtreePayload[any, any]
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(ps2), &payload); err != nil {
			errMsg := fmt.Sprintf("error Unmarshal ManageBtreePayload, details: %v", err)
			return C.CString(errMsg)
		}

		var err error
		switch action {
		case Add:
			ok, err = b3.Add(ctx, payload.Items)
		case AddIfNotExist:
			ok, err = b3.AddIfNotExist(ctx, payload.Items)
		case Update:
			ok, err = b3.Update(ctx, payload.Items)
		case UpdateKey:
			ok, err = b3.UpdateKey(ctx, payload.Items)
		case UpdateCurrentKey:
			if len(payload.Items) == 0 {
				err = fmt.Errorf("UpdateCurrentKey requires at least one item")
			} else {
				ok, err = b3.UpdateCurrentKey(ctx, payload.Items[0])
			}
		case Upsert:
			ok, err = b3.Upsert(ctx, payload.Items)
		default:
			errMsg := fmt.Sprintf("unsupported manage action(%d) of item to B-tree (id=%v)", action, p.BtreeID)
			return C.CString(errMsg)
		}

		if err != nil {
			errMsg := fmt.Sprintf("error manage of item to B-tree (id=%v), details: %v", p.BtreeID, err)
			return C.CString(errMsg)
		}
		return C.CString(fmt.Sprintf("%v", ok))
	}
}

func remove(ctx context.Context, payload, payload2 *C.char) *C.char {
	p, b32, errMsg := extractMetaData(payload)
	if errMsg != nil {
		return errMsg
	}
	if b3, ok := b32.(*jsondb.JsonDBMapKey); ok {
		ps2 := C.GoString(payload2)
		var payload []map[string]any
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(ps2), &payload); err != nil {
			errMsg := fmt.Sprintf("error Unmarshal keys array, details: %v", err)
			return C.CString(errMsg)
		}
		var err error
		ok, err = b3.Remove(ctx, payload)
		if err != nil {
			errMsg := fmt.Sprintf("error remove of item from B-tree (id=%v), details: %v", p.BtreeID, err)
			return C.CString(errMsg)
		}
		return C.CString(fmt.Sprintf("%v", ok))
	} else {
		b3, ok := b32.(*jsondb.JsonDBAnyKey[any, any])
		if !ok {
			errMsg := fmt.Sprintf("found B-tree(id=%v) from lookup is of wrong type", p.BtreeID)
			return C.CString(errMsg)
		}

		ps2 := C.GoString(payload2)
		var payload []any
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(ps2), &payload); err != nil {
			errMsg := fmt.Sprintf("error Unmarshal keys array, details: %v", err)
			return C.CString(errMsg)
		}
		ok, err := b3.Remove(ctx, payload)
		if err != nil {
			errMsg := fmt.Sprintf("error remove of item from B-tree (id=%v), details: %v", p.BtreeID, err)
			return C.CString(errMsg)
		}
		return C.CString(fmt.Sprintf("%v", ok))
	}
}
