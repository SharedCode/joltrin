package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"context"
	"fmt"

	log "log/slog"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/encoding"
	"github.com/sharedcode/joltrin/jsondb"
)

//export navigateBtree
func navigateBtree(ctxID C.longlong, action C.int, payload *C.char, payload2 *C.char) *C.char {
	ctx := getContext(ctxID)
	if ctx == nil {
		return C.CString(fmt.Sprintf("context with ID %v not found", int64(ctxID)))
	}

	switch int(action) {
	case First:
		fallthrough
	case Last:
		fallthrough
	case Next:
		fallthrough
	case Previous:
		return moveTo(ctx, int(action), payload)
	case Find:
		fallthrough
	case FindWithID:
		return find(ctx, payload, payload2)
	default:
		errMsg := fmt.Sprintf("unsupported manage action(%d) of item to B-tree (unknown)", int(action))
		return C.CString(errMsg)
	}
}

//export isUniqueBtree
func isUniqueBtree(payload *C.char) *C.char {
	_, b32, errMsg := extractMetaData(payload)
	if errMsg != nil {
		return errMsg
	}
	if b3, ok := b32.(*jsondb.JsonDBMapKey); ok {
		ok = b3.IsUnique()
		return C.CString(fmt.Sprintf("%v", ok))
	}
	b3, _ := b32.(*jsondb.JsonDBAnyKey[any, any])
	return C.CString(fmt.Sprintf("%v", b3.IsUnique()))
}

//export getFromBtree
func getFromBtree(ctxID C.longlong, action C.int, payload *C.char, payload2 *C.char) (*C.char, *C.char) {
	// GetStoreInfo does not need Context.
	if action == GetStoreInfo {
		return getStoreInfo(payload)
	}

	ctx := getContext(ctxID)
	if ctx == nil {
		return nil, C.CString(fmt.Sprintf("context with ID %v not found", int64(ctxID)))
	}

	switch int(action) {
	case GetKeys:
		fallthrough
	case GetItems:
		fallthrough
	case GetCurrentKey:
		fallthrough
	case GetCurrentValue:
		return get(ctx, int(action), payload, payload2)
	case GetValues:
		return getValues(ctx, payload, payload2)
	default:
		errMsg := fmt.Sprintf("unsupported manage action(%d) of item to B-tree (unknown)", int(action))
		return nil, C.CString(errMsg)
	}
}

//export getFromBtreeOut
func getFromBtreeOut(ctxID C.longlong, action C.int, payload *C.char, payload2 *C.char, result **C.char, error **C.char) {
	r, e := getFromBtree(ctxID, action, payload, payload2)
	*result = r
	*error = e
}

//export getBtreeItemCount
func getBtreeItemCount(payload *C.char) (C.longlong, *C.char) {
	_, b32, errMsg := extractMetaData(payload)
	if errMsg != nil {
		return 0, errMsg
	}
	if b3, ok := b32.(*jsondb.JsonDBMapKey); ok {
		res := b3.Count()
		return C.longlong(res), nil
	} else {
		b3, _ := b32.(*jsondb.JsonDBAnyKey[any, any])
		res := b3.Count()
		return C.longlong(res), nil
	}
}

//export getBtreeItemCountOut
func getBtreeItemCountOut(payload *C.char, count *C.longlong, error **C.char) {
	c, e := getBtreeItemCount(payload)
	*count = c
	*error = e
}

func getStoreInfo(payload *C.char) (*C.char, *C.char) {
	p, b32, errMsg := extractMetaData(payload)
	if errMsg != nil {
		return nil, errMsg
	}
	if b3, ok := b32.(*jsondb.JsonDBMapKey); ok {
		si := b3.GetStoreInfo()
		bo := BtreeOptions{}
		bo.extract(&si)
		bo.TransactionID = p.TransactionID
		ba, err := encoding.DefaultMarshaler.Marshal(bo)
		if err != nil {
			// Should not happen but in case.
			return nil, C.CString(err.Error())
		}
		return C.CString(string(ba)), nil
	} else {
		b3, _ := b32.(*jsondb.JsonDBAnyKey[any, any])
		si := b3.GetStoreInfo()
		bo := BtreeOptions{}
		bo.extract(&si)
		bo.TransactionID = p.TransactionID
		ba, err := encoding.DefaultMarshaler.Marshal(bo)
		if err != nil {
			// Should not happen but in case.
			return nil, C.CString(err.Error())
		}
		return C.CString(string(ba)), nil
	}
}

