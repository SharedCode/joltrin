package streamingdata

import (
	"context"
	"io"

	"github.com/sharedcode/joltrin/v5/btree"
)

type reader[TK btree.Ordered] struct {
	btree      btree.BtreeInterface[StreamingDataKey[TK], []byte]
	ctx        context.Context
	key        TK
	chunkIndex int
	readChunk  []byte
	readCount  int
}

func newReader[TK btree.Ordered](ctx context.Context, key TK, chunkIndex int, btree btree.BtreeInterface[StreamingDataKey[TK], []byte]) *reader[TK] {
	return &reader[TK]{
		btree:      btree,
		ctx:        ctx,
		key:        key,
		chunkIndex: chunkIndex,
	}
}

func (r *reader[TK]) Read(p []byte) (int, error) {
	if r.readChunk != nil {
		c := copy(p, r.readChunk[r.readCount:])
		if c+r.readCount >= len(r.readChunk) {
			r.readChunk = nil
			r.readCount = 0
		} else {
			r.readCount = r.readCount + c
		}
		return c, nil
	}

	var found bool
	var err error
	ck := r.btree.GetCurrentKey().Key
	ck.ChunkIndex++
	sdk := StreamingDataKey[TK]{
		Key:        r.key,
		ChunkIndex: r.chunkIndex,
	}
	if ck.Compare(sdk) == 0 {
		found, err = r.btree.Next(r.ctx)
		if found && r.btree.GetCurrentKey().Key.Compare(sdk) != 0 {
			found = false
		}
	} else {
		found, err = r.btree.Find(r.ctx, StreamingDataKey[TK]{Key: r.key, ChunkIndex: r.chunkIndex}, false)
	}
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, io.EOF
	}
	ba, err := r.btree.GetCurrentValue(r.ctx)
	if err != nil {
		return 0, err
	}
	c := copy(p, ba)
	if c < len(ba) {
		r.readCount = c
		r.readChunk = ba
		return c, nil
	}
	// Increment the chunk index in prep for next (chunk) Read call.
	r.chunkIndex++
	return c, nil
}
