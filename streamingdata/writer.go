package streamingdata

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/v5/btree"
)

type writer[TK btree.Ordered] struct {
	btree       btree.BtreeInterface[StreamingDataKey[TK], []byte]
	ctx         context.Context
	key         TK
	chunkIndex  int
	addOrUpdate bool
}

func newWriter[TK btree.Ordered](ctx context.Context, addOrUpdate bool, key TK, btree btree.BtreeInterface[StreamingDataKey[TK], []byte]) *writer[TK] {
	return &writer[TK]{
		btree:       btree,
		ctx:         ctx,
		addOrUpdate: addOrUpdate,
		key:         key,
	}
}

func (w *writer[TK]) Write(p []byte) (int, error) {
	// Copy p to avoid sharing buffer with json.Encoder or other writers.
	pCopy := make([]byte, len(p))
	copy(pCopy, p)
	p = pCopy

	// Add.
	if w.addOrUpdate {
		if ok, err := w.btree.Add(w.ctx, StreamingDataKey[TK]{Key: w.key, ChunkIndex: w.chunkIndex}, p); err != nil || !ok {
			if err != nil {
				return 0, err
			}
			return 0, fmt.Errorf("write failed, key: %v, chunk: #%d did not insert to the backend Btree", w.key, w.chunkIndex)
		}
		// Increment the chunk index in prep for next (chunk) Write call.
		w.chunkIndex++
		return len(p), nil
	}
	// Update.
	var ok bool
	var err error
	ck := w.btree.GetCurrentKey().Key
	ck.ChunkIndex++
	sdk := StreamingDataKey[TK]{
		Key:        w.key,
		ChunkIndex: w.chunkIndex,
	}
	if ck.Compare(sdk) == 0 {
		ok, err = w.btree.Next(w.ctx)
		if ok && w.btree.GetCurrentKey().Key.Compare(sdk) != 0 {
			ok = false
		}
	} else {
		ok, err = w.btree.Find(w.ctx, StreamingDataKey[TK]{Key: w.key, ChunkIndex: w.chunkIndex}, false)
	}
	if err != nil {
		return 0, err
	}
	if ok {
		if ok, err := w.btree.UpdateCurrentValue(w.ctx, p); err != nil || !ok {
			if err != nil {
				return 0, err
			}
			return 0, fmt.Errorf("write failed, key: %v, chunk: #%d did not update to the backend Btree", w.key, w.chunkIndex)
		}
		// Increment the chunk index in prep for next (chunk) Write call.
		w.chunkIndex++
		return len(p), nil
	}
	// Current chunk with index not found, new chunk should be added.
	if ok, err := w.btree.Add(w.ctx, StreamingDataKey[TK]{Key: w.key, ChunkIndex: w.chunkIndex}, p); err != nil || !ok {
		if err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("write failed, key: %v, chunk: #%d did not insert to the backend Btree", w.key, w.chunkIndex)
	}
	// Increment the chunk index in prep for next (chunk) Write call.
	w.chunkIndex++
	return len(p), nil
}
