// Package streamingdata implements chunked, stream-like storage on top of a B-Tree.
// It splits large values into ordered chunks and provides Encoder/Reader helpers for IO-like access.
package streamingdata

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"

	"github.com/sharedcode/joltrin/v5/btree"
)

const (
	// MinimumStreamingStoreSlotLength is the smallest node slot length allowed for streaming stores.
	MinimumStreamingStoreSlotLength = 50
)

// StreamingDataStore stores entries split into ordered chunks and provides stream-like read/write operations.
type StreamingDataStore[TK btree.Ordered] struct {
	// Inherit or reuse an Object implementing BtreeInterface. Golang's inheritance is actually better,
	// it alows to inherit or reuse any object implementing a given interface. "loosely" & nicely done.
	btree.BtreeInterface[StreamingDataKey[TK], []byte]
}

// StreamingDataKey is the B-Tree key for streaming chunks: (Key, ChunkIndex).
type StreamingDataKey[TK btree.Ordered] struct {
	Key        TK
	ChunkIndex int
}

// Compare implements ordering by Key then ChunkIndex to preserve chunk order.
func (x StreamingDataKey[TK]) Compare(other any) int {
	y := other.(StreamingDataKey[TK])

	// Sorted by user define key and followed by the Chunk Index, so we can navigate/iterate it in the chunk's submitted natural order.
	i := btree.Compare(x.Key, y.Key)
	if i != 0 {
		return i
	}
	return cmp.Compare(x.ChunkIndex, y.ChunkIndex)
}

// Add inserts a new streaming item and returns an Encoder for writing chunks.
func (s *StreamingDataStore[TK]) Add(ctx context.Context, key TK) (*Encoder[TK], error) {
	w := newWriter(ctx, true, key, s.BtreeInterface)
	return newEncoder(w), nil
}

// AddIfNotExist inserts an item only when it does not exist and returns an Encoder to write the value.
func (s *StreamingDataStore[TK]) AddIfNotExist(ctx context.Context, key TK) (*Encoder[TK], error) {
	// Return nil if key already found in B-tree.
	if found, err := s.FindOne(ctx, key); err != nil || found {
		return nil, err
	}
	return s.Add(ctx, key)
}

// Upsert updates the item if it exists; otherwise it adds a new one and returns an Encoder.
func (s *StreamingDataStore[TK]) Upsert(ctx context.Context, key TK) (*Encoder[TK], error) {
	if found, err := s.FindOne(ctx, key); err != nil {
		return nil, err
	} else if found {
		return s.Update(ctx, key)
	}
	// Add if not exist.
	return s.Add(ctx, key)
}

// Remove deletes all chunks for the item with the given key.
func (s *StreamingDataStore[TK]) Remove(ctx context.Context, key TK) (bool, error) {
	if found, err := s.FindOne(ctx, key); err != nil || !found {
		return false, err
	}
	return s.RemoveCurrentItem(ctx)
}

// RemoveCurrentItem deletes all chunks for the current item.
func (s *StreamingDataStore[TK]) RemoveCurrentItem(ctx context.Context) (bool, error) {
	if s.BtreeInterface.Count() == 0 {
		return false, fmt.Errorf("failed to remove current item, store is empty")
	}

	key := s.BtreeInterface.GetCurrentKey().Key.Key
	keys := make([]StreamingDataKey[TK], 0, 5)
	for {
		keys = append(keys, StreamingDataKey[TK]{Key: key, ChunkIndex: s.BtreeInterface.GetCurrentKey().Key.ChunkIndex})
		if ok, err := s.BtreeInterface.Next(ctx); err != nil {
			return false, err
		} else if !ok ||
			s.BtreeInterface.GetCurrentKey().Key.Compare(StreamingDataKey[TK]{Key: key, ChunkIndex: s.BtreeInterface.GetCurrentKey().Key.ChunkIndex}) != 0 {
			break
		}
	}

	var lastErr error
	succeeded := true
	for _, k := range keys {
		if ok, err := s.BtreeInterface.Remove(ctx, k); err != nil {
			lastErr = err
		} else if !ok {
			// Only return success if all "chunks" are deleted.
			succeeded = false
		}
	}
	return succeeded, lastErr
}