// Getter for fetching Keys or Items.
func get(ctx context.Context, getAction int, payload *C.char, payload2 *C.char) (*C.char, *C.char) {
	p, b32, errMsg := extractMetaData(payload)
	if errMsg != nil {
		return nil, errMsg
	}
	if b3, ok := b32.(*jsondb.JsonDBMapKey); ok {
		ps2 := C.GoString(payload2)
		var payload jsondb.PagingInfo
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(ps2), &payload); err != nil {
			errMsg := fmt.Sprintf("error Unmarshal keys array, details: %v", err)
			return nil, C.CString(errMsg)
		}
		var err error
		var res string
		switch getAction {
		case GetKeys:
			res, err = b3.GetKeys(ctx, payload)
		case GetItems:
			res, err = b3.GetItems(ctx, payload)
		case GetCurrentKey:
			res, err = b3.GetCurrentKey()
		case GetCurrentValue:
			res, err = b3.GetCurrentValue(ctx)
		}
		if err != nil {
			errMsg := fmt.Sprintf("error get objects from B-tree (id=%v), details: %v", p.BtreeID, err)
			return nil, C.CString(errMsg)
		}
		return C.CString(res), nil
	} else {
		b3, ok := b32.(*jsondb.JsonDBAnyKey[any, any])
		if !ok {
			errMsg := fmt.Sprintf("found B-tree(id=%v) from lookup is of wrong type", p.BtreeID)
			return nil, C.CString(errMsg)
		}

		ps2 := C.GoString(payload2)
		var payload jsondb.PagingInfo
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(ps2), &payload); err != nil {
			errMsg := fmt.Sprintf("error Unmarshal keys array, details: %v", err)
			return nil, C.CString(errMsg)
		}
		var res string
		var err error
		switch getAction {
		case GetKeys:
			res, err = b3.GetKeys(ctx, payload)
		case GetItems:
			res, err = b3.GetItems(ctx, payload)
		case GetCurrentKey:
			res, err = b3.GetCurrentKey()
		case GetCurrentValue:
			res, err = b3.GetCurrentValue(ctx)
		}
		if err != nil {
			errMsg := fmt.Sprintf("error get objects from B-tree (id=%v), details: %v", p.BtreeID, err)
			return nil, C.CString(errMsg)
		}
		return C.CString(res), nil
	}
}

func getValues(ctx context.Context, payload, payload2 *C.char) (*C.char, *C.char) {
	p, b32, errMsg := extractMetaData(payload)
	if errMsg != nil {
		return nil, errMsg
	}
	if b3, ok := b32.(*jsondb.JsonDBMapKey); ok {
		ps2 := C.GoString(payload2)
		var payload ManageBtreePayload[map[string]any, any]
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(ps2), &payload); err != nil {
			errMsg := fmt.Sprintf("error Unmarshal keys array, details: %v", err)
			return nil, C.CString(errMsg)
		}
		res, err := b3.GetValues(ctx, payload.Items)
		if err != nil {
			errMsg := fmt.Sprintf("error get objects from B-tree (id=%v), details: %v", p.BtreeID, err)
			return nil, C.CString(errMsg)
		}
		return C.CString(res), nil
	} else {
		b3, ok := b32.(*jsondb.JsonDBAnyKey[any, any])
		if !ok {
			errMsg := fmt.Sprintf("found B-tree(id=%v) from lookup is of wrong type", p.BtreeID)
			return nil, C.CString(errMsg)
		}

		ps2 := C.GoString(payload2)
		var payload ManageBtreePayload[any, any]
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(ps2), &payload); err != nil {
			errMsg := fmt.Sprintf("error Unmarshal keys array, details: %v", err)
			return nil, C.CString(errMsg)
		}
		res, err := b3.GetValues(ctx, payload.Items)
		if err != nil {
			errMsg := fmt.Sprintf("error get objects from B-tree (id=%v), details: %v", p.BtreeID, err)
			return nil, C.CString(errMsg)
		}
		return C.CString(res), nil
	}
}

// Find item by key or key & its ID (UUID) will also position the cursor to this found item or nearest item
// giving chance for the code to be able to still fetch and implement logic in case missing.
func find(ctx context.Context, payload, payload2 *C.char) *C.char {
	p, b32, errMsg := extractMetaData(payload)
	if errMsg != nil {
		return errMsg
	}
	if b3, ok := b32.(*jsondb.JsonDBMapKey); ok {
		ps2 := C.GoString(payload2)
		var payload ManageBtreePayload[map[string]any, any]
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(ps2), &payload); err != nil {
			errMsg := fmt.Sprintf("error Unmarshal keys array, details: %v", err)
			return C.CString(errMsg)
		}
		var err error
		id := sop.UUID(payload.Items[0].ID)
		if id.IsNil() {
			ok, err = b3.Find(ctx, payload.Items[0].Key, true)
		} else {
			ok, err = b3.FindWithID(ctx, payload.Items[0].Key, id)
		}
		if err != nil {
			errMsg := fmt.Sprintf("error find with ID from B-tree (id=%v), details: %v", p.BtreeID, err)
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

		log.Info(fmt.Sprintf("Payload: %v", ps2))

		var payload ManageBtreePayload[any, any]
		if err := encoding.DefaultMarshaler.Unmarshal([]byte(ps2), &payload); err != nil {
			errMsg := fmt.Sprintf("error Unmarshal keys array, details: %v", err)
			return C.CString(errMsg)
		}
		var err error
		id := sop.UUID(payload.Items[0].ID)
		if id.IsNil() {
			ok, err = b3.Find(ctx, payload.Items[0].Key, true)
		} else {
			ok, err = b3.FindWithID(ctx, payload.Items[0].Key, id)
		}
		if err != nil {
			errMsg := fmt.Sprintf("error find from B-tree (id=%v), details: %v", p.BtreeID, err)
			return C.CString(errMsg)
		}
		return C.CString(fmt.Sprintf("%v", ok))
	}
}

// Move cursor to First or to Last item of the B-tree.
func moveTo(ctx context.Context, action int, payload *C.char) *C.char {
	p, b32, errMsg := extractMetaData(payload)
	if errMsg != nil {
		return errMsg
	}
	if b3, ok := b32.(*jsondb.JsonDBMapKey); ok {
		var err error
		switch action {
		case First:
			ok, err = b3.First(ctx)
			if err != nil {
				errMsg := fmt.Sprintf("error moving cursor to First item of B-tree (id=%v), details: %v", p.BtreeID, err)
				return C.CString(errMsg)
			}
		case Last:
			ok, err = b3.Last(ctx)
			if err != nil {
				errMsg := fmt.Sprintf("error moving cursor to Last item of B-tree (id=%v), details: %v", p.BtreeID, err)
				return C.CString(errMsg)
			}
		case Next:
			ok, err = b3.Next(ctx)
			if err != nil {
				errMsg := fmt.Sprintf("error moving cursor to Next item of B-tree (id=%v), details: %v", p.BtreeID, err)
				return C.CString(errMsg)
			}
		case Previous:
			ok, err = b3.Previous(ctx)
			if err != nil {
				errMsg := fmt.Sprintf("error moving cursor to Previous item of B-tree (id=%v), details: %v", p.BtreeID, err)
				return C.CString(errMsg)
			}
		}
		return C.CString(fmt.Sprintf("%v", ok))
	} else {
		b3, ok := b32.(*jsondb.JsonDBAnyKey[any, any])
		if !ok {
			errMsg := fmt.Sprintf("found B-tree(id=%v) from lookup is of wrong type", p.BtreeID)
			return C.CString(errMsg)
		}

		var err error
		switch action {
		case First:
			ok, err = b3.First(ctx)
			if err != nil {
				errMsg := fmt.Sprintf("error moving cursor to First item of B-tree (id=%v), details: %v", p.BtreeID, err)
				return C.CString(errMsg)
			}
		case Last:
			ok, err = b3.Last(ctx)
			if err != nil {
				errMsg := fmt.Sprintf("error moving cursor to Last item of B-tree (id=%v), details: %v", p.BtreeID, err)
				return C.CString(errMsg)
			}
		case Next:
			ok, err = b3.Next(ctx)
			if err != nil {
				errMsg := fmt.Sprintf("error moving cursor to Next item of B-tree (id=%v), details: %v", p.BtreeID, err)
				return C.CString(errMsg)
			}
		case Previous:
			ok, err = b3.Previous(ctx)
			if err != nil {
				errMsg := fmt.Sprintf("error moving cursor to Previous item of B-tree (id=%v), details: %v", p.BtreeID, err)
				return C.CString(errMsg)
			}
		}
		return C.CString(fmt.Sprintf("%v", ok))
	}
}

func extractMetaData(payload *C.char) (*ManageBtreeMetaData, any, *C.char) {
	ps := C.GoString(payload)
	var p *ManageBtreeMetaData
	if err := encoding.DefaultMarshaler.Unmarshal([]byte(ps), &p); err != nil {
		errMsg := fmt.Sprintf("error Unmarshal ManageBtreeMetaData, details: %v", err)
		return p, nil, C.CString(errMsg)
	}

	b32, ok := transRegistry.GetBtree(sop.UUID(p.TransactionID), sop.UUID(p.BtreeID))
	if !ok {
		errMsg := fmt.Sprintf("did not find B-tree(id=%v) from lookup", p.BtreeID)
		return p, nil, C.CString(errMsg)
	}
	return p, b32, nil
}