// Update prepares an Encoder for updating chunks of the item with the given key.
func (s *StreamingDataStore[TK]) Update(ctx context.Context, key TK) (*Encoder[TK], error) {
	if found, err := s.FindOne(ctx, key); err != nil || !found {
		return nil, err
	}
	return s.UpdateCurrentValue(ctx)
}

// UpdateCurrentValue returns an Encoder for updating chunks of the current item.
func (s *StreamingDataStore[TK]) UpdateCurrentValue(ctx context.Context) (*Encoder[TK], error) {
	if s.BtreeInterface.Count() == 0 {
		return nil, fmt.Errorf("failed to update current item, store is empty")
	}
	w := newWriter(ctx, false, s.BtreeInterface.GetCurrentKey().Key.Key, s.BtreeInterface)
	return newEncoder(w), nil
}

// AddChunk inserts a new chunk for the specified key and chunk index.
func (s *StreamingDataStore[TK]) AddChunk(ctx context.Context, key TK, chunkIndex int, chunkValue []byte) (bool, error) {
	return s.BtreeInterface.AddIfNotExist(ctx, StreamingDataKey[TK]{
		Key:        key,
		ChunkIndex: chunkIndex,
	}, chunkValue)
}

// UpdateChunk updates an existing chunk for the specified key and chunk index.
func (s *StreamingDataStore[TK]) UpdateChunk(ctx context.Context, key TK, chunkIndex int, newChunkValue []byte) (bool, error) {
	return s.BtreeInterface.Update(ctx, StreamingDataKey[TK]{
		Key:        key,
		ChunkIndex: chunkIndex,
	}, newChunkValue)
}

// RemoveChunk deletes the chunk for the specified key and chunk index.
func (s *StreamingDataStore[TK]) RemoveChunk(ctx context.Context, key TK, chunkIndex int) (bool, error) {
	return s.BtreeInterface.Remove(ctx, StreamingDataKey[TK]{
		Key:        key,
		ChunkIndex: chunkIndex,
	})
}

// GetCurrentKey returns the logical key of the current item cursor.
func (s *StreamingDataStore[TK]) GetCurrentKey(ctx context.Context) TK {
	if s.BtreeInterface.Count() == 0 {
		var d TK
		return d
	}
	k := s.BtreeInterface.GetCurrentKey().Key
	return k.Key
}

// GetCurrentItem returns the current key and a JSON decoder that streams its value.
func (s *StreamingDataStore[TK]) GetCurrentItem(ctx context.Context) (btree.Item[TK, json.Decoder], error) {
	if s.BtreeInterface.Count() == 0 {
		return btree.Item[TK, json.Decoder]{}, fmt.Errorf("failed to get current item, store is empty")
	}
	ck := s.BtreeInterface.GetCurrentKey().Key
	r := newReader(ctx, ck.Key, ck.ChunkIndex, s.BtreeInterface)
	return btree.Item[TK, json.Decoder]{
		Key:   ck.Key,
		Value: json.NewDecoder(r),
	}, nil
}

// GetCurrentValue returns a JSON decoder that streams the current item's value.
func (s *StreamingDataStore[TK]) GetCurrentValue(ctx context.Context) (*json.Decoder, error) {
	if s.BtreeInterface.Count() == 0 {
		return nil, fmt.Errorf("failed to get current value, store is empty")
	}
	ck := s.BtreeInterface.GetCurrentKey().Key
	r := newReader(ctx, ck.Key, ck.ChunkIndex, s.BtreeInterface)
	return json.NewDecoder(r), nil
}

// FindOne finds the first chunk for a key and positions the cursor there.
func (s *StreamingDataStore[TK]) FindOne(ctx context.Context, key TK) (bool, error) {
	k := StreamingDataKey[TK]{Key: key}
	return s.BtreeInterface.Find(ctx, k, false)
}

// FindOneWithID finds a specific chunk index for a key (alias of FindChunk).
func (s *StreamingDataStore[TK]) FindOneWithID(ctx context.Context, key TK, chunkIndex int) (bool, error) {
	return s.FindChunk(ctx, key, chunkIndex)
}

// FindChunk finds a specific chunk for a key and positions the cursor there.
func (s *StreamingDataStore[TK]) FindChunk(ctx context.Context, key TK, chunkIndex int) (bool, error) {
	k := StreamingDataKey[TK]{Key: key, ChunkIndex: chunkIndex}
	return s.BtreeInterface.Find(ctx, k, false)
}
